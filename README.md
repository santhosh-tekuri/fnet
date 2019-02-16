[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0) 
[![GoDoc](https://godoc.org/github.com/santhosh-tekuri/fnet?status.svg)](https://godoc.org/github.com/santhosh-tekuri/fnet)
[![Go Report Card](https://goreportcard.com/badge/github.com/santhosh-tekuri/fnet)](https://goreportcard.com/report/github.com/santhosh-tekuri/fnet)
[![Build Status](https://travis-ci.org/santhosh-tekuri/fnet.svg?branch=master)](https://travis-ci.org/santhosh-tekuri/fnet) 
[![codecov.io](https://codecov.io/github/santhosh-tekuri/fnet/coverage.svg?branch=master)](https://codecov.io/github/santhosh-tekuri/fnet?branch=master)

Package fnet provides programmable firewall, bandwidth to test
network failures in unit testing.

This package (fnet stands for fakenet) is intended for use in unit-testing network related failures.
Your library does not need any dependency on this package for this. only your
tests need this package as dependency.

This package simply wraps `net.Listen`, `net.Dial`, `net.Conn` implementation on
`tcp:localhost`. It enforces firewall/bandwidth before delegating to actual
implementation

Some minimal changes needs to be done in your library for this. Consider following
simple library code, to demonstrate the changes:

~~~go
package myapp

type Server struct{
    HostPort string
    ....
}

func (s *Server) launch() {
    ...
    lr, err := net.Listen("tcp", s.hostPort)
    ...
}
~~~

You have to mock `net.Listen` in your code. For this introduce transport interface as shown below:

~~~go
package myapp

type Server struct{
    HostPort string
    trans    transport
    ....
}

func (s *Server) launch() {
    ...
    lr, err := s.trans.Listen("tcp", s.hostPort)
    ...
}

type transport interface {
    Listen(address string) (net.Listener, error)
    Dial(address string) (net.Conn, error)
}

type tcpTransport struct{}

func (t tcpTransport) Listen(address string) (net.Listener, error) {
    return net.Listen("tcp", address)
}

func (t tcpTransport) Dial(address string, timeout time.Duration) (net.Conn, error) {
    return net.Dial("tcp", address, timeout)
}

// unit test code ---------------------

func TestServer(t *testing.T) {
    // create network of 3 hosts
    nw := fnet.New()
    earth, mars, venus := nw.Host("earth"), nw.Host("mars"), nw.Host("venus")

    s1 := &Server{HostPort: "earth:80", transport: earth} // server1 running on earth
    s2 := &Server{HostPort: "mars:80", transport: mars}   // server2 running on mars
    c := &Client{Servers: []{"earth:80", "mars:80"}, transport: venus} // client is running on venus

    // make s1 unreachable to client
    nw.SetFirewall(fnet.Split([]string{"earth"}, fnet.AllowAll))
    if reply, err := c.SendReq("hello"); err!=nil {
        t.Fatal("expected to connect s2")
    }

    // now make s2 unreachable to client, but not s1
    nw.SetFirewall(fnet.Split([]string{"mars"}, fnet.AllowAll))
    if reply, err := c.SendReq("hello"); err!=nil {
        t.Fatal("expected to connect s1")
    }
}
~~~
You can mock `net.Listen`, `net.Dial` and `net.DialTimeout` using this package as shown above

Now you can various network failures as shown above using `fnet.Firewall` `fnet.Bandwidth`

## Firewalls

This package provides 3 implementations of firewall:

### AllowAll:

This does not block any network traffic.
This is the default firewall set on newly created network.

### AllowSelf:

This blocks traffic between distinct hosts.
Note that traffic within the host is allowed.
Consider network with hosts m1, m2, m3 and m4,
AllowSelf creates 4 network partitions: m1 | m2 | m3 | m4

### Split:

This implements network partioning. Mutiple partitions
can be defined by chaining. See example below:
~~~go
// Consider network with hosts m1, m2, m3, m4, m5 and m6

// 2 partitions: m1 m2 | m3 m4 m5 m6
firewall := fnet.Split([]string{"m1", "m2"}, fnet.AllowAll)

// 3 partitions: m1 m2 | m3 m4 | m5 m6
firewall := fnet.Split([]string{"m1", "m2"}, fnet.AllowAll)
firewall = fnet.Split([]string{"m3", "m4"}, firewall) // chaining
~~~
You can create your own firewall implementation if needed. It is simple single method interface:
~~~go
type Firewall interface {
    Allow(host1, host2 string) bool
}
~~~

## Bandwidth

to set bandwidth between two hosts:
~~~go
nw := fnet.New()
earth, mars := nw.Host("earth"), nw.Host("mars")
nw.SetBandwidth("earth", "mars", fnet.Bandwidth(10*1024*1024)) // 10MB per second between earth and mars

// to revert
nw.SetBandwidth("earth", "mars", fnet.NoLimit)
~~~