//go:build unix

package video

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// acquireSlot takes one of max playback slots shared by every door process
// on the host: slot-N.lock files under dir, held with a non-blocking
// flock. The kernel drops the lock if the process dies, so a crash never
// leaks a slot. ok is false when every slot is taken.
func acquireSlot(dir string, max int) (release func(), ok bool, err error) {
	if max <= 0 {
		max = DefaultMaxConcurrent
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	for i := 0; i < max; i++ {
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("slot-%d.lock", i)), os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return nil, false, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			continue
		}
		return func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			f.Close()
		}, true, nil
	}
	return nil, false, nil
}
