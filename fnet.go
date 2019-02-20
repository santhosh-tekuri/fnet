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
	"sync"
	"time"
)

// New creates an empty network with AllowAll firewall and NoLimit bandwidth.
func New() *Network {
	return &Network{
		hosts:    make(map[string]*Host),
		firewall: AllowAll,
	}
}

// Network represents list of hosts on fake network.
type Network struct {
	mu       sync.RWMutex
	hosts    map[string]*Host
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
//
// Bandwidth can be enforced between different hosts only.
// This method does nothing, if hosts are same or any host
// does not exist.
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

// Listen implements net.Listen.
func (h *Host) Listen(network, address string) (net.Listener, error) {
	host, port, err := lookupHostPort(address)
	if err != nil {
		return nil, &net.OpError{Op: "listen", Net: "tcp", Err: err}
	}
	if host != "" && host != h.Name {
		return nil, &net.OpError{
			Op: "listen", Net: "tcp", Addr: addr{host, port},
			Err: errors.New("cannot bind on different host")}
	}

	if network != "tcp" {
		return nil, &net.OpError{Op: "listen", Net: network, Addr: addr{host, port}, Err: net.UnknownNetworkError(network)}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if port != 0 {
		if _, used := h.lrs[port]; used {
			return nil, &net.OpError{
				Op: "listen", Net: "tcp", Addr: addr{host, port},
				Err: errors.New("port is in use")}
		}
	}

	netL, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, maskError(nil, addr{host, port}, err)
	}
	_, netPort, _ := lookupHostPort(netL.Addr().String())
	if port == 0 {
		port = netPort
	}
	lr := &listener{
		host:     h,
		addr:     addr{h.Name, port},
		netL:     netL,
		acceptCh: make(chan *conn),
	}
	h.lrs[port] = lr

	return lr, nil
}

// Dial implements net.Dial.
func (h *Host) Dial(network, address string) (net.Conn, error) {
	return h.DialTimeout(network, address, 0)
}

// DialTimeout implements net.DialTimeout.
func (h *Host) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	rhost, rport, err := lookupHostPort(address)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: err}
	}

	if network != "tcp" {
		return nil, &net.OpError{Op: "listen", Net: network, Addr: addr{rhost, rport}, Err: net.UnknownNetworkError(network)}
	}

	h.net.mu.RLock()
	remote, ok := h.net.hosts[rhost]
	h.net.mu.RUnlock()
	if !ok {
		return nil, &net.OpError{
			Op: "dial", Net: "tcp", Addr: addr{rhost, rport},
			Err: errors.New("connection refused")}
	}

	if !h.net.Firewall().Allow(h.Name, rhost) {
		return nil, &net.OpError{
			Op: "dial", Net: "tcp", Addr: addr{rhost, rport},
			Err: errors.New("connection refused")}
	}

	remote.mu.RLock()
	lr, ok := remote.lrs[rport]
	remote.mu.RUnlock()
	if !ok {
		return nil, &net.OpError{
			Op: "dial", Net: "tcp", Addr: addr{rhost, rport},
			Err: errors.New("connection refused")}
	}

	lr.mu.Lock()
	defer lr.mu.Unlock()
	var aconn net.Conn
	var aerr error
	accepted := make(chan struct{})
	go func() {
		aconn, aerr = lr.netL.Accept()
		close(accepted)
	}()
	dconn, derr := net.DialTimeout("tcp", lr.netL.Addr().String(), timeout)
	if derr != nil {
		return nil, maskError(nil, addr{rhost, rport}, derr)
	}
	<-accepted
	if aerr != nil {
		_ = dconn.Close()
		return nil, &net.OpError{
			Op: "dial", Net: "tcp", Addr: addr{rhost, rport},
			Err: errors.New("connection failed")}
	}
	_, netPort, _ := lookupHostPort(dconn.LocalAddr().String())
	lr.acceptCh <- &conn{
		net:       h.net,
		local:     lr.addr,
		remote:    addr{h.Name, netPort},
		netConn:   aconn,
		rd:        makeDeadline(),
		wd:        makeDeadline(),
		closeDone: make(chan struct{}),
	}

	return &conn{
		net:       h.net,
		local:     addr{h.Name, netPort},
		remote:    addr{rhost, rport},
		netConn:   dconn,
		rd:        makeDeadline(),
		wd:        makeDeadline(),
		closeDone: make(chan struct{}),
	}, nil
}

// ---------------------------------------------

type listener struct {
	host     *Host
	addr     addr
	netL     net.Listener
	mu       sync.Mutex
	acceptCh chan *conn
}

func (l *listener) Accept() (net.Conn, error) {
	netConn := <-l.acceptCh
	if netConn == nil {
		return nil, &net.OpError{
			Op: "accept", Net: "tcp", Addr: l.addr,
			Err: errors.New("listener closed")}
	}
	return netConn, nil
}

func (l *listener) Close() error {
	l.host.mu.Lock()
	delete(l.host.lrs, l.addr.port)
	l.host.mu.Unlock()

	l.mu.Lock()
	close(l.acceptCh)
	l.mu.Unlock()
	return l.netL.Close()
}

func (l *listener) Addr() net.Addr {
	return l.addr
}

// ---------------------------------------------

// if *net.OpError change its Op, Source and Addr values
// to correspond fnet specific
func maskError(source, addr net.Addr, err error) error {
	if err, ok := err.(*net.OpError); ok {
		err.Source, err.Addr = source, addr
	}
	return err
}

func lookupHostPort(addr string) (host string, port int, err error) {
	host, service, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err = net.LookupPort("tcp", service)
	return host, port, err
}

type addr struct {
	host string
	port int
}

func (addr) Network() string {
	return "tcp"
}

func (a addr) String() string {
	return fmt.Sprintf("%s:%d", a.host, a.port)
}

type timeoutError struct{}

func (timeoutError) Error() string {
	return "timeout"
}

func (timeoutError) Timeout() bool {
	return true
}
