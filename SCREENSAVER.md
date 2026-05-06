# avatar_chat_universal — Screensaver Guide

When a user is idle (no keypress) for `idle_timeout_seconds`, the chat
transcript area swaps to a screensaver. This doc covers the available
animations, the ANSI gallery, and the interleave behavior.

---

## How idle works

1. Every keypress resets the "last active" timestamp.
2. After `idle_timeout_seconds` of no input, the door starts an
   animation in the transcript area.
3. The animation runs at `idle_fps` frames per second (or the
   animation's preferred FPS, if it overrides).
4. After `idle_switch_interval` seconds, the door rotates to the next
   animation.
5. Any keypress dismisses the screensaver and restores the chat UI.
6. **Incoming chat messages do NOT dismiss the screensaver.** Instead
   the latest message shows as a 6-second ticker overlay on the bottom
   of the screen, then fades back to transparent. This way you can leave
   the chat idling, see activity at a glance, and only intervene when
   you actually want to.

---

## Configuration recap

Full reference is in [CONFIG.md](CONFIG.md); the relevant keys:

```ini
idle_enabled             = true       ; master switch
idle_timeout_seconds     = 180        ; how long before idle kicks in
idle_switch_interval     = 60         ; rotate animations every Ns
idle_fps                 = 8          ; default frame rate
idle_random              = true       ; random vs sequence
idle_sequence            = ...        ; explicit playback order
idle_disable             = ...        ; names to skip
idle_interleave_ansi     = true       ; insert ANSI gallery between procedural anims
ansi_gallery_dir         = ./ansi_gallery
```

---

## The animation catalog

Background animations paint behind the chat. Foreground animations
paint a transparent overlay so the chat shows through where the
animation isn't drawing. (Foreground anims are subtler; they coexist
with the chat rather than replacing it.)

### Background

| Name           | What it looks like                                                  |
| -------------- | ------------------------------------------------------------------- |
| `tv_static`    | TV static / snow.                                                   |
| `matrix_rain`  | Falling green characters, Matrix-style.                             |
| `life`         | Conway's Game of Life on a CP437 grid.                              |
| `starfield`    | Forward-warp star field.                                            |
| `fireflies`    | Soft pulsing yellow specks drifting on dark background.             |
| `sine_wave`    | Color-cycling sine wave traversing horizontally.                    |
| `comet_trails` | Small bright bodies leaving fading trails.                          |
| `plasma`       | Classic demoscene plasma color field.                               |
| `fireworks`    | Bursts of color from random launch points.                          |
| `aurora`       | Slow vertical color gradient, aurora-like.                          |
| `fire_smoke`   | Fire effect with smoke rising.                                      |
| `ocean_ripple` | Concentric ripples from random origins.                             |
| `lissajous`    | Animated Lissajous curves cycling through parameters.               |
| `lightning`    | Branching forks across the screen at random intervals.              |
| `tunnel`       | Texture-mapped tunnel effect.                                       |
| `ansi_gallery` | Vertical scroll through SAUCE-tagged ANSI / BIN art (see below).    |

### Foreground

| Name              | What it looks like                                                |
| ----------------- | ----------------------------------------------------------------- |
| `avatars_float`   | 4–12 user avatars bouncing around as 10×6 sprites; on collision they swap velocities and exchange short greetings ("hi alice" / "hey bob") that float above each one for a few seconds. |
| `figlet_message`  | Big block-letter messages from `idle_figlet_messages`, color-cycled per char, optionally bouncing.       |

---

## ANSI gallery

`ansi_gallery` is a special background animation that scrolls SAUCE-tagged
ANSI art through the screensaver area. Sysops drop `.ans` / `.bin` files
into a directory and the door picks them up.

### Setup

```ini
ansi_gallery_dir = ./ansi_gallery
```

The path can be relative (resolved from the door's working dir) or
absolute. Subdirectories are walked recursively; organize however you
like.

```
./ansi_gallery/
├── 1992/
│   ├── ice0192a.ans
│   └── ice0192b.ans
├── acid/
│   └── acdu0299.ans
├── moebius-exports/
│   └── my_art.ans
└── splash.bin
```

If you have the `sixteencolors-archive` cloned, point the dir at it
directly:

```ini
ansi_gallery_dir = /sbbs/text/16Colors/sixteencolors-archive-master
```

### How it renders

1. At idle activation (or interleave turn), the gallery picks a random
   file from its pool.
2. Loads it through the same SAUCE-aware parser used for the splash —
   SGR colors, cursor positioning, charset (CP437 / UTF-8) all honored.
3. Positions horizontally:
   - Art ≤ display width → centered horizontally.
   - Art > display width (132 / 160-col art) → clipped at the right
     edge, anchored at column 0 (alignment of the rest of the artwork
     is preserved; no reflow).
4. Scrolls vertically: art enters from the bottom, rises through the
   visible window, exits off the top.
5. When the art is fully off-screen, picks a new file.

Speed: ~3 rows/sec at 12 fps (the gallery requests its own preferred
FPS to make the scroll smooth). Tweakable in `internal/idle/ansigallery.go`
(`scrollPerTick`).

### Interleave

`idle_interleave_ansi = true` (the default) makes the rotation alternate
between procedural animations and one piece of ANSI art:

```
[matrix_rain for 60s]
   ↓
[one piece of ANSI art, scrolls to off-screen, 8–16s]
   ↓
[plasma for 60s]
   ↓
[next piece of art]
   ↓
[aurora for 60s]
   ↓
[next piece of art]
   ↓
...
```

This mirrors the JS `future_shell` screensaver behavior. Set to `false`
if you'd rather the gallery only show when explicitly listed in
`idle_sequence`, or skip it entirely.

When interleave is off and you still want art in the rotation, just put
`ansi_gallery` in `idle_sequence`:

```ini
idle_random = false
idle_sequence = starfield, ansi_gallery, plasma, ansi_gallery, aurora
idle_interleave_ansi = false
```

In that case the gallery plays multiple pieces back-to-back during its
turn (until `idle_switch_interval` elapses).

---

## Custom animation profiles per theme

Themes can ship their own idle profile. See [THEMING.md](THEMING.md);
short version: in `themes/<name>.ini`,

```ini
idle_random   = false
idle_sequence = aurora, fireflies, ocean_ripple
idle_disable  = lightning, fireworks
```

Theme overrides win over `avatar_chat.ini`. Useful for "vibe" themes
that pin the screensaver to a coordinated set.

---

## When messages arrive during idle

The screensaver does NOT dismiss when a message arrives. Instead, a
one-line ticker shows the latest sender + message at the bottom of the
screen for ~6 seconds, then fades back to transparent so the animation
shows through. Subsequent messages re-extend the 6-second window.

Why: idle screensavers feel broken if every chat message kills them.
Activity should be visible without being intrusive. If you actually
want to read the chat, press any key and the screensaver dismisses
normally.

The ticker uses the theme's `bottom_action_bar` palette so it visually
"belongs" with the chat chrome.

---

## Disabling everything

If you want no screensaver at all:

```ini
idle_enabled = false
```

The transcript stays on the chat regardless of how long the user is
idle. Messages still arrive and render normally.

---

## Source

- `internal/idle/idle.go` — the `Animation` interface, `Category`
  (background / foreground), and the `Registry`.
- `internal/idle/ansigallery.go` — the ANSI gallery animation,
  including one-shot mode used by the interleave path.
- `internal/idle/<name>.go` — one file per procedural animation.
- `internal/ui/app.go` — `idleTick`, `startAnimation`, `renderIdleTicker`
  for the screensaver-friendly chat overlay.
