package termio

import (
	"errors"
	"io"
	"os"
	"time"
)

type stdioConn struct {
	in  *os.File
	out io.Writer
}

// NewStdio returns a Conn backed by os.Stdin and os.Stdout.
func NewStdio() Conn {
	return &stdioConn{in: os.Stdin, out: os.Stdout}
}

// NewStdioWith returns a Conn backed by the supplied input file and
// output writer. Use this when the caller needs to wrap stdout, e.g.
// to layer ANSI-to-Win32 translation for legacy Windows consoles that
// don't speak VT output natively. The input handle stays an *os.File
// so deadline / non-blocking probes still work where the OS allows.
func NewStdioWith(in *os.File, out io.Writer) Conn {
	return &stdioConn{in: in, out: out}
}

func (c *stdioConn) Read(p []byte) (int, error)  { return c.in.Read(p) }
func (c *stdioConn) Write(p []byte) (int, error) { return c.out.Write(p) }
func (c *stdioConn) Close() error                { return nil }

// SetReadDeadline only works when stdin is a regular file or supports
// SetReadDeadline. When stdin is a terminal (the typical door case under
// stdio intercept), this returns nil but Read may not actually time out;
// callers that need polling should fall back to a goroutine + channel.
func (c *stdioConn) SetReadDeadline(t time.Time) error {
	type deadliner interface {
		SetReadDeadline(time.Time) error
	}
	// *os.File has SetReadDeadline since Go 1.10, but it's only honored
	// for non-blocking-capable file descriptors (pipes, sockets).
	if d, ok := any(c.in).(deadliner); ok {
		err := d.SetReadDeadline(t)
		if err != nil && !errors.Is(err, os.ErrInvalid) {
			return err
		}
		return nil
	}
	return nil
}
