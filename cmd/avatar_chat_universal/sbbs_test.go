package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/config"
	"github.com/hmderdoc/avatar_chat_universal/internal/dropfile"
	"github.com/hmderdoc/termprobe"
)

// fakeSbbs lays out <root>/ctrl/main.ini and <root>/data/user/ and points
// SBBSCTRL at the ctrl dir.
func fakeSbbs(t *testing.T, dataKey string) (root string) {
	t.Helper()
	root = t.TempDir()
	ctrl := filepath.Join(root, "ctrl")
	for _, d := range []string{ctrl, filepath.Join(root, "data", "user")} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	ini := "name=Test BBS\noperator=Sysop\n\n[dir]\n\tdata=" + dataKey + "\n\texec=../exec/\n"
	if err := os.WriteFile(filepath.Join(ctrl, "main.ini"), []byte(ini), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SBBSCTRL", ctrl+"/")
	return root
}

func sbbsUser(num int) *dropfile.User {
	return &dropfile.User{
		Source:     "DOOR32.SYS",
		BBSID:      "Synchronet BBS for Linux  Version 3.22a",
		UserRecord: num,
		Handle:     "Tester",
	}
}

func TestResolveSbbsDataDirFromMainINI(t *testing.T) {
	root := fakeSbbs(t, "../data/")
	dir, source, err := resolveSbbsDataDir("")
	if err != nil || source != "main.ini" || dir != filepath.Join(root, "data") {
		t.Fatalf("dir=%q source=%q err=%v", dir, source, err)
	}
	if got := sbbsSystemName(); got != "Test BBS" {
		t.Errorf("system name = %q", got)
	}
}

func TestResolveSbbsDataDirWindowsStyle(t *testing.T) {
	root := fakeSbbs(t, `..\data\`)
	dir, _, err := resolveSbbsDataDir("")
	if err != nil || dir != filepath.Join(root, "data") {
		t.Fatalf("dir=%q err=%v", dir, err)
	}
}

func TestResolveSbbsDataDirExplicitAndFallback(t *testing.T) {
	if dir, source, _ := resolveSbbsDataDir("/x/data"); dir != "/x/data" || source != "configured" {
		t.Errorf("explicit: %q %q", dir, source)
	}
	ctrl := t.TempDir() // no main.ini
	t.Setenv("SBBSCTRL", ctrl)
	if dir, source, _ := resolveSbbsDataDir(""); dir != filepath.Join(ctrl, "..", "data") || source != "SBBSCTRL/../data" {
		t.Errorf("fallback: %q %q", dir, source)
	}
	t.Setenv("SBBSCTRL", "")
	if _, _, err := resolveSbbsDataDir(""); err == nil {
		t.Error("no SBBSCTRL and nothing configured should fail")
	}
}

func TestSelectBackend(t *testing.T) {
	root := fakeSbbs(t, "../data/")
	local := &avatar.Store{Root: t.TempDir(), BBSID: "x"}

	b, sbbs, err := selectBackend("auto", "", sbbsUser(12), local)
	if err != nil || !sbbs {
		t.Fatalf("auto on Synchronet: sbbs=%v err=%v", sbbs, err)
	}
	if s := b.(*avatar.SynchronetStore); s.UserNum != 12 || s.DataDir != filepath.Join(root, "data") {
		t.Errorf("store = %+v", s)
	}

	if _, sbbs, _ := selectBackend("local", "", sbbsUser(12), local); sbbs {
		t.Error("local must stay local")
	}

	mystic := sbbsUser(3)
	mystic.BBSID = "Mystic BBS"
	if _, sbbs, _ := selectBackend("auto", "", mystic, local); sbbs {
		t.Error("another BBS must use the local store")
	}

	standalone := &dropfile.User{Source: "standalone"}
	if b, sbbs, err := selectBackend("auto", "", standalone, local); sbbs || err != nil || b != local {
		t.Error("standalone must use the local store")
	}
	if _, _, err := selectBackend("synchronet", "", standalone, local); err == nil {
		t.Error("forced synchronet without a usable drop file must be fatal")
	}

	if _, sbbs, _ := selectBackend("auto", "", sbbsUser(0), local); sbbs {
		t.Error("user number 0 must fall back")
	}

	if _, sbbs, err := selectBackend("auto", filepath.Join(root, "nowhere"), sbbsUser(1), local); sbbs || err != nil {
		t.Errorf("bad dir should fall back quietly: sbbs=%v err=%v", sbbs, err)
	}
	if _, _, err := selectBackend("synchronet", filepath.Join(root, "nowhere"), sbbsUser(1), local); err == nil {
		t.Error("forced synchronet with a bad dir must be fatal")
	}
	if _, _, err := selectBackend("bogus", "", sbbsUser(1), local); err == nil {
		t.Error("unknown mode must be an error")
	}
}

func TestSynchronetBBSIDLabel(t *testing.T) {
	fakeSbbs(t, "../data/")
	u := sbbsUser(1)
	if got := synchronetBBSID(u); got != "Test BBS" {
		t.Errorf("label = %q", got)
	}
}

func TestAudioWantedGating(t *testing.T) {
	cfg := config.Default()
	var src io.Reader
	// A terminal that is not a CTerm is never sent the probe.
	if audioWanted(cfg, &termprobe.Result{}, nil, &src) {
		t.Fatal("non-CTerm terminal got audio")
	}
	cfg.TVAudio, cfg.AudioLinks = "off", "off"
	if audioWanted(cfg, &termprobe.Result{CTerm: "1.320"}, nil, &src) {
		t.Fatal("audio off everywhere still enabled it")
	}
	cfg.TVAudio = "on"
	if !audioWanted(cfg, nil, nil, &src) {
		t.Fatal("tv_audio = on should trust the sysop")
	}
}
