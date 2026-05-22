// Avatar Chat connector for the Moebius joint server.
//
// Mirrors the joint server's CHAT both ways against Synchronet's JSON-over-TCP
// chat protocol, alongside the existing Discord webhook mirror in server.js.
// This is phase 1 of moebius_chat/objective.MD: protocol-level chat so a user
// sitting in Moebius can send/receive messages on Avatar Chat.
//
// Protocol (reverse-engineered from /sbbs/exec/load/json-{sock,client,chat}.js
// and the Go client at internal/chat/):
//   - Plain TCP, line-delimited JSON, terminated with "\r\n".
//   - Max packet 131072 bytes (json-sock.js max_recv).
//   - Send a chat line by WRITE-ing it to channels.<chan>.messages (live
//     subscribers) and PUSH-ing it to channels.<chan>.history (backlog), the
//     same pair json-chat.js submit() issues.
//   - Receive by SUBSCRIBE-ing to channels.<chan>.messages; each new line
//     arrives as an UPDATE packet with oper WRITE and data = the message.
//   - The server PINGs on scope "SOCKET"; we answer with a PONG.
//
// Echo/loop suppression reuses the host-marker convention the Go bridges use
// (internal/bridgecore/origin.go): every line we send out carries
// nick.host = "BRIDGE:moebius:<label>/<channel>". When the server echoes our
// own WRITE back to us (we're subscribed to the channel we wrote to), we see
// our marker and drop it. Lines from other bridges (BRIDGE:discord:...) and
// from real BBS users (host = their system name) are kept and injected into
// Moebius, so Moebius sees the whole room.

const net = require("net");
const events = require("events");

const MAX_PACKET = 131072; // json-sock.js Socket.prototype.max_recv
const RECONNECT_MS = 5000;

// Strip the control bytes json-chat.js Message() removes, plus ESC (0x1b):
// these lines are rendered on CP437 BBS terminals downstream where a stray
// ESC would be read as the start of an escape sequence and corrupt layout.
function control_strip(str) {
    return String(str).replace(/[\f\r\n\x14\x15\x10\b\x1b]/g, "");
}

// Friendly source label for an inbound message's nick.host. Real BBS users
// carry their system name there (json-chat.js Nick(alias, system.name, ip));
// bridged messages carry "BRIDGE:proto:network/channel", whose network is the
// human-meaningful part (e.g. "discord"). Returns "" when there's nothing
// useful to show.
function origin_label(host) {
    if (!host) return "";
    if (host.indexOf("BRIDGE:") === 0) {
        const rest = host.slice("BRIDGE:".length);
        const colon = rest.indexOf(":");
        const slash = rest.lastIndexOf("/");
        if (colon > 0 && slash > colon) return rest.slice(colon + 1, slash);
        return "";
    }
    return host;
}

class AvatarChat extends events.EventEmitter {
    constructor({host, port = 10088, channel = "main", label = "moebius", nick = "Moebius"} = {}) {
        super();
        this.host = host;
        this.port = port;
        this.channel = channel;
        this.default_nick = nick;
        // Our origin marker. Lowercased to match bridgecore.cleanPart so the
        // comparison against our own echo is exact.
        this.origin = `BRIDGE:moebius:${String(label).toLowerCase()}/${String(channel).toLowerCase()}`;
        this.buffer = "";
        this.socket = undefined;
        this.closed = false;
    }

    connect() {
        this.closed = false;
        const socket = net.createConnection({host: this.host, port: this.port}, () => {
            this._subscribe();
            this.emit("open");
        });
        socket.setEncoding("utf8");
        socket.on("data", (chunk) => this._on_data(chunk));
        socket.on("error", (err) => this.emit("error", err));
        socket.on("close", () => {
            this.socket = undefined;
            this.emit("close");
            if (!this.closed) setTimeout(() => this.connect(), RECONNECT_MS);
        });
        this.socket = socket;
    }

    close() {
        this.closed = true;
        if (this.socket) {
            this._send({scope: "chat", func: "QUERY", oper: "UNSUBSCRIBE", location: this._loc("messages"), timeout: -1});
            this.socket.end();
        }
    }

    _loc(leaf) {
        return `channels.${this.channel}.${leaf}`;
    }

    _send(packet) {
        if (!this.socket) return;
        const data = JSON.stringify(packet) + "\r\n";
        if (Buffer.byteLength(data) > MAX_PACKET) return; // can't carry it; drop
        this.socket.write(data);
    }

    _subscribe() {
        this._send({scope: "chat", func: "QUERY", oper: "SUBSCRIBE", location: this._loc("messages"), timeout: -1});
    }

    // Forward a chat line from a Moebius user out to Avatar Chat. `avatar` is
    // an optional base64 120-byte block that rides on message.nick.avatar so
    // the JS door and other clients render the sender's portrait.
    say(nick, text, avatar) {
        const str = control_strip(text);
        if (!str) return;
        const sender = {name: nick || this.default_nick, host: this.origin};
        if (avatar) sender.avatar = avatar;
        const message = {nick: sender, str, time: Date.now()};
        this._send({scope: "chat", func: "QUERY", oper: "WRITE", location: this._loc("messages"), data: message, lock: 2, timeout: -1});
        this._send({scope: "chat", func: "QUERY", oper: "PUSH", location: this._loc("history"), data: message, lock: 2, timeout: -1});
    }

    _on_data(chunk) {
        this.buffer += chunk;
        // Runaway guard: a peer that never sends a newline shouldn't grow the
        // buffer without bound. json-sock caps a single packet at MAX_PACKET.
        if (this.buffer.length > MAX_PACKET * 2) this.buffer = "";
        let idx;
        while ((idx = this.buffer.indexOf("\n")) >= 0) {
            const line = this.buffer.slice(0, idx).replace(/\r$/, "");
            this.buffer = this.buffer.slice(idx + 1);
            if (!line) continue;
            let packet;
            try {
                packet = JSON.parse(line);
            } catch (e) {
                continue; // malformed line; skip, keep the connection
            }
            this._dispatch(packet);
        }
    }

    _dispatch(packet) {
        const func = (packet.func || "").toUpperCase();
        if (func === "PING") {
            this._send({scope: "SOCKET", func: "PONG", data: Date.now()});
            return;
        }
        if (func !== "UPDATE") return; // PONG, RESPONSE, ERROR: nothing to do here
        if ((packet.oper || "").toUpperCase() !== "WRITE") return; // SUBSCRIBE/UNSUBSCRIBE join/leave noise
        const msg = packet.data;
        if (!msg || typeof msg.str !== "string") return;
        const host = (msg.nick && msg.nick.host) ? msg.nick.host : "";
        if (host === this.origin) return; // our own echo
        const name = (msg.nick && msg.nick.name) ? msg.nick.name : "";
        const avatar = (msg.nick && typeof msg.nick.avatar === "string") ? msg.nick.avatar : "";
        this.emit("message", {nick: name, text: msg.str, system: origin_label(host), avatar});
    }
}

module.exports = {AvatarChat, control_strip, origin_label};
