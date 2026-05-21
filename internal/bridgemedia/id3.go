package bridgemedia

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// Meta holds the fields we pull from an mp3's ID3v2 tag to build a richer
// bridged media card. Any field may be empty if the tag lacks it.
type Meta struct {
	Title  string
	Artist string
	Album  string
	Art    []byte
}

// ParseID3 reads an ID3v2 tag at the start of data and returns the title,
// artist, album, and cover art it finds. ok is false when there's no ID3v2 tag
// or nothing usable. It is deliberately conservative: any malformed or
// truncated field aborts cleanly so a weird tag yields empty metadata rather
// than a panic.
func ParseID3(data []byte) (Meta, bool) {
	var meta Meta
	if len(data) < 10 || data[0] != 'I' || data[1] != 'D' || data[2] != '3' {
		return meta, false
	}
	major := data[3]
	flags := data[5]
	size := synchsafe(data[6:10])
	if size <= 0 || 10+size > len(data) {
		return meta, false
	}
	body := data[10 : 10+size]

	if flags&0x80 != 0 { // global unsynchronisation
		body = deUnsync(body)
	}

	pos := 0
	if flags&0x40 != 0 { // skip extended header
		if len(body) < 4 {
			return meta, false
		}
		if major >= 4 {
			ext := synchsafe(body[0:4])
			if ext < 4 || ext > len(body) {
				return meta, false
			}
			pos = ext
		} else {
			ext := int(binary.BigEndian.Uint32(body[0:4]))
			if ext < 0 || 4+ext > len(body) {
				return meta, false
			}
			pos = 4 + ext
		}
	}

	found := false
	for {
		var id string
		var frame []byte
		if major >= 3 {
			if pos+10 > len(body) {
				break
			}
			id = string(body[pos : pos+4])
			var fsize int
			if major >= 4 {
				fsize = synchsafe(body[pos+4 : pos+8])
			} else {
				fsize = int(binary.BigEndian.Uint32(body[pos+4 : pos+8]))
			}
			fflags := body[pos+8 : pos+10]
			pos += 10
			if fsize <= 0 || pos+fsize > len(body) {
				break
			}
			frame = body[pos : pos+fsize]
			pos += fsize
			if major >= 4 && fflags[1]&0x02 != 0 { // per-frame unsync (v2.4)
				frame = deUnsync(frame)
			}
		} else {
			if pos+6 > len(body) {
				break
			}
			id = string(body[pos : pos+3])
			fsize := int(body[pos+3])<<16 | int(body[pos+4])<<8 | int(body[pos+5])
			pos += 6
			if fsize <= 0 || pos+fsize > len(body) {
				break
			}
			frame = body[pos : pos+fsize]
			pos += fsize
		}

		switch id {
		case "APIC", "PIC":
			if img, front, ok := parsePictureFrame(frame, id == "PIC"); ok && (front || meta.Art == nil) {
				meta.Art = img
				found = true
			}
		case "TIT2", "TT2":
			if meta.Title == "" {
				if v := decodeID3Text(frame); v != "" {
					meta.Title = v
					found = true
				}
			}
		case "TPE1", "TP1":
			if meta.Artist == "" {
				if v := decodeID3Text(frame); v != "" {
					meta.Artist = v
					found = true
				}
			}
		case "TALB", "TAL":
			if meta.Album == "" {
				if v := decodeID3Text(frame); v != "" {
					meta.Album = v
					found = true
				}
			}
		}
	}
	return meta, found
}

// parsePictureFrame extracts the picture bytes from an APIC (or v2.2 PIC) frame
// body and reports whether it is a front cover (picture type 0x03). v22 selects
// the fixed 3-char image-format field instead of a null-terminated MIME string.
func parsePictureFrame(frame []byte, v22 bool) ([]byte, bool, bool) {
	if len(frame) < 2 {
		return nil, false, false
	}
	enc := frame[0]
	i := 1
	if v22 {
		if i+3 > len(frame) {
			return nil, false, false
		}
		i += 3
	} else {
		j := i
		for j < len(frame) && frame[j] != 0x00 {
			j++
		}
		if j >= len(frame) {
			return nil, false, false
		}
		i = j + 1
	}
	if i >= len(frame) {
		return nil, false, false
	}
	picType := frame[i]
	i++
	if enc == 1 || enc == 2 { // UTF-16 description: 2-byte terminator
		for i+1 < len(frame) {
			if frame[i] == 0x00 && frame[i+1] == 0x00 {
				i += 2
				break
			}
			i += 2
		}
	} else {
		for i < len(frame) && frame[i] != 0x00 {
			i++
		}
		i++
	}
	if i >= len(frame) {
		return nil, false, false
	}
	return frame[i:], picType == 0x03, true
}

// decodeID3Text decodes a text frame body (leading encoding byte + bytes) per
// the ID3v2 text encodings and trims trailing NULs/whitespace.
func decodeID3Text(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	enc := b[0]
	payload := b[1:]
	var s string
	switch enc {
	case 1: // UTF-16 with BOM
		s = decodeUTF16(payload, binary.LittleEndian)
	case 2: // UTF-16BE, no BOM
		s = decodeUTF16(payload, binary.BigEndian)
	case 3: // UTF-8
		s = string(payload)
	default: // ISO-8859-1
		runes := make([]rune, 0, len(payload))
		for _, c := range payload {
			runes = append(runes, rune(c))
		}
		s = string(runes)
	}
	return strings.Trim(s, "\x00 \t\r\n")
}

func decodeUTF16(b []byte, def binary.ByteOrder) string {
	bo := def
	if len(b) >= 2 {
		if b[0] == 0xFE && b[1] == 0xFF {
			bo = binary.BigEndian
			b = b[2:]
		} else if b[0] == 0xFF && b[1] == 0xFE {
			bo = binary.LittleEndian
			b = b[2:]
		}
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, bo.Uint16(b[i:i+2]))
	}
	return string(utf16.Decode(u))
}

func synchsafe(b []byte) int {
	if len(b) < 4 {
		return 0
	}
	return int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
}

// deUnsync reverses ID3 unsynchronisation: any 0xFF 0x00 pair becomes 0xFF.
// Cover art (especially JPEG) is full of 0xFF bytes, so this matters when a tag
// is unsynchronised or the image would be corrupt.
func deUnsync(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == 0xFF && i+1 < len(b) && b[i+1] == 0x00 {
			i++
		}
	}
	return out
}
