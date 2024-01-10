package networkio

import (
	"net"
	"sync"
)

// closeOnceConn is a [net.Conn] where the Close method has once semantics.
//
// The zero value is invalid; use [newCloseOnceConn].
type closeOnceConn struct {
	// once ensures we close just once.
	once sync.Once

	// Conn is the underlying conn.
	net.Conn
}

var _ net.Conn = &closeOnceConn{}

// newCloseOnceConn creates a [closeOnceConn].
func newCloseOnceConn(conn net.Conn) *closeOnceConn {
	return &closeOnceConn{
		once: sync.Once{},
		Conn: conn,
	}
}

// Close implements net.Conn
func (c *closeOnceConn) Close() (err error) {
	c.once.Do(func() {
		err = c.Conn.Close()
	})
	return
}
