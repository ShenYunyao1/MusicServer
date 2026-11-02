package netlimit

import (
	"net"
	"sync"
)

// Listener bounds accepted sockets, including requests that have not yet sent
// headers or authenticated. Close wakes both Accept and slot waiters.
type Listener struct {
	net.Listener
	slots chan struct{}
	done  chan struct{}
	once  sync.Once
	err   error
}

func New(inner net.Listener, maximum int) *Listener {
	return &Listener{Listener: inner, slots: make(chan struct{}, max(1, maximum)), done: make(chan struct{})}
}
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	case l.slots <- struct{}{}:
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &connection{Conn: c, release: func() { <-l.slots }}, nil
}
func (l *Listener) Close() error {
	l.once.Do(func() { close(l.done); l.err = l.Listener.Close() })
	return l.err
}

type connection struct {
	net.Conn
	once    sync.Once
	release func()
	err     error
}

func (c *connection) Close() error {
	c.once.Do(func() { c.err = c.Conn.Close(); c.release() })
	return c.err
}
