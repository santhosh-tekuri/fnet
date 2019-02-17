// Copyright 2019 Santhosh Kumar Tekuri
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fnet

import (
	"io"
	"net"
	"sync"
	"syscall"
	"time"
)

type conn struct {
	net      *Network
	local    addr
	remote   addr
	netConn  net.Conn
	usedPort int // ephermal port used. to be released on close
	rd, wd   *deadline

	closeOnce sync.Once
	closeDone chan struct{}
}

func (c *conn) Read(b []byte) (n int, err error) {
	if !c.net.Firewall().Allow(c.local.host, c.remote.host) {
		return 0, c.opError("read", syscall.EPIPE)
	}

	if c.local.host == c.remote.host {
		n, err := c.netConn.Read(b)
		return n, c.maskError("read", err)
	}

	rlimit := c.net.getLimits(c.local.host, c.remote.host)[0]
	if rlimit == nil {
		err = c.netConn.SetReadDeadline(c.rd.get())
		if err == nil {
			n, err = c.netConn.Read(b)
		}
		return n, c.maskError("read", err)
	}

	for {
		d, max, deadline := rlimit.request(false, int64(len(b)), c.rd.get())
		if err := c.sleep(d, c.rd); err != nil {
			return 0, c.opError("read", err)
		}
		if max <= 0 {
			return 0, c.opError("read", timeoutError{})
		}

		err = c.netConn.SetReadDeadline(deadline)
		if err == nil {
			n, err = c.netConn.Read(b[:int(max)])
			rlimit.taken(int64(n))
		}

		if err, ok := err.(net.Error); ok && err.Timeout() {
			if n == 0 {
				continue
			}
			err = nil
		}
		_ = c.sleep(time.Until(deadline), c.rd)
		return n, c.maskError("read", err)
	}
}

func (c *conn) Write(b []byte) (n int, err error) {
	if !c.net.Firewall().Allow(c.local.host, c.remote.host) {
		return 0, c.opError("write", syscall.EPIPE)
	}

	if c.local.host == c.remote.host {
		n, err := c.netConn.Write(b)
		return n, c.maskError("write", err)
	}

	wlimit := c.net.getLimits(c.local.host, c.remote.host)[1]
	if wlimit == nil {
		err = c.netConn.SetWriteDeadline(c.wd.get())
		if err == nil {
			n, err = c.netConn.Write(b)
		}
		return n, c.maskError("write", err)
	}

	wrote := 0
	for {
		d, max, deadline := wlimit.request(true, int64(len(b)-n), c.wd.get())
		if err := c.sleep(d, c.wd); err != nil {
			return 0, c.opError("write", err)
		}
		if max <= 0 {
			return n, c.opError("write", timeoutError{})
		}

		err = c.netConn.SetWriteDeadline(deadline)
		if err == nil {
			wrote, err = c.netConn.Write(b[n : n+int(max)])
			n += wrote
		}

		if err, ok := err.(net.Error); ok && err.Timeout() {
			continue
		}
		if err == nil && n < len(b) {
			continue
		}
		_ = c.sleep(time.Until(deadline), c.wd)
		return n, c.maskError("write", err)
	}
}

func (c *conn) SetDeadline(t time.Time) error {
	if c.local.host == c.remote.host {
		return c.maskError("set", c.netConn.SetDeadline(t))
	}
	c.rd.set(t)
	c.wd.set(t)
	return nil
}

func (c *conn) SetReadDeadline(t time.Time) error {
	if c.local.host == c.remote.host {
		return c.maskError("set", c.netConn.SetReadDeadline(t))
	}
	c.rd.set(t)
	return nil
}

func (c *conn) SetWriteDeadline(t time.Time) error {
	if c.local.host == c.remote.host {
		return c.maskError("set", c.netConn.SetWriteDeadline(t))
	}
	c.wd.set(t)
	return nil
}

func (c *conn) LocalAddr() net.Addr {
	return c.local
}

func (c *conn) RemoteAddr() net.Addr {
	return c.remote
}

func (c *conn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closeDone)
		c.net.setPort(c.usedPort, "")
	})
	return c.maskError("close", c.netConn.Close())
}

func (c *conn) maskError(op string, err error) error {
	err = maskError(c.local, c.remote, err)
	if err, ok := err.(*net.OpError); ok {
		err.Op = op
	}
	return err
}

func (c *conn) opError(op string, err error) error {
	return &net.OpError{Op: op, Net: "fnet", Source: c.local, Addr: c.remote, Err: err}
}

// sleeps for the given duration. if deadline or close, happends
// it wakes up and returns appropriate error if applicable, else
// continues sleeping
func (c *conn) sleep(dur time.Duration, d *deadline) error {
	sleep := time.After(dur)
	for {
		select {
		case <-sleep:
			return nil
		case <-c.closeDone:
			return io.ErrClosedPipe
		case <-d.wait():
			if d.isTimeout() {
				return timeoutError{}
			}
		}
	}
}

// --------------------------------------------------

type deadline struct {
	mu     sync.RWMutex
	time   time.Time
	timer  *time.Timer
	cancel chan struct{}
}

func makeDeadline() *deadline {
	return &deadline{
		cancel: make(chan struct{}),
	}
}

func (d *deadline) get() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.time
}

func (d *deadline) set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.time = t
	if d.timer != nil && !d.timer.Stop() {
		<-d.cancel // Wait for the timer callback to finish and close cancel
	}
	d.timer = nil

	// Time is zero, then there is no deadline.
	var closed bool
	select {
	case <-d.cancel:
		closed = true
	default:
		closed = false
	}

	if t.IsZero() {
		if closed {
			d.cancel = make(chan struct{})
		}
		return
	}

	// Time in the future, setup a timer to cancel in the future.
	if dur := time.Until(t); dur > 0 {
		if closed {
			d.cancel = make(chan struct{})
		}
		d.timer = time.AfterFunc(dur, func() {
			close(d.cancel)
		})
		return
	}

	// Time in the past, so close immediately.
	if !closed {
		close(d.cancel)
	}
}

func (d *deadline) wait() <-chan struct{} {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cancel
}

func (d *deadline) isTimeout() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	select {
	case <-d.cancel:
		return true
	default:
		return false
	}
}
