//go:build unix && !linux

package video

import "syscall"

// sysProcAttr puts a child in its own process group. No Pdeathsig here:
// the duration cap and the door's own cleanup bound a stray child.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
