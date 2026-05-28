package termio

import (
	"io"
	"time"
)

// Conn is the abstraction the rest of the door uses for the user's terminal.
// stdio, telnet socket, and raw socket modes all satisfy this interface.
type Conn interface {
	io.ReadWriteCloser
	// SetReadDeadline sets a deadline for the next Read. Use a zero time to
	// clear. Implementations that cannot honor a deadline (e.g. plain stdin
	// without /dev/tty access) should return ErrNotSupported.
	SetReadDeadline(t time.Time) error
}

// NonBlockingWriter is an optional capability a Conn may implement. WriteNB
// writes as much of p as the kernel send buffer will accept right now without
// blocking, and returns the number of bytes accepted. A short return
// (n < len(p)), including 0, means the buffer is full and the caller should
// retry the remainder later — it is NOT an error. err is non-nil only for a
// genuine write failure. Callers that don't find this capability fall back to
// the blocking io.Writer.Write.
//
// This is the door's backpressure signal: over SSH, Synchronet relays the
// door's output through a loopback passthru socket into a fixed output
// ringbuffer; when the far end (the user's slow link) stalls, that buffer
// fills, the passthru thread stops draining the loopback socket, and a
// non-blocking write here finally short-returns instead of silently piling
// unbounded data into the pipeline.
type NonBlockingWriter interface {
	WriteNB(p []byte) (int, error)
}

type Mode string

const (
	ModeStdio  Mode = "stdio"
	ModeSocket Mode = "socket"
)
