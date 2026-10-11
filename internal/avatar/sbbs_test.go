package avatar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
)

// sbbsUserINI has the shape of a real Synchronet user INI: avatar_lib.js
// and iniSetObject mix "key=value" with "\tkey = value", Synchronet's Date
// spelling with a space-padded day, exporter bookkeeping, and sections
// owned by other mods before and after.
const sbbsUserINI = "[avatar]\n" +
	"created=Aug 24 2025 19:42:28\n" +
	"data=OLDDATA\n" +
	"updated=Sep 28 2026 00:00:14\n" +
	"\tdisabled = false\n" +
	"export_count=63\n" +
	"last_exported=Oct  5 2026 00:31:10\n" +
	"\n" +
	"\n" +
	"[minesweeper]\n" +
	"\tselector = 0\n" +
	"\thighlight = true\n" +
	"\n" +
	"[msglist:mail:your]\n" +
	"\tlast_read_mail = 1048\n"

func fixtureINI(t *testing.T, a Avatar) string {
	t.Helper()
	return strings.Replace(sbbsUserINI, "OLDDATA", a.Base64(), 1)
}

func TestReadINISectionFirstWins(t *testing.T) {
	text := "[other]\ndata=x\n[Avatar]\n\tData = one\ndata=two\n[after]\ndata=y\n"
	vals, ok := readINISection(text, "avatar")
	if !ok || vals["data"] != "one" {
		t.Fatalf("vals = %v ok=%v, want data=one", vals, ok)
	}
	if _, ok := readINISection(text, "missing"); ok {
		t.Fatal("missing section reported found")
	}
}

func TestWriteINISectionPreservesEverythingElse(t *testing.T) {
	out := writeINISection(sbbsUserINI, "avatar", []iniSet{
		{key: "data", value: "NEW"},
		{key: "disabled", value: "true"},
		{key: "updated", value: "Oct 10 2026 12:00:00"},
		{key: "created", value: "never", onlyIfAbsent: true},
	})
	want := strings.NewReplacer(
		"data=OLDDATA", "data=NEW",
		"\tdisabled = false", "\tdisabled = true",
		"updated=Sep 28 2026 00:00:14", "updated=Oct 10 2026 12:00:00",
	).Replace(sbbsUserINI)
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestWriteINISectionAppendsMissingKeys(t *testing.T) {
	in := "[avatar]\ndata=X\n\n[other]\nk=v\n"
	out := writeINISection(in, "avatar", []iniSet{
		{key: "data", value: "Y"},
		{key: "updated", value: "U"},
		{key: "created", value: "C", onlyIfAbsent: true},
	})
	want := "[avatar]\ndata=Y\n\tupdated = U\n\tcreated = C\n\n[other]\nk=v\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestWriteINISectionMissingSectionAndEmptyFile(t *testing.T) {
	sets := []iniSet{{key: "data", value: "D"}}
	if got := writeINISection("", "avatar", sets); got != "[avatar]\n\tdata = D\n" {
		t.Errorf("empty file: %q", got)
	}
	if got := writeINISection("[other]\nk=v\n", "avatar", sets); got != "[other]\nk=v\n\n[avatar]\n\tdata = D\n" {
		t.Errorf("missing section: %q", got)
	}
}

func TestWriteINISectionCRLF(t *testing.T) {
	in := "[other]\r\nk=v\r\n[avatar]\r\ndata = X\r\n"
	out := writeINISection(in, "avatar", []iniSet{{key: "data", value: "Y"}, {key: "updated", value: "U"}})
	want := "[other]\r\nk=v\r\n[avatar]\r\ndata = Y\r\n\tupdated = U\r\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestWriteINISectionDuplicateKeyRewritesFirst(t *testing.T) {
	in := "[avatar]\ndata=A\ndata=B\n"
	out := writeINISection(in, "avatar", []iniSet{{key: "data", value: "C"}})
	if out != "[avatar]\ndata=C\ndata=B\n" {
		t.Fatalf("got %q", out)
	}
	if vals, _ := readINISection(out, "avatar"); vals["data"] != "C" {
		t.Fatalf("read after write = %q, want C", vals["data"])
	}
}

func newSbbsStore(t *testing.T) (*SynchronetStore, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "user"), 0755); err != nil {
		t.Fatal(err)
	}
	return &SynchronetStore{DataDir: dir, UserNum: 7}, filepath.Join(dir, "user", "0007.ini")
}

func TestSynchronetStoreReadsRealShape(t *testing.T) {
	s, path := newSbbsStore(t)
	a := Avatar(validBytes())
	if err := os.WriteFile(path, []byte(fixtureINI(t, a)), 0600); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Read("ignored")
	if err != nil || rec == nil {
		t.Fatalf("Read: %v %v", rec, err)
	}
	if rec.Avatar.Base64() != a.Base64() || rec.Disabled {
		t.Error("avatar or disabled wrong")
	}
	want := time.Date(2025, 8, 24, 19, 42, 28, 0, time.Local)
	if !rec.Created.Equal(want) {
		t.Errorf("created = %v, want %v (two-digit day)", rec.Created, want)
	}
	if rec.Updated.Day() != 28 {
		t.Errorf("updated = %v", rec.Updated)
	}
}

func TestSynchronetStampSingleDigitDay(t *testing.T) {
	got := parseStamp("Apr  9 2026 09:41:26")
	if got.Month() != time.April || got.Day() != 9 || got.Hour() != 9 {
		t.Fatalf("parseStamp = %v", got)
	}
	if !parseStamp("not a date").IsZero() {
		t.Fatal("junk date should be zero")
	}
}

func TestSynchronetStoreWriteRoundTrip(t *testing.T) {
	s, path := newSbbsStore(t)
	old := Avatar(validBytes())
	if err := os.WriteFile(path, []byte(fixtureINI(t, old)), 0600); err != nil {
		t.Fatal(err)
	}
	next := old.Clone()
	next[0] = 'Z'
	before := time.Now().Add(-time.Second)
	if err := s.Write("", &Record{Avatar: next}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	text := string(b)
	for _, keep := range []string{
		"created=Aug 24 2025 19:42:28\n",
		"export_count=63\n",
		"last_exported=Oct  5 2026 00:31:10\n",
		"[minesweeper]\n\tselector = 0\n\thighlight = true\n",
		"[msglist:mail:your]\n\tlast_read_mail = 1048\n",
		"\tdisabled = false\n",
		"data=" + next.Base64() + "\n",
	} {
		if !strings.Contains(text, keep) {
			t.Errorf("missing %q in:\n%s", keep, text)
		}
	}
	rec, err := s.Read("")
	if err != nil || rec == nil {
		t.Fatal(err)
	}
	if rec.Updated.Before(before.Truncate(time.Second)) {
		t.Errorf("updated not bumped: %v", rec.Updated)
	}
	if !strings.Contains(text, "updated="+rec.Updated.Format(SynchronetStampLayout)+"\n") {
		t.Errorf("updated not in Synchronet format:\n%s", text)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want 0600 kept", fi.Mode().Perm())
	}
	if matches, _ := filepath.Glob(path + ".tmp*"); len(matches) > 0 {
		t.Errorf("temp files left: %v", matches)
	}
}

func TestSynchronetStoreDisableToggle(t *testing.T) {
	s, path := newSbbsStore(t)
	a := Avatar(validBytes())
	if err := os.WriteFile(path, []byte(fixtureINI(t, a)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Disable("", true); err != nil {
		t.Fatal(err)
	}
	rec, _ := s.Read("")
	if rec == nil || !rec.Disabled || rec.Avatar.Base64() != a.Base64() {
		t.Fatalf("after disable: %+v", rec)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "\tdisabled = true\n") {
		t.Errorf("disabled line not rewritten in place:\n%s", b)
	}
	if err := s.Disable("", false); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.Read(""); rec.Disabled {
		t.Fatal("re-enable did not persist")
	}
}

func TestSynchronetStoreMissingFile(t *testing.T) {
	s, path := newSbbsStore(t)
	if rec, err := s.Read(""); rec != nil || err != nil {
		t.Fatalf("missing: %v %v", rec, err)
	}
	// Choosing "no avatar" records the choice so the door stops asking.
	if err := s.Write("", &Record{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Read("")
	if err != nil || rec == nil || !rec.Disabled || rec.Avatar != nil {
		t.Fatalf("disabled-only record: %+v %v", rec, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "[avatar]\n\tdisabled = true\n") || !strings.Contains(string(b), "\tcreated = ") {
		t.Errorf("new file:\n%s", b)
	}
}

func TestSynchronetStoreStyle(t *testing.T) {
	s, _ := newSbbsStore(t)
	st, err := s.ReadStyle("")
	if err != nil || !st.IsEmpty() {
		t.Fatalf("missing style: %+v %v", st, err)
	}
	in := &nickstyle.Style{Colors: []string{"#FF4747", "", "#f000d0", ""}, Tag: nickstyle.Tag{Text: "sysop", Fg: "#ff5"}}
	if err := s.WriteStyle("", in); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(s.stylePath())
	if err != nil {
		t.Fatal(err)
	}
	// JSON.stringify(record, null, 1) byte for byte.
	want := "{\n \"version\": 1,\n \"colors\": [\n  \"#ff4747\",\n  \"\",\n  \"#f000d0\"\n ],\n \"tag\": {\n  \"text\": \"sysop\",\n  \"fg\": \"#ffff55\",\n  \"bg\": \"\"\n }\n}"
	if string(b) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b, want)
	}
	got, _ := s.ReadStyle("")
	if len(got.Colors) != 3 || got.Tag.Fg != "#ffff55" {
		t.Fatalf("round trip: %+v", got)
	}
	if err := s.WriteStyle("", &nickstyle.Style{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.stylePath()); !os.IsNotExist(err) {
		t.Fatal("empty style should remove the file")
	}
}

func TestLocalStoreKeepsStyleAndAvatar(t *testing.T) {
	s := &Store{Root: t.TempDir(), BBSID: "x"}
	if err := s.WriteStyle("alice", &nickstyle.Style{Colors: []string{"#ff0000", "", "#00ff00"}, Tag: nickstyle.Tag{Text: "hi", Bg: "#000"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write("alice", &Record{Avatar: Avatar(validBytes())}); err != nil {
		t.Fatal(err)
	}
	st, err := s.ReadStyle("alice")
	if err != nil || len(st.Colors) != 3 || st.Colors[1] != "" || st.Tag.Bg != "#000000" {
		t.Fatalf("style lost after avatar write: %+v %v", st, err)
	}
	if err := s.Disable("alice", true); err != nil {
		t.Fatal(err)
	}
	rec, _ := s.Read("alice")
	if rec == nil || !rec.Disabled {
		t.Fatal("disable lost")
	}
	if st, _ := s.ReadStyle("alice"); st.Tag.Text != "hi" {
		t.Fatal("style lost after disable")
	}
}

func TestLocalStoreDisableWithoutAvatarIsASettledChoice(t *testing.T) {
	s := &Store{Root: t.TempDir(), BBSID: "x"}
	if err := s.Write("bob", &Record{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Read("bob")
	if err != nil || rec == nil || !rec.Disabled {
		t.Fatalf("got %+v %v", rec, err)
	}
	if err := s.Write("bob", &Record{}); err == nil {
		t.Fatal("empty enabled record should fail validation")
	}
}
