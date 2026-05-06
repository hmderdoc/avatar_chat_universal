package dropfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleDoor32 = "2\r\n5\r\n38400\r\nSYNCHRONET\r\n42\r\nReal Name\r\nchairmanmow\r\n90\r\n120\r\n1\r\n3\r\n"

const sampleDoorSys = `COM1:
38400
8
2
38400
N
N
N
N
Real Name
Anytown, USA
555-1234
555-5678
PASSWORD
90
123
05/01/26
3600
60
GR
24
N
1,2,3
0
12/31/30
42
Z
10
20
0
1000
01/01/90
C:\BBS\MAIN\
C:\BBS\GEN\
Sysop Name
chairmanmow
00:00
Y
N
N
7
60
05/04/26
12:00
50
0
2048
1024
NONE
5
30
0
`

func TestParseDoor32(t *testing.T) {
	tmp := writeTemp(t, "DOOR32.SYS", sampleDoor32)
	u, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.Source != "DOOR32.SYS" {
		t.Errorf("source = %q, want DOOR32.SYS", u.Source)
	}
	if u.Handle != "chairmanmow" {
		t.Errorf("handle = %q, want chairmanmow", u.Handle)
	}
	if u.RealName != "Real Name" {
		t.Errorf("realname = %q", u.RealName)
	}
	if u.UserRecord != 42 {
		t.Errorf("user record = %d, want 42", u.UserRecord)
	}
	if u.Node != 3 {
		t.Errorf("node = %d, want 3", u.Node)
	}
	if u.CommType != CommTelnet {
		t.Errorf("comm type = %d, want %d", u.CommType, CommTelnet)
	}
	if u.SocketHandle != 5 {
		t.Errorf("socket handle = %d, want 5", u.SocketHandle)
	}
	if u.BBSID != "SYNCHRONET" {
		t.Errorf("bbsid = %q", u.BBSID)
	}
}

func TestParseDoorSys(t *testing.T) {
	tmp := writeTemp(t, "DOOR.SYS", sampleDoorSys)
	u, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.Source != "DOOR.SYS" {
		t.Errorf("source = %q, want DOOR.SYS", u.Source)
	}
	if u.Handle != "chairmanmow" {
		t.Errorf("handle = %q, want chairmanmow", u.Handle)
	}
	if u.RealName != "Real Name" {
		t.Errorf("realname = %q", u.RealName)
	}
	if u.UserRecord != 42 {
		t.Errorf("user record = %d, want 42", u.UserRecord)
	}
	if u.SysopName != "Sysop Name" {
		t.Errorf("sysop = %q", u.SysopName)
	}
	if u.Node != 2 {
		t.Errorf("node = %d, want 2", u.Node)
	}
	if u.Emulation != EmuANSI {
		t.Errorf("emulation = %d, want ANSI", u.Emulation)
	}
}

func TestParseUnknownByName(t *testing.T) {
	tmp := writeTemp(t, "WHATEVER.TXT", sampleDoor32)
	u, err := Parse(tmp)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.Source != "DOOR32.SYS" {
		t.Errorf("heuristic should detect DOOR32, got %q", u.Source)
	}
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(content, "\r\n", "\n")), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}
