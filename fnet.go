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

// New creates an empty network with AllowAll firewall
func New() *Network {
	return &Network{
		hosts:    make(map[string]*Host),
		ports:    make(map[int]string),
		firewall: AllowAll,
	}
}

// Network represents list of hosts on "fnet" network
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
		net:  n,
		Name: name,
		lrs:  make(map[int]*listener),
	}
	n.hosts[name] = host
	return host
}

// Firewall returns current firewall
func (n *Network) Firewall() Firewall {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.firewall
}

// SetFirewall changes the current firewall
func (n *Network) SetFirewall(firewall Firewall) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.firewall = firewall
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

	mu  sync.RWMutex
	lrs map[int]*listener
}

// Listen implements net.Listen for "fnet" network
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
		return nil, err
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

// Dial implements net.Dial for "fnet" network
func (h *Host) Dial(address string) (net.Conn, error) {
	return h.DialTimeout(address, 0)
}

// DialTimeout implements net.DialTimeout for "fnet" network
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
		return nil, err
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
		return nil, err
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
}

func (c *conn) Read(b []byte) (n int, err error) {
	if !c.net.Firewall().Allow(c.local.host, c.remote.host) {
		return 0, errors.New("Conn.Read: broken pipe")
	}
	return c.netConn.Read(b)
}

func (c *conn) Write(b []byte) (n int, err error) {
	if !c.net.Firewall().Allow(c.local.host, c.remote.host) {
		return 0, errors.New("Conn.Read: broken pipe")
	}
	return c.netConn.Write(b)
}

func (c *conn) SetDeadline(t time.Time) error {
	return c.netConn.SetDeadline(t)
}

func (c *conn) SetReadDeadline(t time.Time) error {
	return c.netConn.SetReadDeadline(t)
}

func (c *conn) SetWriteDeadline(t time.Time) error {
	return c.netConn.SetWriteDeadline(t)
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

	return c.netConn.Close()
}

// ---------------------------------------------

type addr struct {
	host string
	port int
}

func (addr) Network() string {
	return "fnet"
}

func (a addr) String() string {
	return fmt.Sprintf("%s:%d", a.host, a.port)
}

func (a addr) Host() string {
	return a.host
}
func (a addr) Port() int {
	return a.port
}
