package netlimit

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestLimitReleaseAndCloseWakeWaiter(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := New(base, 1)
	defer l.Close()
	client, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	first, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		c   net.Conn
		err error
	}
	next := make(chan result, 1)
	go func() { c, e := l.Accept(); next <- result{c, e} }()
	secondClient, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer secondClient.Close()
	select {
	case r := <-next:
		if r.c != nil {
			r.c.Close()
		}
		t.Fatal("limit did not block second accept", r.err)
	case <-time.After(30 * time.Millisecond):
	}
	first.Close()
	first.Close()
	var second net.Conn
	select {
	case r := <-next:
		if r.err != nil {
			t.Fatal(r.err)
		}
		second = r.c
	case <-time.After(time.Second):
		t.Fatal("close did not release slot")
	}
	defer second.Close()
	go func() { c, e := l.Accept(); next <- result{c, e} }()
	l.Close()
	select {
	case r := <-next:
		if !errors.Is(r.err, net.ErrClosed) {
			t.Fatal(r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener close did not wake slot waiter")
	}
}
