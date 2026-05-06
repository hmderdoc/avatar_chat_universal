//go:build !windows

package termio

import (
	"fmt"
	"net"
	"os"
	"time"
)

type socketConn struct {
	conn net.Conn
}

// NewSocketFD wraps a raw socket file descriptor (as supplied by DOOR32.SYS
// line 2) into a Conn. On *nix the FD is a kernel file descriptor; we wrap it
// with os.NewFile + net.FileConn so we get a real net.Conn with deadlines.
func NewSocketFD(fd int) (Conn, error) {
	f := os.NewFile(uintptr(fd), fmt.Sprintf("door-fd-%d", fd))
	if f == nil {
		return nil, fmt.Errorf("termio: invalid fd %d", fd)
	}
	c, err := net.FileConn(f)
	// FileConn dups the fd internally; release our File reference.
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("termio: net.FileConn(fd=%d): %w", fd, err)
	}
	return &socketConn{conn: c}, nil
}

func (c *socketConn) Read(p []byte) (int, error)         { return c.conn.Read(p) }
func (c *socketConn) Write(p []byte) (int, error)        { return c.conn.Write(p) }
func (c *socketConn) Close() error                       { return c.conn.Close() }
func (c *socketConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
