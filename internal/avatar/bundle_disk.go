// +build !go1.16

//go:build !go1.16

package avatar

import (
	"os"
	"path/filepath"
)

// LoadBundled is the Go 1.10 (XP target) fallback for the embed-based
// LoadBundled. It looks for a directory named "assets/avatars" next to
// the running binary and parses every .bin file in it. The Makefile's
// dist target ships those .bin files alongside the binary so a sysop
// who untars the legacy archive into one directory has the bundled
// collections available without any further configuration.
//
// The Source on each returned collection is "bundled" to match the
// embed path's labeling — the avatar selector UI doesn't care whether
// the asset came from the binary or the disk next to it.
func LoadBundled() ([]*Collection, error) {
	exe, err := os.Executable()
	if err != nil {
		// os.Executable can fail on some legacy POSIX setups. Fall
		// back to looking in the working directory rather than
		// returning empty — sysops who run the binary from its install
		// directory will still get the bundled collections.
		exe = "."
	}
	dir := filepath.Join(filepath.Dir(exe), "assets", "avatars")
	cs, err := LoadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		c.Source = "bundled"
	}
	return cs, nil
}
