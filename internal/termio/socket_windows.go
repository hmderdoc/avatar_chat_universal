//go:build windows

package termio

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Standard Winsock errno values. golang.org/x/sys/windows doesn't export them
// as constants; we hard-code the well-known numerics rather than pulling in
// syscall just for this. WSA error codes are stable and documented by MS.
const (
	wsaeTimedOut    = 10060
	wsaeConnReset   = 10054
	wsaeConnAborted = 10053
	wsaeShutdown    = 10058
)

type socketConn struct {
	handle windows.Handle

	// SO_RCVTIMEO is set on the socket itself rather than per-call, so we
	// remember the last value we wrote and skip the syscall when the new
	// deadline rounds to the same timeout. Cuts a setsockopt off every
	// 100ms input-pump iteration.
	mu          sync.Mutex
	lastTimeout uint32
	timeoutSet  bool
}

// NewSocketFD wraps a Winsock SOCKET handle (as supplied by DOOR32.SYS line 2)
// into a Conn. Unlike on *nix, the value isn't a kernel file descriptor that
// net.FileConn can adopt; it's an opaque Winsock handle and we have to drive
// it directly via the Win32 socket API.
//
// We assume the BBS that handed us this socket has already done WSAStartup;
// every Win32 BBS that passes a SOCKET via DOOR32.SYS must have, otherwise
// the socket wouldn't be usable in this process.
func NewSocketFD(fd int) (Conn, error) {
	if fd <= 0 {
		return nil, fmt.Errorf("termio: invalid Winsock handle %d", fd)
	}
	return &socketConn{handle: windows.Handle(uintptr(fd))}, nil
}

func (c *socketConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	buf := windows.WSABuf{Len: uint32(len(p)), Buf: &p[0]}
	var n uint32
	var flags uint32
	err := windows.WSARecv(c.handle, &buf, 1, &n, &flags, nil, nil)
	if err != nil {
		// Map common Winsock errors to the shapes the rest of the code
		// expects. The input pump's isTimeout() looks for a Timeout()
		// method; the chat client treats io.EOF as a clean disconnect.
		if errno, ok := mapErrno(err); ok {
			switch errno {
			case wsaeTimedOut:
				return 0, timeoutError{}
			case wsaeConnReset, wsaeConnAborted, wsaeShutdown:
				return 0, io.EOF
			}
		}
		return 0, err
	}
	if n == 0 {
		// Per Winsock docs, recv returning 0 means orderly shutdown by peer.
		return 0, io.EOF
	}
	return int(n), nil
}

func (c *socketConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	buf := windows.WSABuf{Len: uint32(len(p)), Buf: &p[0]}
	var n uint32
	err := windows.WSASend(c.handle, &buf, 1, &n, 0, nil, nil)
	if err != nil {
		if errno, ok := mapErrno(err); ok {
			if errno == wsaeConnReset || errno == wsaeConnAborted || errno == wsaeShutdown {
				return int(n), io.EOF
			}
		}
		return int(n), err
	}
	return int(n), nil
}

func (c *socketConn) Close() error {
	return windows.Closesocket(c.handle)
}

// SetReadDeadline maps a Go-style deadline onto SO_RCVTIMEO. Windows treats
// SO_RCVTIMEO as a per-socket DWORD in milliseconds (not a timeval); a value
// of 0 means infinite, so we clamp expired/very-soon deadlines to 1ms rather
// than accidentally disabling the timeout. A zero time.Time disables the
// timeout (Go's "no deadline" sentinel).
func (c *socketConn) SetReadDeadline(t time.Time) error {
	var ms uint32
	if !t.IsZero() {
		d := time.Until(t)
		if d <= 0 {
			ms = 1
		} else {
			ms = uint32(d / time.Millisecond)
			if ms == 0 {
				ms = 1
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timeoutSet && ms == c.lastTimeout {
		return nil
	}
	if err := windows.Setsockopt(c.handle, windows.SOL_SOCKET, windows.SO_RCVTIMEO,
		(*byte)(unsafe.Pointer(&ms)), int32(unsafe.Sizeof(ms))); err != nil {
		return err
	}
	c.lastTimeout = ms
	c.timeoutSet = true
	return nil
}

// timeoutError satisfies the Timeout()-based duck-typing the input pump uses
// to distinguish a deadline expiry from a real error. Doesn't claim
// net.Error to keep the import surface small; only Timeout() and Error() are
// actually consulted.
type timeoutError struct{}

func (timeoutError) Error() string { return "i/o timeout" }
func (timeoutError) Timeout() bool { return true }

// mapErrno extracts the underlying syscall.Errno from a wrapped Winsock
// error. WSA* codes ride inside syscall.Errno and arrive wrapped by os
// helpers in some paths; both callers want the raw number.
func mapErrno(err error) (int, bool) {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return int(errno), true
	}
	return 0, false
}
