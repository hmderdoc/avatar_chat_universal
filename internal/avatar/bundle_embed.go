// +build go1.16

//go:build go1.16

package avatar

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed assets/avatars/*.bin
var bundledFS embed.FS

// LoadBundled returns all .bin collections compiled into the binary via
// //go:embed. This file is excluded from Go 1.10 builds (which lack embed
// and io/fs); the legacy half lives in bundle_disk.go and reads the same
// .bin files from a directory next to the binary.
func LoadBundled() ([]*Collection, error) {
	entries, err := fs.ReadDir(bundledFS, "assets/avatars")
	if err != nil {
		return nil, fmt.Errorf("avatar: read bundled fs: %v", err)
	}
	var out []*Collection
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".bin") {
			continue
		}
		data, err := fs.ReadFile(bundledFS, "assets/avatars/"+e.Name())
		if err != nil {
			continue
		}
		c, err := ParseCollection(strings.TrimSuffix(e.Name(), ".bin"), data)
		if err != nil {
			continue
		}
		c.Source = "bundled"
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
