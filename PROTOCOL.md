# The chat wire protocol

What a client needs to talk to an Avatar Chat server: the public one at
`futureland.today:10088` (Synchronet's `json-service.js` running
`json-chat`), the bundled `avatar_chat_server`, or the bundled server's
WebSocket front for browsers. This is the protocol the JS door, this door,
the bridges, the web pages and the PS3 client
([avatar_chat_ps3](https://github.com/hmderdoc/avatar_chat_ps3)) all speak.
The Go side of it is `internal/chat` (client) and `internal/chatserver`
(server).

## Transport

- TCP, plain text. One JSON object per line, terminated by `\r\n`. A
  bare `\n` is accepted on input; blank lines are ignored.
- A line may not exceed **131072 bytes** (`json-sock.js` `max_recv`);
  the server drops the connection on a longer one.
- Over WebSocket (`/chat` on the bundled server's web listener) each
  frame carries one packet and needs no line ending. Offer the
  subprotocol `binary` or `plain`.
- Strings are UTF-8. BBS clients convert to and from CP437 on screen.
- No authentication. A connection is whoever it says it is; the first
  SUBSCRIBE names it.

## Packet shape

Every packet is an object with some of these fields; unused ones are
left out.

| Field | Meaning |
| --- | --- |
| `scope` | A namespace. Chat uses `chat`; keepalives use `SOCKET`. |
| `func` | Outbound: `QUERY`, `PING`, `PONG`. Inbound: `RESPONSE`, `UPDATE`, `PING`, `PONG`, `ERROR`. |
| `oper` | The operation for a QUERY (below). A RESPONSE and an UPDATE echo it. |
| `location` | A dotted path into the server's data tree, e.g. `channels.main.messages`. |
| `data` | The payload. |
| `lock` | `1` to read, `2` to write, `-1` to unlock. Clients send it; the servers here accept any value. |
| `timeout` | Send `-1`. |
| `nick`, `system` | On SUBSCRIBE: who you are and the name of your board. |

A request:

```json
{"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"channels.main.messages","nick":"SiX","system":"PS3","timeout":-1}
```

A reply, when there is one, is a `RESPONSE` with the same `oper` and
`location`:

```json
{"scope":"chat","func":"RESPONSE","oper":"SUBSCRIBE","location":"channels.main.messages","data":true}
```

Replies are not matched by id. Match them by `oper` (and `location`):
fire-and-forget operations (WRITE, PUSH, SUBSCRIBE, UNSUBSCRIBE) may or
may not be acknowledged depending on the server, so a client waiting for
a SLICE answer must skip any other RESPONSE that arrives first. An
`ERROR` can answer anything:

```json
{"func":"ERROR","data":{"description":"you're sending messages too fast; slow down"}}
```

## Operations

| `oper` | Sends | Gets back |
| --- | --- | --- |
| `SUBSCRIBE` | `location`, `nick`, `system` | Nothing you need. Everyone else subscribed there gets an `UPDATE` with `oper: SUBSCRIBE` and `data: {"nick","system"}` (a join notice). |
| `UNSUBSCRIBE` | `location` | The same as a leave notice. A dropped connection is announced the same way. |
| `WRITE` | `location`, `data` | Everyone subscribed at `location` (except the sender on the bundled server) gets `UPDATE` with `oper: WRITE` and your `data`. |
| `PUSH` | `location`, `data` | Appends to the array at `location`. No fan-out. |
| `SLICE` | `location`, `data: {"start": -40}` (optional `"end"`) | `RESPONSE` whose `data` is the array slice, JavaScript `slice` semantics. `[]` when empty. |
| `READ` | `location` | `RESPONSE` with the value, or `null`. |
| `WHO` | `location` | `RESPONSE` with `[{"id", "nick", "system"}, ...]`, one per subscriber. Some servers answer with bare strings. |
| `KEYS` | `location` | `RESPONSE` listing the child keys as an array of strings (Synchronet) or an object whose keys are the names. |
| `STATUS` | `location` | `RESPONSE` `{"locked": false}`. Unused. |

## Where things live

| Location | What |
| --- | --- |
| `channels.<room>.messages` | The live room. Subscribe here; WRITE a message here to say something. |
| `channels.<room>.history` | The room's log. PUSH each message you WRITE; SLICE it with a negative `start` to load the recent backlog. The bundled server keeps `max_history` entries (default 1000). |
| `channels.<nick>.messages` and `.history` | A user's private mailbox, by plain name. Subscribe to your own on connect; WRITE and PUSH there to send a private message to that user. |
| `channels.<name>@<origin>.messages` | A standalone web user's mailbox (see Private messages). |
| `channels.<room>.tvtuner` | TV-lounge state: a one-element array holding the last `[TVTUNER|...]` marker. Doors read it on join. |
| `channels.motd.history` | The message of the day: the doors show the last entry. |
| `public_channels` | The room registry: KEYS lists the public rooms. |

Room names are `[A-Za-z0-9_-]{1,32}`; compare them without case.

## Messages

The `data` of a chat WRITE, PUSH and UPDATE:

```json
{
  "nick": {
    "name": "SiX",
    "host": "PS3",
    "avatar": "<base64 of 120 bytes>",
    "style": {"colors": ["#ff5555", "..."], "tag": null},
    "mailbox": "alice@My_BBS"
  },
  "str": "hello world",
  "time": 1761000000000
}
```

- `nick.name`: the handle, up to 25 characters of letters, digits, space,
  `-` and `_`. `nick.host`: the sender's board, shown after the name.
- `nick.avatar` (optional): a 10x6 CP437 avatar, 120 bytes of (char,
  attribute) pairs row-major, base64 encoded. Reject it if any char
  byte is `0x00 0x07 0x08 0x09 0x0A 0x0C 0x0D 0x1B 0xFF` or any attribute
  has bit 7 (blink) set. Senders may pad past 120 bytes; take the first
  120. The format is in [AVATARS.md](AVATARS.md).
- `nick.style` (optional): per-letter handle colours; ignore it if you
  don't draw them. `nick.mailbox` (optional): see below.
- `str`: the text. Up to 1000 characters; strip control characters
  before display.
- `time`: unix milliseconds when sent. Used to group messages and to
  tell your own echo from a repeat.

A sender who also subscribes to the room may receive its own WRITE back
from some servers (Synchronet fans out to everyone). Drop a message whose
`nick.name` is yours and whose `time` and `str` match one you sent in
the last few seconds.

### Markers

Some messages are control markers, ordinary `str` values the clients
agree to interpret and hide:

| `str` starts with | Meaning |
| --- | --- |
| `[BITMAP|` | An image or ANSI art envelope, `[BITMAP|<w>x<h>|<base64>]`; show "<name> sent an image" if you can't render it. |
| `[TVTUNER|` | `[TVTUNER|<host>|<port>|<channel>]` tunes the room's TV lounge to a telnetvision feed, `[TVTUNER|off]` turns it off. |

### Private messages

A private message is a normal message written to the recipient's
mailbox: WRITE to `channels.<name>.messages`, PUSH to
`channels.<name>.history`. You see it because you subscribed to your
own `channels.<yournick>.messages` on connect.

Standalone web users have no BBS login, so their messages carry a
`nick.mailbox` of the form `name@origin`, where `origin` is the server's
`system_name` with every character outside `[A-Za-z0-9_-]` turned into
`_`. Honour it only when it is exactly `name@origin(host)` for that
sender (compared without case, and never containing a dot); then send
your reply to `channels.<mailbox>.messages` instead of the plain name. A
mailbox that doesn't match is a forgery: ignore it and use the name.

## Keepalive

The server sends `{"scope":"SOCKET","func":"PING","data":<ms>}` about
every 60 seconds; answer `{"scope":"SOCKET","func":"PONG","data":<ms>}`
promptly or be dropped. You may PING the server the same way; it answers
PONG with your `data`.

## A session, start to finish

```
> {"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"channels.main.messages","nick":"SiX","system":"PS3","timeout":-1}
> {"scope":"chat","func":"QUERY","oper":"SUBSCRIBE","location":"channels.SiX.messages","nick":"SiX","system":"PS3","timeout":-1}
> {"scope":"chat","func":"QUERY","oper":"SLICE","location":"channels.main.history","data":{"start":-40},"lock":1,"timeout":-1}
< {"scope":"chat","func":"RESPONSE","oper":"SLICE","location":"channels.main.history","data":[{"nick":{...},"str":"...","time":...}, ...]}
> {"scope":"chat","func":"QUERY","oper":"WHO","location":"channels.main.messages","timeout":-1}
< {"scope":"chat","func":"RESPONSE","oper":"WHO","location":"channels.main.messages","data":[{"id":"1","nick":"alice","system":"Future Land"},{"id":"2","nick":"SiX","system":"PS3"}]}
< {"scope":"chat","func":"UPDATE","oper":"SUBSCRIBE","location":"channels.main.messages","data":{"nick":"bob","system":"ECBBS"}}
> {"scope":"chat","func":"QUERY","oper":"WRITE","location":"channels.main.messages","data":{"nick":{"name":"SiX","host":"PS3"},"str":"hi","time":1761000000000},"lock":2,"timeout":-1}
> {"scope":"chat","func":"QUERY","oper":"PUSH","location":"channels.main.history","data":{"nick":{"name":"SiX","host":"PS3"},"str":"hi","time":1761000000000},"lock":2,"timeout":-1}
< {"scope":"chat","func":"UPDATE","oper":"WRITE","location":"channels.main.messages","data":{"nick":{"name":"alice","host":"Future Land","avatar":"..."},"str":"hello SiX","time":1761000005000}}
< {"scope":"SOCKET","func":"PING","data":1761000060000}
> {"scope":"SOCKET","func":"PONG","data":1761000060001}
> {"scope":"chat","func":"QUERY","oper":"UNSUBSCRIBE","location":"channels.main.messages","timeout":-1}
```

Changing rooms is UNSUBSCRIBE the old `.messages`, SUBSCRIBE the new
one, then SLICE its `.history` and WHO it. A client may stay subscribed
to several rooms at once and count the others' traffic as unread.

## Limits worth knowing

- The bundled server's flood control (web users by default, TCP with
  `rate_tcp`) answers an over-limit message with an ERROR and drops it;
  the WRITE pays for the PUSH that follows it, so send them as a pair.
- The JS door treats any ERROR as a lost connection and reconnects; a
  new client should not.
- Anything on the TCP port can read any mailbox. Nothing here is private
  from the server operator or from a raw client; see WEB.md for what the
  web front does about browsers.
