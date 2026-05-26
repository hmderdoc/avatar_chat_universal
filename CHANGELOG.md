# Changelog

All notable changes to avatar_chat_universal will be documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versioning follows loose [SemVer](https://semver.org/) (see
[CONTRIBUTING.md](CONTRIBUTING.md#releasing)).

## [Unreleased]

## [0.2.1] - 2026-05-26

### Fixed

- **TV chat popups now wrap instead of clipping at the screen edge.** Long
  messages flow across up to three rows (then trim with `...`); the nick
  keeps its color on the first row. The normal transcript already wrapped.

- **Two stray Unicode characters that rendered as CP437 garbage on the
  wire** -- the `->` in the `/avatar upload` hint (was a `→`, shown as
  `ΓåÆ`) and the idle-ticker truncation marker (now always ASCII `...`;
  the UTF-8 path had emitted a multi-byte ellipsis byte-by-byte).

### Changed

- **TV lounge layout reworked** -- the live caption bar now occupies the
  bottom action row (where the command pills used to be), and the
  command-hint strip moved up to a thin bar across the top of the video.
  Incoming chat popups stack up just above the caption bar.

- **PgUp history in the lounge is now a one-press "show", not a scroll.**
  The first PgUp pulls up the most recent page of chat over the video
  (it no longer also jumps back five lines); subsequent PgUp presses page
  further back. The overlay dismisses on PgDn / Down-arrow at the bottom,
  on sending a message, or after 30s of no interaction.

### Added

- **Telegram, Matrix, and Slack chat bridges** join the existing IRC and
  Discord bridges -- thin per-platform adapters over the shared bridgecore /
  bridgemedia layer, with systemd units and `*_bridge.ini.example` templates.
  See `BRIDGES.md`. (Tokens load from the environment or gitignored configs.)

- **Per-nick color badges in TV chat popups** -- each speaker's name renders
  as a stable colored badge (a foreground from the 12 saturated CGA colors,
  omitting the neutrals, plus a contrast-checked background), seeded by the
  nick so it's consistent per person. A quick-glance "who said that" cue as
  chat scrolls over the video. TV-mode only; the transcript is unchanged.

- **Themeable caption bar color** via the `tv_caption` theme key
  (default `white|bgblue`). See `themes/futurewave.ini`.

- **Ctrl-T toggles the TV video between 24-bit truecolor and the 16-color
  CGA fallback** (same effect as `/tvcolor`), for a quick flip without
  typing a command. Truecolor can't be auto-detected over a BBS link, so
  the switch stays manual; the startup default comes from `tv_color`.

## [0.1.8] - 2026-05-07

### Fixed

- **Arrow keys / PgUp / PgDn / Esc didn't dispatch to the input pump
  when launched locally with `-dropfile`** -- the OS line-edit layer
  was eating them as cmd-history scrollback (Windows console) or
  readline shortcuts (Unix shells). `setupRawTTY()` was gated on
  standalone-only; now fires for any stdio I/O mode, so local-test
  launches with `-dropfile` work correctly. BBS-spawned door-mode
  runs over an inherited socket are unaffected (the gate keys on
  resolved I/O mode, not standalone).

- **Avatars rendered as colored question marks in stdio dropfile mode
  on local consoles** -- charset auto-default to UTF-8 was also
  standalone-only. Now defaults to UTF-8 whenever stdio mode + stdout
  is a local terminal (per-platform `isLocalConsole()` probe). BBS-
  spawned door-mode keeps the cfg-supplied default (typically CP437,
  correct for SyncTERM/NetRunner remote clients).

### Added

- **Legacy windows/386 build path for Windows XP** via Go 1.10 + the
  new `make dist-windows-xp` target. Output:
  `dist/windows_386_xp/avatar_chat_universal.exe`. Major BBS sysops
  on XP can drop the binary alongside their dropfile config and run.

  Implementation in `compat/_legacy/`:
  - Hand-rolled minimal `golang.org/x/sys/windows` shim (~210 lines
    wrapping the 11 Win32 calls the door uses, via Go 1.10's stdlib
    `syscall.Syscall*` against kernel32.dll / ws2_32.dll).
  - `build-windows-xp.sh` handles the GOPATH dance (Go 1.10 is pre-
    modules), clones go-colorable / go-isatty / x/term at HEAD into
    a temp GOPATH, runs the cross-compile.

  See `compat/_legacy/README.md` for prerequisites and details.

### Changed

- **Source restricted to a Go-1.10-compatible feature subset** so the
  same source compiles cleanly on both modern Go (1.26 verified) and
  Go 1.10 (XP target). This locks the project's surface against
  `errors.Is/As`, `fmt.Errorf %w`, `//go:embed` (without build-tag
  splits), `io/fs`, `time.UnixMilli`, generics, `any`,
  `strings.ReplaceAll`, `os.ReadFile`, `io.ReadAll`,
  `filepath.WalkDir`, and 0o-prefix octal literals. The two files
  that genuinely need `//go:embed` (avatar bundle, splash artwork)
  are split into modern + legacy build-tagged pairs:
  `internal/avatar/bundle_{embed,disk}.go` and
  `internal/ui/splash_{embed,disk}.go`. Same public API, modern Go
  uses embed, Go 1.10 reads from disk relative to the binary.

## [0.1.7] - 2026-05-07

### Fixed

- **Garbage output on legacy Windows consoles in standalone mode.** On
  Win10 builds without `ENABLE_VIRTUAL_TERMINAL_PROCESSING` (older Win10,
  Server 2016, LTSB/LTSC, some 32-bit Win10), our ANSI escape sequences
  rendered as literal `^[[31m` characters scrolling down the screen.
  The Windows TTY setup tried to enable VT output and silently swallowed
  the failure. Two changes: (1) surface the mode-set failure on stderr
  so the cause is visible, (2) wrap stdout with `go-colorable` in
  standalone stdio mode -- it translates ANSI to Win32 console API
  calls when VT processing isn't available, passes through unchanged
  when it is. Same `windows_386` build now renders correctly on both
  Win10 64-bit and Win10 32-bit. Reported by an external sysop testing
  on Win10 32-bit.

### Added

- `github.com/mattn/go-colorable` v0.1.14 (with `go-isatty` as transitive
  dep). BSD-licensed, no CGO, ~50KB binary impact on Windows.

### Changed

- Reporter attributions in v0.1.6 entries anonymized to "an external
  sysop" to match the project's standing convention. The v0.1.6 commit
  message in git history is unchanged (rewriting public history is
  destructive); CHANGELOG.md is the authoritative public record going
  forward.

## [0.1.6] - 2026-05-07

### Fixed

- **Standalone origin no longer shows `standalone-local`.** `standaloneUser`
  was hardcoding `BBSID="standalone"` + `SysopName="local"`, and `-bbs`
  flowed only into `SystemName` (which the displayed origin doesn't
  consult). Result: setting `-bbs "My BBS"` had no visible effect. Fixed
  by clearing the synthetic `SysopName` and threading `-bbs` through
  `resolveBBSID` as a CLI override that wins over both the dropfile and
  `cfg.BBSID`. Reported by an external sysop running standalone under
  Mystic.

- **`avatar_chat.ini` not picked up under Mystic.** `defaultConfigPath`
  resolved to `<cwd>/avatar_chat.ini` only, but Mystic (and likely other
  BBSes) sets CWD to the BBS root before exec'ing the door — so the
  door's local `.ini` was never seen and `bbs_id`/`sysop`/etc. silently
  fell back to defaults. Lookup now tries CWD first, then
  `<binary_dir>/avatar_chat.ini`. When neither exists, a one-line stderr
  warning surfaces the missing-file path so a sysop sees the actual
  lookup result instead of staring at default behavior.

- **Cryptic `fcntl: bad file descriptor` from `net.FileConn`.** The
  socket-mode wrapper now wraps the underlying error in a message that
  names the fd, says explicitly that the BBS may not be inheriting it,
  and points the sysop at `-io stdio` as the workaround. Same surface
  failure, much more actionable error text.

### Documentation

- **Mystic install page**: `*F` in the Cmd line was wrong — corrected to
  `%PDOOR32.SYS` (Mystic's `%P` resolves to `/path/to/mystic/tempN/`).
  Added a Standalone-fallback subsection covering the `-bbs`/`-user`
  invocation. Added troubleshooting rows for the new stderr config
  warning, `socket fd N not usable`, and the `standalone-local` origin
  case. Reported by an external sysop.

## [0.1.5] - 2026-05-06

### Fixed

- **Windows socket-mode door crash on first read** (`selector: A non-blocking
  socket operation could not be completed immediately.`). EleBBS (and other
  Win32 BBSes that drive their listener through a select loop) hands the
  spawned door a SOCKET still configured for non-blocking I/O. v0.1.4 assumed
  the socket was already in blocking mode and relied on `SO_RCVTIMEO` for
  read deadlines, so `WSARecv` returned `WSAEWOULDBLOCK` (10035) immediately
  on the first read and the door died right after rendering the avatar
  selector. We now flip the socket to blocking mode via `WSAIoctl`/`FIONBIO`
  at adopt time, and `Read` defensively maps any residual `WSAEWOULDBLOCK`
  to a Timeout-bearing error so the input pump retries instead of crashing.
  Reported by Shurato (Heavenly Sphere BBS) on EleBBS.

### Added

- Pre-chat avatar selector now wires through to real Upload and Editor
  flows. Previously, picking either option from the first-run selector
  bounced you to "disabled" with a "use /avatar from chat instead" message
  -- both flows are now shared with the in-chat `/avatar` paths so they
  can't drift apart.

## [0.1.4] - 2026-05-06

### Added

- **Windows socket I/O mode** (DOOR32.SYS comm type 1, 2, 3). Previously
  deferred and stubbed -- now implemented natively against Winsock via
  `golang.org/x/sys/windows`. Required for any Windows BBS that hands the
  door a SOCKET handle instead of stdio (EleBBS, Mystic Win32, Synchronet
  Windows in non-stdio configs). The implementation does its own
  WSARecv/WSASend with `SO_RCVTIMEO` driving `SetReadDeadline`, since
  Winsock SOCKET handles can't be adopted by Go's `net.FileConn` the way
  *nix file descriptors can. Reported by Shurato (Heavenly Sphere BBS) on
  EleBBS, where socket mode is the only available option.

## [0.1.3] - 2026-05-06

### Fixed

- **Standalone mode crash on Windows** (`make tty raw: The parameter is
  incorrect.`). `golang.org/x/term`'s `MakeRaw` issues a single
  `SetConsoleMode` call that combines clearing cooked-mode flags with
  setting `ENABLE_VIRTUAL_TERMINAL_INPUT`; legacy ConHost (older Win10
  builds, Server 2016, VT-disabled environments) rejects the combined
  call. The Windows TTY setup is now in-house and does it in two passes:
  the mandatory raw-input mode first, then VT input/output as best-effort
  follow-ups so a VT-incapable host still gets a working raw stdin.
  Reported by Shurato (Heavenly Sphere BBS) on EleBBS Win32.
- **Splash screen ordering**: the splash now renders first, before the
  greeting and avatar selector. Previously it sat between
  `session.Connect` and the chat UI, so first-time users (no saved
  avatar) never saw it and returning users only caught it briefly on
  the way into chat. The artwork is also now compiled into the binary
  via `go:embed` so a misconfigured / missing `splash.ans` can't
  silently disable it. Sysops who want a custom splash drop their
  replacement at the repo root and rebuild.

### Changed

- `splash_path` config key removed (artwork is embedded). Set
  `splash_timeout_seconds = 0` to skip the splash entirely.
- CI Go version bumped to 1.25 to match `go.mod`.
- Distribution tarballs now include the full doc set (README, INSTALL,
  CONFIG, THEMING, AVATARS, SCREENSAVER, CONTRIBUTING, CHANGELOG, LICENSE),
  the bundled themes, and pre-created `avatars/sysop/` and `ansi_gallery/`
  directories with stub READMEs telling sysops what to drop where.

## [0.1.2] - 2026-05-06

### Fixed

- Avatar selector (and any other modal that treats Esc as cancel) no
  longer immediately closes itself on terminals that emit
  vendor-specific CSI sequences on startup. The input parser was
  falling back to `KeyEsc` for any unrecognized CSI; it now returns a
  no-op character. Reported on macOS Terminal.app and on EleBBS Win32.

### Changed

- `idle_timeout_seconds` default bumped from 10 to 60. The 10-second
  default was a development convenience.
- Removed developer-debug greeting line that exposed drop-file
  internals (`Drop file: ... user record: ... time-left: -1m`).

## [0.1.0] - 2026-05-06

Initial pre-1.0 development. Everything below is "what works today"
relative to a clean checkout.

### Added

- **Door integration** for Synchronet, Mystic, ENiGMA½, and any BBS
  that emits DOOR32.SYS or DOOR.SYS. Auto-detects stdio vs. socket I/O.
- **Standalone CLI mode** when run without `-dropfile`: synthesizes a
  user from `$USER`/`-user`, puts the local TTY into raw mode via
  `golang.org/x/term`, talks to the configured chat server. Useful for
  testing without a BBS, or as a personal chat client.
- **Chat protocol** compatible with the JS `avatar_chat` door's wire
  format: line-delimited JSON-RPC over TCP, PING/PONG keepalive,
  fire-and-forget WRITE/PUSH/SUB, oper-matched query responses for
  WHO/SLICE/STATUS.
- **Self-hostable chat server** (`cmd/avatar_chat_server`) speaking the
  same protocol, suitable for offline / private deployments. JS door
  interoperates against it without changes.
- **Avatar system** matching `avatar_lib.js`'s 120-byte CP437 format
  byte-for-byte, with the same validation rules. Bundled collections
  via `go:embed` (corporate, danger, futureland, etc.); sysop-curated
  `.bin` collections via `sysop_avatars_dir`.
- **Avatar selector** (`/avatar`) — auto-sized grid, tab between
  collections, action pills for Upload / Disable / Editor.
- **Zmodem upload** of `.bin` avatars via the embedded ZMODEM-CRC32
  receiver. Tested against lrzsz `sz` (e2e test in repo) and SyncTERM.
  Esc-to-cancel works via both proper 5×CAN sequences and bare-Esc
  detection.
- **In-door pixel editor** (BETA) — half-block resolution editing,
  pixel/char/brush modes, FG/BG cycling, undo, fill bucket, flip X/Y,
  load-from-existing baseline.
- **Theming** via `themes/<name>.ini` — color palette + optional
  screensaver profile per theme. Bundled `futurewave.ini` as default.
- **Splash screen** support for SAUCE-tagged `.ans` and `.bin` art.
  Centered, charset-aware, with an RGB palette strobe while displayed.
- **Idle screensaver** with 15 procedural background animations
  (starfield, matrix_rain, plasma, aurora, etc.) plus 2 foreground
  animations (avatars_float, figlet_message).
- **ANSI gallery** screensaver — recursively scans a sysop-managed
  directory of `.ans`/`.bin` art and scrolls each piece vertically.
  132/160-column art is clipped at the right edge without disturbing
  alignment.
- **Idle interleave** — between every procedural animation, slot in
  one piece of ANSI gallery art (mirrors the JS `future_shell`
  screensaver). Toggle via `idle_interleave_ansi`.
- **Screensaver-friendly chat ticker** — incoming messages no longer
  dismiss the screensaver; instead they show as a 6-second overlay at
  the bottom of the screen so activity stays visible.
- **Relative timestamps** ("just now", "5m ago", "2h 15m ago",
  "yesterday HH:MM") on chat bubbles, recomputed every render.
- **Speaker / time / BBS host** rendered above each bubble, with the
  BBS host truncated with `...` if it would overflow the bubble width.
- **Sweep glow effect** on join/leave notices (green left-to-right,
  red right-to-left).
- **CP437 ↔ UTF-8 charset translation** at the cell-render layer.
  Standalone mode auto-defaults to UTF-8 for regular terminals; door
  mode keeps CP437 default for BBS clients.
- **One-shot terminal-size probe** at startup so a session that
  reports incorrect dims via the drop file gets corrected without
  visible flicker mid-session.
- **CI** (`.github/workflows/ci.yml`) and **release** workflows
  (`.github/workflows/release.yml`) — tag-driven multi-platform builds
  to GitHub Releases.

### Known limitations

- **Avatar editor is BETA**: the half-block UX works but external
  editors (Moebius / Pablo Draw) + Zmodem upload remain the
  recommended path for serious art. Editor warns the user about this
  on entry.
- **Mid-session terminal resize** is not detected; size is locked at
  startup. Reconnect after resizing.
- **Chat-server state** is in-memory only; restart loses history.
  Disk persistence is on the roadmap.
- **WHO list** returns connection identifiers (IP:port) rather than
  declared nicknames — matches the JS server's behavior; nicks are
  carried in message envelopes, not registered out-of-band.
- **Image sending** (BITMAP wire format) is decode-only; `/img`
  displays bitmaps that other clients post but there's no built-in
  encoder yet.

[Unreleased]: https://github.com/hmderdoc/avatar_chat_universal/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/hmderdoc/avatar_chat_universal/compare/v0.1.0...v0.1.2
[0.1.0]: https://github.com/hmderdoc/avatar_chat_universal/releases/tag/v0.1.0
