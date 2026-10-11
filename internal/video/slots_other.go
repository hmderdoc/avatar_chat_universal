//go:build !unix

package video

import "sync"

var (
	slotMu   sync.Mutex
	slotUsed int
)

// acquireSlot caps playbacks within this process only: there is no
// portable advisory lock here, and a Windows door runs one caller per
// process, so the host-wide cap is not enforced on Windows.
func acquireSlot(dir string, max int) (release func(), ok bool, err error) {
	if max <= 0 {
		max = DefaultMaxConcurrent
	}
	slotMu.Lock()
	defer slotMu.Unlock()
	if slotUsed >= max {
		return nil, false, nil
	}
	slotUsed++
	var once sync.Once
	return func() {
		once.Do(func() {
			slotMu.Lock()
			slotUsed--
			slotMu.Unlock()
		})
	}, true, nil
}
