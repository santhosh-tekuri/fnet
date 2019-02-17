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

func TestConnCloseError(t *testing.T) {
	nw := New()
	earth, mars := nw.Host("earth"), nw.Host("mars")
	lr := listen(t, earth, 80)
	c1, c2, err := dial(lr, mars)
	if err != nil {
		t.Fatal(err)
	}

	_ = c1.Close()

	if _, err := c1.Read(nil); err != io.ErrClosedPipe {
		t.Errorf("c1.Read() = %v, want io.ErrClosedPipe", err)
	}
	if _, err := c1.Write(nil); err != io.ErrClosedPipe {
		t.Errorf("c1.Write() = %v, want io.ErrClosedPipe", err)
	}
	if err := c1.SetDeadline(time.Time{}); err != io.ErrClosedPipe {
		t.Errorf("c1.SetDeadline() = %v, want io.ErrClosedPipe", err)
	}
	if _, err := c2.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("c2.Read() = %v, want io.EOF", err)
	}
}

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

// tests that if user's deadline is less than
// bucket wait, it should sleep and timeout
func TestConn_SleepAndTimeout(t *testing.T) {
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
	timeNow = orig

	var wg sync.WaitGroup
	wg.Add(2)
	defer wg.Wait()
	go func() {
		defer wg.Done()
		now := time.Now()
		if err := dconn.SetReadDeadline(now.Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		n, err := dconn.Read(make([]byte, 1))
		ensureTimeout(t, err)
		if n != 0 {
			t.Fatalf("got %d, want 0", n)
		}
		if wait := time.Now().Sub(now).Seconds(); wait < 2 {
			t.Fatalf("wait: got %f, want >=2", wait)
		}
	}()
	go func() {
		defer wg.Done()
		now := time.Now()
		if err := dconn.SetWriteDeadline(now.Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		n, err := dconn.Write(make([]byte, 1))
		ensureTimeout(t, err)
		if n != 0 {
			t.Fatalf("got %d, want 0", n)
		}
		if wait := time.Now().Sub(now).Seconds(); wait < 2 {
			t.Fatalf("wait: got %f, want >=2", wait)
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
