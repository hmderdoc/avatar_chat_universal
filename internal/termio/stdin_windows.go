//go:build windows

package termio

import "os"

// pollableStdin is a no-op on Windows: console handles take no deadlines,
// so the probes there still get no replies in standalone mode.
func pollableStdin(in *os.File) (*os.File, func()) { return in, func() {} }
