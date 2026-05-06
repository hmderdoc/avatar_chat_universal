//go:build windows

package termio

import "fmt"

// NewSocketFD on Windows wraps a Winsock SOCKET handle from DOOR32.SYS line 2.
// Implementing this correctly requires golang.org/x/sys/windows — deferred to
// P9 (Windows smoke). For now stdio mode is the recommended Windows path.
func NewSocketFD(fd int) (Conn, error) {
	return nil, fmt.Errorf("termio: socket mode on Windows is not yet implemented; use stdio mode")
}
