package dropfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CommType int

const (
	CommLocal  CommType = 0
	CommSerial CommType = 1
	CommTelnet CommType = 2
	CommRaw    CommType = 3
	CommNTPipe CommType = 4
	CommDOS    CommType = 5
	CommStdio  CommType = 6
)

type Emulation int

const (
	EmuASCII Emulation = iota
	EmuANSI
	EmuAVATAR
	EmuRIP
	EmuMAXGRAPHIC
)

type User struct {
	BBSID         string
	SystemName    string
	SysopName     string
	RealName      string
	Handle        string
	UserRecord    int
	SecurityLevel int
	TimeLeftMin   int
	Node          int
	Emulation     Emulation
	CommType      CommType
	SocketHandle  int
	Source        string
}

func (u *User) DisplayName() string {
	if u.Handle != "" {
		return u.Handle
	}
	return u.RealName
}

func Parse(path string) (*User, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dropfile: read %s: %w", path, err)
	}
	base := strings.ToUpper(filepath.Base(path))
	switch {
	case strings.HasSuffix(base, "DOOR32.SYS"):
		return parseDoor32(data)
	case strings.HasSuffix(base, "DOOR.SYS"):
		return parseDoorSys(data)
	}
	lines := splitLines(data)
	switch {
	case len(lines) >= 11 && len(lines) <= 12 && looksLikeDoor32(lines):
		return parseDoor32(data)
	case len(lines) >= 50:
		return parseDoorSys(data)
	}
	return nil, fmt.Errorf("dropfile: cannot determine format for %s (%d lines)", base, len(lines))
}

func splitLines(data []byte) []string {
	s := string(data)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func looksLikeDoor32(lines []string) bool {
	if len(lines) < 11 {
		return false
	}
	for _, idx := range []int{0, 1, 2, 4, 7, 8, 10} {
		if !isAllDigits(strings.TrimSpace(lines[idx])) {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func trimNullPad(b []byte) []byte {
	return bytes.TrimRight(b, "\x00\r\n\t ")
}
