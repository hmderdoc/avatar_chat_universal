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

## Per-user themes

Every theme in `themes/` is on offer to users unless `avatar_chat.ini`
sets `user_themes = off`:

- `/theme` lists them (the active one highlighted).
- `/theme <name>` switches at once and remembers the pick for next time.
- `/theme default` goes back to the sysop's `theme`.
- `/settings` has a Theme row: `c` / `C` step through the themes, the
  panel redraws in the picked theme's colours as a preview, and `S`
  applies and saves it.

The sysop's `theme` stays the default for anyone who hasn't picked. A
pick is a door preference, stored in `<data>/prefs/<bbs>/<user>.ini`,
never in the BBS's user records (in Synchronet mode the avatar and handle
style are board-wide; the theme is only this door's). If a sysop removes
a theme someone picked, that user quietly gets the default.

A theme's name is its file name, lowercase letters, digits, `-` and `_`
(the `name =` key inside is ignored for this). Anything else in `themes/`
isn't offered. Switching themes resets the screensaver profile to the
main config's before applying the new theme's, so profiles don't stack.

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

### Buttons

The two rows under the header hold the raised buttons (Who, Rooms,
Images, Settings, Help, Quit): a face with the label on the first row and
a half-block shadow under it and to its right.

| Key                    | Default                | Used for                                              |
| ---------------------- | ---------------------- | ----------------------------------------------------- |
| `top_action_bar`       | `white\|bgblue`        | The rows' fill around the buttons                     |
| `top_action_pill`      | `lightcyan\|bgmagenta` | A button's face and label                             |
| `top_action_highlight` | `white\|bgred`         | A button calling for attention (flashing `Images [N]`, `Who [N]` for PMs, `Rooms [N]`) |
| `top_action_shadow`    | `black`                | The shadow (foreground only, drawn on `top_action_bar`'s background); pick a colour darker than that background |

### Strips

| Key          | Default             | Used for                                                       |
| ------------ | ------------------- | -------------------------------------------------------------- |
| `strip_bar`  | `white\|bgmagenta`  | The TV lounge's key strip over the picture, and the screensaver's message ticker |
| `strip_pill` | `black\|bgcyan`     | The key labels on the lounge strip                             |

There is no bottom bar any more. A theme file written for it still loads:
`bottom_action_bar` and `bottom_action_pill` set `strip_bar` and
`strip_pill` (which they coloured before), and `bottom_action_highlight` is
ignored.

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
| `bubble_mention` | `black\|bgbrown`   | Others' lines that say your name (PMs keep `bubble_private`) |
| `mention_edge`   | `yellow`           | Bar down the left edge of a mention (foreground only) |
| `card_site`      | `darkgray`         | Site name on a link-preview card (foreground only) |
| `card_title`     | `white`            | Page title on a link-preview card (foreground only) |
| `card_edge`      | `lightblue`        | Bar down the left edge of a link-preview card (foreground only) |

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

### Input row fill

| Key        | Default              | Used for                                   |
| ---------- | -------------------- | ------------------------------------------ |
| `input_bg` | `lightgray\|bgblack` | The rest of the input row, past the text   |

### Pickers and panels

`/who`, `/channels`, `/settings` (and its colour grid) and the `/img`
viewer. Each is drawn in a box with an `[X]` at its top left that a mouse
click treats as Esc; the upload dialog gets the same box.

| Key            | Default                | Used for                                        |
| -------------- | ---------------------- | ----------------------------------------------- |
| `modal_bg`     | `lightgray\|bgblack`   | Fill and plain list text                        |
| `modal_title`  | `yellow\|bgblue`       | The title on the box's top edge                 |
| `modal_close`  | `white\|bgblue`        | "[Esc] close" on the `/settings` top edge       |
| `modal_text`   | `white\|bgblack`       | Values and key letters                          |
| `modal_label`  | `lightcyan\|bgblack`   | Section headings and field labels               |
| `modal_select` | `black\|bgcyan`        | The highlighted row                             |
| `modal_hint`   | `yellow\|bgblack`      | Key help along the bottom, the letter marker    |
| `modal_unread` | `yellow\|bgblack`      | Rooms with unread lines in `/channels`          |
| `modal_rule`   | `darkgray\|bgblack`    | Divider lines, "(none)"                         |
| `modal_ok`     | `lightgreen\|bgblack`  | Info line in `/settings`                        |
| `viewer_bar`   | `white\|bgblue`        | `/img` header and footer on the box edges       |
| `modal_frame`  | `yellow`               | The box around each panel and dialog (foreground only, drawn on the panel's background) |
| `close_button` | `lightred\|bgblack`    | The `[X]` at the top left of each panel and of the chat screen's first row |

### Dialogs

The avatar-upload instructions box drawn over the chat.

| Key            | Default                 |
| -------------- | ----------------------- |
| `dialog_bg`    | `white\|bgblue`         |
| `dialog_title` | `lightcyan\|bgmagenta`  |
| `dialog_head`  | `lightcyan\|bgblue`     |
| `dialog_hint`  | `yellow\|bgblue`        |
| `dialog_ok`    | `lightgreen\|bgblue`    |

### NSFW veil

| Key          | Default               | Used for                       |
| ------------ | --------------------- | ------------------------------ |
| `veil_dark`  | `darkgray\|bgblack`   | Darker shade-noise cells       |
| `veil_light` | `lightgray\|bgblack`  | Lighter shade-noise cells      |
| `veil_label` | `white\|bgmagenta`    | "NSFW - /nsfw to show"         |

### TV lounge

| Key           | Default               | Used for                                  |
| ------------- | --------------------- | ----------------------------------------- |
| `tv_caption`  | `white\|bgblue`       | Live-subtitle bar on the last row of the picture |
| `tv_chat`     | `white\|bgblack`      | Chat lines shown over the picture         |
| `tv_chat_dim` | `lightgray\|bgblack`  | The band behind them, the ": " separator  |

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
top_action_shadow    = black

strip_bar  = white|bgbrown
strip_pill = white|bggreen

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

## Shipped themes

`futurewave` (the default, and the built-in values), `green`, `midnight`,
`amber`, `forest` and `sunset`. Each sets every key, so any of them is a
complete template; a test in the repo lints them for unknown keys, bad
colours and text the same colour as its background.

## Not themable

- The avatar editor and `/draw` editor chrome.
- Each screensaver animation's own palette.
- The splash screen strobe and the join/leave sweep glow.
- Handle styles: those are each user's own colours.
