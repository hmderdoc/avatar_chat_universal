//go:build linux

package video

import "syscall"

// sysProcAttr puts a child in its own process group and has the kernel
// kill it if the door dies first (a dropped caller must not leave ffmpeg
// running).
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
