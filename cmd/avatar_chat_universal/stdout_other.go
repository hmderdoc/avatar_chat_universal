//go:build !windows
// +build !windows

package main

import (
	"io"
	"os"

	"golang.org/x/term"
)

// standaloneStdout on non-Windows platforms is a straight pass-through.
// Linux/macOS/BSD terminals natively interpret ANSI escapes, and even
// non-terminal stdouts (pipes, redirects) preserve the bytes for the
// downstream consumer. No translation needed.
func standaloneStdout() io.Writer {
	return os.Stdout
}

// isLocalConsole reports whether stdout points at a local interactive
// terminal (as opposed to a pipe / file / socket). Used to decide
// whether the door should auto-default to UTF-8 charset and whether
// to put stdin in raw mode -- both are correct for local-test launches
// (sysop running with -dropfile from a shell) and wrong for BBS-spawned
// stdio modes (where stdin/stdout are pipes connected to the user's
// remote session).
func isLocalConsole() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
