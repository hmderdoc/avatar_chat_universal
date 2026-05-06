package avatar

import (
	"os"
	"testing"
)

func TestLoadBundledFindsCollections(t *testing.T) {
	cs, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	if len(cs) == 0 {
		t.Fatalf("expected at least one bundled collection")
	}
	totalAvatars := 0
	for _, c := range cs {
		if len(c.Avatars) == 0 {
			t.Errorf("collection %q has no avatars", c.Name)
		}
		for i, a := range c.Avatars {
			if err := a.Validate(); err != nil {
				t.Errorf("collection %q avatar %d: %v", c.Name, i, err)
			}
		}
		totalAvatars += len(c.Avatars)
	}
	t.Logf("loaded %d collections, %d avatars total", len(cs), totalAvatars)
}

func TestParseCollectionAgainstRealFile(t *testing.T) {
	// danger.bin is small (3729 bytes, 30 avatars) and well-formed.
	data, err := os.ReadFile("/sbbs/text/avatars/danger.bin")
	if err != nil {
		t.Skipf("source avatar file not present: %v", err)
	}
	c, err := ParseCollection("danger", data)
	if err != nil {
		t.Fatalf("ParseCollection: %v", err)
	}
	if len(c.Avatars) != 30 {
		t.Errorf("danger.bin: got %d avatars, want 30", len(c.Avatars))
	}
}

func TestParseCollectionEmptyData(t *testing.T) {
	if _, err := ParseCollection("empty", []byte{}); err == nil {
		t.Error("empty collection should fail to parse")
	}
}
