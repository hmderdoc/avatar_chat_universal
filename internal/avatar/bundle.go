package avatar

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Collection is a named group of avatars loaded from a .bin file. The Source
// field describes where it came from ("bundled", "sysop", or a path) so the
// selector UI can show provenance.
type Collection struct {
	Name    string
	Source  string
	Avatars []Avatar
	Title   string // SAUCE title if present
	Author  string // SAUCE author if present
}

// LoadDir scans dir for .bin files and returns the parsed collections. Missing
// directory is not an error (returns an empty slice).
//
// Uses ioutil.ReadDir / ioutil.ReadFile rather than the os.ReadDir family
// (Go 1.16+) so the same source compiles on Go 1.10 (XP target).
func LoadDir(dir string) ([]*Collection, error) {
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("avatar: read dir %s: %v", dir, err)
	}
	var out []*Collection
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".bin") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, err := ioutil.ReadFile(full)
		if err != nil {
			continue
		}
		c, err := ParseCollection(strings.TrimSuffix(e.Name(), ".bin"), data)
		if err != nil {
			continue
		}
		c.Source = full
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ParseCollection extracts avatars from a .bin payload. Handles SAUCE
// metadata (128-byte trailer), optional COMNT block (5+64*N bytes), and
// the optional 0x1A EOF marker that may sit between body and SAUCE.
//
// Trailing bytes that don't form a complete valid avatar are silently
// dropped — some real-world collections have padding after the last avatar.
func ParseCollection(name string, data []byte) (*Collection, error) {
	c := &Collection{Name: name}
	body := data

	if len(data) >= 128 && string(data[len(data)-128:len(data)-128+7]) == "SAUCE00" {
		sauce := data[len(data)-128:]
		c.Title = strings.TrimRight(string(sauce[7:7+35]), " \x00")
		c.Author = strings.TrimRight(string(sauce[42:42+20]), " \x00")
		commentCount := int(sauce[104])
		trailer := 128
		if commentCount > 0 {
			trailer += 5 + 64*commentCount
		}
		body = data[:len(data)-trailer]
		// Strip optional EOF marker (0x1A) just before the trailer.
		if len(body) > 0 && body[len(body)-1] == 0x1A {
			body = body[:len(body)-1]
		}
	}

	for i := 0; i+Bytes <= len(body); i += Bytes {
		a := Avatar(body[i : i+Bytes]).Clone()
		if err := a.Validate(); err != nil {
			continue // skip malformed avatars rather than reject the whole collection
		}
		c.Avatars = append(c.Avatars, a)
	}
	if len(c.Avatars) == 0 {
		return nil, fmt.Errorf("avatar: collection %s has no valid avatars", name)
	}
	return c, nil
}
