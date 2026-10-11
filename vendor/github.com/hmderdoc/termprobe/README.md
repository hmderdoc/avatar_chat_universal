# termprobe

In-band color depth detection for BBS doors and other terminal programs that
only have the terminal's own byte stream to go on. No dependencies, no
platform code, no help from the BBS.

```go
import "github.com/hmderdoc/termprobe"

// Before your input reader goroutine starts:
res := termprobe.ColorDepth(conn, termprobe.DeadlineReader(conn), 400*time.Millisecond)
log.Println("color:", res)          // truecolor (CTerm 1.3 (SyncTERM) answered Device Attributes; 1 replies, 0 leftover bytes)
switch res.Depth {
case termprobe.DepthTrue:  // emit 38;2;r;g;b
case termprobe.Depth256:   // emit 38;5;n
default:                   // emit the 16-color palette
}
input.Unread(res.Leftover) // keystrokes typed during the probe
```

## How it decides

Four standard queries go out in one burst; replies are read for a bounded
time, ending early when Device Attributes answers.

| Query | Terminal answers | Meaning |
|---|---|---|
| `CSI > q` (XTVERSION) | `DCS > \| name version ST` | known 24-bit terminal → truecolor; anything else → 256 |
| `SGR 38;2;1;2;3` then `DCS $ q m ST` (DECRQSS) | echo of the current SGR | `38;2` kept → truecolor; `38;5` → the terminal quantized, 256 |
| `CSI ? 2 ; 1 S` (XTSMGRAPHICS) | `CSI ? 2 ; 0 ; w ; h S` | sixel-capable terminal (fTelnet, xterm+sixel) → truecolor |
| `CSI c` (DA) | `CSI = 67;84;101;114;109;maj;min;rev c` | CTerm/SyncTERM ≥ 1.0 → truecolor |

**Silence means 16 colors.** NetRunner, the Linux console and most Windows
telnet clients answer none of these and render 24-bit SGR as mush, so the
default has to be the classic palette. Only a positive answer raises it.

The DECRQSS echo outranks a terminal name: a terminal that quantized the
24-bit SGR renders 256 colors whatever it calls itself.

## Sixel and cell size

The same probe says whether the terminal draws DEC sixel (`Result.Sixel`,
with `Result.SixelReason`):

- **xterm family:** DA1 (`CSI ? level ; attrs c`) lists attribute `4`.
- **CTerm / SyncTERM:** CTerm Device Attributes (`CSI < c`) lists
  attribute `4`, pixel operations; a text-mode SyncTERM build answers
  without it. Every CTerm is asked, whatever revision it reports. Synchronet's
  `cterm_lib.js` `supports_sixel()` only asks from 1.207, but forks such as
  fTelnet_enhanced report an older revision and answer truthfully. A CTerm
  that does not answer is never sent sixel.

Only when one of those looks possible does a second, short burst go out:
`CSI < c` (CTerm only), then `CSI 16 t`, `CSI 14 t`, `CSI 18 t` for the cell
size in pixels, ended early by another `CSI c`. A sane answer lands in
`Result.CellW` / `Result.CellH`; otherwise they stay 0 and a door should
assume 8x16. Plain terminals see exactly the first burst and nothing more.

## Readers

The probe reads through a `TimedReader`, a read that gives up after a
duration and reports the timeout as `(0, nil)`:

- `DeadlineReader(conn)` for a `net.Conn` or anything with `SetReadDeadline`.
- `PollReader(read, interval)` for a non-blocking descriptor, where `read`
  reports `again=true` on EAGAIN.
- A type with its own `ReadTimeout(p, d)` method satisfies it directly.

Run the probe before any goroutine that reads terminal input, or that
goroutine will eat the replies as keystrokes. Bytes read during the probe
that are not part of a reply come back in `Result.Leftover`.

## Doors using it

- [Spekder](https://github.com/hmderdoc/spekder) (OPTIONS > COLOR pins a choice; otherwise probed)
- [telnetvision](https://github.com/hmderdoc/telnetvision) door (`color = auto`)
- [avatar_chat_universal](https://github.com/hmderdoc/avatar_chat_universal) TV lounge (`tv_color = auto`)

MIT licensed.
