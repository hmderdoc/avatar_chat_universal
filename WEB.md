# Web chat page

`avatar_chat_server` can serve a web page for Avatar Chat: plain HTML, one
JavaScript file and one CSS file, no build step. Web users share rooms
with the doors, the shell and the bridges.

The page only ever talks to the server that served it. The chat, the
pictures in messages, link cards and uploads all go through that one
origin, and the page's Content-Security-Policy keeps the browser to it.
The server either hosts the chat itself or relays web users to someone
else's chat server.

Synchronet boards have their own web app, which goes through the BBS's
APIs and logins (`web/synchronet`). This page is for sysops without
Synchronet, or without a BBS at all.

## Two ways to run it

**Host your own chat.** The usual chat server on TCP for doors, plus the
web listener:

```sh
./avatar_chat_server -addr :10088 -web-addr :8080 -system-name "My BBS"
```

**Relay to someone else's chat.** Your server hosts no chat; each web user
gets their own TCP connection to the other server, the way a door would:

```sh
./avatar_chat_server -upstream futureland.today:10088 -web-addr :8080 -system-name "My BBS"
```

With `upstream` set there is no TCP listener on your side. The upstream can
be another `avatar_chat_server` or a Synchronet board's `json-service.js`;
both speak the same packets.

Then open `http://your-host:8080/`. A user picks a nickname, which the
browser remembers, and lands in the default room. `?room=name` opens
another room.

## Settings

As flags, or in the `-config` INI at the top or under `[web]`. A flag wins
over the INI.

```ini
; avatar_chat_server.ini
[web]
web_addr        = :8080
system_name     = My BBS
default_channel = main
; upstream      = futureland.today:10088
; web_create_rooms = on
```

| Key (INI) | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `web_addr` (or `ws_addr`) | `-web-addr` (or `-ws-addr`) | (off) | Listen address for the page and its WebSocket. |
| `upstream` | `-upstream` | (none) | `host:port` of a chat server to relay web users to. Set it and this server hosts no chat. |
| `system_name` | `-system-name` | `web` | The board name stamped on web users' messages and in who's-here. |
| `default_channel` | `-default-channel` | `main` | The room the page opens in. |
| `web_create_rooms` | `-web-create-rooms` | off | Let web users register new public rooms. |
| `web_max_conns` | `-web-max-conns` | `50` | Web users connected at once. In relay mode this caps your connections to the upstream. |
| `web_max_per_ip` | `-web-max-per-ip` | `5` | Web users connected at once from one address. |
| `ws_origins` | `-ws-origins` | (none) | Other web pages allowed to open `/chat`: a full origin (`https://bbs.example.org`) or a bare host name. The page's own origin is always allowed. |
| `web_dir` | `-web-dir` | (built in) | Serve the page from this directory instead. See Theming. |
| `web_cache_dir` | `-web-cache-dir` | `webcache` | Where fetched pictures and link cards are cached; `""` for none. |
| `image_max_mb` | `-image-max-mb` | `5` | Largest picture `/img` passes through. |
| `fetch_per_minute` | `-fetch-per-minute` | `120` | `/img` plus `/preview` requests per address per minute. |
| `deny_file` | `-deny` | (none) | Addresses refused everywhere: chat, web and media. |

**Uploads.** Configure the media endpoint (MEDIA.md) and the page accepts
dropped or pasted pictures and ANSI art, posts them to `/upload` on the
same origin, and puts the link in the composer. `media_addr = web` serves
it only on the web listener. A `media_addr` port of its own keeps it there
too (for doors' `upload_url`) and also mounts it on the web listener.

## What the server does for the page

| Route | What |
| --- | --- |
| `/` | The page (`index.html`, `app.js`, `codec.js`, `style.css`, `cp437.png`). Nothing else is served from `web_dir`. |
| `/chat` | WebSocket carrying chat packets. Upgrades at `/` still work, the way the old `ws_addr` listener took them. |
| `/config.json` | Default room, board name, message limit, whether uploads and room creation are on. |
| `/img?url=` | A picture from a chat message, fetched by the server. |
| `/preview?url=` | A link's card (title, description, picture), fetched by the server. |
| `/upload`, `/chatmedia/…` | Attachments, when media is configured. |

### What a web user may send

The chat protocol has no accounts, so the server screens every packet a
browser sends before it reaches any chat server, yours or the upstream:

- Only what the page uses gets through: join and leave a room, post to it,
  load its history, who's here, list rooms, and creating a room if
  `web_create_rooms` is on. Anything else gets an error back.
- Rooms are the default room or ones in the chat's `public_channels`
  registry, the list the door's `/channels` shows. A web user can't open a
  member's private mailbox by naming it as a room.
- The nickname is set when the user first joins and stays for that
  connection; changing it reconnects. Every message is stamped with that
  nick and `system_name`, whatever the browser claims. The server also
  sets the time, cleans the text, caps it at 1000 characters, refuses TV
  tuner markers, and drops a malformed avatar or style.

This doesn't make names trustworthy, since anyone can pick any nickname.
What it does is label every web message with your board, so other boards
can tell who carried it. Private messages and posting the message of the
day aren't offered on this page.

### `/img` and `/preview` are not an open proxy

- They only fetch a URL that has gone out to a browser on this server in
  chat traffic (a message, history), that a web user posted, or that a
  card names as its picture. Anything else gets 403.
- Fetches use the door's guards: http and https on ports 80 and 443 only,
  every resolved address checked to be public at connect time, redirects
  re-checked, a byte cap and a timeout.
- `/img` passes only png, jpeg, gif, webp and bmp, judged by the bytes,
  never HTML or SVG. It sends them with `nosniff`, a sandboxing CSP and a
  day's caching.
- `fetch_per_minute` applies per address, and at most eight fetches run at
  once. Put a cache or rate limit in your reverse proxy too if the page is
  public.

Anyone can still post a URL in chat to make your server fetch it once.
That's the same exposure a door's link previews have.

## HTTPS

The listener speaks plain HTTP and `ws://`. For a public page put it
behind a TLS proxy (nginx, Caddy, HAProxy) that also forwards WebSocket
upgrades on `/chat`. The page picks `wss://` itself when it's loaded over
HTTPS. Uploads get `https://` links from the page's own origin, or set
`media_url_base`.

Behind a proxy every browser appears to come from the proxy's address.
`X-Forwarded-For` is not trusted, so the per-address limits and
`deny_file` see the proxy. Do per-client filtering at the proxy.

An nginx sketch:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_read_timeout 120s;
}
```

`Host` must reach the server unchanged. The WebSocket origin check compares
the page's `Origin` with it.

## Theming

Copy `web/standalone` from the repository somewhere, edit `style.css` (the
CGA colours are custom properties at the top), and point `web_dir` at the
copy. The server serves only the page's file types from there (html, js,
css, png, ico, woff2) and refuses dotfiles.

## Wire format

The page speaks the packets the doors send, over WebSocket:

- It opens `/chat` with the `binary` subprotocol and sends each packet as
  `JSON.stringify(packet) + "\r\n"` in one frame.
- It buffers what it receives and splits it on newlines, so it copes with a
  packet spread over frames or several in one. The server sends one packet
  per text frame.
- It skips lines that aren't JSON and answers a `SOCKET` `PING` with a
  `PONG`.

The server pings every 30 seconds and drops a browser that sends nothing,
not even a pong, for 75 seconds. Packets over 128 KB close the socket with
code 1009. In relay mode the server answers the upstream's own keepalive
pings itself.
