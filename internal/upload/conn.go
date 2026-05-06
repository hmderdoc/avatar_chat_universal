// Package upload receives files via classic BBS file-transfer protocols.
//
// Currently only ZMODEM-CRC32 is implemented (sufficient for the 120-byte
// avatar use case and what every modern terminal -- SyncTERM, NetRunner,
// fTelnet, mtelnet -- speaks natively). XMODEM was tried as a fallback but
// removed: terminals that auto-launch into Zmodem-send mode interpret our
// XMODEM 'C' polls as malformed Zmodem frames, which corrupts the wire.
// One protocol per upload.
package upload

import (
	"errors"
	"io"
	"time"
)

// ConnIO is what the upload layer needs from a connection: read with
// deadline, write, and the ability to clear deadlines. The termio.Conn
// interface satisfies this.
type ConnIO interface {
	io.Reader
	io.Writer
	SetReadDeadline(time.Time) error
}

var errTimeout = errors.New("read timeout")

func readByte(conn ConnIO, timeout time.Duration) (byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		// Connection doesn't support deadlines -- fall back to a blocking
		// read. (Unlikely in socket mode; possible in stdio-pipe mode.)
		var b [1]byte
		_, err := io.ReadFull(conn, b[:])
		if err != nil {
			return 0, err
		}
		return b[0], nil
	}
	defer conn.SetReadDeadline(time.Time{})
	var b [1]byte
	_, err := io.ReadFull(conn, b[:])
	if err != nil {
		if isTimeout(err) {
			return 0, errTimeout
		}
		return 0, err
	}
	return b[0], nil
}

func isTimeout(err error) bool {
	type timeouter interface{ Timeout() bool }
	if t, ok := err.(timeouter); ok {
		return t.Timeout()
	}
	return false
}
