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
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// tests that setting deadline applies to
// Read and Write operations which are sleeping.
func TestConn_ReadWrite_interruptSleepByDeadline(t *testing.T) {
	orig := timeNow
	defer func() {
		timeNow = orig
	}()

	nw := New()
	earth, mars := nw.Host("earth"), nw.Host("mars")
	lr := listen(t, earth, 80)
	dconn, _, err := dial(lr, mars)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	timeNow = func() time.Time {
		return now.Add(time.Hour)
	}
	nw.SetBandwidth("earth", "mars", Bandwidth(1024))
	timeNow = func() time.Time {
		return now
	}

	var wg sync.WaitGroup
	wg.Add(3)
	defer wg.Wait()
	ch := make(chan struct{})
	go func() {
		defer wg.Done()
		close(ch)
		time.Sleep(100 * time.Millisecond)
		if err := dconn.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}()
	go func() {
		defer wg.Done()
		<-ch
		n, err := dconn.Read(make([]byte, 1024))
		ensureTimeout(t, err)
		if n != 0 {
			t.Fatalf("Read: got %d, want 0", n)
		}
	}()
	go func() {
		defer wg.Done()
		<-ch
		n, err := dconn.Write(make([]byte, 1024))
		ensureTimeout(t, err)
		if n != 0 {
			t.Fatalf("Write: got %d, want 0", n)
		}
	}()
}

// tests that on closing any pending
// Read and Write operations which are sleeping return closed pipe.
func TestConn_ReadWrite_interruptSleepByClose(t *testing.T) {
	orig := timeNow
	defer func() {
		timeNow = orig
	}()

	nw := New()
	earth, mars := nw.Host("earth"), nw.Host("mars")
	lr := listen(t, earth, 80)
	dconn, _, err := dial(lr, mars)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	timeNow = func() time.Time {
		return now.Add(time.Hour)
	}
	nw.SetBandwidth("earth", "mars", Bandwidth(1024))
	timeNow = func() time.Time {
		return now
	}

	var wg sync.WaitGroup
	wg.Add(3)
	defer wg.Wait()
	ch := make(chan struct{})
	go func() {
		defer wg.Done()
		close(ch)
		time.Sleep(100 * time.Millisecond)
		if err := dconn.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	go func() {
		defer wg.Done()
		<-ch
		n, err := dconn.Read(make([]byte, 1024))
		ensureClosedPipe(t, err)
		if n != 0 {
			t.Fatalf("Read: got %d, want 0", n)
		}
	}()
	go func() {
		defer wg.Done()
		<-ch
		n, err := dconn.Write(make([]byte, 1024))
		ensureClosedPipe(t, err)
		if n != 0 {
			t.Fatalf("Write: got %d, want 0", n)
		}
	}()
}

func ensureTimeout(t *testing.T, err error) {
	t.Helper()
	if nerr, ok := err.(net.Error); ok {
		if !nerr.Timeout() {
			t.Errorf("err.Timeout() = false, want true")
		}
	} else {
		t.Errorf("got %T, want net.Error", err)
	}
}

func ensureClosedPipe(t *testing.T, err error) {
	t.Helper()
	if nerr, ok := err.(*net.OpError); ok {
		if nerr.Err != io.ErrClosedPipe {
			t.Errorf("got %v, want %v", nerr.Err, io.ErrClosedPipe)
		}
	} else {
		t.Errorf("got %T, want *net.OpError", err)
	}
}
