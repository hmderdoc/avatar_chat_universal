package dropfile

import (
	"fmt"
	"strconv"
	"strings"
)

func parseDoor32(data []byte) (*User, error) {
	lines := splitLines(data)
	if len(lines) < 11 {
		return nil, fmt.Errorf("dropfile: DOOR32.SYS needs 11 lines, got %d", len(lines))
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}

	commType, err := parseInt(lines[0], "DOOR32 line 1 (comm type)")
	if err != nil {
		return nil, err
	}
	socketHandle, err := parseInt(lines[1], "DOOR32 line 2 (socket handle)")
	if err != nil {
		return nil, err
	}
	bbsID := lines[3]
	userRec, err := parseInt(lines[4], "DOOR32 line 5 (user record)")
	if err != nil {
		return nil, err
	}
	realName := lines[5]
	handle := lines[6]
	secLevel, err := parseInt(lines[7], "DOOR32 line 8 (security level)")
	if err != nil {
		return nil, err
	}
	timeLeft, err := parseInt(lines[8], "DOOR32 line 9 (time left)")
	if err != nil {
		return nil, err
	}
	emu, err := parseInt(lines[9], "DOOR32 line 10 (emulation)")
	if err != nil {
		return nil, err
	}
	node, err := parseInt(lines[10], "DOOR32 line 11 (node)")
	if err != nil {
		return nil, err
	}

	return &User{
		BBSID:         bbsID,
		RealName:      realName,
		Handle:        handle,
		UserRecord:    userRec,
		SecurityLevel: secLevel,
		TimeLeftMin:   timeLeft,
		Node:          node,
		Emulation:     Emulation(emu),
		CommType:      CommType(commType),
		SocketHandle:  socketHandle,
		Source:        "DOOR32.SYS",
	}, nil
}

func parseInt(s, ctx string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("dropfile: %s: %v", ctx, err)
	}
	return n, nil
}
