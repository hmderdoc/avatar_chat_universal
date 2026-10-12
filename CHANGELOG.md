# Changelog

All notable changes to avatar_chat_universal will be documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versioning follows loose [SemVer](https://semver.org/) (see
[CONTRIBUTING.md](CONTRIBUTING.md#releasing)).

## [Unreleased]

### Added

- door: **raised buttons under the header**: Who, Rooms, Images,
  Settings, Help and Quit Esc on one row, each a coloured face with a
  half-block shadow (CP437 and UTF-8). A click shows the button pressed
  (face shifted into the shadow) for about 100 ms, then runs it; Quit does
  what Esc does. All six fit at 80 columns; narrower, Help, Settings,
  Rooms and Who drop out in that order (never one flashing a count).
- door: **Help in a panel.** `/help` (or the Help button) opens a boxed
  "Help" panel with the same lines it used to print, grouped (Commands,
  Rooms and people, Your look, Pictures and links, TV and media), scrolled
  with Up/Down, PgUp/PgDn, Home/End and the wheel; Esc or `[X]` closes.
- door: **`J` in the Rooms panel** (`/channels`) asks for a room name on a
  prompt line in the panel; Enter joins (or creates) it, Esc puts it away.
- door: **`M` and `I` in `/who`**: M closes the panel with `/msg <name> `
  in the input line for the selected person; I ignores or unignores them
  (as `/ignore` / `/unignore`), and the panel shows whether they are.
- door: **clickable key lines**: in every panel the actions on its key
  line (`M message`, `J join by name`, `Enter join/switch`, `Esc close`,
  `S save`, `Enter use`, `[Enter] start upload`, ...) run on a click.
- Theme keys `top_action_shadow` (the buttons' shadow, foreground only),
  `strip_bar` and `strip_pill` (the TV lounge's key strip and the
  screensaver ticker); set in every shipped theme.
- door: **an `[X]` dismiss button** at the top left of every panel
  (`/who`, `/channels`, `/settings`, the colour grid, `/img`, the avatar
  upload dialog) and at the left of the chat screen's first row (the header). A
  click is Esc: the panel closes (unsaved `/settings` edits are dropped,
  as with Esc) and on the chat screen it does what the `Esc exit` pill
  does. Keys are unchanged.
- door: **a colour grid in the `/settings` handle editor.** Enter, or a
  click on a letter, opens "Pick a colour": the 16 CGA colours in two
  rows, and on truecolor terminals a 12 x 5 hue/lightness block and a
  grey ramp below them. Arrows move the `[ ]` cursor, Enter or a click
  uses the colour, Esc or `[X]` cancels; the line under the grid names
  CGA colours and calls the rest "custom". `f`/`F` and presets 1-4 still
  work.
- Theme keys `modal_frame` (the box around panels, foreground only) and
  `close_button` (the `[X]`), 47 to 49; set in every shipped theme.

- **A first-logon welcome.** Someone who has never chosen an avatar is
  asked to, as the door already did: the standalone page (after the name
  is taken, before the no-rooms prompt) and the Synchronet page show their
  avatar and name with **Choose avatar**, **Colour your name** (only while
  the name has no colours) and **Not now** (this page load only). An
  explicit "no avatar" is settled and not asked again: Remove on the
  standalone page (kept in the browser), Disable on the Synchronet page.
- **The door offers name colours on first run**, once: after the avatar
  picker, a user whose handle style has no colours is asked "Press C to
  colour your name"; C opens /settings at the handle editor as chat
  starts. The offer is recorded in the door prefs
  (`handle_colors_offered`).
- **Tab completes names on both web pages**, like the door: the word
  before the caret (or the last two to four, for names with spaces)
  against the room's members, anyone seen speaking and the PM peer,
  case-insensitive, sorted; Tab again cycles, Shift+Tab goes back. With no
  match Tab moves focus as usual.
- door: Tab completes names with spaces, e.g. 'Beta T' -> 'Beta Tester'.
- **Private messages on the standalone page.** A web user's mailbox is
  `name@origin` (the server's `system_name` with anything outside
  `[A-Za-z0-9_-]` as `_`), carried as `nick.mailbox`, which the server
  stamps itself. The first browser to use a name holds it on that server
  by a key it keeps (`web_claims_file`; a sysop releases a name by
  deleting its line; `web_claims_per_ip_day`). A browser can read only
  its own mailbox, never a plain-name one. The door (`/msg`, name clicks,
  `/r`, which now answers the exact sender), the Synchronet page, the
  futureland shell and website route PMs to a valid mailbox; anything
  that doesn't match `name@origin(host)` is ignored. BBS users' PMs are
  unchanged and still reach every client they're logged in on. Old
  clients keep working; their PMs to web users don't arrive.
- **`/media?url=` on the standalone server**: guarded same-origin
  passthrough for audio and video (seen-URL rule, type checks,
  `media_proxy_max_mb`, `media_streams`, Range passed on with a 416
  retry), so download links and track pages play on the page.
- **Handle colours on the standalone page**: per-letter colours and
  presets in Settings, kept in the browser per name, colours only.
- **Download links play on the Synchronet page too**, by the same rule as
  the door's cards.

### Fixed (web)

- The first-logon welcome closed for good once you picked an avatar, so
  name colours were never offered. Closing the avatar or colour dialog
  opened from it now comes back to it, showing what's set (**Change
  avatar**, **Done**).
- Synchronet page: sending a PM against the bundled Go server wiped the
  sender's PM history; link-card fetches never sent their Range header.
- The bundled chat server's WHO answers `{nick, system}` like
  Synchronet's instead of bare names.
- **Standalone web page caught up.** A members column with avatars and
  handle colours; avatar settings (bundled collections and an optional
  `avatars_dir`, a 10x6 editor, `.bin`/`.ans` upload, kept in the browser
  and sent on messages); the NSFW blur with the door's keyword list
  (`nsfw_keywords`); the Synchronet page's rooms UI; the ANSI editor and
  attach icon; the same eight page styles (`web_styles`,
  `web_default_style`). Every action has a button; no slash commands
  needed. WEB.md has an "Enabling uploads" section.
- **Ignore list in the door.** `/ignore <name>` hides someone's lines,
  art, private messages and join/leave notices (live, from history and in
  other joined rooms' unread counts) and stops them ringing; `/ignore`
  lists, `/unignore` undoes. Kept per user in the door's prefs. On
  Synchronet, `shared_ignore_file` also honours a shell's ignore list
  read-only, so people ignored in the futureland shell or website are
  hidden in the door too. Matching follows the shell: case-insensitive,
  everywhere or local-chat entries, bridge-relayed `<name>` lines.
- **Web chat in `avatar_chat_server`.** One listener (`web_addr`; the
  older `ws_addr` still works) serves the standalone page from
  `web/standalone`, the WebSocket at `/chat`, `/config.json`, `/img` and
  `/preview` for pictures and link cards, and the media endpoint when it's
  set up. The browser only ever talks to that one origin; WebSocket
  origins default to same-origin (`ws_origins` adds more). `upstream =
  host:port` relays each browser to someone else's chat server instead of
  running a local one. Browser packets are limited to what the page needs,
  stamped with the user's nick and `system_name`, and capped
  (`web_max_conns`, `web_max_per_ip`, `fetch_per_minute`). No accounts:
  the page has no private messages or MOTD. WEB.md covers it.
- **Synchronet web app** in `web/synchronet`, moved from the old JS door's
  bundle (`bbs_avatar_chat`) and caught up with the door: handle styles
  shown and edited, inline pictures with the NSFW veil, audio and video
  players, link cards fetched on the BBS with SSRF guards, mention
  highlight, remote avatars by alias and QWK ID, and file drop/paste
  uploads stored under the web root (off unless `web_uploads = on`). It
  installs into stock webv4, goes through its own SSJS APIs as the
  logged-in user, reads this door's `avatar_chat.ini`, and checks a
  per-session token on writes. Server side is TypeScript built to
  `dist/webv4`; see web/synchronet/README.md.
- **Synchronet web app, after the first live try:**
  - The composer is one row: a paperclip (attach), a pencil (ANSI
    editor), the message box and Send. Drop a file anywhere on the chat or
    paste it into the box. The ANSI file button, Clear button, drop box,
    helper text and the two stray "Choose File" inputs (bootstrap's
    `input[type=file]` rule beat `hidden`) are gone.
  - The ANSI editor (Futureland's, vendored in `web/synchronet/editor`):
    80x25 from the pencil, Done sends a BITMAP.
  - An **Avatar** button for your real BBS avatar: pick from the sysop's
    `text/avatars` collections, draw or edit at 10x6, upload a `.bin` or
    `.ans`, or disable it. Written through Synchronet's `avatar_lib`.
  - The handle style editor sets letter colours only; the page no longer
    shows or edits tags, and a save keeps whatever tag another client
    stored in the record.
  - Upload links keep the port the browser used (`:4080`). New
    `web_media_base_url` pins them for a proxy that rewrites `Host`
    (`web_upload_url_base` still read).
  - Rooms work like Futureland's page: the sidebar lists the rooms you
    joined (click to view, x to leave, unread badges), **Join a room** /
    **Leave** sit beside the MOTD, and the picker lists every room with
    its user count and state. Its button reads **Join #x** or **Create
    #x**, so creating a room is never a hidden side effect of joining.
    Leaving the last room offers the default one; sending in a room joins
    it. Joined rooms are kept per account in `data/chat/webprefs/NNNN.json`.
    The "Public Rooms" heading, the Create Room button and the "Avatar
    Chat" title in the header are gone.
  - Page styles: one **Page style** pick in the Handle Style modal, each a
    complete look (colours and shape): Classic, CGA, Modern dark, Modern
    light, Amber phosphor, Green phosphor, Windows 95, Flat compact. Saved
    per account with the rooms and applied in the served HTML. Sysop keys
    `web_styles` and `web_default_style`.
- **Mouse.** The door asks for xterm mouse reports (SGR, with X10
  fallback): the wheel scrolls the chat and lists, pills run their
  commands, a name starts `/msg`, a picture opens in `/img`, a `/play`
  line plays, a link card shows its URL, `/channels` rows select and
  join. `mouse = on|off`; `/mouse` per user (door prefs). Reports pause
  for the editors, pickers and Zmodem.
- **Live resize.** Every `resize_probe_seconds` (default 5) the door
  re-asks the terminal its size between frames (cursor hidden for the
  trip) and re-lays out on a change, keeping the typed line and scroll
  position. Off when the size is pinned or the terminal never answered.
- **Per-user themes.** `/theme` lists the sysop's `themes/*.ini`, `/theme
  <name>` switches live and remembers it (door prefs under `<data>/prefs/`,
  kept out of the BBS's own user records), `/theme default` goes back to
  the sysop's; `/settings` has a Theme row (`c`/`C`) that previews in the
  panel's own colours. `user_themes = off` pins everyone to `theme`.
  Switching themes no longer stacks screensaver profiles.
- **More theme keys** (27 to 47): `input_bg`; `modal_close`, `modal_text`,
  `modal_label`, `modal_select`, `modal_hint`, `modal_unread`, `modal_rule`,
  `modal_ok`, `viewer_bar` for /who, /channels, /settings and /img;
  `dialog_*` for the upload box; `veil_*` for the NSFW veil; `tv_chat`,
  `tv_chat_dim` for chat over the TV picture. Defaults are the old
  hard-coded colours, so nothing changes until a theme sets them.
- **Four more themes**: midnight, amber, forest, sunset. Every shipped
  theme now sets every key, and a test lints them.

- **Video links.** With ffmpeg/ffprobe on the host, `.mp4 .m4v .webm .mov
  .mkv` links and pages announcing `og:video` get a `► /play` card and play
  full screen on the TV-lounge renderer (truecolor or 16-colour shade
  cells) with SyncTERM sound; with yt-dlp, links to `video_sites` too.
  Downloads go through the picture guards into a private dir; ffmpeg reads
  only that file (protocol `file`, demuxers mov/matroska); children are
  isolated, niced, killed with their group, time capped; a host-wide slot
  lock caps concurrent playbacks. `Esc`/`/stop` end it; a tuned TV channel
  steps aside and returns. `video*` keys; HLS/DASH not played.
- **`/play` for audio links.** mp3, Ogg Vorbis, FLAC and WAV links get a
  one-line card; `/play`, `/play N`, `/play list` and `/stop` play them on
  terminals with SyncTERM audio, decoded in pure Go and streamed in real
  time on their own channel. Never plays without `/play`. `audio_links`,
  `audio_max_mb`.
- **TV lounge sound.** On terminals that play SyncTERM Audio APCs, the
  lounge asks the relay for the channel's audio and plays it (WAV clips,
  decoded by the terminal). `tv_audio = auto|on|off`; `/tvmute` mutes.
  Only terminals that identify as CTerm are probed. Audio is sent between
  frames, never inside one.

- **`/draw`: ANSI art editor.** A full-screen CP437 canvas (default 80x25,
  up to 80x40) in the 16 CGA colours with PabloDraw/Moebius F-key glyph
  sets, half-block pixel mode, brush, flood fill, colour pick and 64-step
  undo. Ctrl-Y posts the art as a `[BITMAP]` message, byte-compatible with
  the web chat's and Moebius's encoders (including their palette order), so
  every client draws it.

- **Deny list for `avatar_chat_server`.** `-deny <file>` (`deny_file`) lists
  IP addresses and CIDR ranges to refuse on the chat port and the media
  endpoint; edits take effect within a second. See MEDIA.md.
- **TV lounge popups show handle styles** for senders who have one.

- **Synchronet users keep their own avatar.** On Synchronet the door now
  reads and writes the caller's real BBS avatar (`data/user/NNNN.ini`), so
  nobody is asked to pick an avatar they already have, and a change made in
  the door is their avatar everywhere on the system and in the network
  export. Only the avatar lines are rewritten; the rest of the file is kept
  byte for byte. Turns on by itself (`avatar_store = auto`); see
  `avatar_store`, `synchronet_data_dir` and `-sbbs-data`. Mystic, ENiGMA and
  standalone keep the door's own store.
- **Handle styles.** Names now show in their owner's colours (one per
  letter) with an optional tag, the same `nick.style` the futureland shell
  and website send. 16-colour terminals get each colour's perceptually
  nearest CGA colour; truecolor terminals get it exactly. The door sends
  your own style on every message.
- **`/settings` panel** (`/prefs`, `/options`, and a pill in the top bar):
  avatar actions plus a handle editor with per-letter colours, Solid /
  Gradient / Rainbow / Clear presets and a tag. On Synchronet it edits the
  shared `data/chat/nickstyle/NNNN.json` record.
- **Attention cues.** Lines that say your name (whole word, optional `@`,
  the website's rule) get the new `bubble_mention` / `mention_edge` theme
  colours. A PM or mention rings the bell (`bell = true`, at most once per
  3 s). Unread PMs show as a flashing `/r [N]` pill.
- **Real channel list.** `/channels` (or `/list`) lists joined channels with
  unread counts and every room in the server's `public_channels` registry
  with live user counts; Enter joins, `L` leaves. `/join` now keeps the old
  channel joined, counting its messages on a flashing `/channels [N]` pill,
  and the header shows how many channels you're in.

- **Inline images.** Image links (png, jpeg, gif first frame, webp, bmp)
  show inside the bubble, up to 10 rows: sixel on terminals that report it
  (SyncTERM 1.207+ pixel operations, xterm DA1), 24-bit half blocks on
  truecolor terminals, and 16-colour shadecell cells everywhere else. The
  sixel encoder is pure Go. Downloads are guarded (public addresses only,
  ports 80/443, 3 redirects, size cap, timeout) and cached under the data
  dir. NSFW-keyword links are veiled and not fetched until `/nsfw`. `/img`
  now also zooms these pictures to the full screen. New keys: `images`,
  `image_sixel`, `image_max_mb`, `image_cache_dir`, `image_cache_mb`,
  `nsfw_keywords`.

- **Link previews.** The first plain link in a message gets a card under
  the text, as on the website: site name, title, description and a small
  `og:image` thumbnail (24-bit or 16-colour shade cells). Fetched with the
  inline-image guards and the website's limits, cached in
  `<data>/linkpreviews` (24 h, failures 1 h), veiled by the NSFW keywords.
  New keys `link_previews` and `link_preview_ttl_hours`; theme keys
  `card_site`, `card_title`, `card_edge`.
- **Chat attachments.** `avatar_chat_server` has an optional HTTP media
  endpoint (`-media-addr`, `-media-dir`, `-media-url-base`, or a `[media]`
  section via `-config`): `POST /upload` stores a picture (re-encoded,
  metadata stripped) or ANSI art for `retention_days` (7) under the web
  chat's `/chatmedia/<date>/<id>.<ext>` layout and answers with its URL;
  HTML and SVG are never stored, files are served with a fixed type,
  `nosniff` and a `default-src 'none'` policy, uploads are rate limited
  per address and capped in total. The door's new `/upload` command
  receives a file by Zmodem, posts it to `upload_url` and puts the link in
  the input line to send. See MEDIA.md.

### Fixed

- door: a sixel picture showed with its cell version around it. The box is
  sized for an assumed cell size, so on a terminal with bigger cells the
  sixel came out smaller than the box; while sixels are drawn, the box
  is now left as the bubble's background under them.
- door: a stray "7" under the cursor (and an "8" in the far corner) on
  BBS terminals without DECSC. The size probe saved and restored the
  cursor with ESC 7 / ESC 8, which those terminals print; it now uses
  CSI s / CSI u, which they ignore if unknown.
- door: with a panel up, the cursor was left after the box's bottom-right
  corner, so a terminal that ignores the no-wrap mode (or echoes keys)
  scrolled garbage rows in at the bottom. It now waits on the input row,
  as on the chat screen.
- First run: the avatar picker's Enter often arrives as CR LF, and the
  LF answered the "Press C to colour your name" prompt as a skip. Keys
  typed ahead are now dropped before it.
- `/settings`: unsaved handle edits were lost on a trip to the avatar
  picker, editor or uploader and back.
- `themes/green.ini`: highlight pills were green on green; now red.

- SyncTERM's upload key is Alt-U; the avatar upload hints said Alt-S.

- After a Zmodem upload (avatar or attachment) the sender's closing "OO"
  is consumed instead of landing in the input line as two typed O's.

### Changed

- door: **the bottom bar is gone**; the chat gains its row. Its pills
  (`/join`, `/part`, `/me`, `/msg`, `/r`, `/clear`, `/quit`, `/theme`) and
  the top bar's `Tab user` and `Esc exit` have no button now; the commands
  all still work typed and are listed in Help, and the theme is in
  `/settings` (`c`/`C`). The unread-PM count moved from the flashing
  `/r [N]` pill to the Who button (`Who [N]`, flashing); the unread-rooms
  count is on Rooms (`Rooms [N]`), the picture count and flash on Images.
- door: `/help` no longer prints its lines into the chat (see the Help
  panel).
- door: the Rooms panel (`/channels`, now titled "Rooms") always lists the
  joined rooms first.
- door: in the TV lounge the caption bar is on the last row of the picture
  area (the video is drawn one row shorter, popups stack above it); it used
  to take the bottom bar's row.
- Theme keys `bottom_action_bar`, `bottom_action_pill` and
  `bottom_action_highlight` are retired (no bottom bar). Old theme files
  still load: the first two set `strip_bar` / `strip_pill`, which they
  coloured anyway, and the highlight is ignored.
- door: every panel and dialog is drawn in a single-line box in the
  theme's `modal_frame` colour, the title on its top edge; contents moved
  in so nothing draws over it.
- door: the `/settings` letter line names an off-palette colour by its
  nearest CGA colour ("~light blue") instead of a hex code, and mentions
  Enter.
- door: the bottom bar's first pill is `/theme` (lists the themes, as
  typing it does) when users may pick a theme, and is dropped otherwise;
  the "Up/PgUp history" pill is gone. PgUp and Up still scroll.

- **Handle tags are gone from the door.** `/settings` no longer edits a
  tag, the door doesn't draw tags on anyone's name, and it sends only the
  colours on its messages. A tag after a name made handles read differently
  and broke lookups by name. Tags already stored by the shell or a website
  stay in the record; a save from the door keeps them.

- The terminal probe now also runs when `tv_color` is pinned but images are
  on, to learn about sixel; it uses termprobe's new sixel and cell-size
  report.
- `/part` leaves the current channel for the previous one instead of
  exiting; it still exits when it's the last channel.

- A disabled avatar ("no avatar") is now a settled choice: the picker no
  longer opens at every launch for it. `-select` still forces it.
- In Synchronet mode without `bbs_id`, the host label is the system name
  from `main.ini` instead of Synchronet's version string, with a warning.
- The top action bar shows `/settings` in place of `/avatar` (which still
  works when typed).

## [0.2.2] - 2026-05-28

### Fixed

- **TV lounge no longer disconnects the session over SSH under load.**
  Watching a tuned feed for a while could drop the connection (the client
  logging `peer ignored channel window`): the lounge wrote every video
  frame to the wire with no backpressure, so it outran a slow SSH link and
  overflowed Synchronet's output pipeline until the client gave up. The
  render loop now keeps a single frame in flight and writes it
  non-blocking, composing the next frame only once the previous has fully
  drained -- viewers get frames at their own link-limited rate (newest
  frame wins, no stale backlog) instead of a firehose.

### Added

- **TV rendering now honors the broadcaster's per-frame mode/ramp hint.**
  A `mode=1` (ramp) frame paints as a single foreground-colored glyph
  chosen by brightness (ASCII ` .:-=+*#%@` or CP437 shade blocks) rather
  than two-color half-blocks -- roughly a third of the bytes, which the
  broadcaster sends for material that reads fine as a gradient.

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

[Unreleased]: https://github.com/hmderdoc/avatar_chat_universal/compare/v0.2.2...HEAD
[0.2.2]: https://github.com/hmderdoc/avatar_chat_universal/compare/v0.2.1...v0.2.2
[0.1.2]: https://github.com/hmderdoc/avatar_chat_universal/compare/v0.1.0...v0.1.2
[0.1.0]: https://github.com/hmderdoc/avatar_chat_universal/releases/tag/v0.1.0
