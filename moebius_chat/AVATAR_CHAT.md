# Moebius × Avatar Chat

This vendored copy of [Moebius](https://github.com/blocktronics/moebius) is wired
into the Avatar Chat ecosystem. Three phases from `objective.MD` are implemented.

## What works

1. **Protocol-level chat** — the joint server mirrors its `CHAT` traffic both
   ways against Avatar Chat's JSON-over-TCP protocol, alongside the existing
   Discord webhook. Anyone in Moebius chat talks to BBS users (and vice versa),
   plus any other bridges on the channel (Discord/IRC/Telegram/…).
2. **Send to Chat** (File ▸ Send to Chat, `Ctrl/Cmd+Shift+C`) — encodes the
   current canvas as a `[BITMAP|…]` message and posts it to the channel. This is
   the existing ecosystem convention (the JS door and Go bridges already decode
   it), so terminal users and bridges render it too. The encoder is byte-for-byte
   compatible with `internal/bitmap/bitmap.go` (cross-checked).
3. **Rich chat** — the chat panel renders sender **avatars** (10×6 CP437 blocks)
   and inline **art** (`[BITMAP]` messages) using Moebius's own font, and colours
   each nick. Set your avatar with File ▸ Set Avatar from Canvas (uses the
   top-left 10×6 of the current canvas; persisted in the renderer's localStorage
   and attached to your outgoing messages on `message.nick.avatar`).

## Architecture (server-side mirror)

```
Moebius client ──ws CHAT {nick,group,text,avatar}──▶ joint server (app/server.js)
                                                       │  ├─ broadcast to other Moebius clients
                                                       │  ├─ discord webhook (existing)
                                                       │  └─ app/avatar_chat.js connector ──TCP JSON──▶ Avatar Chat server
Avatar Chat server ──UPDATE──▶ connector ──inject_chat()──▶ all Moebius clients (id -1)
```

- `app/avatar_chat.js` — JSON-over-TCP connector (subscribe, WRITE/PUSH, PING/PONG,
  reconnect). Echo-dedup via the bridgecore host-marker (`BRIDGE:moebius:<host>/<chan>`).
- `app/avatar_codec.js` — pure (DOM-free) avatar + BITMAP codec, shared by the
  menu actions (encode) and the chat panel (decode).
- Chat is mirrored **at the server**, so chat/Send-to-Chat require being **joined
  to a server**. A solo, unconnected editor has no chat (by design).

## Run it

```sh
cd moebius_chat
# 1. Point the joint server at an Avatar Chat server. Use your local one,
#    or futureland.today:10088 for the public board.
node server.js --file build/ans/6.5.ans --avatar_chat 127.0.0.1:10088:main
#    (--avatar_chat host[:port[:channel]]; port default 10088, channel default main)

# 2. In another terminal, launch the editor.
npm start
```

In Moebius: **File ▸ Connect to Server**, enter `localhost:8000`. The chat panel
opens on connect.

### Try
- **Chat:** type in the chat box → it appears for BBS users on the channel, and
  their messages appear in Moebius.
- **Avatar:** draw something in the top-left 10×6 cells, File ▸ Set Avatar from
  Canvas, then chat → your portrait rides next to your messages.
- **Send art:** File ▸ Send to Chat → the canvas posts as `[BITMAP]`; it renders
  inline in the chat panel, on other Moebius clients, on the BBS door, and on
  bridges (as a PNG).
- Best visual test: connect **two** Moebius clients (or chat from the BBS door)
  so you see avatars + art rendered on the receiving side.

## Known limits (call them out / candidates for follow-up)

- Avatars/art in the **initial backlog replay** show a text placeholder if the
  document font hasn't rendered yet; live messages render fully.
- Incoming **256-colour** BITMAPs are clamped to the 16-colour font (Moebius's
  own art is 16-colour, so its output is exact).
- `Send to Chat` supports canvases up to **255 rows** (BITMAP height is one byte)
  and ~120 KB encoded (the avatar-chat packet ceiling); larger canvases are
  refused with a dialog rather than truncated.
- Avatar persistence uses **localStorage**, not Moebius `preferences.json`
  (kept the change renderer-only; easy to switch).

## Files changed in the vendored tree

- `app/avatar_chat.js` (new), `app/avatar_codec.js` (new)
- `app/server.js` (mirror + avatar passthrough), `server.js` (`--avatar_chat` flag)
- `app/menu.js` (File menu items)
- `app/controller.js` (Send to Chat / Set Avatar handlers)
- `app/document/doc.js` (avatar var, relaxed CHAT guard, outgoing avatar, set_avatar)
- `app/document/ui/chat.js` (avatar + art + colorized nick rendering)
