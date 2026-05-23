// Avatar Chat connector for the Moebius joint server.
//
// Bridges the joint server's chat to Synchronet's JSON-over-TCP chat protocol,
// alongside the existing Discord webhook. To make Moebius users show up as real
// participants on the Avatar Chat channel (not a single faceless bridge), this
// is a *connection manager*: it opens one TCP connection per Moebius user,
// subscribed under that user's nick, so each appears in the channel roster and
// triggers join/leave notices. One of those connections (the "receiver") is the
// one we read inbound messages and poll WHO on; the rest are presence-only.
//
// Protocol (reverse-engineered from /sbbs/exec/load/json-{sock,client,chat}.js):
//   - Plain TCP, line-delimited JSON, "\r\n" terminated, 131072-byte cap.
//   - SUBSCRIBE to channels.<chan>.messages (nick on the packet => roster name).
//   - Send a line by WRITE-ing it to .messages and PUSH-ing it to .history.
//   - New lines arrive as UPDATE packets (oper WRITE); SUBSCRIBE/UNSUBSCRIBE
//     opers are join/leave notices. The server PINGs on scope "SOCKET".
//
// Echo/loop suppression reuses the Go bridges' host-marker convention
// (internal/bridgecore/origin.go): outgoing lines carry
// nick.host = "BRIDGE:moebius:<label>/<channel>"; any connection that sees its
// own marker drops it.

const net = require("net");
const events = require("events");

const MAX_PACKET = 131072; // json-sock.js Socket.prototype.max_recv
const RECONNECT_MS = 5000;
const WHO_INTERVAL_MS = 15000; // how often we poll the channel roster
const SEEN_TTL_MS = 10 * 60 * 1000; // keep recent speakers in the roster this long

// Strip control bytes json-chat.js drops, plus ESC: these render on CP437 BBS
// terminals where a stray ESC would corrupt layout.
function control_strip(str) {
    return String(str).replace(/[\f\r\n\x14\x15\x10\b\x1b]/g, "");
}

// Friendly source label for an inbound message's nick.host: real BBS users
// carry their system name; bridged messages carry "BRIDGE:proto:network/chan".
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

// One TCP connection to the Avatar Chat server, subscribed to one channel under
// one nick. Auto-reconnects. Inbound packets go to `onpacket` (set only on the
// receiver connection); PINGs are answered here regardless.
class Conn extends events.EventEmitter {
    constructor({host, port, channel, nick, system}) {
        super();
        this.host = host;
        this.port = port;
        this.channel = channel;
        this.nick = nick;
        this.system = system;
        this.buffer = "";
        this.socket = undefined;
        this.closed = false;
        this.onpacket = null;
    }

    loc(leaf) { return `channels.${this.channel}.${leaf}`; }

    connect() {
        this.closed = false;
        const socket = net.createConnection({host: this.host, port: this.port}, () => {
            this.subscribe();
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
            this.send({scope: "chat", func: "QUERY", oper: "UNSUBSCRIBE", location: this.loc("messages"), timeout: -1});
            this.socket.end();
        }
    }

    send(packet) {
        if (!this.socket) return;
        const data = JSON.stringify(packet) + "\r\n";
        if (Buffer.byteLength(data) > MAX_PACKET) return;
        this.socket.write(data);
    }

    subscribe() {
        this.send({scope: "chat", func: "QUERY", oper: "SUBSCRIBE", location: this.loc("messages"), nick: this.nick, system: this.system, timeout: -1});
    }

    who() {
        this.send({scope: "chat", func: "QUERY", oper: "WHO", location: this.loc("messages"), timeout: -1});
    }

    write_message(message) {
        this.send({scope: "chat", func: "QUERY", oper: "WRITE", location: this.loc("messages"), data: message, lock: 2, timeout: -1});
        this.send({scope: "chat", func: "QUERY", oper: "PUSH", location: this.loc("history"), data: message, lock: 2, timeout: -1});
    }

    _on_data(chunk) {
        this.buffer += chunk;
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
                continue;
            }
            if ((packet.func || "").toUpperCase() === "PING") {
                this.send({scope: "SOCKET", func: "PONG", data: Date.now()});
                continue;
            }
            if (this.onpacket) this.onpacket(packet);
        }
    }
}

class AvatarChat extends events.EventEmitter {
    constructor({host, port = 10088, channel = "main", label = "moebius"} = {}) {
        super();
        this.host = host;
        this.port = port;
        this.channel = channel;
        this.label = label;
        this.origin = `BRIDGE:moebius:${String(label).toLowerCase()}/${String(channel).toLowerCase()}`;
        this.avatarByNick = Object.create(null); // nick -> base64 avatar (from messages)
        this.seen = new Map();                    // nick -> {system, ts} recent speakers
        this.userConns = new Map();               // moebius user id -> Conn
        this.receiver = undefined;                // Conn we read messages + WHO on
        this.lastWho = [];                        // last WHO response, [{nick, system}]
        this.whoTimer = undefined;
        this.closed = false;
    }

    // --- Moebius user lifecycle (called by the joint server) ---------------

    add_user(id, nick, system) {
        if (this.closed || this.userConns.has(id)) {
            if (this.userConns.has(id)) this.rename_user(id, nick);
            return;
        }
        const conn = new Conn({host: this.host, port: this.port, channel: this.channel, nick: nick || "Anonymous", system: system || this.label});
        this.userConns.set(id, conn);
        conn.on("open", () => {
            if (!this.receiver) this._set_receiver(conn);
            this.emit("open");
            if (this.receiver) this.receiver.who(); // refresh roster on any join
        });
        conn.on("error", (err) => this.emit("error", err));
        conn.connect();
    }

    remove_user(id) {
        const conn = this.userConns.get(id);
        if (!conn) return;
        this.userConns.delete(id);
        const was_receiver = (conn === this.receiver);
        conn.onpacket = null;
        conn.close();
        if (was_receiver) {
            this.receiver = undefined;
            this._stop_who_poll();
            const next = this.userConns.values().next().value;
            if (next) this._set_receiver(next);
        }
        this._emit_roster();
    }

    rename_user(id, nick) {
        const conn = this.userConns.get(id);
        if (!conn || !nick || conn.nick === nick) return;
        conn.nick = nick;
        conn.subscribe(); // re-announce presence under the new nick
    }

    // Send a Moebius user's line out via their own connection so it originates
    // from their presence identity. Falls back to the receiver if we don't have
    // a connection for that id yet.
    say(id, nick, text, avatar) {
        const str = control_strip(text);
        if (!str) return;
        const sender = {name: nick || "Moebius", host: this.origin};
        if (avatar) sender.avatar = avatar;
        const conn = this.userConns.get(id) || this.receiver;
        if (conn) conn.write_message({nick: sender, str, time: Date.now()});
    }

    close() {
        this.closed = true;
        this._stop_who_poll();
        for (const conn of this.userConns.values()) conn.close();
        this.userConns.clear();
        this.receiver = undefined;
    }

    // --- receiver: inbound messages + WHO ----------------------------------

    _set_receiver(conn) {
        this.receiver = conn;
        conn.onpacket = (packet) => this._on_packet(packet);
        this._start_who_poll();
    }

    _start_who_poll() {
        this._stop_who_poll();
        if (this.receiver) this.receiver.who();
        this.whoTimer = setInterval(() => { if (this.receiver) this.receiver.who(); }, WHO_INTERVAL_MS);
    }

    _stop_who_poll() {
        if (this.whoTimer) {
            clearInterval(this.whoTimer);
            this.whoTimer = undefined;
        }
    }

    _on_packet(packet) {
        const func = (packet.func || "").toUpperCase();
        const oper = (packet.oper || "").toUpperCase();
        if (func === "RESPONSE") {
            if (oper === "WHO") this._handle_who(packet.data);
            return;
        }
        if (func !== "UPDATE") return;
        if (oper === "SUBSCRIBE" || oper === "UNSUBSCRIBE") {
            if (this.receiver) this.receiver.who(); // someone joined/left -> refresh
            return;
        }
        if (oper !== "WRITE") return;
        const msg = packet.data;
        if (!msg || typeof msg.str !== "string") return;
        const host = (msg.nick && msg.nick.host) ? msg.nick.host : "";
        if (host === this.origin) return; // our own echo
        const name = (msg.nick && msg.nick.name) ? msg.nick.name : "";
        const avatar = (msg.nick && typeof msg.nick.avatar === "string") ? msg.nick.avatar : "";
        if (name && avatar) this.avatarByNick[name] = avatar;
        if (name) {
            const was_new = !this.seen.has(name);
            this.seen.set(name, {system: origin_label(host), ts: Date.now()});
            if (was_new) this._emit_roster();
        }
        this.emit("message", {nick: name, text: msg.str, system: origin_label(host), avatar});
    }

    // WHO returns either ["name", ...] (our Go server) or [{nick, system}, ...]
    // (Synchronet json-db). Store it and rebuild the roster.
    _handle_who(data) {
        const list = [];
        if (Array.isArray(data)) {
            for (const entry of data) {
                if (typeof entry === "string") list.push({nick: entry, system: ""});
                else if (entry && typeof entry === "object" && (entry.nick || entry.name)) list.push({nick: entry.nick || entry.name, system: entry.system || ""});
            }
        }
        this.lastWho = list;
        this._emit_roster();
    }

    // Roster shown in Moebius = (current WHO) merged with (recent speakers),
    // minus our own Moebius users (they're already in the collaborator list),
    // each enriched with any avatar we've seen.
    _emit_roster() {
        const ours = new Set();
        for (const conn of this.userConns.values()) if (conn.nick) ours.add(conn.nick);
        const now = Date.now();
        const byNick = new Map();
        const add = (nick, system) => {
            if (!nick || ours.has(nick) || byNick.has(nick)) return;
            byNick.set(nick, {nick, system: system || "", avatar: this.avatarByNick[nick] || ""});
        };
        for (const e of this.lastWho) add(e.nick, e.system);
        for (const [nick, info] of this.seen) {
            if (now - info.ts > SEEN_TTL_MS) { this.seen.delete(nick); continue; }
            add(nick, info.system);
        }
        this.emit("roster", [...byNick.values()]);
    }
}

module.exports = {AvatarChat, control_strip, origin_label};
