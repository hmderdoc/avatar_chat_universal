package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/dropfile"
)

// synchronetVersionPrefix starts line 4 of every DOOR32.SYS Synchronet
// writes (xtrn_sec.cpp: "Synchronet BBS for <platform>  Version <ver>").
const synchronetVersionPrefix = "Synchronet BBS for "

// isSynchronetDoor32 reports whether the drop file came from Synchronet and
// names a real user record.
func isSynchronetDoor32(user *dropfile.User) bool {
	return user != nil && user.Source == "DOOR32.SYS" && user.UserRecord >= 1 &&
		strings.HasPrefix(user.BBSID, synchronetVersionPrefix)
}

// resolveSbbsDataDir finds Synchronet's data dir: an explicit dir (flag or
// config) wins; otherwise main.ini [dir] data under $SBBSCTRL, relative to
// the ctrl dir; otherwise $SBBSCTRL/../data/. The second return says where
// the answer came from, for the stderr diagnostic.
func resolveSbbsDataDir(explicit string) (dir, source string, err error) {
	if explicit != "" {
		return toNativePath(explicit), "configured", nil
	}
	ctrl := os.Getenv("SBBSCTRL")
	if ctrl == "" {
		return "", "", fmt.Errorf("SBBSCTRL is not set and no synchronet_data_dir is configured")
	}
	ctrl = toNativePath(ctrl)
	if v, ok := readMainINI(filepath.Join(ctrl, "main.ini"), "dir", "data"); ok && v != "" {
		v = toNativePath(v)
		if !filepath.IsAbs(v) && !isWindowsAbs(v) {
			v = filepath.Join(ctrl, v)
		}
		return filepath.Clean(v), "main.ini", nil
	}
	return filepath.Join(ctrl, "..", "data"), "SBBSCTRL/../data", nil
}

// sbbsSystemName reads the BBS name from $SBBSCTRL/main.ini (the root-level
// name= key), or "" when unavailable.
func sbbsSystemName() string {
	ctrl := os.Getenv("SBBSCTRL")
	if ctrl == "" {
		return ""
	}
	v, _ := readMainINI(filepath.Join(toNativePath(ctrl), "main.ini"), "", "name")
	return v
}

// readMainINI returns key from section ("" = before any section header).
func readMainINI(path, section, key string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	cur := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			cur = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		if cur != strings.ToLower(section) {
			continue
		}
		if eq := strings.IndexByte(line, '='); eq >= 0 && strings.EqualFold(strings.TrimSpace(line[:eq]), key) {
			return strings.TrimSpace(line[eq+1:]), true
		}
	}
	return "", false
}

// toNativePath accepts either slash style: main.ini on Windows uses
// backslashes, and a sysop may paste either into avatar_chat.ini.
func toNativePath(p string) string {
	if filepath.Separator == '/' {
		return strings.ReplaceAll(p, `\`, "/")
	}
	return filepath.FromSlash(p)
}

func isWindowsAbs(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// selectBackend picks where avatars and handle styles live. mode is the
// avatar_store config key: "local" always uses the door's own store;
// "synchronet" requires the Synchronet backend (a failure is fatal, so a
// sysop who asked for it finds out); "auto" (default) uses Synchronet when
// the drop file says so and the data dir resolves, else falls back to local
// with one stderr line saying why. The bool reports Synchronet mode.
func selectBackend(mode, explicitDir string, user *dropfile.User, local *avatar.Store) (avatar.Backend, bool, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "local":
		return local, false, nil
	case "", "auto", "synchronet":
	default:
		return nil, false, fmt.Errorf("avatar_store = %q: want auto, synchronet or local", mode)
	}
	fail := func(why string) (avatar.Backend, bool, error) {
		if mode == "synchronet" {
			return nil, false, fmt.Errorf("avatar_store = synchronet: %s", why)
		}
		if isSynchronetDoor32(user) {
			fmt.Fprintf(os.Stderr, "avatar_chat_universal: Synchronet avatars unavailable (%s); using the door's own avatar store\n", why)
		}
		return local, false, nil
	}
	if user == nil || user.Source != "DOOR32.SYS" {
		return fail("drop file is not DOOR32.SYS")
	}
	if user.UserRecord < 1 {
		return fail("drop file has no user number")
	}
	if !strings.HasPrefix(user.BBSID, synchronetVersionPrefix) && mode != "synchronet" {
		return local, false, nil // another BBS: the private store, silently
	}
	dir, source, err := resolveSbbsDataDir(explicitDir)
	if err != nil {
		return fail(err.Error())
	}
	users := filepath.Join(dir, "user")
	if fi, err := os.Stat(users); err != nil || !fi.IsDir() {
		return fail(fmt.Sprintf("%s (from %s) is not a readable directory", users, source))
	}
	d, err := os.Open(users)
	if err == nil {
		_, err = d.Readdirnames(1)
		d.Close()
	}
	if err != nil && err != io.EOF {
		return fail(fmt.Sprintf("%s: %v", users, err))
	}
	return &avatar.SynchronetStore{DataDir: dir, UserNum: user.UserRecord}, true, nil
}

// synchronetBBSID is the host label in Synchronet mode when the sysop set
// no bbs_id: DOOR32.SYS line 4 is Synchronet's version notice, not a name,
// so use the system name from main.ini (else the hostname) and say so.
func synchronetBBSID(user *dropfile.User) string {
	name := sbbsSystemName()
	if name == "" {
		name, _ = os.Hostname()
	}
	if name == "" {
		name = "unknown"
	}
	fmt.Fprintf(os.Stderr, "avatar_chat_universal: bbs_id is not set; using %q instead of the drop file's %q. Set bbs_id in avatar_chat.ini.\n", name, user.BBSID)
	return name
}
