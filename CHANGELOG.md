# Changelog

All notable changes to avatar_chat_universal will be documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versioning follows loose [SemVer](https://semver.org/) (see
[CONTRIBUTING.md](CONTRIBUTING.md#releasing)).

## [Unreleased]

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
