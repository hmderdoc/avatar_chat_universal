package termio

import (
	"io"
	"os"
	"time"
)

type stdioConn struct {
	in      *os.File
	out     io.Writer
	restore func()
}

// NewStdio returns a Conn backed by os.Stdin and os.Stdout.
func NewStdio() Conn {
	return NewStdioWith(os.Stdin, os.Stdout)
}

// NewStdioWith returns a Conn backed by the supplied input file and
// output writer. Use this when the caller needs to wrap stdout, e.g.
// to layer ANSI-to-Win32 translation for legacy Windows consoles that
// don't speak VT output natively. The input handle stays an *os.File
// so deadline / non-blocking probes still work where the OS allows.
// On a terminal, stdin is made pollable so SetReadDeadline works (see
// pollableStdin); Close puts it back.
func NewStdioWith(in *os.File, out io.Writer) Conn {
	in, restore := pollableStdin(in)
	return &stdioConn{in: in, out: out, restore: restore}
}

func (c *stdioConn) Read(p []byte) (int, error)  { return c.in.Read(p) }
func (c *stdioConn) Write(p []byte) (int, error) { return c.out.Write(p) }
func (c *stdioConn) Close() error {
	if c.restore != nil {
		c.restore()
		c.restore = nil
	}
	return nil
}

// SetReadDeadline works when stdin is on Go's poller: a terminal (made so
// by pollableStdin on Unix), a socket or a non-blocking pipe. Elsewhere it
// returns os.ErrNoDeadline and callers carry on without the reply.
func (c *stdioConn) SetReadDeadline(t time.Time) error {
	type deadliner interface {
		SetReadDeadline(time.Time) error
	}
	// *os.File has SetReadDeadline since Go 1.10, but it's only honored
	// for non-blocking-capable file descriptors (pipes, sockets).
	if d, ok := interface{}(c.in).(deadliner); ok {
		err := d.SetReadDeadline(t)
		if err != nil && err != os.ErrInvalid {
			return err
		}
		return nil
	}
	return nil
}
