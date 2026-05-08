//go:build windows
// +build windows

package main

import (
	"io"
	"os"

	"github.com/mattn/go-colorable"
	"golang.org/x/sys/windows"
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

// isLocalConsole reports whether stdout points at a local interactive
// console (as opposed to a pipe / file / socket). On Windows we probe
// via GetConsoleMode -- a real console returns success; pipes and
// files return ERROR_INVALID_HANDLE. Used to decide whether to auto-
// default to UTF-8 and whether to put stdin in raw mode at startup.
func isLocalConsole() bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(os.Stdout.Fd()), &mode) == nil
}
