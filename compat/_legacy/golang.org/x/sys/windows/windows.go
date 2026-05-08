// Package windows is a minimal shim of golang.org/x/sys/windows for the
// avatar_chat_universal Go-1.10/XP-target build. It provides only the
// 11 symbols the door actually uses (Handle, GetConsoleMode,
// SetConsoleMode, Closesocket, WSARecv, WSASend, WSAIoctl, WSABuf,
// Setsockopt, SOL_SOCKET, SO_RCVTIMEO) by calling Win32 APIs through
// Go 1.10's stdlib `syscall` package against kernel32.dll and ws2_32.dll.
//
// The real x/sys/windows is too modern to compile on Go 1.10 (uses
// unsafe.Add, syscall.SyscallN, etc.) and 2018-era versions have their
// own internal bugs (zerrors_windows.go duplicate-const issues, missing
// setupapi function bodies). Hand-rolling the small subset we need is
// faster and more reliable than vendor archaeology.
//
// Public API mirrors x/sys/windows so socket_windows.go and
// tty_windows.go in the main project compile against either this shim
// or the real package without modification.
package windows

import (
	"syscall"
	"unsafe"
)

// Handle wraps a Win32 HANDLE / SOCKET. Defined as a distinct uintptr
// type (matching x/sys/windows) rather than a type alias so callers
// can't accidentally pass an arbitrary uintptr.
type Handle uintptr

// WSABuf mirrors the Winsock WSABUF struct: a length-and-pointer pair
// describing a single buffer in a vectored I/O call.
type WSABuf struct {
	Len uint32
	Buf *byte
}

// Overlapped mirrors the Win32 OVERLAPPED struct. We don't actually use
// overlapped I/O in this door, but the type has to exist so callers can
// pass nil through WSARecv / WSASend / WSAIoctl signatures.
type Overlapped struct {
	Internal     uintptr
	InternalHigh uintptr
	Offset       uint32
	OffsetHigh   uint32
	HEvent       Handle
}

// Setsockopt levels and option names. SOL_SOCKET = 0xFFFF is the only
// level we use; SO_RCVTIMEO is a per-socket DWORD timeout in ms.
const (
	SOL_SOCKET  = 0xffff
	SO_RCVTIMEO = 0x1006
)

// Lazy DLL handles + proc addresses. NewLazyDLL / NewProc exist in Go
// 1.10's syscall package; the actual LoadLibrary/GetProcAddress calls
// happen on the first invocation of each Proc.Addr().
var (
	modkernel32 = syscall.NewLazyDLL("kernel32.dll")
	modws2_32   = syscall.NewLazyDLL("ws2_32.dll")

	procGetConsoleMode = modkernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = modkernel32.NewProc("SetConsoleMode")
	procClosesocket    = modws2_32.NewProc("closesocket")
	procWSARecv        = modws2_32.NewProc("WSARecv")
	procWSASend        = modws2_32.NewProc("WSASend")
	procWSAIoctl       = modws2_32.NewProc("WSAIoctl")
	procSetsockopt     = modws2_32.NewProc("setsockopt")
)

// errnoErr converts a syscall.Errno into an error, preserving the
// concrete type so callers using type assertion (mapErrno() in
// socket_windows.go) can extract the WSA* code. Returns nil for the
// zero errno.
func errnoErr(e syscall.Errno) error {
	if e == 0 {
		return nil
	}
	return e
}

// GetConsoleMode reads the current input/output mode flags from a
// console handle. Returns nil on success; on failure the error is a
// syscall.Errno (call.GetLastError-equivalent).
func GetConsoleMode(handle Handle, mode *uint32) error {
	r1, _, e1 := syscall.Syscall(procGetConsoleMode.Addr(), 2,
		uintptr(handle), uintptr(unsafe.Pointer(mode)), 0)
	if r1 == 0 {
		return errnoErr(e1)
	}
	return nil
}

// SetConsoleMode writes new input/output mode flags. Used by the door's
// raw-TTY setup to clear cooked-mode flags and (best-effort) enable VT
// processing on stdin/stdout.
func SetConsoleMode(handle Handle, mode uint32) error {
	r1, _, e1 := syscall.Syscall(procSetConsoleMode.Addr(), 2,
		uintptr(handle), uintptr(mode), 0)
	if r1 == 0 {
		return errnoErr(e1)
	}
	return nil
}

// Closesocket releases a Winsock socket. Returns nil on success.
func Closesocket(s Handle) error {
	r1, _, e1 := syscall.Syscall(procClosesocket.Addr(), 1,
		uintptr(s), 0, 0)
	// closesocket returns 0 on success, SOCKET_ERROR (-1) on failure.
	if int32(r1) == -1 {
		return errnoErr(e1)
	}
	return nil
}

// WSARecv reads from a Winsock socket into one or more buffers. We only
// ever pass a single buffer (bufCount=1); the multi-buffer path exists
// for API parity. The completion-routine arg is *byte (matching x/sys's
// shape) so callers can pass nil cleanly without a uintptr conversion.
// Returns nil on success; on failure errno is a Winsock WSA* code.
func WSARecv(s Handle, bufs *WSABuf, bufCount uint32, recvd *uint32, flags *uint32, overlapped *Overlapped, croutine *byte) error {
	r1, _, e1 := syscall.Syscall9(procWSARecv.Addr(), 7,
		uintptr(s),
		uintptr(unsafe.Pointer(bufs)),
		uintptr(bufCount),
		uintptr(unsafe.Pointer(recvd)),
		uintptr(unsafe.Pointer(flags)),
		uintptr(unsafe.Pointer(overlapped)),
		uintptr(unsafe.Pointer(croutine)),
		0, 0)
	if r1 != 0 {
		return errnoErr(e1)
	}
	return nil
}

// WSASend writes a single buffer to a Winsock socket. Same vectored-I/O
// shape as WSARecv; we always pass bufCount=1.
func WSASend(s Handle, bufs *WSABuf, bufCount uint32, sent *uint32, flags uint32, overlapped *Overlapped, croutine *byte) error {
	r1, _, e1 := syscall.Syscall9(procWSASend.Addr(), 7,
		uintptr(s),
		uintptr(unsafe.Pointer(bufs)),
		uintptr(bufCount),
		uintptr(unsafe.Pointer(sent)),
		uintptr(flags),
		uintptr(unsafe.Pointer(overlapped)),
		uintptr(unsafe.Pointer(croutine)),
		0, 0)
	if r1 != 0 {
		return errnoErr(e1)
	}
	return nil
}

// WSAIoctl issues a Winsock I/O control. The door uses this once at
// adopt time with FIONBIO to flip the inherited socket from the BBS's
// non-blocking mode into blocking mode (the EleBBS / Win32-BBS-listener
// problem v0.1.5 fixed). 9 args is exactly what Syscall9 takes.
//
// Signature note: completionRoutine is uintptr (not *byte) to mirror
// the modern x/sys/windows WSAIoctl signature exactly. WSARecv and
// WSASend in modern x/sys take *byte for their croutine arg, but
// WSAIoctl takes uintptr -- an inconsistency in the upstream API we
// have to mirror so the same socket_windows.go callsites work against
// both this shim and the real package.
func WSAIoctl(s Handle, ioControlCode uint32, inBuffer *byte, cbInBuffer uint32, outBuffer *byte, cbOutBuffer uint32, bytesReturned *uint32, overlapped *Overlapped, completionRoutine uintptr) error {
	r1, _, e1 := syscall.Syscall9(procWSAIoctl.Addr(), 9,
		uintptr(s),
		uintptr(ioControlCode),
		uintptr(unsafe.Pointer(inBuffer)),
		uintptr(cbInBuffer),
		uintptr(unsafe.Pointer(outBuffer)),
		uintptr(cbOutBuffer),
		uintptr(unsafe.Pointer(bytesReturned)),
		uintptr(unsafe.Pointer(overlapped)),
		completionRoutine)
	if r1 != 0 {
		return errnoErr(e1)
	}
	return nil
}

// --- go-colorable-needed symbols ---
//
// go-colorable imports x/sys/windows aliased as `syscall` and pulls in
// CloseHandle, NewLazySystemDLL, and UTF16PtrFromString. We expose
// those here so the same colorable_windows.go binary works against
// either the real x/sys or this shim.

// CloseHandle releases a kernel32-managed handle (HANDLE). Used by
// go-colorable when it opens / dups CONOUT$ for legacy console probing.
func CloseHandle(handle Handle) error {
	procCloseHandle := modkernel32.NewProc("CloseHandle")
	r1, _, e1 := syscall.Syscall(procCloseHandle.Addr(), 1, uintptr(handle), 0, 0)
	if r1 == 0 {
		return errnoErr(e1)
	}
	return nil
}

// NewLazySystemDLL is x/sys's LoadLibrary-with-system-path-only wrapper;
// the safer-than-NewLazyDLL form mitigates DLL hijacking on hosts where
// the working directory has unwritten-by-admin DLLs. Stdlib syscall in
// Go 1.10 doesn't have the system-path variant, so we degrade to plain
// NewLazyDLL. Acceptable for our use case (BBS doors run in trusted
// sysop-controlled directories).
func NewLazySystemDLL(name string) *LazyDLL {
	return &LazyDLL{inner: syscall.NewLazyDLL(name)}
}

// LazyDLL wraps stdlib *syscall.LazyDLL with the same shape x/sys
// exposes (so callers can chain .NewProc).
type LazyDLL struct {
	inner *syscall.LazyDLL
}

// NewProc returns a lazily-bound proc address inside this DLL.
func (d *LazyDLL) NewProc(name string) *LazyProc {
	return &LazyProc{inner: d.inner.NewProc(name)}
}

// LazyProc mirrors x/sys's *LazyProc shape.
type LazyProc struct {
	inner *syscall.LazyProc
}

// Find resolves the proc address (no-op if already resolved). Returns
// nil on success.
func (p *LazyProc) Find() error { return p.inner.Find() }

// Addr returns the resolved proc address. Triggers Find() if needed.
func (p *LazyProc) Addr() uintptr { return p.inner.Addr() }

// Call invokes the proc with the supplied args. Mirrors x/sys's
// signature: returns r1, r2, lastErr.
func (p *LazyProc) Call(a ...uintptr) (uintptr, uintptr, error) {
	return p.inner.Call(a...)
}

// UTF16PtrFromString converts a Go string into a *uint16 pointing at
// a NUL-terminated UTF-16 buffer. Identical to stdlib syscall's same-
// named helper; re-exported here so go-colorable's calls resolve.
func UTF16PtrFromString(s string) (*uint16, error) {
	return syscall.UTF16PtrFromString(s)
}

// Setsockopt configures a per-socket option. The door uses this with
// (SOL_SOCKET, SO_RCVTIMEO) to install a per-read timeout via the
// Winsock-native DWORD-millisecond mechanism (rather than overlapped
// I/O + WaitForSingleObject, which would require IOCP plumbing).
func Setsockopt(s Handle, level int32, optname int32, optval *byte, optlen int32) error {
	r1, _, e1 := syscall.Syscall6(procSetsockopt.Addr(), 5,
		uintptr(s),
		uintptr(level),
		uintptr(optname),
		uintptr(unsafe.Pointer(optval)),
		uintptr(optlen),
		0)
	// setsockopt returns 0 on success, SOCKET_ERROR (-1) on failure.
	if int32(r1) == -1 {
		return errnoErr(e1)
	}
	return nil
}
