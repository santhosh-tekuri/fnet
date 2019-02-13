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

package fnet_test

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/fnet"
)

func TestCommunication(t *testing.T) {
	nw := fnet.New()
	earth, mars := nw.Host("earth"), nw.Host("mars")

	e80, e0 := listen(t, earth, 80), listen(t, earth, 0)
	if e80.Addr().String() != "earth:80" {
		t.Errorf("lr.Addr: got %s, want %s", e80.Addr(), "earth:80")
	}
	if !strings.HasPrefix(e0.Addr().String(), "earth:") {
		t.Errorf("lr.Addr: got %s, want %s", e80.Addr(), "earth:")
	}
	if e0.Addr().String() == "earth:0" {
		t.Errorf("ephermal port is not chosen")
	}

	ede80, e80ae, err := dial(e80, earth)
	if err != nil {
		t.Fatal("same host dial failed")
	}
	if !strings.HasPrefix(ede80.LocalAddr().String(), "earth:") {
		t.Errorf("LocalAddr: got %s, want %s", e80.Addr(), "earth:")
	}
	if ede80.RemoteAddr().String() != "earth:80" {
		t.Errorf("RemoteAddr: got %s, want %s", e80.Addr(), "earth:80")
	}
	if e80ae.LocalAddr().String() != "earth:80" {
		t.Errorf("LocalAddr: got %s, want %s", e80.Addr(), "earth:80")
	}
	if !strings.HasPrefix(e80ae.RemoteAddr().String(), "earth:") {
		t.Errorf("RemoteAddr: got %s, want %s", e80.Addr(), "earth:")
	}

	b := []byte("hello")
	if _, err := ede80.Write(b); err != nil {
		t.Fatal(err)
	}
	if n, err := e80ae.Read(b); err != nil || n != 5 || string(b) != "hello" {
		t.Fatal(err)
	}

	mde0, e0am, err := dial(e0, mars)
	if err != nil {
		t.Fatal("different host dial failed")
	}
	if _, err := e0am.Write(b); err != nil {
		t.Fatal(err)
	}
	if n, err := mde0.Read(b); err != nil || n != 5 || string(b) != "hello" {
		t.Fatal(err)
	}

	e90, err := earth.Listen(":90")
	if err != nil {
		t.Fatal("listen :90 failed")
	}
	if e90.Addr().String() != "earth:90" {
		t.Errorf("lr.Addr: got %s, want %s", e80.Addr(), "earth:90")
	}
	if _, err := earth.Listen("earth:90"); err == nil {
		t.Error("listening twice on same port")
	}
	e90.Close()
	listen(t, earth, 90)

}

func xxxTestPorts(t *testing.T) {
	var mu sync.RWMutex
	host := ""

	lr, err := net.Listen("tcp", "localhost:8888")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := lr.Accept()
		if err != nil {
			t.Fatal(err)
		}
		mu.RLock()
		h := host
		mu.RUnlock()
		fmt.Println("host", h)
		fmt.Println("accepted", conn.LocalAddr(), conn.RemoteAddr())
	}()
	conn, err := net.Dial("tcp", "localhost:8888")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	host = "mars"
	mu.Unlock()
	fmt.Println("dialed", conn.LocalAddr(), conn.RemoteAddr())
}

// -------------------------------------------------------

func listen(t *testing.T, host *fnet.Host, port int) net.Listener {
	t.Helper()
	lr, err := host.Listen(fmt.Sprintf("%s:%d", host.Name, port))
	if err != nil {
		t.Fatal(err)
	}
	return lr
}

func dial(lr net.Listener, host *fnet.Host) (dialed, accepted net.Conn, err error) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, errr := lr.Accept()
		if errr != nil {
			err = errr
		}
		accepted = conn
	}()

	conn, errr := host.DialTimeout(lr.Addr().String(), 1*time.Second)
	if errr != nil {
		err = errr
		return
	}
	dialed = conn
	wg.Wait()
	return
}
