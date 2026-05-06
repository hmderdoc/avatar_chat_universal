# avatar_chat_universal — Theming Guide

Themes let a sysop override the door's color palette and (optionally)
its idle screensaver profile. A theme is a flat INI under
`themes/<name>.ini` that the door loads at startup based on the main
config's `theme = <name>` key. Anything the theme doesn't set falls
through to the built-in `futurewave` defaults, so partial themes are
fine.

---

## How it works

1. `avatar_chat.ini` declares `theme = futurewave`.
2. At startup, the door tries to load `themes/futurewave.ini` (resolved
   relative to the door's working directory, or absolute if you pass a
   full path).
3. Theme overrides are applied AFTER all other config has been read, so
   theme wins over `avatar_chat.ini` for any keys both touch (notably
   the idle profile).
4. Missing theme file = no error, just the built-in defaults. Missing
   keys = built-in default for that key.

`themes/futurewave.ini` ships in the repo as a working reference. Copy
it to `themes/myname.ini`, change what you want, set
`theme = myname` in the main ini.

---

## Color value syntax

Each color is a pipe-separated combination of CGA tokens:

```
fg            -> just a foreground
fg|bg         -> foreground over background
```

### Foreground tokens (16 colors)

`black`, `blue`, `green`, `cyan`, `red`, `magenta`, `brown`, `lightgray`,
`darkgray`, `lightblue`, `lightgreen`, `lightcyan`, `lightred`,
`lightmagenta`, `yellow`, `white`.

### Background tokens (8 colors)

`bgblack`, `bgblue`, `bggreen`, `bgcyan`, `bgred`, `bgmagenta`,
`bgbrown`, `bglightgray`.

CGA / VGA-text doesn't expose 16-color backgrounds (the 4th attribute bit
is "blink"); avatar art validation rejects the blink-bit slot for the
same reason. Stick to the 8 backgrounds and 16 foregrounds.

Examples:

```ini
header_motd  = white|bggreen
top_action_pill = lightcyan|bgmagenta
notice_default = darkgray
```

Whitespace and case are ignored: `White | Bg_blue` parses but `bg_blue`
isn't a valid token (use `bgblue`).

---

## Color slots

Below is every slot the door currently honors. Values shown are the
`futurewave` defaults.

### Header (top MOTD strip)

| Key            | Default            | Used for                                            |
| -------------- | ------------------ | --------------------------------------------------- |
| `header_stats` | `black\|bggreen`   | "MOTD :" label + the row's background fill          |
| `header_motd`  | `white\|bggreen`   | The MOTD body text                                  |

### Action bars

The top pill row shows quick-link commands; the bottom row shows
secondary commands. Each row has a background, a pill text color, and a
"highlight" attr used for transient flashes (e.g. unread `/img [N]`).

| Key                         | Default              |
| --------------------------- | -------------------- |
| `top_action_bar`            | `white\|bgblue`      |
| `top_action_pill`           | `lightcyan\|bgmagenta` |
| `top_action_highlight`      | `white\|bgred`       |
| `bottom_action_bar`         | `white\|bgmagenta`   |
| `bottom_action_pill`        | `black\|bgcyan`      |
| `bottom_action_highlight`   | `white\|bgred`       |

### Input line (where you type)

| Key            | Default                |
| -------------- | ---------------------- |
| `input_prompt` | `yellow`               |
| `input_text`   | `lightgreen`           |
| `input_cursor` | `lightgray\|bglightgray` |

### Chat bubbles

| Key              | Default            | Used for                       |
| ---------------- | ------------------ | ------------------------------ |
| `bubble_left`    | `black\|bgcyan`    | Other people's messages        |
| `bubble_self`    | `white\|bgblue`    | Your own messages              |
| `bubble_private` | `white\|bgred`     | PMs (regardless of side)       |

### Bubble header line

The line above each bubble showing speaker, timestamp, and BBS host.

| Key            | Default          |
| -------------- | ---------------- |
| `speaker_left` | `lightmagenta`   |
| `speaker_self` | `lightcyan`      |
| `timestamp`    | `lightblue`      |
| `host`         | `magenta`        |

### Notices

The "* Last msg ..." / "* Users in main: ..." / "* /avatar upload: ..."
status lines.

| Key              | Default     |
| ---------------- | ----------- |
| `notice_default` | `darkgray`  |

### Modals

The roster, image viewer, and avatar selector chrome.

| Key           | Default                | Used for                              |
| ------------- | ---------------------- | ------------------------------------- |
| `modal_bg`    | `lightgray\|bgblack`   | Default fill of any modal frame       |
| `modal_title` | `yellow\|bgblue`       | The title bar at the top of a modal   |

---

## Screensaver profile (optional)

A theme can ship its own idle-animation rotation:

| Key             | Notes                                                          |
| --------------- | -------------------------------------------------------------- |
| `idle_random`   | `true` to pick the next animation randomly.                    |
| `idle_sequence` | Comma-separated explicit playback order.                       |
| `idle_disable`  | Comma-separated names to skip.                                 |

These mirror the keys in `avatar_chat.ini` but are scoped to whatever's
in the theme file. If a theme sets any of them, it wins over the main
config; if not set, main config applies.

This is useful for shipping a "vibe" — `cyberpunk.ini` could pin the
rotation to `matrix_rain, plasma, lightning`; `forest.ini` could pin to
`aurora, fireflies, ocean_ripple`. Or skip the screensaver section
entirely and just override colors.

---

## Sample: a "Forest" theme

```ini
; themes/forest.ini

name = forest

header_stats = white|bggreen
header_motd  = yellow|bggreen

top_action_bar       = black|bggreen
top_action_pill      = white|bgbrown
top_action_highlight = white|bgred

bottom_action_bar       = white|bgbrown
bottom_action_pill      = white|bggreen
bottom_action_highlight = white|bgred

input_prompt = brown
input_text   = lightgreen
input_cursor = green|bggreen

bubble_left    = black|bggreen
bubble_self    = lightgray|bgbrown
bubble_private = white|bgred

speaker_left = lightgreen
speaker_self = brown
timestamp    = green
host         = lightgreen

notice_default = green

modal_bg    = lightgreen|bgblack
modal_title = brown|bggreen

idle_random   = false
idle_sequence = aurora, fireflies, ocean_ripple, fire_smoke
```

Then in `avatar_chat.ini`:

```ini
theme = forest
```

---

## Tips

- **Test against a real terminal.** What looks fine in iTerm might be
  unreadable in SyncTERM and vice versa. CGA contrast is a thing.
- **Don't pick `bgblack` everywhere.** The transcript area uses the
  bubble BGs as visual separation; if every bubble is on black, they
  blend into the background animation when the screensaver kicks in.
- **The `notice_default` color is also the default for any unstyled
  text in notices.** Inline color escapes (`\x01w` etc.) override it
  per-segment, but lines without escapes paint in this color.
- **Themes can't change the chrome layout** — the size of the action
  bars, the avatar gutter width, etc. are baked into the rendering
  code. Themes are color-only (plus screensaver profile).

---

## Slot list — what's covered, what's not

Currently themed:

✓ Header bar (stats + MOTD)
✓ Top + bottom action bars (bg, pill, highlight)
✓ Input line (prompt, text, cursor)
✓ Chat bubbles (left, self, private)
✓ Bubble header (speaker, timestamp, host)
✓ Notices (default color)
✓ Modal chrome (bg, title)

Not yet themed (uses hardcoded colors, easy follow-up if you need them):

- The avatar editor's title bar (LightCyan|BgMagenta).
- The "ESC cancel" pill in modals (LightCyan|BgMagenta).
- The animation-specific palettes (each idle anim has its own; not
  swappable via theme).
- The splash screen RGB strobe (uses fixed CGA red/green/blue).
- Sweep glow effect on join/leave notices (uses a fixed gradient).

If you want any of these themable, file an issue with the slot name and
I'll add it.
