// Package bitmap parses the chat protocol's image messages and decodes
// them into per-cell color/character grids ready to render.
//
// Wire format (from /sbbs/xtrn/avatar_chat/avatar_chat.js:240-345):
//
//	[BITMAP|<width>|<height>|<fromName>|<hex-encoded-zlib-deflated-payload>]
//
// The deflated payload, once unpacked, is laid out as:
//
//	byte 0           : data height (rows)
//	bytes 1..N+1     : foreground color indices (one per cell, row-major)
//	bytes N+1..2N+1  : background color indices
//	bytes 2N+1..3N+1 : character codes (CP437)
//
// where N = (total_payload_length-1)/3 and width = N / height. Color
// indices are 8-bit; values 0-15 are CGA, 16-255 are extended palette.
package bitmap

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
)

// Cell is one position in a decoded image.
type Cell struct {
	Char byte  // CP437 byte
	FG   uint8 // ANSI 256-color index (0-15 are CGA)
	BG   uint8
}

// Image is a decoded BITMAP payload.
type Image struct {
	Width    int
	Height   int
	From     string
	Cells    [][]Cell // [row][col]
}

const prefix = "[BITMAP|"

// IsBitmap reports whether s appears to be a BITMAP message envelope.
// Cheap check; doesn't validate inner contents.
func IsBitmap(s string) bool {
	return strings.HasPrefix(s, prefix) && strings.HasSuffix(s, "]") && len(s) > len(prefix)+1
}

// Parse decodes a [BITMAP|...] envelope into an Image.
func Parse(s string) (*Image, error) {
	if !IsBitmap(s) {
		return nil, fmt.Errorf("bitmap: not a BITMAP message")
	}
	inner := s[1 : len(s)-1] // strip outer brackets
	parts := strings.SplitN(inner, "|", 5)
	if len(parts) != 5 || parts[0] != "BITMAP" {
		return nil, fmt.Errorf("bitmap: bad envelope: have %d parts", len(parts))
	}
	width, err := strconv.Atoi(parts[1])
	if err != nil || width < 1 {
		return nil, fmt.Errorf("bitmap: bad width %q", parts[1])
	}
	height, err := strconv.Atoi(parts[2])
	if err != nil || height < 1 {
		return nil, fmt.Errorf("bitmap: bad height %q", parts[2])
	}
	from := parts[3]
	hexData := parts[4]
	if len(hexData) == 0 || len(hexData)%2 != 0 {
		return nil, fmt.Errorf("bitmap: bad hex payload length %d", len(hexData))
	}

	compressed, err := hex.DecodeString(hexData)
	if err != nil {
		return nil, fmt.Errorf("bitmap: hex decode: %v", err)
	}
	decompressed, err := inflate(compressed)
	if err != nil {
		return nil, fmt.Errorf("bitmap: inflate: %v", err)
	}
	if len(decompressed) < 4 {
		return nil, fmt.Errorf("bitmap: payload too short (%d bytes)", len(decompressed))
	}

	dataHeight := int(decompressed[0])
	if dataHeight < 1 {
		return nil, fmt.Errorf("bitmap: header height = 0")
	}
	dataLen := len(decompressed) - 1
	slice := dataLen / 3
	totalPixels := slice
	dataWidth := totalPixels / dataHeight

	// If the announced dims don't match the actual payload, trust the payload.
	if width*height != totalPixels {
		width = dataWidth
		height = dataHeight
	}

	img := &Image{Width: width, Height: height, From: from}
	img.Cells = make([][]Cell, height)
	for y := 0; y < height; y++ {
		img.Cells[y] = make([]Cell, width)
	}

	for i := 0; i < totalPixels; i++ {
		y := i / width
		x := i % width
		if y >= height {
			break
		}
		fg := decompressed[1+i]
		bg := decompressed[1+slice+i]
		ch := decompressed[1+slice*2+i]
		if ch == 0 {
			ch = ' '
		}
		img.Cells[y][x] = Cell{Char: ch, FG: fg, BG: bg}
	}
	return img, nil
}

// inflate decompresses a zlib- or raw-deflate-encoded payload. The JS
// chat door uses zlib (with header); some senders may produce raw deflate.
// We try zlib first and fall back to raw deflate.
func inflate(compressed []byte) ([]byte, error) {
	if zr, err := zlib.NewReader(bytes.NewReader(compressed)); err == nil {
		defer zr.Close()
		out, ierr := ioutil.ReadAll(zr)
		if ierr == nil {
			return out, nil
		}
	}
	fr := flate.NewReader(bytes.NewReader(compressed))
	defer fr.Close()
	out, err := ioutil.ReadAll(fr)
	if err != nil {
		return nil, err
	}
	return out, nil
}
