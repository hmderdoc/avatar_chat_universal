//go:build !windows
// +build !windows

package termio

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

type socketConn struct {
	conn net.Conn
}

// NewSocketFD wraps a raw socket file descriptor (as supplied by DOOR32.SYS
// line 2) into a Conn. On *nix the FD is a kernel file descriptor; we wrap it
// with os.NewFile + net.FileConn so we get a real net.Conn with deadlines.
//
// "bad file descriptor" here almost always means the BBS that wrote
// DOOR32.SYS didn't actually inherit the socket fd to the door process
// — some Mystic-on-Linux configurations (and any BBS using FD_CLOEXEC
// on the listen socket, or piping through stdin/stdout instead of an
// inherited fd) do this. The error message points at -io stdio so a
// sysop hitting this can switch comm modes without grepping source.
func NewSocketFD(fd int) (Conn, error) {
	f := os.NewFile(uintptr(fd), fmt.Sprintf("door-fd-%d", fd))
	if f == nil {
		return nil, fmt.Errorf("termio: invalid fd %d", fd)
	}
	c, err := net.FileConn(f)
	// FileConn dups the fd internally; release our File reference.
	f.Close()
	if err != nil {
		return nil, fmt.Errorf(
			"termio: socket fd %d not usable in this process (%v); "+
				"the BBS may not be inheriting the socket fd to the door -- "+
				"try -io stdio if your BBS pipes the user connection through stdin/stdout",
			fd, err)
	}
	return &socketConn{conn: c}, nil
}

func (c *socketConn) Read(p []byte) (int, error)        { return c.conn.Read(p) }
func (c *socketConn) Write(p []byte) (int, error)       { return c.conn.Write(p) }
func (c *socketConn) Close() error                      { return c.conn.Close() }
func (c *socketConn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

// WriteNB implements termio.NonBlockingWriter. The fd is already in
// non-blocking mode (Go's net package manages it via the runtime poller), so
// a raw write(2) issued through RawConn.Control returns EAGAIN when the send
// buffer is full instead of blocking. We do a single attempt and report the
// short count; the caller retries the remainder on the next tick. If the conn
// can't expose a raw fd, we fall back to the blocking Write so behavior never
// regresses.
func (c *socketConn) WriteNB(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	sc, ok := c.conn.(syscall.Conn)
	if !ok {
		return c.conn.Write(p)
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return c.conn.Write(p)
	}
	var n int
	var werr error
	if cerr := raw.Control(func(fd uintptr) {
		n, werr = syscall.Write(int(fd), p)
	}); cerr != nil {
		return 0, cerr
	}
	if werr != nil {
		if werr == syscall.EAGAIN || werr == syscall.EWOULDBLOCK {
			return 0, nil // send buffer full; retry remainder next tick
		}
		return 0, werr
	}
	if n < 0 {
		n = 0
	}
	return n, nil
}
