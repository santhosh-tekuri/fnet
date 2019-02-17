package fnet

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestConn_ReadWrite_interruptSleep(t *testing.T) {
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
