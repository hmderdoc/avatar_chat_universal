# avatar_chat_universal — Avatar Guide

Everything about avatars: the format, where they're stored, the bundled
collections, how sysops add their own, and the three ways a user can pick
one (selector / Zmodem upload / in-door pixel editor).

---

## Format

An avatar is exactly **120 bytes**: a 10-cell × 6-row grid of CP437
character/attribute pairs in row-major order.

```
cell layout  : (col, row) — col 0..9, row 0..5
byte stream  : (char_0,0)(attr_0,0)(char_1,0)(attr_1,0) ... (char_9,5)(attr_9,5)
total        : 10 * 6 * 2 = 120 bytes
```

- `char` is a CP437 byte (0x00–0xFF).
- `attr` is a standard CGA attribute byte: low 4 bits foreground, bits
  4–6 background, bit 7 must be 0 (the "blink" bit is rejected by
  validation).

This is byte-for-byte the same as Synchronet's `avatar_lib.js` format,
so avatars round-trip cleanly between the two doors.

### Validation rules

These mirror `avatar_lib.js:88-101`:

- Length must be exactly 120.
- For every char byte (even-indexed), reject: `0x00`, `0x07` (BEL),
  `0x08` (BS), `0x09` (TAB), `0x0A` (LF), `0x0C` (FF), `0x0D` (CR),
  `0x1B` (ESC), `0xFF` (IAC).
- For every attr byte (odd-indexed), reject: bit 7 set (blink).

`0x0B` (VT) and `0x1A` (SUB) are explicitly **allowed** — Synchronet
allows them, and they render as legit CP437 glyphs (♂, →).

---

## `.bin` collection files

Multiple avatars are concatenated into one `.bin` file = one
**collection**. The selector treats each `.bin` as a tab-able grouping.

A `.bin` file can contain:

- N × 120 bytes of avatars (no metadata, no separator) — minimal form.
- Optional 0x1A EOF marker before the SAUCE record.
- Optional 128-byte SAUCE record at the very end of the file (with an
  optional comment block of `5 + 64*N` bytes preceding it).

The loader strips the SAUCE / EOF / comment block automatically. Any
trailing partial avatar (file size not a multiple of 120 after stripping)
is silently dropped, matching Synchronet's tolerance for "malformed
suffix" cases.

Inside `parseSauce`, only `DataType=5` (binary art) and the `FileType`
byte (which encodes the canvas width as `file_type * 2`) are inspected.
For our purposes we just want the boundary, not the metadata.

---

## Storage

Per-user avatars live in:

```
<data-dir>/users/<bbs_id>/<username-lowercased>.ini
```

INI shape (matches `avatar_lib.js:109-119`):

```ini
[avatar]
data     = <base64 of 120 bytes>
disabled = false
created  = 2026-05-04T12:34:56Z
updated  = 2026-05-04T12:34:56Z
```

`disabled = true` keeps the data around but tells the door not to attach
the avatar to outgoing messages. Use this for the `/avatar off` flow so
the user can re-enable later by picking again.

---

## Bundled collections

Shipped inside the binary via `go:embed`, found at
`internal/avatar/assets/avatars/*.bin`. Current set:

| Collection                      | Source                                                                           |
| ------------------------------- | -------------------------------------------------------------------------------- |
| `corporate`                     | Brand logos (Twitter, Apple, etc.).                                              |
| `danger`                        | Misc.                                                                            |
| `DIGDIST.startrek`              | Star Trek faces from DIGDIST.                                                    |
| `ECBBS.animals` / `.emoji` / `.gaming` | ECBBS-curated sets.                                                       |
| `FUTURELD.cyberpunks` / `.Eighties` / `.glitched` / `.misc` | futureland.today contributions.                       |

Updates: drop new `.bin` files into `internal/avatar/assets/avatars/`,
rebuild the binary. They get embedded automatically at build time.

---

## Sysop-managed collections

If you want to add collections without rebuilding (or curating per-BBS),
set `sysop_avatars_dir` in `avatar_chat.ini`:

```ini
sysop_avatars_dir = ./avatars/sysop
```

The door scans the directory at startup and offers every `.bin` file
there as an additional collection in the selector. The collection name
is the filename minus extension. `.bin` files in subdirectories are
**not** scanned (top-level only).

Examples:

```
./avatars/sysop/
├── futurelink.bin           -> "futurelink" collection
├── retro_logos.bin          -> "retro_logos" collection
└── archive/                 (ignored — top level only)
    └── old_avatars.bin      (ignored)
```

To smoke-test: copy any bundled `.bin` to your sysop dir, restart the
door, open `/avatar`, and confirm you see the same collection appear
twice (once "bundled", once with your filename).

---

## How a user picks an avatar

Three paths in the door:

### 1. Library picker (`/avatar`)

Opens a grid of every avatar in every collection, paginated. Auto-sizes
to the screen.

| Key                        | Action                                                |
| -------------------------- | ----------------------------------------------------- |
| Arrows                     | Move within the grid.                                 |
| `PgUp` / `PgDn`            | Page through long collections.                        |
| `Tab` / `]` / `>`          | Next collection.                                      |
| `Shift+Tab` / `[` / `<`    | Previous collection.                                  |
| `Enter`                    | Pick the highlighted avatar.                          |
| `U`                        | Switch to the Zmodem upload flow.                     |
| `D`                        | Disable your avatar (sets `disabled = true`).         |
| `E`                        | Open the in-door pixel editor (BETA).                 |
| `Q` / `Esc`                | Cancel without changing anything.                     |

The action pills along the bottom row show all four actions visually.

### 2. Zmodem upload (selector → `U`)

Pick `Upload` from the selector and you're handed an instructional
modal:

> **Avatar Upload** — Tip: Moebius / Pablo Draw + Upload may be smoother
> than the in-door editor.
>
> 1. Use any ANSI editor that exports CP437 `.bin`.
> 2. Draw inside a 10×6 canvas.
> 3. Save as a `.bin` file.
> 4. Press Enter below; we'll wait for your client to start a Zmodem send.

Press Enter, the door enters Zmodem-receive mode, your terminal client
detects the inbound `ZRINIT` and pops its file picker (Alt-S in
SyncTERM). Pick the `.bin`, send. The door:

1. Receives via the embedded Zmodem-CRC32 receiver.
2. Strips trailing SAUCE / 0x1A markers.
3. Validates the first 120 bytes.
4. Stores into your INI.
5. Returns to chat.

### 3. In-door pixel editor (selector → `E`)

A "scaled-up" 10×12 half-pixel grid editor. Each avatar cell is
displayed as 2 chars wide × 1 char tall on screen (scaled horizontally
2× so pixels look ~square on typical terminal fonts), with the cell's
foreground color painting the top half (CP437 `▀` block) and the
background painting the bottom half. So one avatar cell = two
independently-colored half-pixels.

| Key                | Action                                                          |
| ------------------ | --------------------------------------------------------------- |
| Arrows             | Move cursor by one half-pixel.                                  |
| `Space`            | Paint the current half-pixel with the active color.             |
| `f` / `F`          | Cycle foreground color forward / backward (16 CGA colors).      |
| `g` / `G`          | Cycle background color forward / backward (8 CGA colors).       |
| `Tab`              | Swap FG ↔ BG (clamped to 8 since BG is 3-bit).                  |
| `c`                | Open the CP437 glyph picker (insert any non-forbidden char).    |
| `b`                | Toggle brush mode (every arrow paints + moves).                 |
| `k`                | Flood-fill connected matching pixels with the current color.    |
| `u`                | Undo (last 64 cell mutations).                                  |
| `x`                | Clear the canvas to all-black.                                  |
| `l`                | Load an avatar from any collection as the new baseline.         |
| `h`                | Flip the canvas horizontally.                                   |
| `v`                | Flip the canvas vertically.                                     |
| `s`                | Save (validate, persist, return to chat).                       |
| `Esc`              | Cancel without saving.                                          |

The editor is marked **BETA**. The recommended path for serious art is
still external editor + Zmodem upload — the editor is fine for quick
sketches and experimentation but not as polished as Moebius / Pablo
Draw.

---

## Power-user commands

```
/avatar set <base64>     -- replace your avatar from a base64 120-byte payload
/avatar off              -- disable your avatar without losing it
/avatar pick             -- open the selector directly
```

The `/avatar set` flow is the door's escape hatch for power users who
want to script avatar changes (paste from clipboard, CI integration,
bot-driven testing). The base64 must decode to exactly 120 bytes that
pass validation.

---

## Format references

- **Synchronet's reference:** `/sbbs/repo/exec/load/avatar_lib.js`
  (definitive validation rules at lines 82–107; per-user INI shape at
  lines 109–119).
- **Our parser:** `internal/avatar/avatar.go` (`Validate`,
  `charForbidden`, `Bytes`, `Width`, `Height`).
- **Our collection loader:** `internal/avatar/bundle.go` (SAUCE
  stripping, partial-avatar tolerance).
- **Our store:** `internal/avatar/store.go` (per-user INI read/write).
- **Our selector:** `internal/avatar/selector.go`.
- **Our editor:** `internal/avatar/editor.go`.

If you want to programmatically build avatars, the JS door's
`exec/load/avatar_lib.js` and our Go `internal/avatar/avatar.go` are the
two reference implementations of the same format.
