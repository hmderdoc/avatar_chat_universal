//go:build !windows

package termio

import (
	"os"
	"syscall"

	"golang.org/x/term"
)

// pollableStdin returns a version of in whose Read honours
// SetReadDeadline, and a func that undoes it.
//
// Go opens stdin in blocking mode, and a blocking *os.File refuses
// deadlines (os.ErrNoDeadline). The terminal probes (color depth, sixel,
// audio) read with a deadline, so on a local terminal they gave up before
// reading a byte. Switching the descriptor to non-blocking and wrapping it
// again with os.NewFile puts it on Go's poller: deadlines then work, and a
// Read with no deadline still parks until input arrives.
//
// Only terminals are touched. A pipe's file description can be shared
// with the program on the other end, which may not expect O_NONBLOCK.
func pollableStdin(in *os.File) (*os.File, func()) {
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		return in, func() {}
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		return in, func() {}
	}
	f := os.NewFile(uintptr(fd), in.Name())
	if f == nil {
		_ = syscall.SetNonblock(fd, false)
		return in, func() {}
	}
	// The shell that started us shares this file description; leave it
	// blocking again on the way out.
	return f, func() { _ = syscall.SetNonblock(fd, false) }
}
