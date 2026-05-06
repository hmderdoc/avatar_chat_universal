//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows console-mode flags. Listed explicitly so the flag intent is
// readable next to the SetConsoleMode call rather than buried behind
// upstream symbol names.
const (
	enableProcessedInput        uint32 = 0x0001
	enableLineInput             uint32 = 0x0002
	enableEchoInput             uint32 = 0x0004
	enableVirtualTerminalInput  uint32 = 0x0200
	enableProcessedOutput       uint32 = 0x0001
	enableVirtualTerminalOutput uint32 = 0x0004
	disableNewlineAutoReturn    uint32 = 0x0008
)

// setupRawTTY puts the Windows console into single-keystroke / VT-aware mode
// for standalone use. It deliberately does NOT use golang.org/x/term's
// MakeRaw, which always sets ENABLE_VIRTUAL_TERMINAL_INPUT in the same
// SetConsoleMode call as the cleared cooked-mode flags. On legacy ConHost
// (older Win10 / Server 2016 / VT-disabled envs) that combined call is
// rejected with ERROR_INVALID_PARAMETER, killing the door before it ever
// renders. Here we set the cooked-mode-cleared state first (the part that
// always works), then attempt VT input/output as separate best-effort
// follow-ups -- if the host doesn't support them, we still get a working
// raw stdin and the door can run, even if VT-only features (arrow keys
// from CSI sequences, ANSI color output) degrade.
func setupRawTTY() (restore func(), err error) {
	stdin := windows.Handle(os.Stdin.Fd())
	stdout := windows.Handle(os.Stdout.Fd())

	var origIn, origOut uint32
	inHasMode := windows.GetConsoleMode(stdin, &origIn) == nil
	outHasMode := windows.GetConsoleMode(stdout, &origOut) == nil

	if !inHasMode && !outHasMode {
		// Neither handle is a console (piped / redirected). Nothing to do.
		return func() {}, nil
	}

	if inHasMode {
		rawIn := origIn &^ (enableLineInput | enableEchoInput | enableProcessedInput)
		// Step 1: minimal raw-input mode. Required.
		if err := windows.SetConsoleMode(stdin, rawIn); err != nil {
			return nil, err
		}
		// Step 2: VT input as a separate optional call. Older ConHost
		// rejects this; ignore the failure rather than tear everything down.
		_ = windows.SetConsoleMode(stdin, rawIn|enableVirtualTerminalInput)
	}
	if outHasMode {
		// Best-effort VT processing on stdout so our ANSI escapes render
		// instead of showing up as literal "^[[31m" garbage. If unavailable,
		// the user sees garbled output but the door still runs and they
		// can at least quit cleanly.
		_ = windows.SetConsoleMode(stdout, origOut|enableVirtualTerminalOutput|disableNewlineAutoReturn)
	}

	restore = func() {
		if inHasMode {
			_ = windows.SetConsoleMode(stdin, origIn)
		}
		if outHasMode {
			_ = windows.SetConsoleMode(stdout, origOut)
		}
	}
	return restore, nil
}
