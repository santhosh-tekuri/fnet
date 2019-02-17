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
	"net"
	"syscall"
	"testing"

	"github.com/santhosh-tekuri/fnet"
)

func TestFirewall_AllowSelf(t *testing.T) {
	nw := fnet.New()
	earth, mars, venus := nw.Host("earth"), nw.Host("mars"), nw.Host("venus")

	elr, mlr, vlr := listen(t, earth, 80), listen(t, mars, 80), listen(t, venus, 80)
	dconn, aconn, err := dial(elr, mars)
	if err != nil {
		t.Fatal(err)
	}

	nw.SetFirewall(fnet.AllowSelf)
	ensureBroken(t, dconn)
	ensureBroken(t, aconn)
	if _, _, err := dial(elr, earth); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dial(mlr, mars); err != nil {
		t.Fatal(err)
	}
	if _, err := venus.Dial(mlr.Addr().String()); err == nil {
		t.Fatal()
	}
	if _, err := mars.Dial(vlr.Addr().String()); err == nil {
		t.Fatal()
	}
	if _, err := earth.Dial(mlr.Addr().String()); err == nil {
		t.Fatal("earth should not be able to dial mars")
	}
	if _, err := mars.Dial(elr.Addr().String()); err == nil {
		t.Fatal("mars should not be able to dial earth")
	}

	nw.SetFirewall(fnet.AllowAll)
	if _, _, err := dial(vlr, mars); err != nil {
		t.Fatal()
	}

	_, _, _ = elr, mlr, vlr
}

func TestFirewall_Split(t *testing.T) {
	nw := fnet.New()
	earth, mars, venus := nw.Host("earth"), nw.Host("mars"), nw.Host("venus")

	elr, mlr, vlr := listen(t, earth, 80), listen(t, mars, 80), listen(t, venus, 80)
	dconn, aconn, err := dial(elr, mars)
	if err != nil {
		t.Fatal(err)
	}

	nw.SetFirewall(fnet.Split([]string{"mars", "venus"}, fnet.AllowAll))
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
	if _, err := earth.Dial(mlr.Addr().String()); err == nil {
		t.Fatal("earth should not be able to dial mars")
	}
	if _, err := mars.Dial(elr.Addr().String()); err == nil {
		t.Fatal("mars should not be able to dial earth")
	}
}

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
