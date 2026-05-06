package dropfile

import (
	"fmt"
	"strconv"
	"strings"
)

// parseDoorSys parses the RBBS-PC 52-line DOOR.SYS format.
// Field map (1-based):
//   4  = node number
//   10 = user real name
//   15 = security level
//   19 = minutes remaining this call
//   20 = graphics mode (GR/NG/7E)
//   26 = user record number
//   35 = sysop name
//   36 = user handle/alias
// We only read the fields we use; the rest are tolerated.
func parseDoorSys(data []byte) (*User, error) {
	lines := splitLines(data)
	if len(lines) < 36 {
		return nil, fmt.Errorf("dropfile: DOOR.SYS needs at least 36 lines, got %d", len(lines))
	}
	get := func(n int) string {
		if n-1 >= len(lines) {
			return ""
		}
		return strings.TrimSpace(lines[n-1])
	}
	getInt := func(n int) int {
		v, _ := strconv.Atoi(get(n))
		return v
	}

	emu := EmuASCII
	switch strings.ToUpper(get(20)) {
	case "GR":
		emu = EmuANSI
	case "RIP":
		emu = EmuRIP
	}

	return &User{
		Node:          getInt(4),
		RealName:      get(10),
		SecurityLevel: getInt(15),
		TimeLeftMin:   getInt(19),
		Emulation:     emu,
		UserRecord:    getInt(26),
		SysopName:     get(35),
		Handle:        get(36),
		CommType:      CommLocal,
		Source:        "DOOR.SYS",
	}, nil
}
