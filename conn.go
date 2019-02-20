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
	net     *Network
	local   addr
	remote  addr
	netConn net.Conn
	rd, wd  *deadline

	closeOnce sync.Once
	closeDone chan struct{}
}

func (c *conn) Read(b []byte) (n int, err error) {
	if c.isClosed() {
		return 0, c.opError("read", io.ErrClosedPipe)
	}
	if !c.net.Firewall().Allow(c.local.host, c.remote.host) {
		return 0, c.opError("read", syscall.EPIPE)
	}

	if c.local.host == c.remote.host {
		n, err := c.netConn.Read(b)
		return n, c.maskError("read", err)
	}

	rlimit := c.net.getLimits(c.local.host, c.remote.host)[0]
	if rlimit == nil {
		rd := c.rd.get()
		c.rd.setNetconn(rd)
		err = c.netConn.SetReadDeadline(rd)
		if err == nil {
			n, err = c.netConn.Read(b)
		}
		return n, c.maskError("read", err)
	}

	for {
		d, max, deadline := rlimit.request(int64(len(b)), c.rd.get())
		if d > 0 {
			c.sleep(d, c.rd)
			if c.isClosed() {
				return 0, c.opError("read", io.ErrClosedPipe)
			}
			continue
		}
		if max <= 0 {
			return 0, c.opError("read", timeoutError{})
		}

		c.rd.setNetconn(deadline)
		err = c.netConn.SetReadDeadline(deadline)
		if err == nil {
			n, err = c.netConn.Read(b[:int(max)])
		}

		if err, ok := err.(net.Error); ok && err.Timeout() {
			continue
		}
		c.sleep(time.Until(deadline), c.rd)
		return n, c.maskError("read", err)
	}
}

func (c *conn) Write(b []byte) (n int, err error) {
	if c.isClosed() {
		return 0, c.opError("write", io.ErrClosedPipe)
	}
	if !c.net.Firewall().Allow(c.local.host, c.remote.host) {
		return 0, c.opError("write", syscall.EPIPE)
	}

	if c.local.host == c.remote.host {
		n, err := c.netConn.Write(b)
		return n, c.maskError("write", err)
	}

	wlimit := c.net.getLimits(c.local.host, c.remote.host)[1]
	if wlimit == nil {
		wd := c.wd.get()
		c.wd.setNetconn(wd)
		err = c.netConn.SetWriteDeadline(wd)
		if err == nil {
			n, err = c.netConn.Write(b)
		}
		return n, c.maskError("write", err)
	}

	wrote := 0
	for {
		d, max, deadline := wlimit.request(int64(len(b)-n), c.wd.get())
		if d > 0 {
			c.sleep(d, c.wd)
			if c.isClosed() {
				return n, c.opError("write", io.ErrClosedPipe)
			}
			continue
		}
		if max <= 0 {
			return n, c.opError("write", timeoutError{})
		}

		c.wd.setNetconn(deadline)
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
		c.sleep(time.Until(deadline), c.wd)
		return n, c.maskError("write", err)
	}
}

func (c *conn) SetDeadline(t time.Time) error {
	err := c.SetReadDeadline(t)
	if err == nil {
		err = c.SetWriteDeadline(t)
	}
	return err
}

func (c *conn) SetReadDeadline(t time.Time) error {
	if c.isClosed() {
		return c.opError("set", io.ErrClosedPipe)
	}
	if c.local.host == c.remote.host {
		return c.maskError("set", c.netConn.SetReadDeadline(t))
	}
	if c.rd.set(t) {
		c.rd.setNetconn(t)
		return c.maskError("set", c.netConn.SetReadDeadline(t))
	}
	return nil
}

func (c *conn) SetWriteDeadline(t time.Time) error {
	if c.isClosed() {
		return c.opError("set", io.ErrClosedPipe)
	}
	if c.local.host == c.remote.host {
		return c.maskError("set", c.netConn.SetWriteDeadline(t))
	}
	if c.wd.set(t) {
		c.wd.setNetconn(t)
		return c.maskError("set", c.netConn.SetWriteDeadline(t))
	}
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
	})
	return c.maskError("close", c.netConn.Close())
}

func (c *conn) isClosed() bool {
	select {
	case <-c.closeDone:
		return true
	default:
		return false
	}
}

func (c *conn) maskError(op string, err error) error {
	err = maskError(c.local, c.remote, err)
	if err, ok := err.(*net.OpError); ok {
		err.Op = op
	}
	return err
}

func (c *conn) opError(op string, err error) error {
	return &net.OpError{Op: op, Net: "tcp", Source: c.local, Addr: c.remote, Err: err}
}

// sleeps for the given duration. if deadline is changed/reached or
// connection is closed, it wakes up and returns immediately
func (c *conn) sleep(dur time.Duration, d *deadline) {
	select {
	case <-time.After(dur):
	case <-c.closeDone:
	case <-d.wait():
	}
}

// --------------------------------------------------

type deadline struct {
	mu      sync.RWMutex
	user    time.Time // deadline set by user
	netconn time.Time // deadline set on netconn
	timer   *time.Timer
	cancel  chan struct{}
}

func makeDeadline() *deadline {
	return &deadline{
		cancel: make(chan struct{}),
	}
}

func (d *deadline) get() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.user
}

func (d *deadline) setNetconn(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.netconn = t
}

// returns true if user given deadline is netConn's deadline
func (d *deadline) set(t time.Time) (reduced bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.user = t
	reduced = !t.IsZero() && (d.netconn.IsZero() || t.Before(d.netconn))
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
	return
}

func (d *deadline) wait() <-chan struct{} {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cancel
}
