//go:build !windows

package main

import (
	"io"
	"os"
)

// standaloneStdout on non-Windows platforms is a straight pass-through.
// Linux/macOS/BSD terminals natively interpret ANSI escapes, and even
// non-terminal stdouts (pipes, redirects) preserve the bytes for the
// downstream consumer. No translation needed.
func standaloneStdout() io.Writer {
	return os.Stdout
}
