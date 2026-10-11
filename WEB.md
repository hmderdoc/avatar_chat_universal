# Browser clients (WebSocket)

Browsers can't open the chat server's raw TCP port, so a web page reaches
the chat over WebSocket instead. Two servers offer it:

- the bundled `avatar_chat_server`, with its own WebSocket listener;
- a Synchronet board running `json-service.js`, through Synchronet's stock
  `websocketservice.js` proxy.

The web page itself is not written yet; this file covers the server side
and the wire format a page has to speak.

## Bundled server

The listener is off unless you give it an address:

```sh
./avatar_chat_server -addr :10088 -ws-addr :10090
```

or in the `-config` INI (top level or a `[websocket]` section):

```ini
[websocket]
ws_addr    = :10090
ws_origins = https://bbs.example.org, chat.example.net
```

| Key (INI) | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `ws_addr` | `-ws-addr` | (off) | Listen address for WebSocket clients. Upgrades are accepted at `/` and `/chat`. |
| `ws_origins` | `-ws-origins` | (any) | Comma-separated web pages allowed to connect: a full origin (`https://bbs.example.org`, scheme and port must match) or a bare host name (`chat.example.net`, any scheme or port). Requests with no `Origin` header (not a browser) are always let in. |

WebSocket clients share channels, history and `WHO` with the TCP clients
(doors, the shell, bridges). The server pings every 30 seconds and drops a
client that sends nothing, not even a pong, for 75 seconds. Packets over
128 KB are refused with close code 1009. `deny_file` applies here too.

**TLS.** The listener speaks plain `ws://`. A page served over HTTPS may
only open `wss://`, so put the port behind a TLS proxy (nginx, Caddy,
HAProxy). Behind a proxy every client appears to come from the proxy's
address: `X-Forwarded-For` is not trusted, so do IP filtering at the proxy.

## Synchronet (json-service.js)

Add a service that hands each WebSocket connection to the JSON service
port; it takes effect when Synchronet's services restart:

```ini
[JSON-WS]
Port=10090
Options=NO_HOST_LOOKUP
Command=websocketservice.js localhost 10088
```

Use `Options=NO_HOST_LOOKUP|TLS` on a second port for `wss://`, the way
the stock `[WSS]` entry does for fTelnet.

The proxy is a raw byte pipe, not a packet relay, which shapes what a page
must do (below). The JSON service also sees every web client as
`localhost`, so its own IP filters can't tell them apart.

## Wire format (one page, both servers)

The packets are the same JSON objects the doors send. To work through
both servers a page should:

1. **Open with the `binary` subprotocol:** `new WebSocket(url, ["binary"])`
   and `binaryType = "arraybuffer"`. `websocketservice.js` refuses any
   other subprotocol, and in text mode it re-encodes every byte as if it
   were Latin-1 and kills the connection on a 3-byte UTF-8 character (`►`,
   most emoji). In binary mode bytes pass through untouched.
2. **Send** each packet as UTF-8 bytes of `JSON.stringify(packet) + "\r\n"`
   in one binary frame. The JSON service reads lines; the Go server takes
   the line ending or leaves it.
3. **Receive by buffering, not by frame.** Through the proxy a frame is
   whatever arrived on the socket (at most 4 KB): a packet can span frames
   and a frame can hold several. Append each frame's bytes, split on `\n`,
   strip `\r`, decode UTF-8, and parse each complete line. The Go server
   sends one packet per text frame; feeding those through the same
   splitter works. Handle both `string` and `ArrayBuffer` frames.
4. **Skip lines that aren't JSON.** The proxy sends
   `Redirecting to server...` first.
5. **Answer `{"scope":"SOCKET","func":"PING","data":x}`** with a `PONG`
   carrying the same `data`. Futureland's JSON service drops clients that
   go quiet; the proxy itself neither sends nor answers WebSocket pings.
   A page can also send its own `SOCKET` `PING` to keep a NAT open; both
   servers answer it.
