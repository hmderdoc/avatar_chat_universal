package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/config"
	"github.com/hmderdoc/avatar_chat_universal/internal/dropfile"
	"github.com/hmderdoc/avatar_chat_universal/internal/termio"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
	"github.com/hmderdoc/avatar_chat_universal/internal/ui"
	"github.com/hmderdoc/avatar_chat_universal/internal/upload"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "avatar_chat_universal: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		dropPath       = flag.String("dropfile", "", "path to DOOR32.SYS or DOOR.SYS (omit for standalone mode)")
		ioMode         = flag.String("io", "", "I/O mode: stdio | socket (default: auto from drop file)")
		configPath     = flag.String("config", defaultConfigPath(), "path to door config (avatar_chat.ini)")
		dataDir        = flag.String("data", defaultDataDir(), "path to per-user data directory")
		sysopAvatarDir = flag.String("sysop-avatars", "", "directory of sysop-managed .bin avatar collections (overrides config)")
		forceSelect    = flag.Bool("select", false, "always show the avatar selector, even if user has one stored")
		charsetFlag    = flag.String("charset", "", "output charset: cp437 (BBS default) or utf8 (overrides config)")
		colsFlag       = flag.Int("cols", 0, "screen columns; 0 = auto-detect via terminal query, fall back to 80")
		rowsFlag       = flag.Int("rows", 0, "screen rows; 0 = auto-detect via terminal query, fall back to 24")
		userFlag       = flag.String("user", "", "username (standalone mode only; defaults to $USER)")
		bbsFlag        = flag.String("bbs", "", "BBS / system name to advertise (standalone mode only; defaults to hostname)")
	)
	flag.Parse()

	var user *dropfile.User
	standalone := *dropPath == ""
	if standalone {
		// Run as a regular CLI app: synthesize a stub User from env
		// vars / flags, put the local TTY into raw mode, behave the
		// same as door mode otherwise.
		user = standaloneUser(*userFlag, *bbsFlag)
		restore, rerr := setupRawTTY()
		if rerr != nil {
			return fmt.Errorf("standalone: %w", rerr)
		}
		defer restore()
	} else {
		var err error
		user, err = dropfile.Parse(*dropPath)
		if err != nil {
			return err
		}
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	conn, err := openConn(*ioMode, user)
	if err != nil {
		return fmt.Errorf("open termio: %w", err)
	}
	defer conn.Close()

	// Detect screen size BEFORE the input pump starts, otherwise the pump
	// would consume the terminal's response packet as if it were keystrokes.
	cols, rows := resolveScreen(*colsFlag, *rowsFlag, cfg, conn)

	input := ansi.NewInput(conn)

	bbsID := resolveBBSID(cfg, user)
	store := &avatar.Store{Root: *dataDir, BBSID: bbsID}

	// Standalone mode (no drop file) defaults to UTF-8 since regular
	// terminals (Linux console, Terminal.app, iTerm, kitty, Windows
	// Terminal) interpret stdin/stdout as UTF-8 by default and choke on
	// raw CP437 high bytes. Door-mode users (BBS clients) keep the
	// config's CP437 default. Explicit -charset always wins.
	charsetCfg := cfg.OutputCharset
	if standalone && *charsetFlag == "" && charsetCfg == "" {
		charsetCfg = "utf8"
	} else if standalone && *charsetFlag == "" {
		// Config might say cp437 but the user almost certainly didn't
		// pre-tune it for standalone mode. Override to utf8.
		charsetCfg = "utf8"
	}
	charset := resolveCharset(*charsetFlag, charsetCfg)

	collections, err := loadCollections(cfg, *sysopAvatarDir)
	if err != nil {
		return err
	}

	greet(conn, user, bbsID, *configPath, *dataDir, len(collections))

	rec, err := store.Read(user.DisplayName())
	if err != nil {
		fmt.Fprintf(conn, "warning: could not read avatar: %v\r\n", err)
	}

	needsSelect := *forceSelect || rec == nil || rec.Avatar == nil || rec.Disabled
	if needsSelect {
		fmt.Fprintf(conn, "\r\nNo avatar yet -- let's pick one. Press any key to continue...\r\n")
		_, _, _ = input.Next(30 * time.Second)

		sel := avatar.NewSelector(collections, conn, input, cols, rows)
		sel.Charset = charset
		action, picked, err := sel.Run()
		if err != nil {
			return fmt.Errorf("selector: %w", err)
		}
		switch action {
		case avatar.SelectorPicked:
			rec = &avatar.Record{Avatar: picked.Clone()}
		case avatar.SelectorDisable:
			rec = &avatar.Record{Disabled: true}
		case avatar.SelectorUpload, avatar.SelectorEditor:
			ansi.Reset(conn)
			ansi.ClearScreen(conn)
			fmt.Fprintf(conn, "Upload/editor flow not available pre-chat. Use /avatar from chat instead.\r\n")
			rec = &avatar.Record{Disabled: true}
		default: // SelectorCancelled
			ansi.Reset(conn)
			ansi.ClearScreen(conn)
			fmt.Fprintf(conn, "Avatar selection cancelled. Goodbye, %s.\r\n", user.DisplayName())
			return nil
		}
		if err := store.Write(user.DisplayName(), rec); err != nil {
			return fmt.Errorf("save avatar: %w", err)
		}
	}

	ansi.Reset(conn)
	ansi.ClearScreen(conn)

	// Connect to chat.
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	fmt.Fprintf(conn, "Connecting to chat server %s...\r\n", addr)

	avatarB64 := ""
	if rec != nil && rec.Avatar != nil && !rec.Disabled {
		avatarB64 = rec.Avatar.Base64()
	}
	self := &chat.Nick{
		Name:   user.DisplayName(),
		Host:   bbsID,
		Avatar: avatarB64,
	}

	client := chat.NewClient(addr)
	client.Nick = self.Name
	client.System = bbsID

	session := chat.NewSession(client, self, cfg.DefaultChannel)
	session.MaxBuffer = cfg.MaxHistory
	if session.MaxBuffer < 100 {
		session.MaxBuffer = 200
	}
	if cfg.MotdChannel != "" {
		session.MotdChannel = cfg.MotdChannel
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := session.Connect(ctx, cfg.MaxHistory); err != nil {
		fmt.Fprintf(conn, "Could not connect: %v\r\n", err)
		fmt.Fprintf(conn, "Press any key to exit.\r\n")
		_, _, _ = input.Next(30 * time.Second)
		return nil
	}
	defer session.Close()

	app := ui.NewApp(conn, input, session, cols, rows, charset)
	app.ServerAddr = addr
	app.IdleEnabled = cfg.IdleEnabled
	if cfg.IdleTimeoutSeconds > 0 {
		app.IdleTimeout = time.Duration(cfg.IdleTimeoutSeconds) * time.Second
	}
	if cfg.IdleSwitchInterval > 0 {
		app.IdleSwitch = time.Duration(cfg.IdleSwitchInterval) * time.Second
	}
	if cfg.IdleFPS > 0 {
		app.IdleFPS = cfg.IdleFPS
	}
	app.IdleRandom = cfg.IdleRandom
	app.IdleSequence = cfg.IdleSequence
	app.IdleDisable = cfg.IdleDisable
	app.FigletMessages = cfg.FigletMessages
	app.FigletColors = cfg.FigletColors
	app.FigletMove = cfg.FigletMove
	app.AnsiGalleryFiles = scanAnsiGallery(cfg.AnsiGalleryDir)
	app.IdleInterleaveAnsi = cfg.IdleInterleaveAnsi

	// Theme overlay. Main config sets the baseline; theme can override
	// any color or screensaver field. Missing theme files are not fatal --
	// the door always works in "futurewave" defaults.
	if cfg.Theme != "" {
		themePath := cfg.Theme
		if !strings.HasSuffix(strings.ToLower(themePath), ".ini") {
			themePath = filepath.Join("themes", themePath+".ini")
		}
		if !filepath.IsAbs(themePath) {
			if wd, werr := os.Getwd(); werr == nil {
				themePath = filepath.Join(wd, themePath)
			}
		}
		if t, terr := theme.Load(themePath); terr == nil && t != nil {
			app.ApplyTheme(t)
		}
	}
	app.OnAvatarSet = func(b64 string) error {
		a, err := avatar.FromBase64(b64)
		if err != nil {
			return err
		}
		return store.Write(user.DisplayName(), &avatar.Record{Avatar: a})
	}
	app.OnAvatarDisable = func() error {
		return store.Disable(user.DisplayName(), true)
	}
	app.OnAvatarPick = func() (string, string, error) {
		sel := avatar.NewSelector(collections, conn, input, cols, rows)
		sel.Charset = charset
		action, picked, err := sel.Run()
		if err != nil {
			return "", "", err
		}
		switch action {
		case avatar.SelectorPicked:
			if err := store.Write(user.DisplayName(), &avatar.Record{Avatar: picked.Clone()}); err != nil {
				return "", "", err
			}
			return picked.Base64(), "picked", nil
		case avatar.SelectorUpload:
			return "", "upload", nil
		case avatar.SelectorDisable:
			return "", "disable", nil
		case avatar.SelectorEditor:
			return "", "editor", nil
		}
		return "", "cancelled", nil
	}
	app.OnAvatarUpload = func() (string, error) {
		var data []byte
		err := input.WithRawAccess(conn, func() error {
			received, _, zerr := upload.ReceiveZMODEM(conn, 4096)
			if zerr != nil {
				return zerr
			}
			data = received
			return nil
		})
		if err != nil {
			return "", err
		}
		// Take the first 120 bytes; senders may pad to a power-of-two block.
		if len(data) < avatar.Bytes {
			return "", fmt.Errorf("received %d bytes, need %d", len(data), avatar.Bytes)
		}
		a := avatar.Avatar(data[:avatar.Bytes])
		if err := a.Validate(); err != nil {
			return "", fmt.Errorf("invalid avatar: %w", err)
		}
		if err := store.Write(user.DisplayName(), &avatar.Record{Avatar: a.Clone()}); err != nil {
			return "", err
		}
		return a.Base64(), nil
	}
	app.OnAvatarEditor = func() (string, error) {
		// Seed the editor with the user's current avatar if they have one.
		var baseline avatar.Avatar
		if rec, _ := store.Read(user.DisplayName()); rec != nil && len(rec.Avatar) == avatar.Bytes {
			baseline = rec.Avatar.Clone()
		}
		ed := avatar.NewEditor(conn, input, cols, rows, baseline, collections)
		ed.Charset = charset
		action, result, eerr := ed.Run()
		if eerr != nil {
			return "", eerr
		}
		if action != avatar.EditorSaved {
			return "", nil
		}
		if err := result.Validate(); err != nil {
			return "", fmt.Errorf("invalid avatar: %w", err)
		}
		if err := store.Write(user.DisplayName(), &avatar.Record{Avatar: result.Clone()}); err != nil {
			return "", err
		}
		return result.Base64(), nil
	}
	// Splash screen before chat opens. Auto-dismisses after the configured
	// timeout, or earlier if the user presses any key. Missing file is fine.
	if cfg.SplashPath != "" {
		splashPath := cfg.SplashPath
		if !filepath.IsAbs(splashPath) {
			if wd, werr := os.Getwd(); werr == nil {
				splashPath = filepath.Join(wd, splashPath)
			}
		}
		splashTimeout := time.Duration(cfg.SplashTimeoutSeconds) * time.Second
		if splashTimeout <= 0 {
			splashTimeout = 5 * time.Second
		}
		if serr := ui.ShowSplash(conn, input, splashPath, cols, rows, charset, splashTimeout); serr != nil {
			if errors.Is(serr, io.EOF) {
				return nil
			}
		}
	}

	// One-shot terminal size probe. The drop file's reported dimensions
	// are sometimes wrong (BBS client reconfigured between login and door
	// launch); the CPR response tells us what the terminal actually is.
	// We do this here, after the splash has cleared, so any stray cursor
	// movement isn't visible mid-UI. No periodic re-probe -- the constant
	// flicker isn't worth catching mid-session resizes.
	if pCols, pRows := ui.ProbeTerminalSize(conn, input, 500*time.Millisecond); pCols > 0 && pRows > 0 {
		if pCols != app.Width || pRows != app.Height {
			app.Width = pCols
			app.Height = pRows
			app.Relayout()
		}
	}

	if err := app.Run(ctx); err != nil {
		// EOF means the user dropped their connection -- the writes
		// below will fail silently and the process exits, which is
		// exactly what we want so Synchronet can free the node.
		if !errors.Is(err, io.EOF) {
			ansi.Reset(conn)
			ansi.ClearScreen(conn)
			fmt.Fprintf(conn, "\r\nChat ended: %v\r\nGoodbye, %s.\r\n", err, user.DisplayName())
		}
		return nil
	}

	ansi.Reset(conn)
	ansi.ClearScreen(conn)
	fmt.Fprintf(conn, "Goodbye, %s.\r\n", user.DisplayName())
	return nil
}

func greet(conn io.Writer, user *dropfile.User, bbsID, cfgPath, dataDir string, numCollections int) {
	fmt.Fprintf(conn, "\r\n\x1b[1;36mavatar_chat_universal\x1b[0m\r\n")
	fmt.Fprintf(conn, "Hello, \x1b[1;33m%s\x1b[0m, from %s!\r\n", user.DisplayName(), bbsID)
	fmt.Fprintf(conn, "Loaded %d avatar collections.\r\n", numCollections)
	_ = cfgPath
	_ = dataDir
}

func loadCollections(cfg *config.Config, sysopOverride string) ([]*avatar.Collection, error) {
	bundled, err := avatar.LoadBundled()
	if err != nil {
		return nil, fmt.Errorf("load bundled: %w", err)
	}
	dir := sysopOverride
	if dir == "" {
		dir = cfg.SysopAvatarsDir
	}
	if dir == "" {
		dir = "./avatars/sysop"
	}
	sysop, err := avatar.LoadDir(dir)
	if err != nil {
		// Non-fatal: just log to stderr.
		fmt.Fprintf(os.Stderr, "warning: sysop avatars dir %s: %v\n", dir, err)
	}
	return append(bundled, sysop...), nil
}

func resolveCharset(flagVal, cfgVal string) ansi.Charset {
	pick := strings.ToLower(flagVal)
	if pick == "" {
		pick = strings.ToLower(cfgVal)
	}
	switch pick {
	case "utf8", "utf-8":
		return ansi.CharsetUTF8
	default:
		return ansi.CharsetCP437
	}
}

func resolveBBSID(cfg *config.Config, user *dropfile.User) string {
	if cfg.BBSID != "" {
		return cfg.BBSID
	}
	if user.BBSID != "" && user.SysopName != "" {
		return user.BBSID + "-" + user.SysopName
	}
	if user.BBSID != "" {
		return user.BBSID
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

// resolveScreen picks the screen dimensions from (in priority order) CLI
// flags, config keys, terminal-query response, and finally an 80x24 default.
// The terminal query is sent to conn directly before the ansi input pump
// starts, so the response (`\x1b[8;R;Ct`) reaches us instead of being
// consumed as keystrokes.
func resolveScreen(flagCols, flagRows int, cfg *config.Config, conn termio.Conn) (cols, rows int) {
	cols, rows = 80, 24
	if c, r, ok := ansi.ScreenSize(conn, 300*time.Millisecond); ok {
		cols, rows = c, r
	}
	if cfg.ScreenCols > 0 {
		cols = cfg.ScreenCols
	}
	if cfg.ScreenRows > 0 {
		rows = cfg.ScreenRows
	}
	if flagCols > 0 {
		cols = flagCols
	}
	if flagRows > 0 {
		rows = flagRows
	}
	if cols < 40 {
		cols = 40
	}
	if rows < 12 {
		rows = 12
	}
	return cols, rows
}

func openConn(mode string, user *dropfile.User) (termio.Conn, error) {
	resolved := strings.ToLower(mode)
	if resolved == "" {
		switch user.CommType {
		case dropfile.CommTelnet, dropfile.CommRaw:
			resolved = string(termio.ModeSocket)
		default:
			resolved = string(termio.ModeStdio)
		}
	}
	switch termio.Mode(resolved) {
	case termio.ModeStdio:
		return termio.NewStdio(), nil
	case termio.ModeSocket:
		if user.SocketHandle <= 0 {
			return nil, fmt.Errorf("socket mode requested but drop file has no socket handle")
		}
		return termio.NewSocketFD(user.SocketHandle)
	default:
		return nil, fmt.Errorf("unknown io mode %q (want stdio|socket)", mode)
	}
}

func defaultConfigPath() string {
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, "avatar_chat.ini")
	}
	return "avatar_chat.ini"
}

func defaultDataDir() string {
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, "data")
	}
	return "data"
}

// scanAnsiGallery walks dir (recursively) and returns every .ans/.bin
// file it finds as an absolute path, suitable for passing to
// idle.NewAnsiGallery. Empty dir returns nil so the rotation excludes the
// animation rather than rendering black.
//
// When dir is set but the scan finds nothing (missing path / empty tree /
// no matching extensions), a one-line warning is written to stderr so a
// misconfigured sysop sees the cause in Synchronet's node log instead of
// staring at a screensaver that never plays art. Per-file walk errors
// are silently skipped so one unreadable file doesn't kill the whole
// gallery.
func scanAnsiGallery(dir string) []string {
	if dir == "" {
		return nil
	}
	resolved := dir
	if !filepath.IsAbs(resolved) {
		if wd, werr := os.Getwd(); werr == nil {
			resolved = filepath.Join(wd, resolved)
		}
	}
	// Probe the root explicitly so a missing-path misconfig produces a
	// distinct warning from "exists but no art."
	info, statErr := os.Stat(resolved)
	if statErr != nil {
		fmt.Fprintf(os.Stderr,
			"avatar_chat_universal: ansi_gallery_dir = %q does not exist; gallery animation disabled\r\n",
			dir)
		return nil
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr,
			"avatar_chat_universal: ansi_gallery_dir = %q is not a directory; gallery animation disabled\r\n",
			dir)
		return nil
	}
	var out []string
	_ = filepath.WalkDir(resolved, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".ans" && ext != ".bin" {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if len(out) == 0 {
		fmt.Fprintf(os.Stderr,
			"avatar_chat_universal: ansi_gallery_dir = %q has no .ans/.bin files; gallery animation disabled\r\n",
			dir)
	}
	return out
}

// standaloneUser synthesizes a User struct when there's no drop file --
// the door is being launched as a regular CLI app instead of through a
// BBS. Username falls back to $USER then "guest"; BBS name falls back to
// $HOSTNAME then "standalone". CommType is stdio so openConn picks the
// terminal-pipe path automatically.
func standaloneUser(userOverride, bbsOverride string) *dropfile.User {
	name := userOverride
	if name == "" {
		name = os.Getenv("USER")
	}
	if name == "" {
		name = "guest"
	}
	bbs := bbsOverride
	if bbs == "" {
		bbs, _ = os.Hostname()
	}
	if bbs == "" {
		bbs = "standalone"
	}
	return &dropfile.User{
		BBSID:         "standalone",
		SystemName:    bbs,
		SysopName:     "local",
		Handle:        name,
		RealName:      name,
		UserRecord:    0,
		SecurityLevel: 99,
		TimeLeftMin:   -1, // unlimited
		Node:          0,
		Emulation:     dropfile.EmuANSI,
		CommType:      dropfile.CommStdio,
		Source:        "standalone",
	}
}

// setupRawTTY puts os.Stdin into raw mode so single keystrokes flow into
// the input pump without the terminal's line-buffering or echo. Returns a
// restore func the caller must defer; if stdin isn't a TTY (e.g. piped
// input under tests) it's a no-op so the binary still runs.
func setupRawTTY() (restore func(), err error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return func() {}, nil
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("make tty raw: %w", err)
	}
	return func() { _ = term.Restore(fd, state) }, nil
}
