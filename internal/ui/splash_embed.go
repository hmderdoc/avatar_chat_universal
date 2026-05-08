// +build go1.16

//go:build go1.16

package ui

import (
	_ "embed"
)

// Splash artwork is sourced from the repo root (./splash.ans). The
// Makefile copies it here before each build so //go:embed (which can
// only read paths at-or-below this file) can pick it up. To customize,
// drop your replacement at the repo's top-level splash.ans and rebuild
// -- don't edit this copy directly, it gets overwritten.
//
//go:embed splash.ans
var embeddedSplash []byte
