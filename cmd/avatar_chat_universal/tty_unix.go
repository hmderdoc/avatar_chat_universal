//go:build !windows
// +build !windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

func setupRawTTY() (restore func(), err error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return func() {}, nil
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("make tty raw: %v", err)
	}
	return func() { _ = term.Restore(fd, state) }, nil
}
