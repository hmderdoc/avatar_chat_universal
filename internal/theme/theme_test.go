package theme

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Every Attr field on Theme has a key, and every key a field.
func TestKeysCoverEverySlot(t *testing.T) {
	n := 0
	rt := reflect.TypeOf(Theme{})
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).Type == reflect.TypeOf(ansi.Attr(0)) {
			n++
		}
	}
	if n != len(Keys()) {
		t.Fatalf("%d Attr fields, %d keys", n, len(Keys()))
	}
	var th Theme
	seen := map[*ansi.Attr]string{}
	for _, k := range Keys() {
		p := colorSlot(&th, k)
		if p == nil {
			t.Fatalf("key %q has no slot", k)
		}
		if other, dup := seen[p]; dup {
			t.Fatalf("%q and %q share a slot", k, other)
		}
		seen[p] = k
	}
}

// The shipped themes are templates: each sets every key, cleanly.
func TestShippedThemes(t *testing.T) {
	files, _ := filepath.Glob("../../themes/*.ini")
	if len(files) < 2 {
		t.Fatalf("found %d theme files", len(files))
	}
	for _, f := range files {
		problems, err := Lint(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range problems {
			t.Errorf("%s: %s", filepath.Base(f), p)
		}
		body, _ := os.ReadFile(f)
		for _, k := range Keys() {
			if !strings.Contains(string(body), "\n"+k+" ") {
				t.Errorf("%s: doesn't set %s", filepath.Base(f), k)
			}
		}
	}
	// futurewave.ini documents the built-in values exactly.
	got, err := Load("../../themes/futurewave.ini")
	if err != nil {
		t.Fatal(err)
	}
	def := Default()
	for _, k := range Keys() {
		if *colorSlot(got, k) != *colorSlot(def, k) {
			t.Errorf("futurewave.ini %s differs from Default()", k)
		}
	}
}

func TestListAndLoadNamed(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Neon.ini", "name = Something Else\nbubble_self = black|bgcyan\n")
	write("bad name.ini", "")
	write("notes.txt", "")
	if got := List(dir); !reflect.DeepEqual(got, []string{"futurewave", "neon"}) {
		t.Fatalf("List = %v", got)
	}
	th, err := LoadNamed(dir, "NEON")
	if err != nil {
		t.Fatal(err)
	}
	if th.Name != "neon" || th.BubbleSelf != ansi.Black|ansi.BgCyan {
		t.Fatalf("loaded %q bubble_self %v", th.Name, th.BubbleSelf)
	}
	if th, err := LoadNamed(dir, "futurewave"); err != nil || th.Name != "futurewave" {
		t.Fatalf("built-in default without a file: %v %v", th, err)
	}
	for _, bad := range []string{"../neon", "nope", "", "a/b"} {
		if _, err := LoadNamed(dir, bad); err == nil {
			t.Errorf("LoadNamed(%q) succeeded", bad)
		}
	}
}

func TestLintFlagsMistakes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.ini")
	os.WriteFile(p, []byte("bubble_self = white|bg_blue\nbubbel_left = white\nmodal_text = blue|bgblue\nname = x\n"), 0o644)
	got, err := Lint(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("problems = %q", got)
	}
}
