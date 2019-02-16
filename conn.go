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
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

type conn struct {
	net     *Network
	local   addr
	remote  addr
	netConn net.Conn
	dialed  bool

	mu     sync.RWMutex
	rd, wd time.Time
}

func (c *conn) Read(b []byte) (n int, err error) {
	if !c.net.Firewall().Allow(c.local.host, c.remote.host) {
		return 0, c.opError("read", errors.New("broken pipe"))
	}

	if c.local.host == c.remote.host {
		n, err := c.netConn.Read(b)
		return n, c.maskError(err, "read")
	}

	rlimit := c.net.getLimits(c.local.host, c.remote.host)[0]
	if rlimit == nil {
		err = c.netConn.SetReadDeadline(c.readDeadline())
		if err == nil {
			n, err = c.netConn.Read(b)
		}
		return n, c.maskError(err, "read")
	}

	for {
		d, max, deadline := rlimit.request(false, int64(len(b)), c.readDeadline())
		time.Sleep(d)
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
		if rd := c.readDeadline(); rd.IsZero() || deadline.Before(rd) { // user could have change rd, meanwhile
			time.Sleep(deadline.Sub(time.Now()))
		}
		return n, c.maskError(err, "read")
	}
}

func (c *conn) Write(b []byte) (n int, err error) {
	if !c.net.Firewall().Allow(c.local.host, c.remote.host) {
		return 0, c.opError("write", errors.New("broken pipe"))
	}

	if c.local.host == c.remote.host {
		n, err := c.netConn.Write(b)
		return n, c.maskError(err, "write")
	}

	wlimit := c.net.getLimits(c.local.host, c.remote.host)[1]
	if wlimit == nil {
		err = c.netConn.SetWriteDeadline(c.writeDeadline())
		if err == nil {
			n, err = c.netConn.Write(b)
		}
		return n, c.maskError(err, "write")
	}

	wrote := 0
	for {
		d, max, deadline := wlimit.request(true, int64(len(b)-n), c.writeDeadline())
		time.Sleep(d)
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
		if wd := c.writeDeadline(); wd.IsZero() || deadline.Before(wd) { // user could have change wd, meanwhile
			time.Sleep(deadline.Sub(time.Now()))
		}
		return n, c.maskError(err, "write")
	}
}

func (c *conn) SetDeadline(t time.Time) error {
	if c.local.host == c.remote.host {
		return c.maskError(c.netConn.SetDeadline(t), "set")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rd, c.wd = t, t
	return nil
}

func (c *conn) SetReadDeadline(t time.Time) error {
	if c.local.host == c.remote.host {
		return c.maskError(c.netConn.SetReadDeadline(t), "set")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rd = t
	return nil
}

func (c *conn) SetWriteDeadline(t time.Time) error {
	if c.local.host == c.remote.host {
		return c.maskError(c.netConn.SetWriteDeadline(t), "set")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wd = t
	return nil
}

func (c *conn) readDeadline() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rd
}

func (c *conn) writeDeadline() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.wd
}

func (c *conn) LocalAddr() net.Addr {
	return c.local
}

func (c *conn) RemoteAddr() net.Addr {
	return c.remote
}

func (c *conn) Close() error {
	var hostPort string
	if c.dialed {
		hostPort = c.netConn.LocalAddr().String()
	} else {
		hostPort = c.netConn.RemoteAddr().String()
	}
	_, sport, _ := net.SplitHostPort(hostPort)
	port, _ := strconv.Atoi(sport)
	c.net.setPort(port, "")

	return c.maskError(c.netConn.Close(), "close")
}

func (c *conn) maskError(err error, op string) error {
	err = maskError(err, c.local, c.remote)
	if err, ok := err.(*net.OpError); ok {
		err.Op = op
	}
	return err
}

func (c *conn) opError(op string, err error) error {
	return &net.OpError{Op: op, Net: "fnet", Source: c.local, Addr: c.remote, Err: err}
}
