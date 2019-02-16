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
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// New creates an empty network with AllowAll firewall and NoLimit bandwidth.
func New() *Network {
	return &Network{
		hosts:    make(map[string]*Host),
		ports:    make(map[int]string),
		firewall: AllowAll,
	}
}

// Network represents list of hosts on "fnet" network.
type Network struct {
	mu       sync.RWMutex
	hosts    map[string]*Host
	ports    map[int]string // port -> hostname
	firewall Firewall
}

// Host returns given host. If host does not
// exist, it creates new host and returns the
// same.
func (n *Network) Host(name string) *Host {
	n.mu.Lock()
	defer n.mu.Unlock()
	if host, ok := n.hosts[name]; ok {
		return host
	}
	host := &Host{
		net:    n,
		Name:   name,
		lrs:    make(map[int]*listener),
		limits: make(map[string][2]*bucket),
	}
	for _, h := range n.hosts {
		host.limits[h.Name] = [2]*bucket{nil, nil}
		h.mu.Lock()
		h.limits[host.Name] = [2]*bucket{nil, nil}
		h.mu.Unlock()
	}
	n.hosts[name] = host
	return host
}

// Firewall returns current firewall.
func (n *Network) Firewall() Firewall {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.firewall
}

// SetFirewall changes the current firewall.
func (n *Network) SetFirewall(firewall Firewall) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.firewall = firewall
}

// SetBandwidth enforces given bandwidth between the given two hosts.
func (n *Network) SetBandwidth(host1, host2 string, bw Bandwidth) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	h1, h2 := n.hosts[host1], n.hosts[host2]
	if h1 == nil || h2 == nil {
		return
	}

	h1.mu.Lock()
	h1.limits[h2.Name] = [2]*bucket{newBucket(bw), newBucket(bw)}
	h1.mu.Unlock()

	h2.mu.Lock()
	h2.limits[h1.Name] = [2]*bucket{newBucket(bw), newBucket(bw)}
	h2.mu.Unlock()
}

func (n *Network) getLimits(local, remote string) [2]*bucket {
	n.mu.RLock()
	defer n.mu.RUnlock()
	h := n.hosts[local]
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.limits[remote]
}

func (n *Network) hostname(port int) string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.ports[port]
}

func (n *Network) setPort(port int, host string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if host == "" {
		delete(n.ports, port)
	} else {
		n.ports[port] = host
	}
}

// ---------------------------------------------

// Host defines the network transport for
// a host. It provides Listen, Dial and DialTimeout
// for that host.
type Host struct {
	net  *Network
	Name string

	mu     sync.RWMutex
	lrs    map[int]*listener
	limits map[string][2]*bucket
}

// Listen implements net.Listen for "fnet" network.
func (h *Host) Listen(address string) (net.Listener, error) {
	host, sport, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if host != "" && host != h.Name {
		return nil, fmt.Errorf("fnet.Listen: cannot listen on diffent host %s", host)
	}
	port, err := strconv.Atoi(sport)
	if err != nil {
		return nil, fmt.Errorf("fnet.Listen: invalid port in address %s", address)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if port != 0 {
		if _, used := h.lrs[port]; used {
			return nil, fmt.Errorf("fnet.Listen: port %d is already bound on host %s", port, h.Name)
		}
	}

	netL, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, maskError(err, nil, addr{host, port})
	}
	_, sport, _ = net.SplitHostPort(netL.Addr().String())
	netPort, _ := strconv.Atoi(sport)
	if port == 0 {
		port = netPort
	}
	h.net.setPort(netPort, h.Name)
	lr := &listener{
		host: h,
		addr: addr{h.Name, port},
		netL: netL,
	}
	h.lrs[port] = lr

	return lr, nil
}

// Dial implements net.Dial for "fnet" network.
func (h *Host) Dial(address string) (net.Conn, error) {
	return h.DialTimeout(address, 0)
}

// DialTimeout implements net.DialTimeout for "fnet" network.
func (h *Host) DialTimeout(address string, timeout time.Duration) (net.Conn, error) {
	rhost, sport, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	rport, err := strconv.Atoi(sport)
	if err != nil {
		return nil, fmt.Errorf("fnet.Dial: invalid port in address %s", address)
	}

	h.net.mu.RLock()
	remote, ok := h.net.hosts[rhost]
	h.net.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("fnet.Dial: %s connection refused", address)
	}

	if !h.net.Firewall().Allow(h.Name, rhost) {
		return nil, fmt.Errorf("fnet.Dial: %s connection refused", address)
	}

	remote.mu.RLock()
	lr, ok := remote.lrs[rport]
	remote.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("fnet.Dial: %s connection refused", address)
	}

	netConn, err := net.DialTimeout("tcp", lr.netL.Addr().String(), timeout)
	if err != nil {
		return nil, maskError(err, addr{h.Name, -1}, addr{rhost, rport})
	}

	_, sport, _ = net.SplitHostPort(netConn.LocalAddr().String())
	netPort, _ := strconv.Atoi(sport)
	h.net.setPort(netPort, h.Name)

	return &conn{
		net:     h.net,
		local:   addr{h.Name, netPort},
		remote:  addr{rhost, rport},
		netConn: netConn,
		dialed:  true,
	}, nil
}

// ---------------------------------------------

type listener struct {
	host *Host
	addr addr
	netL net.Listener
}

func (l *listener) Accept() (net.Conn, error) {
	netConn, err := l.netL.Accept()
	if err != nil {
		return nil, maskError(err, l.addr, nil)
	}

	var remote addr
	_, port, _ := net.SplitHostPort(netConn.RemoteAddr().String())
	remote.port, _ = strconv.Atoi(port)

	for {
		host := l.host.net.hostname(remote.port)
		if host == "" {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		remote.host = host
		break
	}

	return &conn{
		net:     l.host.net,
		local:   l.addr,
		remote:  remote,
		netConn: netConn,
	}, nil
}

func (l *listener) Close() error {
	l.host.mu.Lock()
	delete(l.host.lrs, l.addr.port)
	l.host.mu.Unlock()

	_, sport, _ := net.SplitHostPort(l.Addr().String())
	port, _ := strconv.Atoi(sport)
	l.host.net.setPort(port, "")

	return l.netL.Close()
}

func (l *listener) Addr() net.Addr {
	return l.addr
}

// ---------------------------------------------

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
		return 0, errors.New("Conn.Read: broken pipe")
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
			return 0, &net.OpError{Op: "read", Net: "fnet", Source: c.local, Addr: c.remote, Err: timeoutError{}}
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
		return 0, errors.New("Conn.Read: broken pipe")
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
			return n, &net.OpError{Op: "write", Net: "fnet", Source: c.local, Addr: c.remote, Err: timeoutError{}}
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

// ---------------------------------------------

// if *net.OpError change its Op, Source and Addr values
// to correspond fnet specific
func maskError(err error, local, remote net.Addr) error {
	if err, ok := err.(*net.OpError); ok {
		err.Net = "fnet"
		if err.Source == nil {
			err.Addr = local
		} else {
			err.Source, err.Addr = local, remote
		}
	}
	return err
}

type addr struct {
	host string
	port int
}

func (addr) Network() string {
	return "fnet"
}

func (a addr) String() string {
	if a.port == -1 {
		return a.host
	}
	return fmt.Sprintf("%s:%d", a.host, a.port)
}

type timeoutError struct{}

func (timeoutError) Error() string {
	return "deadline reached"
}

func (timeoutError) Timeout() bool {
	return true
}
