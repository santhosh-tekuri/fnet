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
	"net"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestFirewall_AllowSelf(t *testing.T) {
	nw := New()
	earth, mars, venus := nw.Host("earth"), nw.Host("mars"), nw.Host("venus")

	elr, mlr, vlr := listen(t, earth, 80), listen(t, mars, 80), listen(t, venus, 80)
	dconn, aconn, err := dial(elr, mars)
	if err != nil {
		t.Fatal(err)
	}

	nw.SetFirewall(AllowSelf)
	ensureBroken(t, dconn)
	ensureBroken(t, aconn)
	if _, _, err := dial(elr, earth); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dial(mlr, mars); err != nil {
		t.Fatal(err)
	}
	if _, err := venus.Dial("tcp", mlr.Addr().String()); err == nil {
		t.Fatal()
	}
	if _, err := mars.Dial("tcp", vlr.Addr().String()); err == nil {
		t.Fatal()
	}
	if _, err := earth.Dial("tcp", mlr.Addr().String()); err == nil {
		t.Fatal("earth should not be able to dial mars")
	}
	if _, err := mars.Dial("tcp", elr.Addr().String()); err == nil {
		t.Fatal("mars should not be able to dial earth")
	}

	nw.SetFirewall(AllowAll)
	if _, _, err := dial(vlr, mars); err != nil {
		t.Fatal()
	}

	_, _, _ = elr, mlr, vlr
}

func TestFirewall_Split(t *testing.T) {
	nw := New()
	earth, mars, venus := nw.Host("earth"), nw.Host("mars"), nw.Host("venus")

	elr, mlr, vlr := listen(t, earth, 80), listen(t, mars, 80), listen(t, venus, 80)
	dconn, aconn, err := dial(elr, mars)
	if err != nil {
		t.Fatal(err)
	}

	nw.SetFirewall(Split([]string{"mars", "venus"}, AllowAll))
	ensureBroken(t, dconn)
	ensureBroken(t, aconn)
	if _, _, err := dial(elr, earth); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dial(mlr, mars); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dial(mlr, venus); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dial(vlr, mars); err != nil {
		t.Fatal(err)
	}
	if _, err := earth.Dial("tcp", mlr.Addr().String()); err == nil {
		t.Fatal("earth should not be able to dial mars")
	}
	if _, err := mars.Dial("tcp", elr.Addr().String()); err == nil {
		t.Fatal("mars should not be able to dial earth")
	}
}

// ensures that any read/write that were in progress,
// will result broken pipe on firewall restriction
func TestFirewall_InflightRW(t *testing.T) {
	nw, dconn, _, stop, err := makePipe("earth", "mars")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	// make netConn write timout
	makeWriteTimeout(t, dconn, 10)

	var startWg sync.WaitGroup
	startWg.Add(2)
	errorsCh := make(chan error, 2)
	go func() {
		startWg.Done()
		_, err := dconn.Read([]byte("hello"))
		errorsCh <- err
	}()
	go func() {
		startWg.Done()
		_, err := dconn.Write(make([]byte, 10))
		errorsCh <- err
	}()

	startWg.Wait()
	time.Sleep(2 * time.Second)
	nw.SetFirewall(AllowSelf)

	select {
	case err = <-errorsCh:
		ensureOpError(t, err, "", syscall.EPIPE)
	case <-time.After(5 * time.Second):
		t.Fatal("none failed")
	}
	select {
	case <-errorsCh:
		ensureOpError(t, err, "", syscall.EPIPE)
	case <-time.After(5 * time.Second):
		t.Fatal("only one failed")
	}
}

// --------------------------------------------------------------------------

func ensureBroken(t *testing.T, conn net.Conn) {
	t.Helper()
	if n, err := conn.Read(make([]byte, 10)); n != 0 || !isBrokenPipe(err) {
		t.Fatalf("dconn.Read: got n=%d err=%v, want n=0 brokenPipe", n, err)
	}
	if n, err := conn.Write(make([]byte, 10)); n != 0 || !isBrokenPipe(err) {
		t.Fatalf("dconn.Write: got n=%d err=%v, want n=0 brokenPipe", n, err)
	}
}

func isBrokenPipe(err error) bool {
	opError, ok := err.(*net.OpError)
	return ok && opError.Err == syscall.EPIPE
}
