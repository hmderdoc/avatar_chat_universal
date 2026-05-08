//go:build windows
// +build windows

package termio

import (
	"fmt"
	"io"
	"os"
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
	wsaeWouldBlock  = 10035
	wsaeConnAborted = 10053
	wsaeConnReset   = 10054
	wsaeShutdown    = 10058
	wsaeTimedOut    = 10060
)

// FIONBIO is the BSD-style "set non-blocking I/O" ioctl, fed through Winsock's
// WSAIoctl. The numeric encoding is fixed (IOC_IN | sizeof(u_long)<<16 | 'f'<<8
// | 126) -- always 0x8004667e on every Win32. We pass mode = 0 to *clear*
// non-blocking, i.e. force blocking-with-SO_RCVTIMEO.
const fionbio uint32 = 0x8004667E

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
//
// We force the socket into BLOCKING mode at adopt time. EleBBS (and likely
// other Win32 BBSes that use a select loop on the listener) hands us a socket
// that's still in non-blocking mode, which makes WSARecv return immediately
// with WSAEWOULDBLOCK ("A non-blocking socket operation could not be
// completed immediately") instead of waiting -- crashing whoever read first.
// The door owns the connection now; blocking + SO_RCVTIMEO is the model the
// rest of the code is written against.
func NewSocketFD(fd int) (Conn, error) {
	if fd <= 0 {
		return nil, fmt.Errorf("termio: invalid Winsock handle %d", fd)
	}
	h := windows.Handle(uintptr(fd))
	if err := setSocketBlocking(h); err != nil {
		// Non-fatal: surface the warning so the operator can see it, but
		// hand back a working Conn anyway. The defensive WSAEWOULDBLOCK
		// path in Read keeps the door functional even if FIONBIO failed --
		// the input pump just retries. Failing the whole call here would
		// be worse than running degraded.
		fmt.Fprintf(os.Stderr, "termio: warning: could not switch socket to blocking mode: %v\n", err)
	}
	return &socketConn{handle: h}, nil
}

// setSocketBlocking clears the non-blocking flag via FIONBIO. We use WSAIoctl
// rather than the legacy ioctlsocket() because golang.org/x/sys/windows
// exposes WSAIoctl directly; the FIONBIO opcode is identical between them.
func setSocketBlocking(h windows.Handle) error {
	var mode uint32 // 0 = blocking
	var bytesReturned uint32
	return windows.WSAIoctl(
		h, fionbio,
		(*byte)(unsafe.Pointer(&mode)), uint32(unsafe.Sizeof(mode)),
		nil, 0,
		&bytesReturned, nil, nil,
	)
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
			case wsaeTimedOut, wsaeWouldBlock:
				// WSAEWOULDBLOCK shouldn't happen after setSocketBlocking
				// succeeds at adopt time. Treat it as a timeout anyway so a
				// belt-and-braces non-blocking socket can't crash the door --
				// the input pump will just retry.
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
	if errno, ok := err.(syscall.Errno); ok {
		return int(errno), true
	}
	return 0, false
}
