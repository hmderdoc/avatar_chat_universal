// +build !go1.16

//go:build !go1.16

package ui

import (
	"io/ioutil"
	"os"
	"path/filepath"
)

// embeddedSplash is the Go 1.10 (XP target) fallback for the //go:embed
// version. We read splash.ans from a path next to the running binary
// at package init time, populating the same variable name the modern
// path uses so ShowSplash doesn't need to know which build it's in.
//
// If the file is missing, we leave embeddedSplash nil — ShowSplash
// already short-circuits when len(embeddedSplash) == 0, so the door
// just skips the splash and proceeds to the chat UI.
var embeddedSplash []byte

func init() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "splash.ans"),
		filepath.Join(filepath.Dir(exe), "assets", "splash.ans"),
	}
	for _, p := range candidates {
		data, err := ioutil.ReadFile(p)
		if err == nil {
			embeddedSplash = data
			return
		}
	}
}
