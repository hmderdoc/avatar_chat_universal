//go:build windows

package video

import (
	"os/exec"
	"syscall"
)

func sysProcAttr() *syscall.SysProcAttr { return nil }

func lowerPriority(*exec.Cmd) {}

// killTree kills the child; its own children (yt-dlp's ffmpeg) are not
// tracked on Windows, and are bounded by the size and time caps.
func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
