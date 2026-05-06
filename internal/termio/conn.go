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

type Mode string

const (
	ModeStdio  Mode = "stdio"
	ModeSocket Mode = "socket"
)
