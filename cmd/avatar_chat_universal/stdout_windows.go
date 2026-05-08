//go:build windows

package main

import (
	"io"

	"github.com/mattn/go-colorable"
)

// standaloneStdout returns a stdout writer that handles ANSI on Windows
// consoles which don't have ENABLE_VIRTUAL_TERMINAL_PROCESSING enabled
// (older Win10 builds, Server 2016, LTSB/LTSC images, some 32-bit Win10
// installations). go-colorable detects whether the underlying handle is
// a console with VT processing on:
//
//   - VT processing on  -> pass-through (no per-write overhead)
//   - VT processing off -> translate CSI sequences to Win32 console API
//     calls (SetConsoleTextAttribute, FillConsoleOutputCharacter, etc.)
//   - not a console (pipe, redirect, door-mode-with-stdio) -> pass-through
//
// So wrapping unconditionally on Windows is safe; the overhead is only
// paid by users on legacy consoles, who would otherwise see literal
// "^[[31m" garbage scroll down the screen.
func standaloneStdout() io.Writer {
	return colorable.NewColorableStdout()
}
