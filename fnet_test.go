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
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestErrors(t *testing.T) {
	nw := New()
	earth, _ := nw.Host("earth"), nw.Host("mars")

	nw.SetBandwidth("pluto", "earth", NoLimit)

	_, err := earth.Listen("tcp", "venus")
	ensureOpError(t, err, "listen", nil)

	_, err = earth.Listen("tcp", "mars:80")
	ensureOpError(t, err, "listen", nil)

	_, err = earth.Dial("tcp", "venus")
	ensureOpError(t, err, "dial", nil)

	_, err = earth.Dial("tcp", "pluto:80")
	ensureOpError(t, err, "dial", nil)

	_, err = earth.Dial("tcp", "mars:80")
	ensureOpError(t, err, "dial", nil)

	lr, err := earth.Listen("tcp", ":80")
	if err != nil {
		t.Fatal(err)
	}
	_ = lr.(*listener).netL.Close() // close underlaying listener

	_, err = earth.Dial("tcp", "earth:80")
	ensureOpError(t, err, "dial", nil)
}

func TestCommunication(t *testing.T) {
	nw := New()
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

	e90, err := earth.Listen("tcp", ":90")
	if err != nil {
		t.Fatal("listen :90 failed")
	}
	if e90.Addr().String() != "earth:90" {
		t.Errorf("lr.Addr: got %s, want %s", e80.Addr(), "earth:90")
	}
	if _, err := earth.Listen("tcp", "earth:90"); err == nil {
		t.Error("listening twice on same port")
	}
	_ = e90.Close()
	listen(t, earth, 90)

}

func TestLookupPort(t *testing.T) {
	nw := New()
	earth := nw.Host("earth")
	lr, err := earth.Listen("tcp", "earth:http")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := lr.Addr().String(), "earth:80"; got != want {
		t.Fatalf("addr: got %s, want %s", got, want)
	}
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		conn, err := lr.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if got, want := conn.LocalAddr().String(), "earth:80"; got != want {
			t.Fatalf("addr: got %s, want %s", got, want)
		}
	}()
	conn, err := earth.Dial("tcp", "earth:http")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := conn.RemoteAddr().String(), "earth:80"; got != want {
		t.Fatalf("addr: got %s, want %s", got, want)
	}
	<-ch
}

func TestHostBandwidth(t *testing.T) {
	nw := New()
	earth, mars := nw.Host("earth"), nw.Host("mars")

	lnr := listen(t, earth, 80)

	writeRead := func(t *testing.T, start *sync.WaitGroup, wconn, rconn net.Conn, n int64) (w, r time.Duration) {
		ch := make(chan time.Duration)
		wb, rb := make([]byte, n), make([]byte, n)
		_, _ = rand.Read(wb)
		go func() {
			start.Done()
			start.Wait()
			now := time.Now()
			n, err := io.ReadFull(rconn, rb)
			rb = rb[0:n]
			if err != nil && err != io.ErrUnexpectedEOF {
				t.Fatal(err)
			}
			ch <- time.Now().Sub(now)
		}()
		start.Done()
		start.Wait()
		now := time.Now()
		wn, err := wconn.Write(wb)
		writeTook := time.Now().Sub(now)
		if err != nil {
			fmt.Println("ww", err)
			t.Fatalf("write failed: %v", err)
		}
		if wn != len(wb) {
			t.Fatalf("got %d, want %d", wn, len(wb))
		}
		_ = wconn.Close()
		readTook := <-ch

		if !bytes.Equal(wb, rb) {
			t.Fatal("bytes mismatch")
		}
		rn, err := rconn.Read(wb)
		if rn != 0 || err != io.EOF {
			t.Fatalf("got: %d %s, want 0 EOF", rn, err)
		}
		_ = rconn.Close()

		return writeTook, readTook
	}

	tests := []struct {
		bw     int64
		rounds int64
	}{
		{1024, 5},
		{500, 5},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("singleConn(%d,%d)", test.bw, test.rounds), func(t *testing.T) {
			dconn, aconn, err := dial(lnr, mars)
			if err != nil {
				t.Fatal(err)
			}
			nw.SetBandwidth("earth", "mars", Bandwidth(test.bw))
			wg := new(sync.WaitGroup)
			wg.Add(2)
			wd, rd := writeRead(t, wg, dconn, aconn, test.rounds*test.bw)
			t.Logf("write: %v, read: %v", wd, rd)
			if diff := math.Abs(wd.Seconds() - float64(test.rounds)); diff > 0.2 {
				t.Fatalf("writeTook unexpected: %s", wd)
			}
			if diff := math.Abs(rd.Seconds() - float64(test.rounds)); diff > 0.2 {
				t.Fatalf("readTook unexpected: %s", rd)
			}
		})
	}

	t.Run("resetBandwidth", func(t *testing.T) {
		dconn, aconn, err := dial(lnr, mars)
		if err != nil {
			t.Fatal(err)
		}
		nw.SetBandwidth("earth", "mars", NoLimit)
		wg := new(sync.WaitGroup)
		wg.Add(2)
		wd, rd := writeRead(t, wg, dconn, aconn, 10*1024*1024)
		t.Logf("write: %v, read: %v", wd, rd)
		if wd.Seconds() > 5 {
			t.Fatalf("writeTook unexpected: %s", wd)
		}
		if rd.Seconds() > 5 {
			t.Fatalf("readTook unexpected: %s", rd)
		}
	})
}

// -------------------------------------------------------

func listen(t *testing.T, host *Host, port int) net.Listener {
	t.Helper()
	lr, err := host.Listen("tcp", fmt.Sprintf("%s:%d", host.Name, port))
	if err != nil {
		t.Fatal(err)
	}
	return lr
}

func dial(lr net.Listener, host *Host) (dialed, accepted net.Conn, err error) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, errr := lr.Accept()
		accepted, err = conn, errr
	}()

	conn, errr := host.DialTimeout("tcp", lr.Addr().String(), 1*time.Second)
	if errr != nil {
		return nil, nil, errr
	}
	dialed = conn
	wg.Wait()
	return
}
