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
//
// Bandwidth can be enforced between different hosts only.
// this method does nothing, if hosts are same or any host
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
	host, port, err := lookupHostPort(address)
	if err != nil {
		return nil, &net.OpError{Op: "listen", Net: "fnet", Err: err}
	}
	if host != "" && host != h.Name {
		return nil, &net.OpError{
			Op: "listen", Net: "fnet", Addr: addr{host, port},
			Err: errors.New("cannot bind on different host")}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if port != 0 {
		if _, used := h.lrs[port]; used {
			return nil, &net.OpError{
				Op: "listen", Net: "fnet", Addr: addr{host, port},
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
	rhost, rport, err := lookupHostPort(address)
	if err != nil {
		return nil, err
	}

	h.net.mu.RLock()
	remote, ok := h.net.hosts[rhost]
	h.net.mu.RUnlock()
	if !ok {
		return nil, &net.OpError{
			Op: "dial", Net: "fnet", Addr: addr{rhost, rport},
			Err: errors.New("connection refused")}
	}

	if !h.net.Firewall().Allow(h.Name, rhost) {
		return nil, &net.OpError{
			Op: "dial", Net: "fnet", Addr: addr{rhost, rport},
			Err: errors.New("connection refused")}
	}

	remote.mu.RLock()
	lr, ok := remote.lrs[rport]
	remote.mu.RUnlock()
	if !ok {
		return nil, &net.OpError{
			Op: "dial", Net: "fnet", Addr: addr{rhost, rport},
			Err: errors.New("connection refused")}
	}

	netConn, err := net.DialTimeout("tcp", lr.netL.Addr().String(), timeout)
	if err != nil {
		return nil, maskError(addr{h.Name, -1}, addr{rhost, rport}, err)
	}

	_, netPort, _ := lookupHostPort(netConn.LocalAddr().String())
	h.net.setPort(netPort, h.Name)

	return &conn{
		net:       h.net,
		local:     addr{h.Name, netPort},
		remote:    addr{rhost, rport},
		netConn:   netConn,
		usedPort:  netPort,
		rd:        makeDeadline(),
		wd:        makeDeadline(),
		closeDone: make(chan struct{}),
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
		return nil, maskError(l.addr, nil, err)
	}

	var remote addr
	_, remote.port, _ = lookupHostPort(netConn.RemoteAddr().String())

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
		net:       l.host.net,
		local:     l.addr,
		remote:    remote,
		netConn:   netConn,
		usedPort:  remote.port,
		rd:        makeDeadline(),
		wd:        makeDeadline(),
		closeDone: make(chan struct{}),
	}, nil
}

func (l *listener) Close() error {
	l.host.mu.Lock()
	delete(l.host.lrs, l.addr.port)
	l.host.mu.Unlock()

	_, port, _ := lookupHostPort(l.Addr().String())
	l.host.net.setPort(port, "")

	return l.netL.Close()
}

func (l *listener) Addr() net.Addr {
	return l.addr
}

// ---------------------------------------------

// if *net.OpError change its Op, Source and Addr values
// to correspond fnet specific
func maskError(local, remote net.Addr, err error) error {
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
	return "timeout"
}

func (timeoutError) Timeout() bool {
	return true
}
