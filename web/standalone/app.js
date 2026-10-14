// Avatar Chat, standalone page. Talks only to the server that served it:
// the chat over WebSocket at /chat, pictures through /img, link cards
// through /preview, the avatar library under /avatars/, uploads to
// /upload. See WEB.md.
//
// The markup and classes are the Synchronet page's (web/synchronet), so
// shared/app.css and its page styles dress both; the rooms, avatar and
// attach behaviour follow that page too. There are no accounts here, so
// the nickname, avatar, page style and joined rooms live in this browser.
//
// Private messages (WEB.md "Private messages"): the server gives a name to
// the first browser that claims it, with a key kept here. PMs to us arrive
// in our own web mailbox, channels.<nick>@<origin>.messages, which only
// this browser may read; we send to a peer's web mailbox when their nick
// carries a valid one, else to their plain name (a BBS user). No mailbox
// string is ever shown.
"use strict";

(function () {
    const $ = (id) => document.getElementById(id);
    const els = {
        app: $("app"), chatpanel: $("chatpanel"), messages: $("messages"),
        rooms: $("rooms"), private: $("private"), roomLabel: $("room-label"), count: $("count"), roomActions: $("room-actions"),
        status: $("status"), statusText: $("status-text"),
        form: $("form"), input: $("input"), file: $("file"), attach: $("attach"), draw: $("draw"),
        members: $("members"), membersTitle: $("members-title"), membersList: $("members-list"), membersClose: $("members-close"),
        meAvatar: $("me-avatar"), meName: $("me-name"), avatarOpen: $("avatar-open"), settingsOpen: $("settings-open"),
        nickModal: $("nick-modal"), nickForm: $("nick-form"), nickInput: $("nick-input"), nickError: $("nick-error"),
        roomModal: $("room-modal"), roomClose: $("room-close"), roomText: $("room-modal-text"), roomList: $("room-list"),
        roomForm: $("room-form"), roomInput: $("room-input"), roomSubmit: $("room-submit"),
        noRoomsModal: $("norooms-modal"), noRoomsDefault: $("norooms-default"), noRoomsMain: $("norooms-main"),
        noRoomsPick: $("norooms-pick"), noRoomsCancel: $("norooms-cancel"),
        welcomeModal: $("welcome-modal"), welcomeAvatar: $("welcome-avatar"), welcomeName: $("welcome-name"),
        welcomeAvatarOpen: $("welcome-avatar-open"), welcomeColors: $("welcome-colors"), welcomeLater: $("welcome-later"), welcomeText: $("welcome-text"),
        settingsModal: $("settings-modal"), settingsForm: $("settings-form"), settingsNick: $("settings-nick"),
        settingsColors: $("settings-colors"),
        styleLetters: $("style-letters"), stylePreview: $("style-preview"), stylePreset: $("style-preset"),
        styleA: $("style-a"), styleB: $("style-b"),
        settingsStyle: $("settings-style"), settingsStatus: $("settings-status"), settingsCancel: $("settings-cancel"),
        avatarModal: $("avatar-modal"), avatarPreview: $("avatar-preview"), avatarDraw: $("avatar-draw"),
        avatarUpload: $("avatar-upload"), avatarRemove: $("avatar-remove"), avatarFile: $("avatar-file"),
        avatarLibrary: $("avatar-library"), avatarCollection: $("avatar-collection"), avatarGrid: $("avatar-grid"),
        avatarStatus: $("avatar-status"), avatarUse: $("avatar-use"), avatarClose: $("avatar-close"),
        cardModal: $("card-modal"), cardAvatar: $("card-avatar"), cardName: $("card-name"), cardBoard: $("card-board"),
        cardClose: $("card-close"), cardMessage: $("card-message"),
    };

    const HISTORY = 60;            // messages loaded when a room is opened
    const GROUP_MS = 2 * 60000;    // same sender within this joins one group
    const KEEP = 400;              // messages kept on screen
    const COUNTS_EVERY = 30000;    // user counts for the joined rooms
    const MAX_ROOMS = 50;          // joined rooms kept
    const PICKER_COUNTS = 40;      // rooms whose user count the picker asks for
    const ART_EXTENSIONS = /\.(ans|asc|nfo|diz|ice|txt)$/i;
    const ART_MAX_BYTES = 1024 * 1024;
    const ROOM_NAME = /^[A-Za-z0-9_-]{1,32}$/;
    const PM_HISTORY = 200;        // messages read from our own mailbox
    const MAX_THREAD = 300;        // messages kept per private thread
    // A socket can be dead yet still say OPEN (a laptop slept, a phone
    // backgrounded the tab). Nothing heard for FRESH_MS and a SOCKET PING
    // goes first; no reply in PROBE_WAIT and it's dropped for a new one.
    const FRESH_MS = 20000;
    const PROBE_WAIT = 5000;
    const PROBE_EVERY = 25000;     // while the page is visible
    const SEND_WAIT = 15000;       // a message not through by then failed

    const cfg = {
        defaultChannel: "main", systemName: "", maxMessage: 1000, createRooms: false, uploads: false,
        mode: "local", nsfwKeywords: [], styles: [{id: "classic", label: "Classic"}], defaultStyle: "classic", avatars: false,
    };
    const state = {
        nick: "", avatar: "", style: "", colors: [], draftColors: [], joined: [], view: "",
        ws: null, open: false, retry: 0, retryTimer: 0, subscribed: new Set(),
        heard: 0, probeTimer: 0, everClaimed: false,
        outbox: [],            // messages shown but not yet through, oldest first
        fenced: null,          // the outbox entry whose PINGs are being answered
        holdOutbox: 0,         // set while a reconnect's history loads
        restore: null,         // where the reader was, kept across a reconnect
        messages: [], lastGroup: null, pendingSelf: [], pendingArt: null, busy: false,
        who: new Map(),        // room (lower) -> [{nick, system}]
        unread: new Map(),     // room (lower) -> count
        publicRooms: [],       // names from public_channels
        whoTimers: new Map(),
        key: "", claimed: false, refused: false,
        welcomed: false, welcomeNext: false, // the first-logon welcome, once per page load
        welcomeReturn: false, welcomeOffersColors: false, // a modal opened from it comes back to it
        pm: "",                // key of the private thread on screen, "" for a room
        threads: new Map(),    // lower(name)|lower(host) -> {name, host, mailbox, msgs, unread, last}
    };

    // --- storage (this browser only; may be unavailable) -------------------
    function load(key) { try { return localStorage.getItem("avchat." + key) || ""; } catch (e) { return ""; } }
    function save(key, v) {
        try {
            if (v) localStorage.setItem("avchat." + key, v);
            else localStorage.removeItem("avchat." + key);
        } catch (e) { /* private mode */ }
    }
    function loadJSON(key, fallback) { try { return JSON.parse(load(key)) || fallback; } catch (e) { return fallback; } }

    const lower = (s) => String(s || "").toLowerCase();
    const plain = (s) => String(s || "").replace(/\x01./g, "").replace(/[\x00-\x08\x0b-\x1f\x7f]/g, "");
    function el(tag, cls, text) {
        const e = document.createElement(tag);
        if (cls) e.className = cls;
        if (text !== undefined) e.textContent = text;
        return e;
    }

    // --- avatars and styles seen on messages -------------------------------
    // Keyed by name and board, kept in this browser so the members list
    // has faces before those people speak again.
    const seen = new Map(Object.entries(loadJSON("seen", {})));
    let seenDirty = 0;
    function seenKey(name, host) { return lower(name) + "|" + lower(host); }
    // remember reports whether it learned anything new.
    function remember(nick) {
        if (!nick || !nick.name) return false;
        const key = seenKey(nick.name, nick.host);
        const prev = seen.get(key) || {};
        const next = {
            a: nick.avatar || prev.a || "",
            s: nick.style && Array.isArray(nick.style.colors) ? nick.style.colors : prev.s || null,
            m: validMailbox(nick.name, nick.host, nick.mailbox) ? nick.mailbox : prev.m || "",
        };
        if (next.a === prev.a && next.m === (prev.m || "") && JSON.stringify(next.s) === JSON.stringify(prev.s)) return false;
        seen.delete(key);
        seen.set(key, next);
        while (seen.size > 300) seen.delete(seen.keys().next().value);
        clearTimeout(seenDirty);
        seenDirty = setTimeout(() => save("seen", JSON.stringify(Object.fromEntries(seen))), 1000);
        return true;
    }
    function isMe(name, host) { return lower(name) === lower(state.nick) && (!host || lower(host) === lower(cfg.systemName)); }
    // seenFor finds what was seen for name on host; with no host (the
    // bundled server's WHO lists bare names) the latest from any board.
    function seenFor(name, host) {
        if (host) return seen.get(seenKey(name, host)) || null;
        const prefix = lower(name) + "|";
        let hit = null;
        seen.forEach((v, k) => { if (k.indexOf(prefix) === 0) hit = v; });
        return hit;
    }
    function avatarOf(name, host) {
        if (isMe(name, host)) return state.avatar;
        const e = seenFor(name, host);
        return e ? e.a : "";
    }
    // --- web mailboxes (chat.MailboxFor in Go) ---------------------------------
    const origin = (host) => String(host || "").replace(/[^A-Za-z0-9_-]/g, "_");
    const mailboxFor = (name, host) => name + "@" + origin(host);
    function validMailbox(name, host, mb) {
        if (!mb || !name || String(mb).indexOf(".") >= 0) return false;
        return lower(mb) === lower(mailboxFor(name, host));
    }
    function ownMailbox() { return state.nick ? mailboxFor(state.nick, cfg.systemName) : ""; }
    // peerMailbox is where a PM to name on host goes when they're a web
    // user: from a message they sent, or, for someone on this same server
    // (their board is ours), worked out; "" means a BBS user's plain name.
    function peerMailbox(name, host) {
        const e = host ? seen.get(seenKey(name, host)) : null;
        if (e && e.m && validMailbox(name, host, e.m)) return e.m;
        if (host && lower(host) === lower(cfg.systemName)) return mailboxFor(name, host);
        return "";
    }
    function colorsOf(name, host) {
        if (isMe(name, host)) return state.colors;
        const e = seenFor(name, host);
        return e && Array.isArray(e.s) ? e.s : null;
    }

    // An avatar as a picture: drawn once per avatar, then reused.
    const avatarURLs = new Map();
    function avatarURL(b64) {
        if (!avatarURLs.has(b64)) {
            const blocks = AvatarCodec.avatarToBlocks(b64);
            avatarURLs.set(b64, blocks ? AvatarCodec.render(blocks).then((c) => c.toDataURL()) : Promise.resolve(""));
        }
        return avatarURLs.get(b64);
    }
    // fillAvatar puts name's avatar (or its initial) into box.
    function fillAvatar(box, b64, name) {
        box.textContent = "";
        if (b64) {
            const img = el("img");
            img.alt = "";
            box.appendChild(img);
            avatarURL(b64).then((u) => { if (u) img.src = u; else box.textContent = initial(name); });
            return;
        }
        if (name !== undefined) box.textContent = initial(name);
    }
    function initial(name) { return String(name || "?").charAt(0).toUpperCase(); }

    // --- handle styles, links, NSFW -------------------------------------------
    const HEX = /^#[0-9a-fA-F]{6}$/;
    function handle(name, colors) {
        const strong = el("strong", "avatar-chat-handle");
        const list = Array.isArray(colors) ? colors : [];
        if (!list.some((c) => HEX.test(c || ""))) {
            strong.textContent = name;
            return strong;
        }
        Array.from(name).forEach((ch, i) => {
            if (HEX.test(list[i] || "")) {
                const s = el("span", "", ch);
                s.style.color = list[i];
                strong.appendChild(s);
            } else {
                strong.appendChild(document.createTextNode(ch));
            }
        });
        return strong;
    }

    // The URL rules are the door's and the Synchronet page's: links up to
    // whitespace, quotes, angle brackets or a pipe, trailing punctuation
    // dropped; pictures by the path's extension, three at most.
    const URL_RE = /https?:\/\/[^\s<>"'\x01|]+/gi;
    const TRAILING = /[).,;:!?]+$/;
    const IMAGE_EXT = /\.(png|jpe?g|gif|webp|bmp)$/i;
    const AUDIO_EXT = /\.(mp3|ogg|oga|opus|wav|flac|m4a|aac)$/i;
    const VIDEO_EXT = /\.(mp4|webm|mov|m4v)$/i;
    const ANSI_EXT = /\.(ans|asc)$/i;
    const MEDIA_PATH = /\/chatmedia\/\d{4}-\d{2}-\d{2}\/[a-f0-9]{24}\.[a-z0-9]{1,4}$/i;

    function findURLs(text) {
        const out = [];
        (String(text).match(URL_RE) || []).forEach((m) => {
            const u = m.replace(TRAILING, "");
            try { if (!new URL(u).host) return; } catch (e) { return; }
            if (out.indexOf(u) < 0) out.push(u);
        });
        return out;
    }
    function pathOf(u) { try { return new URL(u).pathname; } catch (e) { return ""; } }
    function sameOrigin(u) { try { return new URL(u, location.href).origin === location.origin; } catch (e) { return false; } }
    function imgSrc(u) { return sameOrigin(u) ? u : "img?url=" + encodeURIComponent(u); }

    // The keyword rule (the door's inlineimg.LooksNSFW): any keyword as a
    // whole word, any case, in the text or its percent-decoded form.
    function looksNSFW(text) {
        const words = cfg.nsfwKeywords || [];
        if (!words.length) return false;
        let s = String(text || "");
        try { s += " " + decodeURIComponent(s); } catch (e) { /* not encoded */ }
        const found = new Set(s.toLowerCase().split(/[^a-z0-9]+/));
        return words.some((w) => found.has(String(w).toLowerCase()));
    }

    function mentionsMe(text) {
        if (!state.nick) return false;
        const esc = state.nick.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
        return new RegExp("(^|[^a-z0-9])@?" + esc + "([^a-z0-9]|$)", "i").test(text);
    }

    // --- protocol ---------------------------------------------------------------
    const loc = {
        messages: (room) => "channels." + room + ".messages",
        history: (room) => "channels." + room + ".history",
    };
    const enc = new TextEncoder(), dec = new TextDecoder();
    let rx = new Uint8Array(0);

    function send(pkt) {
        if (!state.ws || state.ws.readyState !== WebSocket.OPEN) return false;
        state.ws.send(enc.encode(JSON.stringify(pkt) + "\r\n"));
        return true;
    }
    function query(oper, location, extra) {
        return send(Object.assign({scope: "chat", func: "QUERY", oper, location, timeout: -1}, extra || {}));
    }

    // Frames are buffered and split on newlines: one packet may span frames
    // or one frame carry several.
    function receive(data) {
        const bytes = typeof data === "string" ? enc.encode(data + "\n") : new Uint8Array(data);
        const joined = new Uint8Array(rx.length + bytes.length);
        joined.set(rx);
        joined.set(bytes, rx.length);
        let start = 0;
        for (let i = 0; i < joined.length; i++) {
            if (joined[i] !== 10) continue;
            const line = dec.decode(joined.subarray(start, i)).trim();
            start = i + 1;
            if (!line || line[0] !== "{") continue;
            let pkt;
            try { pkt = JSON.parse(line); } catch (e) { continue; }
            handle_(pkt);
        }
        rx = joined.slice(start);
    }

    function connect() {
        clearTimeout(state.retryTimer);
        const scheme = location.protocol === "https:" ? "wss://" : "ws://";
        const ws = new WebSocket(scheme + location.host + "/chat", ["binary"]);
        ws.binaryType = "arraybuffer";
        state.ws = ws;
        state.subscribed = new Set();
        rx = new Uint8Array(0);
        setStatus("info", "Connecting...");
        state.claimed = false;
        state.refused = false;
        ws.onopen = () => {
            state.retry = 0;
            // The name first: nothing else is allowed until it's ours.
            send({scope: "SOCKET", func: "CLAIM", data: {nick: state.nick, key: claimKey()}});
        };
        ws.onmessage = (ev) => {
            if (state.ws !== ws) return; // one given up on
            heardFrom();
            receive(ev.data);
        };
        ws.onclose = () => {
            if (state.ws !== ws) return;
            state.open = false;
            state.claimed = false;
            if (state.refused) { lostInFlight(); return; } // waits for another nickname
            const wait = Math.min(30000, 1000 * Math.pow(2, state.retry++));
            setStatus("warning", "Disconnected. Reconnecting in " + Math.round(wait / 1000) + "s...");
            state.retryTimer = setTimeout(connect, wait);
            lostInFlight(); // after, so a "Not sent" shows over the warning
        };
    }
    // claimed runs once the server has given us our name. On a reconnect
    // the reader stays where they were: the history reloads under them.
    function claimed() {
        const at = state.everClaimed && els.messages.querySelector(".avatar-chat-bubble") ? viewAnchor() : null;
        state.everClaimed = true;
        state.open = true;
        state.claimed = true;
        clearStatus("info");
        clearStatus("warning");
        state.joined.forEach(subscribe);
        query("SUBSCRIBE", loc.messages(ownMailbox()), {nick: state.nick, system: cfg.systemName});
        query("SLICE", loc.history(ownMailbox()), {data: {start: -PM_HISTORY}, lock: 1});
        if (state.pm && state.threads.has(state.pm)) {
            if (at) state.restore = at; // setThreads redraws it
            else openThread(state.pm);
        } else openView(state.view || state.joined[0] || cfg.defaultChannel, at);
        // Messages waiting for this connection go once the history is in,
        // so it can't draw over them.
        clearTimeout(state.holdOutbox);
        state.holdOutbox = setTimeout(releaseOutbox, PROBE_WAIT);
        refreshCounts();
        if (!state.welcomed && needsWelcome() && !els.nickModal.classList.contains("is-open")) openWelcome();
        else offerRooms();
    }
    function offerRooms() {
        if (!state.joined.length && !state.pm) openModal(els.noRoomsModal, els.noRoomsMain);
    }
    function onClaim(data) {
        if (data && data.ok) { claimed(); return; }
        const reason = data && data.reason;
        state.refused = true;
        if (state.ws) state.ws.close();
        clearStatus("info");
        // A BBS user's name comes back "taken" with detail "bbs".
        askNick(reason === "taken" && data.detail === "bbs" ? "That name belongs to a BBS user. Pick another."
            : reason === "taken" ? "That name is taken here. Pick another."
            : reason === "limit" ? "Too many new names from your address today. Use one you've had here before, or try again tomorrow."
                : reason === "invalid" ? "Letters, digits, spaces, - and _ only, up to 25."
                    : "The server couldn't give you that name. Try again.");
    }
    // claimKey is this browser's key for the names it holds, made once.
    function claimKey() {
        if (state.key) return state.key;
        let k = load("key");
        if (!/^[0-9a-f]{64}$/.test(k)) {
            const b = new Uint8Array(32);
            crypto.getRandomValues(b);
            k = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
            save("key", k);
        }
        state.key = k;
        return k;
    }

    function reconnect() {
        const old = state.ws;
        state.ws = null;
        if (old) old.close();
        connect();
    }

    // --- liveness -------------------------------------------------------------
    // Anything from the server shows the socket is alive and answers a probe.
    function heardFrom() {
        state.heard = Date.now();
        if (!state.probeTimer) return;
        clearTimeout(state.probeTimer);
        state.probeTimer = 0;
        flushOutbox();
    }
    // probe asks the server for a PONG; nothing back in PROBE_WAIT and the
    // socket is taken for dead.
    function probe() {
        if (!state.claimed || state.probeTimer) return;
        if (!send({scope: "SOCKET", func: "PING", data: {probe: Date.now()}})) return;
        state.probeTimer = setTimeout(dropDead, PROBE_WAIT);
    }
    // dropDead gives up on a socket that stopped answering (its close may
    // never come) and opens a new one straight away.
    function dropDead() {
        clearTimeout(state.probeTimer);
        state.probeTimer = 0;
        state.open = false;
        state.claimed = false;
        state.retry = 0;
        reconnect();
        lostInFlight(); // after, so a "Not sent" shows over "Connecting..."
    }
    // wake: the page is back (shown, online, restored); check the socket
    // now, or retry a closed one without waiting out the backoff.
    function wake() {
        if (document.visibilityState === "hidden" || !state.nick || state.refused) return;
        if (state.claimed) probe();
        else if (!state.ws || state.ws.readyState === WebSocket.CLOSED || state.ws.readyState === WebSocket.CLOSING) {
            state.retry = 0;
            reconnect();
        }
    }

    function subscribe(room) {
        if (state.subscribed.has(lower(room))) return;
        state.subscribed.add(lower(room));
        query("SUBSCRIBE", loc.messages(room), {nick: state.nick, system: cfg.systemName});
    }
    function unsubscribe(room) {
        if (!state.subscribed.delete(lower(room))) return;
        query("UNSUBSCRIBE", loc.messages(room), {nick: state.nick, system: cfg.systemName});
    }
    function askWho(room, delay) {
        const key = lower(room);
        clearTimeout(state.whoTimers.get(key));
        state.whoTimers.set(key, setTimeout(() => query("WHO", loc.messages(room)), delay || 0));
    }
    function refreshCounts() {
        state.joined.forEach((r) => askWho(r, 0));
        if (state.view && !isJoined(state.view)) askWho(state.view, 0);
    }

    function roomOf(location) {
        const m = /^channels\.([A-Za-z0-9_-]{1,32})\.(messages|history)$/.exec(String(location || ""));
        return m ? m[1] : "";
    }

    function handle_(pkt) {
        if (pkt.scope === "SOCKET") {
            if (pkt.func === "PING") send({scope: "SOCKET", func: "PONG", data: pkt.data});
            else if (pkt.func === "CLAIM") onClaim(pkt.data);
            else if (pkt.func === "PONG" && pkt.data && pkt.data.send) onSendPong(pkt.data);
            return;
        }
        if (pkt.func === "ERROR") {
            const why = pkt.data && pkt.data.description ? String(pkt.data.description) : "error";
            if (/^no public room named (\S+)/.test(why)) {
                const gone = /^no public room named (\S+)/.exec(why)[1];
                state.joined = state.joined.filter((r) => lower(r) !== lower(gone));
                saveRooms();
                state.subscribed.delete(lower(gone));
                if (lower(state.view) === lower(gone)) openView(state.joined[0] || cfg.defaultChannel);
                renderRooms();
            }
            // Between a send's two PINGs, an ERROR is that send's.
            const e = state.fenced;
            if (e) {
                if (!e.failed) failSend(e, why);
                return;
            }
            if (/nickname/.test(why)) { askNick(why); return; }
            setStatus("error", why);
            return;
        }
        const mine = ownMailbox();
        if (mine && pkt.location === loc.history(mine) && pkt.func === "RESPONSE" && pkt.oper === "SLICE") {
            setThreads(Array.isArray(pkt.data) ? pkt.data : []);
            return;
        }
        if (mine && pkt.location === loc.messages(mine)) {
            if (pkt.func === "UPDATE" && String(pkt.oper || "").toUpperCase() === "WRITE" && pkt.data && typeof pkt.data === "object") {
                incomingPM(pkt.data);
            }
            return;
        }
        const room = roomOf(pkt.location);
        if (pkt.func === "RESPONSE") {
            if (pkt.oper === "SLICE" && room && !state.pm && lower(room) === lower(state.view)) {
                setHistory(Array.isArray(pkt.data) ? pkt.data : []);
            } else if (pkt.oper === "WHO" && room) {
                setWho(room, Array.isArray(pkt.data) ? pkt.data : []);
            } else if (pkt.oper === "KEYS" && pkt.location === "public_channels") {
                setPublicRooms(pkt.data);
            }
            return;
        }
        if (pkt.func !== "UPDATE" || !room) return;
        const oper = String(pkt.oper || "").toUpperCase();
        const here = !state.pm && lower(room) === lower(state.view);
        if (oper === "WRITE") {
            const m = pkt.data;
            if (!m || typeof m !== "object") return;
            const learned = m.nick && typeof m.nick === "object" && remember(m.nick);
            if (here && learned) renderMembers();
            if (here) {
                if (!isSelfEcho(m)) addMessage(m, true);
            } else if (isJoined(room) && !(m.nick && isMe(m.nick.name, m.nick.host))) {
                state.unread.set(lower(room), (state.unread.get(lower(room)) || 0) + 1);
                renderRooms();
            }
        } else if (oper === "SUBSCRIBE" || oper === "UNSUBSCRIBE") {
            const who = pkt.data && pkt.data.nick;
            if (here && who && !isMe(who, pkt.data.system)) {
                const sys = pkt.data.system ? " (" + plain(pkt.data.system) + ")" : "";
                systemLine(plain(who) + sys + (oper === "SUBSCRIBE" ? " joined" : " left"));
            }
            askWho(room, 400);
        }
    }

    // --- messages -------------------------------------------------------------
    const spoken = new Map(); // lower(name) -> name, for Tab completion
    function isTuner(s) { return /^\[TVTUNER\|.*\]$/.test(s); }
    function isSelfEcho(m) {
        if (!m.nick || !isMe(m.nick.name, m.nick.host)) return false;
        const now = Date.now();
        state.pendingSelf = state.pendingSelf.filter((p) => now - p.at < 30000);
        const i = state.pendingSelf.findIndex((p) => p.str === m.str);
        if (i < 0) return false;
        state.pendingSelf.splice(i, 1);
        return true;
    }

    function setHistory(list) {
        state.messages = [];
        state.lastGroup = null;
        els.messages.textContent = "";
        list.forEach((m) => {
            if (m && typeof m === "object") {
                if (m.nick && typeof m.nick === "object") remember(m.nick);
                addMessage(m, false);
            }
        });
        if (!state.messages.length && !pendingHere().length) els.messages.appendChild(el("div", "avatar-chat-empty", "No messages yet in #" + state.view + ". Say hello."));
        showOutbox();
        renderMembers();
        settleView(state.restore);
        state.restore = null;
        releaseOutbox();
    }

    function addMessage(m, live) {
        const str = String(m.str || "");
        if (isTuner(str)) return;
        const nick = m.nick && typeof m.nick === "object" ? m.nick : {name: String(m.nick || "")};
        const name = plain(nick.name).trim();
        if (!name) { systemLine(plain(str)); return; }
        spoken.set(lower(name), name);
        const empty = els.messages.querySelector(".avatar-chat-empty");
        if (empty) empty.remove();
        const pinned = nearBottom();
        state.messages.push(m);
        const time = Number(m.time) || Date.now();
        const host = plain(nick.host || "");
        const self = isMe(name, host);
        let g = state.lastGroup;
        if (!g || g.name !== name || g.host !== host || time - g.time > GROUP_MS) {
            g = newGroup(nick, name, host, time, self);
            state.lastGroup = g;
        }
        g.time = time;
        const b = bubble(str, self);
        // Keys to find this message again after the history reloads: the
        // server stamps its own time, so the loose one leaves time out.
        b.dataset.loose = lower(name) + "|" + lower(host) + "|" + hashText(str);
        b.dataset.key = b.dataset.loose + "|" + time;
        g.content.appendChild(b);
        while (els.messages.childElementCount > KEEP) els.messages.firstElementChild.remove();
        if (live || pinned) scrollDown(pinned || (live && self));
        return b;
    }
    function hashText(str) {
        let h = 0x811c9dc5;
        for (let i = 0; i < str.length; i++) h = Math.imul(h ^ str.charCodeAt(i), 16777619);
        return (h >>> 0).toString(36);
    }

    function newGroup(nick, name, host, time, self) {
        const row = el("div", "avatar-chat-group" + (self ? " is-self" : ""));
        const av = el(self ? "div" : "button", "avatar-chat-avatar" + (self ? "" : " avatar-chat-actor"));
        if (!self) {
            av.type = "button";
            av.title = name;
            av.onclick = () => openCard(name, host);
        }
        fillAvatar(av, nick.avatar || avatarOf(name, host), name);
        if (!av.querySelector("img")) av.replaceChildren(el("div", "avatar-chat-avatar-placeholder", initial(name)));
        const content = el("div", "avatar-chat-content");
        const header = el("div", "avatar-chat-header");
        const who = self ? header : el("button", "avatar-chat-sender-button");
        if (!self) {
            who.type = "button";
            who.onclick = () => openCard(name, host);
            header.appendChild(who);
        }
        who.appendChild(handle(name, nick.style && nick.style.colors));
        if (host) {
            who.appendChild(document.createTextNode(" "));
            who.appendChild(el("span", "avatar-chat-system-name", "- " + host));
        }
        who.appendChild(document.createTextNode(" "));
        who.appendChild(el("span", "avatar-chat-time", new Date(time).toLocaleTimeString([], {hour: "numeric", minute: "2-digit"})));
        content.appendChild(header);
        row.appendChild(av);
        row.appendChild(content);
        els.messages.appendChild(row);
        return {name, host, time, content};
    }

    function bubble(str, self) {
        if (AvatarCodec.isBitmap(str)) return bitmapBubble(str);
        const b = el("div", "avatar-chat-bubble");
        const text = plain(str);
        if (!self && mentionsMe(text)) b.classList.add("is-mention");
        const rich = el("div", "avatar-chat-rich-text");
        const urls = findURLs(text);
        let rest = text;
        urls.forEach((u) => {
            const at = rest.indexOf(u);
            if (at < 0) return;
            rich.appendChild(document.createTextNode(rest.slice(0, at)));
            const a = el("a", "", u);
            a.href = u;
            a.target = "_blank";
            a.rel = "noopener noreferrer";
            rich.appendChild(a);
            rest = rest.slice(at + u.length);
        });
        rich.appendChild(document.createTextNode(rest));
        b.appendChild(rich);

        const veiled = looksNSFW(text);
        const media = el("div", "avatar-chat-rich-media");
        let pictures = 0, card = "";
        urls.forEach((u) => {
            const path = pathOf(u);
            if (IMAGE_EXT.test(path)) {
                if (pictures >= 3) return;
                pictures++;
                const a = el("a", "avatar-chat-rich-image" + (veiled || looksNSFW(u) ? " is-veiled" : ""));
                a.href = u;
                a.target = "_blank";
                a.rel = "noopener noreferrer";
                const img = el("img");
                img.alt = "";
                img.loading = "lazy";
                img.referrerPolicy = "no-referrer";
                img.onload = () => { if (nearBottom(img.height + 60)) scrollDown(true); };
                img.onerror = () => a.remove();
                img.src = imgSrc(u);
                a.appendChild(img);
                media.appendChild(a);
            } else if (AUDIO_EXT.test(path) || VIDEO_EXT.test(path)) {
                // Another site's file plays through this server's /media,
                // so the browser never fetches from it.
                media.appendChild(player(AUDIO_EXT.test(path) ? "audio" : "video", mediaSrc(u)));
            } else if (!card && !ANSI_EXT.test(path) && !MEDIA_PATH.test(u) && !AUDIO_EXT.test(path) && !VIDEO_EXT.test(path) && u.length <= 2048) {
                card = u;
            }
        });
        if (media.childElementCount) b.appendChild(media);
        if (card) linkCard(card, b);
        return b;
    }

    // mediaSrc: this server's own file as is, another site's through /media.
    function mediaSrc(u) { return sameOrigin(u) ? u : "media?url=" + encodeURIComponent(u); }
    function player(kind, src) {
        const p = el(kind, kind === "audio" ? "avatar-chat-rich-audio" : "avatar-chat-rich-video");
        p.controls = true;
        p.preload = "none";
        p.src = src;
        return p;
    }

    function bitmapBubble(str) {
        const b = el("div", "avatar-chat-bubble avatar-chat-bitmap-bubble");
        const shell = el("div", "avatar-chat-bitmap-shell");
        const frame = el("div", "avatar-chat-bitmap-frame");
        const stage = el("div", "avatar-chat-bitmap-stage");
        stage.appendChild(el("div", "avatar-chat-bitmap-placeholder", "Rendering art..."));
        frame.appendChild(stage);
        shell.appendChild(frame);
        b.appendChild(shell);
        AvatarCodec.decodeBitmap(str).then((art) => {
            if (!art) throw new Error("unreadable");
            return AvatarCodec.render(art).then((c) => {
                const pinned = nearBottom();
                shell.style.maxWidth = c.width + "px";
                c.className = "avatar-chat-bitmap-image";
                c.title = "ANSI art " + art.columns + "x" + art.rows + (art.from ? " from " + art.from : "");
                stage.replaceChildren(c);
                if (pinned) scrollDown(true);
            });
        }).catch(() => {
            stage.classList.add("is-error");
            stage.replaceChildren(el("div", "avatar-chat-bitmap-placeholder", "Unreadable ANSI art"));
        });
        return b;
    }

    const cards = new Map();
    function linkCard(u, into) {
        if (!cards.has(u)) {
            cards.set(u, fetch("preview?url=" + encodeURIComponent(u), {credentials: "same-origin"})
                .then((r) => (r.ok ? r.json() : null)).catch(() => null));
        }
        const box = el("div", "avatar-chat-card");
        into.appendChild(box);
        cards.get(u).then((d) => {
            if (!d || d.error || !(d.title || d.description || d.image)) { box.remove(); return; }
            const pinned = nearBottom();
            if (d.image && /^https?:\/\//i.test(d.image)) {
                const img = el("img", "avatar-chat-card-image" + (looksNSFW(d.title + " " + d.description + " " + u) ? " is-veiled" : ""));
                img.alt = "";
                img.loading = "lazy";
                img.referrerPolicy = "no-referrer";
                img.onerror = () => img.remove();
                img.src = imgSrc(d.image);
                box.appendChild(img);
            }
            const body = el("div", "avatar-chat-card-body");
            let site = d.siteName || "";
            if (!site) { try { site = new URL(d.finalUrl || u).host; } catch (e) { /* none */ } }
            if (site) body.appendChild(el("div", "avatar-chat-card-site", site));
            const title = el("a", "avatar-chat-card-title", d.title || u);
            title.href = d.finalUrl || u;
            title.target = "_blank";
            title.rel = "noopener noreferrer";
            body.appendChild(title);
            if (d.description) body.appendChild(el("div", "avatar-chat-card-text", d.description));
            // A download link or a track page (og:audio) plays in the card.
            if (d.audio && /^https?:\/\//i.test(d.audio)) body.appendChild(player("audio", mediaSrc(d.audio)));
            else if (d.video && /^https?:\/\//i.test(d.video)) body.appendChild(player("video", mediaSrc(d.video)));
            box.appendChild(body);
            box.classList.add("is-ready");
            if (pinned) scrollDown(true);
        });
    }

    function systemLine(text) {
        const pinned = nearBottom();
        els.messages.appendChild(el("div", "avatar-chat-system", text));
        state.lastGroup = null;
        if (pinned) scrollDown(true);
    }
    function nearBottom(slack) {
        const t = els.messages;
        return t.scrollHeight - t.scrollTop - t.clientHeight < (slack || 80);
    }
    function scrollDown(force) {
        if (force) requestAnimationFrame(() => { els.messages.scrollTop = els.messages.scrollHeight; });
    }
    // viewAnchor is where the reader is: pinned to the bottom, or the
    // first message in view and how far down the pane its top sits.
    function viewAnchor() {
        if (nearBottom()) return {pinned: true};
        const top = els.messages.getBoundingClientRect().top;
        const b = Array.from(els.messages.querySelectorAll(".avatar-chat-bubble[data-key]"))
            .find((n) => n.getBoundingClientRect().bottom > top);
        if (!b) return {pinned: true};
        return {key: b.dataset.key, loose: b.dataset.loose, offset: b.getBoundingClientRect().top - top};
    }
    // settleView puts the reader back at an anchor once the redraw is
    // laid out (after addMessage's own scrolls), or at the bottom.
    function settleView(at) {
        if (!at || at.pinned) { scrollDown(true); return; }
        requestAnimationFrame(() => {
            const list = Array.from(els.messages.querySelectorAll(".avatar-chat-bubble[data-key]"));
            const b = list.find((n) => n.dataset.key === at.key) || list.filter((n) => n.dataset.loose === at.loose).pop();
            // Gone from the reloaded history: it was older than all of it.
            if (!b) { els.messages.scrollTop = 0; return; }
            els.messages.scrollTop += b.getBoundingClientRect().top - els.messages.getBoundingClientRect().top - at.offset;
        });
    }

    // A veiled picture shows on the first click.
    els.messages.addEventListener("click", (ev) => {
        const veiled = ev.target && ev.target.closest ? ev.target.closest(".is-veiled") : null;
        if (veiled) {
            ev.preventDefault();
            veiled.classList.remove("is-veiled");
        }
    });

    // --- status bar --------------------------------------------------------------
    // Notes (info) fade after a few seconds; warnings and errors stay
    // until what they are about changes.
    let statusType = "", statusTimer = 0;
    function setStatus(type, text) {
        statusType = type;
        els.status.className = "avatar-chat-status is-visible is-" + type;
        els.statusText.textContent = text;
        clearTimeout(statusTimer);
        if (type === "info" && state.open) statusTimer = setTimeout(() => clearStatus("info"), 4000);
    }
    function clearStatus(type) {
        if (type && statusType !== type) return;
        statusType = "";
        els.status.className = "avatar-chat-status";
        els.statusText.textContent = "";
    }

    // --- rooms -----------------------------------------------------------------
    function isJoined(name) { return state.joined.some((r) => lower(r) === lower(name)); }
    function saveRooms() { save("rooms", JSON.stringify(state.joined)); }
    function userCount(room) { return (state.who.get(lower(room)) || []).length; }
    function plural(n) { return n + " user" + (n === 1 ? "" : "s"); }

    // openView shows room; keep (a reconnect's viewAnchor, same room)
    // leaves what's on screen until the history comes back.
    function openView(room, keep) {
        room = String(room || "").replace(/^#/, "");
        if (!ROOM_NAME.test(room)) room = cfg.defaultChannel;
        if (state.pm || lower(room) !== lower(state.view)) keep = null;
        state.view = room;
        state.pm = "";
        state.unread.delete(lower(room));
        state.restore = keep || null;
        if (!keep) {
            state.messages = [];
            state.lastGroup = null;
            els.messages.replaceChildren(el("div", "avatar-chat-empty", "Loading #" + room + "..."));
        }
        document.title = "#" + room + " - Avatar Chat";
        query("SLICE", loc.history(room), {data: {start: -HISTORY}, lock: 1});
        askWho(room, 0);
        renderHeader();
        renderRooms();
        renderPrivate();
        renderMembers();
    }

    function joinRoom(name, create) {
        name = String(name || "").trim().replace(/^#/, "");
        if (!ROOM_NAME.test(name)) {
            setStatus("error", "Room names are letters, digits, - and _ (up to 32).");
            return;
        }
        const known = knownRoom(name);
        if (known) name = known;
        if (create && !known) {
            query("WRITE", "public_channels." + name, {data: true, lock: 2});
            state.publicRooms.push(name);
        }
        if (!isJoined(name)) {
            state.joined.push(name);
            while (state.joined.length > MAX_ROOMS) state.joined.shift();
            saveRooms();
        }
        subscribe(name);
        closeModal(els.roomModal);
        closeModal(els.noRoomsModal);
        openView(name);
        if (create && !known) setStatus("info", "Created #" + name + " and joined it.");
    }

    function leaveRoom(name) {
        state.joined = state.joined.filter((r) => lower(r) !== lower(name));
        saveRooms();
        unsubscribe(name);
        state.unread.delete(lower(name));
        if (lower(state.view) === lower(name)) {
            if (state.joined.length) {
                openView(state.joined[state.joined.length - 1]);
            } else {
                renderHeader();
                renderRooms();
                openModal(els.noRoomsModal, els.noRoomsMain);
            }
            return;
        }
        renderRooms();
    }

    function knownRoom(name) {
        const all = [cfg.defaultChannel].concat(state.publicRooms, state.joined);
        return all.find((r) => lower(r) === lower(name)) || "";
    }

    function renderHeader() {
        if (state.pm) { renderThreadHeader(); return; }
        els.count.hidden = false;
        els.roomLabel.textContent = "#" + state.view;
        els.count.textContent = plural(userCount(state.view));
        els.roomActions.replaceChildren();
        const action = (label, cls, fn) => {
            const b = el("button", "avatar-chat-room-action" + (cls ? " " + cls : ""), label);
            b.type = "button";
            b.onclick = fn;
            els.roomActions.appendChild(b);
            return b;
        };
        if (isJoined(state.view)) {
            action("Join a room", "", openRoomModal);
            action("Leave", "is-leave", () => leaveRoom(state.view)).title = "Leave #" + state.view;
        } else {
            action("Join #" + state.view, "is-primary", () => joinRoom(state.view));
            action("Other rooms", "", openRoomModal);
        }
    }

    function renderRooms() {
        els.rooms.replaceChildren();
        if (!state.joined.length) {
            els.rooms.appendChild(el("div", "avatar-chat-nav-empty", "No rooms joined."));
            return;
        }
        state.joined.slice().sort((a, b) => {
            const ua = state.unread.get(lower(a)) ? 1 : 0, ub = state.unread.get(lower(b)) ? 1 : 0;
            return ub - ua || a.toLowerCase().localeCompare(b.toLowerCase());
        }).forEach((room) => {
            const active = !state.pm && lower(room) === lower(state.view);
            const row = el("div", "avatar-chat-nav-item avatar-chat-room" + (active ? " is-active" : ""));
            row.setAttribute("role", "button");
            row.tabIndex = 0;
            row.dataset.room = room;
            const top = el("div", "avatar-chat-nav-top");
            top.appendChild(el("span", "avatar-chat-nav-title", "#" + room));
            const unread = state.unread.get(lower(room)) || 0;
            if (unread) top.appendChild(el("span", "avatar-chat-badge is-unread", String(unread)));
            const x = el("button", "avatar-chat-nav-icon is-leave", "×");
            x.type = "button";
            x.title = "Leave #" + room;
            x.setAttribute("aria-label", "Leave #" + room);
            x.onclick = (ev) => { ev.stopPropagation(); leaveRoom(room); };
            top.appendChild(x);
            row.appendChild(top);
            row.appendChild(el("div", "avatar-chat-nav-secondary", plural(userCount(room)) + (active ? " · Current" : "")));
            const open = () => { if (!active) openView(room); };
            row.onclick = open;
            row.onkeydown = (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); open(); } };
            els.rooms.appendChild(row);
        });
    }

    // The picker: every public room with its user count, one click to join;
    // a typed name joins it, or creates it where the server allows.
    function openRoomModal() {
        closeModal(els.noRoomsModal);
        els.roomInput.value = "";
        els.roomText.textContent = cfg.createRooms
            ? "Pick a room, or type a name (letters, numbers, _ or -). A new name creates the room."
            : "Pick a room, or type its name.";
        renderPicker();
        openModal(els.roomModal, els.roomInput);
        query("KEYS", "public_channels", {lock: 1});
    }

    function setPublicRooms(data) {
        let names = Array.isArray(data) ? data.slice() : data && typeof data === "object" ? Object.keys(data) : [];
        names = names.map(String).filter((n) => ROOM_NAME.test(n));
        const seenNames = new Set();
        state.publicRooms = names.filter((n) => !seenNames.has(lower(n)) && seenNames.add(lower(n)));
        pickerRooms().slice(0, PICKER_COUNTS).forEach((r) => askWho(r, 0));
        renderPicker();
    }

    function pickerRooms() {
        const out = [];
        const have = new Set();
        [cfg.defaultChannel].concat(state.publicRooms, state.joined).forEach((r) => {
            if (!have.has(lower(r))) { have.add(lower(r)); out.push(r); }
        });
        return out.sort((a, b) => userCount(b) - userCount(a) || a.toLowerCase().localeCompare(b.toLowerCase()));
    }

    function renderPicker() {
        if (!els.roomModal.classList.contains("is-open")) return;
        els.roomList.replaceChildren();
        pickerRooms().forEach((room) => {
            const current = lower(room) === lower(state.view);
            const btn = el("button", "avatar-chat-picker-room" + (current ? " is-current" : ""));
            btn.type = "button";
            btn.dataset.room = room;
            btn.appendChild(el("span", "avatar-chat-picker-room-name", "#" + room));
            btn.appendChild(el("span", "avatar-chat-picker-room-count", plural(userCount(room))));
            const st = current ? "Current" : isJoined(room) ? "Joined" : "";
            if (st) btn.appendChild(el("span", "avatar-chat-nav-state", st));
            btn.onclick = () => joinRoom(room);
            els.roomList.appendChild(btn);
        });
        updateRoomSubmit();
    }

    // Join #x when x is a room, Create #x when it would be new.
    function updateRoomSubmit() {
        const typed = els.roomInput.value.trim().replace(/^#/, "");
        const b = els.roomSubmit;
        delete b.dataset.create;
        if (!typed || !ROOM_NAME.test(typed)) {
            b.textContent = "Join";
            b.disabled = true;
            return;
        }
        const known = knownRoom(typed);
        if (known) {
            b.textContent = "Join #" + known;
            b.disabled = false;
        } else if (cfg.createRooms) {
            b.textContent = "Create #" + typed;
            b.dataset.create = "1";
            b.disabled = false;
        } else {
            b.textContent = "No room #" + typed;
            b.disabled = true;
        }
    }

    // --- members -----------------------------------------------------------------
    function setWho(room, list) {
        const people = [];
        const have = new Set();
        list.forEach((e) => {
            const nick = plain(typeof e === "string" ? e : e && e.nick).trim();
            const system = plain(e && typeof e === "object" ? e.system || "" : "").trim();
            const key = lower(nick) + "|" + lower(system);
            if (!nick || have.has(key)) return;
            have.add(key);
            people.push({nick, system});
        });
        state.who.set(lower(room), people);
        if (lower(room) === lower(state.view)) {
            renderHeader();
            renderMembers();
        }
        renderRooms();
        renderPicker();
    }

    // Grouped by board like a member list, names sorted in each, your own
    // board first.
    function renderMembers() {
        const people = state.who.get(lower(state.view)) || [];
        els.membersTitle.textContent = "Members — " + people.length;
        els.membersList.replaceChildren();
        const groups = new Map();
        people.forEach((p) => {
            const k = p.system || "";
            if (!groups.has(k)) groups.set(k, []);
            groups.get(k).push(p);
        });
        const order = Array.from(groups.keys()).sort((a, b) => {
            if (lower(a) === lower(cfg.systemName)) return -1;
            if (lower(b) === lower(cfg.systemName)) return 1;
            return a.toLowerCase().localeCompare(b.toLowerCase());
        });
        order.forEach((board) => {
            const list = groups.get(board).sort((a, b) => a.nick.toLowerCase().localeCompare(b.nick.toLowerCase()));
            els.membersList.appendChild(el("div", "sa-member-group", (board || "Online") + " — " + list.length));
            list.forEach((p) => {
                const row = el("button", "sa-member");
                row.type = "button";
                row.dataset.nick = p.nick;
                const box = el("div", "sa-mini-avatar");
                fillAvatar(box, avatarOf(p.nick, p.system), p.nick);
                row.appendChild(box);
                const name = el("span", "sa-member-name");
                name.appendChild(handle(p.nick, colorsOf(p.nick, p.system)));
                row.appendChild(name);
                row.onclick = () => openCard(p.nick, p.system);
                els.membersList.appendChild(row);
            });
        });
        if (!people.length) els.membersList.appendChild(el("div", "avatar-chat-nav-empty", "Nobody here yet."));
    }

    function openCard(name, host) {
        fillAvatar(els.cardAvatar, avatarOf(name, host), name);
        els.cardName.replaceChildren(handle(name, colorsOf(name, host)));
        els.cardBoard.textContent = host || "";
        els.cardMessage.hidden = isMe(name, host);
        els.cardMessage.onclick = () => { closeModal(els.cardModal); messageTo(name, host); };
        openModal(els.cardModal, els.cardClose);
    }

    // --- private messages ----------------------------------------------------------
    const threadKey = (name, host) => lower(name) + "|" + lower(host);

    // thread returns (making it) the thread with name on host.
    function thread(name, host, mailbox) {
        const key = threadKey(name, host);
        let t = state.threads.get(key);
        if (!t) {
            t = {key, name, host: host || "", mailbox: "", msgs: [], unread: 0, last: 0};
            state.threads.set(key, t);
        }
        if (validMailbox(name, host, mailbox)) t.mailbox = mailbox;
        return t;
    }
    // peerOf: who a message in our mailbox is with, and whether we sent it.
    function peerOf(m) {
        const n = m.nick && typeof m.nick === "object" ? m.nick : null;
        if (!n || !n.name) return null;
        if (isMe(n.name, n.host)) {
            const to = m.private && m.private.to;
            return to && to.name ? {name: plain(to.name).trim(), host: plain(to.host || "").trim(), mailbox: to.mailbox || "", mine: true} : null;
        }
        return {name: plain(n.name).trim(), host: plain(n.host || "").trim(), mailbox: n.mailbox || "", mine: false};
    }
    function filePM(m) {
        const p = peerOf(m);
        if (!p || !p.name) return null;
        if (!p.mine) remember(m.nick);
        const t = thread(p.name, p.host, p.mailbox);
        t.msgs.push(m);
        if (t.msgs.length > MAX_THREAD) t.msgs.shift();
        t.last = Math.max(t.last, Number(m.time) || 0);
        return t;
    }
    function setThreads(list) {
        const unread = new Map();
        state.threads.forEach((t, k) => unread.set(k, t.unread));
        state.threads = new Map();
        list.forEach((m) => { if (m && typeof m === "object") filePM(m); });
        state.threads.forEach((t, k) => { t.unread = unread.get(k) || 0; });
        if (state.pm) {
            if (state.threads.has(state.pm)) openThread(state.pm, true);
            state.restore = null;
            releaseOutbox();
        }
        renderPrivate();
    }
    function incomingPM(m) {
        const t = filePM(m);
        if (!t) return;
        if (state.pm === t.key) addMessage(m, true);
        else t.unread++;
        renderPrivate();
    }

    // messageTo opens (or starts) the thread with name on host.
    function messageTo(name, host) {
        const t = thread(name, host, peerMailbox(name, host));
        openThread(t.key);
        els.input.focus();
    }
    // openThread shows a private thread; reload (setThreads after a
    // reconnect) keeps the reader where they were.
    function openThread(key, reload) {
        const t = state.threads.get(key);
        if (!t) return;
        const at = reload ? state.restore : null;
        state.restore = null;
        state.pm = key;
        t.unread = 0;
        state.messages = [];
        state.lastGroup = null;
        els.messages.textContent = "";
        t.msgs.forEach((m) => addMessage(m, false));
        if (!t.msgs.length && !pendingHere().length) els.messages.appendChild(el("div", "avatar-chat-empty", "No messages with " + t.name + " yet. Say hello."));
        showOutbox();
        document.title = "@" + t.name + " - Avatar Chat";
        renderHeader();
        renderRooms();
        renderPrivate();
        settleView(at);
    }
    function renderThreadHeader() {
        const t = state.threads.get(state.pm);
        els.roomLabel.textContent = "@" + (t ? t.name : "");
        els.count.hidden = true;
        els.roomActions.replaceChildren();
        const b = el("button", "avatar-chat-room-action", "Back to #" + state.view);
        b.type = "button";
        b.onclick = () => openView(state.view);
        els.roomActions.appendChild(b);
    }
    function renderPrivate() {
        els.private.replaceChildren();
        $("private-section").classList.toggle("is-empty", state.threads.size === 0);
        const items = Array.from(state.threads.values()).sort((a, b) =>
            (b.unread ? 1 : 0) - (a.unread ? 1 : 0) || b.last - a.last || a.name.toLowerCase().localeCompare(b.name.toLowerCase()));
        if (!items.length) {
            els.private.appendChild(el("div", "avatar-chat-nav-empty", "No private chats yet."));
            return;
        }
        items.forEach((t) => {
            const row = el("button", "avatar-chat-nav-item" + (state.pm === t.key ? " is-active" : ""));
            row.type = "button";
            row.dataset.thread = t.name;
            const inner = el("div", "avatar-chat-thread-row");
            const online = (state.who.get(lower(state.view)) || []).some((p) => lower(p.nick) === lower(t.name) && (!p.system || !t.host || lower(p.system) === lower(t.host)));
            const dot = el("span", "avatar-chat-presence-dot " + (online ? "is-online" : "is-offline"));
            dot.setAttribute("aria-hidden", "true");
            inner.appendChild(dot);
            const av = el("div", "avatar-chat-thread-avatar");
            fillAvatar(av, avatarOf(t.name, t.host), t.name);
            if (!av.querySelector("img")) av.replaceChildren(el("div", "avatar-chat-avatar-placeholder", initial(t.name)));
            inner.appendChild(av);
            const text = el("div");
            const top = el("div", "avatar-chat-nav-top");
            const title = el("span", "avatar-chat-nav-title");
            title.appendChild(document.createTextNode("@"));
            title.appendChild(handle(t.name, colorsOf(t.name, t.host)));
            top.appendChild(title);
            if (t.unread) top.appendChild(el("span", "avatar-chat-badge is-unread", String(t.unread)));
            text.appendChild(top);
            if (t.host) text.appendChild(el("div", "avatar-chat-nav-secondary", t.host));
            const last = t.msgs[t.msgs.length - 1];
            if (last) {
                const pv = AvatarCodec.isBitmap(String(last.str || "")) ? "(art)" : plain(last.str).slice(0, 60);
                text.appendChild(el("div", "avatar-chat-nav-preview", pv));
            }
            inner.appendChild(text);
            row.appendChild(inner);
            row.onclick = () => openThread(t.key);
            els.private.appendChild(row);
        });
    }
    // ownNick is our nick as it goes on a message.
    function ownNick() {
        const nick = {name: state.nick, host: cfg.systemName};
        if (state.avatar) nick.avatar = state.avatar;
        if (state.colors.some((c) => HEX.test(c || ""))) nick.style = {colors: state.colors.slice()};
        return nick;
    }

    // pmOut is a message to the open thread and where it goes: the peer's
    // web mailbox when they have one, else their plain name, with a copy
    // in our own.
    function pmOut(text) {
        const t = state.threads.get(state.pm);
        if (!t) return null;
        const to = {name: t.name, host: t.host};
        if (validMailbox(t.name, t.host, t.mailbox)) to.mailbox = t.mailbox;
        const msg = {nick: ownNick(), str: text, time: Date.now(), private: {to}};
        const target = to.mailbox || t.name;
        return {msg, where: {pm: t.key}, writes: [["WRITE", loc.messages(target)], ["PUSH", loc.history(target)], ["PUSH", loc.history(ownMailbox())]]};
    }

    function renderMe() {
        els.meName.replaceChildren(handle(state.nick || "?", state.colors));
        fillAvatar(els.meAvatar, state.avatar, state.nick);
    }

    // --- modals --------------------------------------------------------------------
    function openModal(m, focus) {
        m.classList.add("is-open");
        m.setAttribute("aria-hidden", "false");
        if (focus) focus.focus();
    }
    function closeModal(m) {
        m.classList.remove("is-open");
        m.setAttribute("aria-hidden", "true");
    }
    function anyModalOpen() { return !!document.querySelector(".avatar-chat-modal.is-open"); }

    // --- first logon: like the door's first run, no avatar yet (and not
    // settled by a Remove) gets one welcome per page load -------------------
    function needsWelcome() { return !state.avatar && !load("noavatar"); }
    function openWelcome() {
        state.welcomed = true;
        state.welcomeNext = true;
        state.welcomeOffersColors = !state.colors.some((c) => c);
        showWelcome();
    }
    function showWelcome() {
        const has = !!state.avatar;
        fillAvatar(els.welcomeAvatar, state.avatar, state.nick);
        if (!has) els.welcomeAvatar.textContent = "none";
        els.welcomeName.replaceChildren(handle(state.nick, state.colors));
        els.welcomeText.textContent = has ? "Your avatar is set." : "You have no avatar yet. Pick one to go with your messages.";
        els.welcomeAvatarOpen.textContent = has ? "Change avatar" : "Choose avatar";
        els.welcomeColors.hidden = !state.welcomeOffersColors;
        els.welcomeLater.textContent = has || state.colors.some((c) => c) ? "Done" : "Not now";
        openModal(els.welcomeModal, els.welcomeAvatarOpen);
    }
    // welcomeDone offers the rooms once the welcome, and whatever it
    // opened, has closed.
    function welcomeDone() {
        if (!state.welcomeNext || anyModalOpen()) return;
        if (state.welcomeReturn) {
            state.welcomeReturn = false;
            showWelcome();
            return;
        }
        state.welcomeNext = false;
        offerRooms();
    }

    // --- nickname and settings --------------------------------------------------------
    function validNick(n) { return /^[\p{L}\p{N} _-]{1,25}$/u.test(n); }
    function askNick(why) {
        els.nickInput.value = state.nick;
        els.nickError.textContent = why || "";
        openModal(els.nickModal, els.nickInput);
    }
    function setNick(n) {
        n = String(n || "").replace(/\s+/g, " ").trim();
        if (!validNick(n)) return false;
        const changed = n !== state.nick;
        state.nick = n;
        save("nick", n);
        state.colors = storedColors(n);
        renderMe();
        if (changed) {
            // Another name, another mailbox: its threads come with the claim.
            state.threads = new Map();
            state.pm = "";
            renderPrivate();
        }
        if (changed && state.ws || state.refused) reconnect(); // the server fixes a nick per connection
        else if (!state.ws) connect();
        return true;
    }

    function styleOffered(id) { return cfg.styles.some((s) => s.id === id); }
    function applyStyle(id) {
        if (!styleOffered(id)) id = cfg.defaultStyle;
        Array.from(els.app.classList).filter((c) => c.indexOf("ac-style-") === 0).forEach((c) => els.app.classList.remove(c));
        els.app.classList.add("ac-style-" + id);
    }

    // --- handle colours: one per letter of the name, kept in this browser
    // per name, sent as nick.style (colours only, no tag) ------------------
    const colorsKey = (name) => "colors." + lower(name);
    function storedColors(name) {
        const list = loadJSON(colorsKey(name), []);
        const n = Array.from(name || "").length;
        const out = [];
        for (let i = 0; i < n; i++) out.push(HEX.test((list && list[i]) || "") ? list[i] : "");
        return out;
    }
    function saveColors(name, colors) {
        const list = colors.slice();
        while (list.length && !list[list.length - 1]) list.pop();
        save(colorsKey(name), list.length ? JSON.stringify(list) : "");
    }
    const hexChannel = (v) => Math.max(0, Math.min(255, Math.round(v))).toString(16).padStart(2, "0");
    const hexRgb = (h) => HEX.test(h || "") ? [parseInt(h.substr(1, 2), 16), parseInt(h.substr(3, 2), 16), parseInt(h.substr(5, 2), 16)] : null;
    function hsvHex(h, s, v) {
        const c = v * s, x = c * (1 - Math.abs((h / 60) % 2 - 1)), m = v - c;
        const rgb = h < 60 ? [c, x, 0] : h < 120 ? [x, c, 0] : h < 180 ? [0, c, x] : h < 240 ? [0, x, c] : h < 300 ? [x, 0, c] : [c, 0, x];
        return "#" + hexChannel((rgb[0] + m) * 255) + hexChannel((rgb[1] + m) * 255) + hexChannel((rgb[2] + m) * 255);
    }
    // presetColors matches the Synchronet page's (and the door's) presets.
    function presetColors(kind, n, a, b) {
        const ra = hexRgb(a), rb = hexRgb(b), out = [];
        for (let i = 0; i < n; i++) {
            if (kind === "solid") out.push(a);
            else if (kind === "gradient" && ra && rb) {
                const t = n > 1 ? i / (n - 1) : 0;
                out.push("#" + hexChannel(ra[0] + (rb[0] - ra[0]) * t) + hexChannel(ra[1] + (rb[1] - ra[1]) * t) + hexChannel(ra[2] + (rb[2] - ra[2]) * t));
            } else if (kind === "rainbow") out.push(hsvHex(i / Math.max(1, n) * 360, 0.85, 1));
            else out.push("");
        }
        return out;
    }
    function draftName() { return els.settingsNick.value.replace(/\s+/g, " ").trim() || state.nick; }
    function renderStyleEditor() {
        const name = draftName();
        const chars = Array.from(name);
        els.styleLetters.replaceChildren();
        chars.forEach((ch, i) => {
            const c = state.draftColors[i] || "";
            const box = el("span", "avatar-chat-style-letter" + (c ? "" : " is-default"));
            box.appendChild(el("span", "avatar-chat-style-char", ch));
            const pick = el("input");
            pick.type = "color";
            pick.value = c || "#aaaaaa";
            pick.dataset.letter = String(i);
            pick.setAttribute("aria-label", "Colour of letter " + (i + 1));
            box.appendChild(pick);
            const x = el("button", "", "x");
            x.type = "button";
            x.dataset.reset = String(i);
            x.title = "Default colour";
            box.appendChild(x);
            els.styleLetters.appendChild(box);
        });
        els.stylePreview.replaceChildren(handle(name, state.draftColors));
    }

    function openSettings() {
        els.settingsNick.value = state.nick;
        state.draftColors = state.colors.slice();
        renderStyleEditor();
        els.settingsStyle.replaceChildren();
        cfg.styles.forEach((s) => {
            const o = el("option", "", s.label);
            o.value = s.id;
            els.settingsStyle.appendChild(o);
        });
        els.settingsStyle.value = styleOffered(state.style) ? state.style : cfg.defaultStyle;
        els.settingsStatus.textContent = "";
        els.settingsStatus.classList.remove("sa-error");
        openModal(els.settingsModal, els.settingsNick);
    }
    function closeSettings(saved) {
        if (!saved) applyStyle(state.style);
        closeModal(els.settingsModal);
        welcomeDone();
    }

    // --- composer ----------------------------------------------------------------------
    function submit() {
        if (state.pendingArt) { sendArt(); return; }
        const text = els.input.value.replace(/[\r\n]+/g, " ").trim();
        if (!text) return;
        if (Array.from(text).length > cfg.maxMessage) {
            setStatus("error", "That message is longer than " + cfg.maxMessage + " characters.");
            return;
        }
        if (postText(text)) els.input.value = "";
    }

    // --- Tab completes a name, like the door's input line: the word before
    // the caret, or the last two to four for names with spaces (the
    // longest that starts a name wins), against the room's members, anyone
    // seen speaking and the PM peer. Tab again cycles, Shift+Tab goes back,
    // any other key ends it; with no match Tab moves focus as usual. -------
    const tab = {matches: null, idx: 0, start: 0, end: 0};
    function knownNames() {
        const out = new Map();
        const add = (n) => {
            n = plain(n).trim();
            if (n && !out.has(lower(n))) out.set(lower(n), n);
        };
        (state.who.get(lower(state.view)) || []).forEach((p) => add(p.nick));
        spoken.forEach(add);
        const t = state.pm && state.threads.get(state.pm);
        if (t) add(t.name);
        return Array.from(out.values());
    }
    // nameMatches gives {start, matches} for text up to caret, or null.
    function nameMatches(text, caret, names) {
        const before = text.slice(0, caret);
        if (!before || /\s$/.test(before)) return null;
        const starts = [];
        for (let i = before.length - 1; i >= 0 && starts.length < 4; i--) {
            if (!/\s/.test(before[i]) && (i === 0 || /\s/.test(before[i - 1]))) starts.push(i);
        }
        for (let k = starts.length - 1; k >= 0; k--) {
            const prefix = lower(before.slice(starts[k]));
            const matches = names.filter((n) => lower(n).indexOf(prefix) === 0).sort();
            if (matches.length) return {start: starts[k], matches};
        }
        return null;
    }
    function completeName(ev) {
        const input = els.input;
        if (tab.matches) {
            tab.idx = (tab.idx + (ev.shiftKey ? tab.matches.length - 1 : 1)) % tab.matches.length;
        } else {
            if (ev.shiftKey || input.selectionStart !== input.selectionEnd) return;
            const hit = nameMatches(input.value, input.selectionStart, knownNames());
            if (!hit) return;
            Object.assign(tab, {matches: hit.matches, idx: 0, start: hit.start, end: input.selectionStart});
        }
        ev.preventDefault();
        const name = tab.matches[tab.idx];
        input.value = input.value.slice(0, tab.start) + name + input.value.slice(tab.end);
        tab.end = tab.start + name.length;
        input.setSelectionRange(tab.end, tab.end);
    }

    // postText shows text as sending and puts it in the outbox. It goes
    // now if the socket was heard from lately, else after a probe (or the
    // reconnect a failed probe starts). art is the pending art it came
    // from, put back if the send fails.
    function postText(text, art) {
        if (!state.open) { setStatus("error", "Not connected yet."); return false; }
        let out;
        if (state.pm) {
            out = pmOut(text);
            if (!out) return false;
        } else {
            if (!isJoined(state.view)) joinRoom(state.view);
            out = {msg: {nick: ownNick(), str: text, time: Date.now()}, where: {room: state.view},
                writes: [["WRITE", loc.messages(state.view)], ["PUSH", loc.history(state.view)]]};
        }
        const e = Object.assign(out, {id: ++outboxSeq, text, art: art || null, phase: "queued", node: null, timer: 0});
        state.outbox.push(e);
        showEntry(e, true);
        clearStatus("error");
        e.timer = setTimeout(() => failSend(e, "the connection isn't back yet."), SEND_WAIT);
        if (Date.now() - state.heard < FRESH_MS && !state.probeTimer && !state.holdOutbox) transmit(e);
        else probe();
        return true;
    }

    // --- the outbox ----------------------------------------------------------
    // A message is sent between two SOCKET PINGs. The page's packets are
    // answered in order, so an ERROR after the first PONG and before the
    // second is this message's (a policy refusal: too long, too fast...),
    // and the second PONG means it went through. The chat protocol's
    // WRITE has no reply of its own (timeout -1), so the PONG is the ack.
    let outboxSeq = 0;
    function pendingHere() {
        return state.outbox.filter((e) => (state.pm ? e.where.pm === state.pm : lower(e.where.room) === lower(state.view)));
    }
    function showEntry(e, live) {
        e.node = addMessage(e.msg, live) || null;
        if (!e.node) return;
        e.node.classList.add(e.phase === "failed" ? "is-failed" : "is-sending");
        if (e.phase === "failed") failedNote(e);
    }
    // showOutbox redraws the open view's unsent messages under a redraw.
    function showOutbox() { pendingHere().forEach((e) => showEntry(e, false)); }
    function releaseOutbox() {
        if (!state.holdOutbox) return;
        clearTimeout(state.holdOutbox);
        state.holdOutbox = 0;
        flushOutbox();
    }
    function flushOutbox() {
        if (!state.claimed || state.holdOutbox) return;
        state.outbox.filter((e) => e.phase === "queued").forEach(transmit);
    }
    function transmit(e) {
        if (!send({scope: "SOCKET", func: "PING", data: {send: e.id, at: "before"}})) return;
        e.phase = "sent";
        const lp = {data: e.msg, lock: 2};
        e.writes.forEach((w) => query(w[0], w[1], lp));
        if (e.where.room) state.pendingSelf.push({str: e.text, at: Date.now()});
        send({scope: "SOCKET", func: "PING", data: {send: e.id, at: "after"}});
        // No ack in time (more for a big art payload): the socket is dead.
        clearTimeout(e.timer);
        e.timer = setTimeout(() => { if (e.phase !== "done" && e.phase !== "failed") dropDead(); }, PROBE_WAIT + Math.floor(e.text.length / 4));
    }
    function onSendPong(d) {
        const e = state.outbox.find((x) => x.id === d.send) || (state.fenced && state.fenced.id === d.send ? state.fenced : null);
        if (d.at === "before") { state.fenced = e; return; }
        if (state.fenced && state.fenced.id === d.send) state.fenced = null;
        if (!e || e.failed) return;
        e.phase = "done";
        clearTimeout(e.timer);
        state.outbox = state.outbox.filter((x) => x !== e);
        if (e.node) e.node.classList.remove("is-sending");
        const t = e.where.pm && state.threads.get(e.where.pm);
        if (t) {
            t.msgs.push(e.msg);
            if (t.msgs.length > MAX_THREAD) t.msgs.shift();
            t.last = Math.max(t.last, e.msg.time);
            renderPrivate();
        }
    }
    // lostInFlight: the socket went with messages sent and not acked. They
    // may or may not have arrived, so they fail rather than go twice.
    function lostInFlight() {
        state.fenced = null;
        state.outbox.filter((e) => e.phase === "sent").forEach((e) => failSend(e, "the connection dropped, so it may not have gone through."));
    }
    // failSend: the text goes back in the box when the box is free, else
    // the bubble stays, marked, and a click puts it back later.
    function failSend(e, why) {
        if (e.phase === "done" || e.phase === "failed") return;
        clearTimeout(e.timer);
        e.failed = true;
        e.phase = "failed";
        const back = putBack(e);
        if (back) state.outbox = state.outbox.filter((x) => x !== e);
        else if (e.node) {
            e.node.classList.replace("is-sending", "is-failed");
            failedNote(e);
        }
        if (e.where.room) state.pendingSelf = state.pendingSelf.filter((p) => p.str !== e.text);
        why = String(why).replace(/[.\s]*$/, ".");
        setStatus("error", "Not sent: " + why + (back ? " Your message is back in the box." : " It's marked in the chat; click it to edit and resend."));
    }
    function putBack(e) {
        if (state.pendingArt || els.input.value.trim()) return false;
        if (e.art) setPending(e.art);
        else els.input.value = e.text;
        dropNode(e);
        return true;
    }
    function failedNote(e) {
        e.node.title = "Not sent. Click to put it back in the box.";
        e.node.onclick = () => {
            if (!putBack(e)) { setStatus("error", "Send or clear what's in the box first."); return; }
            state.outbox = state.outbox.filter((x) => x !== e);
            clearStatus("error");
            els.input.focus();
        };
    }
    function dropNode(e) {
        const b = e.node;
        e.node = null;
        state.messages = state.messages.filter((m) => m !== e.msg);
        if (!b) return;
        const group = b.closest(".avatar-chat-group");
        b.remove();
        if (group && !group.querySelector(".avatar-chat-bubble")) {
            if (state.lastGroup && state.lastGroup.content.parentNode === group) state.lastGroup = null;
            group.remove();
        }
    }

    function fromName() { return (cfg.systemName && state.nick) ? cfg.systemName + ":" + state.nick : state.nick || cfg.systemName; }

    function setPending(art) {
        state.pendingArt = art;
        els.form.classList.toggle("has-art", !!art);
        els.input.readOnly = !!art;
        els.input.placeholder = art
            ? "ANSI art ready (" + art.width + "x" + art.height + "): Send posts it, Esc drops it"
            : "Type a message...";
    }

    function sendArt() {
        const art = state.pendingArt;
        if (!art || state.busy) return;
        state.busy = true;
        AvatarArt.buildBitmapPayload(art.cells, art.width, art.height, fromName()).then((payload) => {
            if (payload.length > 32000) throw new Error("That art is too big to send (BITMAP messages are capped at 32,000 characters).");
            if (postText(payload, art)) setPending(null);
        }).catch((e) => setStatus("error", String(e.message || e))).then(() => { state.busy = false; });
    }

    function readBytes(file) {
        return new Promise((resolve, reject) => {
            const r = new FileReader();
            r.onload = () => resolve(new Uint8Array(r.result));
            r.onerror = () => reject(new Error("Could not read " + file.name + "."));
            r.readAsArrayBuffer(file);
        });
    }

    // A dropped, pasted or chosen file: ANSI art by its name, or anything
    // that isn't a picture, sound or video, is tried as art; pictures go up
    // when the server has uploads on.
    function looksLikeArt(file) {
        if (ART_EXTENSIONS.test(file.name || "")) return true;
        return !/^(image|audio|video)\//.test(file.type || "") && file.size <= ART_MAX_BYTES;
    }

    function acceptFile(file) {
        if (!file || state.busy) return;
        clearStatus("error");
        if (looksLikeArt(file)) {
            readBytes(file).then((bytes) => setPending(AvatarArt.prepareAnsiBitmap(bytes)))
                .catch((e) => setStatus("error", String(e.message || e)));
            return;
        }
        if (!cfg.uploads) {
            setStatus("error", "This server only takes ANSI art here; picture uploads are off.");
            return;
        }
        upload(file);
    }

    async function upload(file) {
        state.busy = true;
        els.attach.disabled = true;
        els.input.placeholder = "Uploading " + file.name + "...";
        try {
            const r = await fetch("upload?name=" + encodeURIComponent(file.name), {
                method: "POST", body: file, credentials: "same-origin",
                headers: {"Content-Type": "application/octet-stream"},
            });
            const reply = await r.json().catch(() => ({}));
            if (!r.ok || !reply.url) throw new Error(reply.error || "Upload failed (HTTP " + r.status + ").");
            const v = els.input.value;
            els.input.value = (v && !/\s$/.test(v) ? v + " " : v) + reply.url;
            els.input.focus();
        } catch (e) {
            setStatus("error", String(e.message || e));
        } finally {
            state.busy = false;
            els.attach.disabled = false;
            els.input.placeholder = "Type a message...";
        }
    }

    // --- the ANSI editor (the Synchronet page's vendored copy, in shared/) ---------------
    let editorLoading = null;
    function loadScript(src) {
        return new Promise((resolve, reject) => {
            const s = document.createElement("script");
            s.src = src;
            s.onload = resolve;
            s.onerror = () => reject(new Error("The ANSI editor could not be loaded."));
            document.head.appendChild(s);
        });
    }
    function addSheet(href, id) {
        const l = document.createElement("link");
        l.rel = "stylesheet";
        l.href = href;
        if (id) l.id = id;
        document.head.appendChild(l);
    }
    function ensureEditor() {
        if (window.AnsiEditorModal && window.AnsiEditor) return Promise.resolve();
        if (!editorLoading) {
            // The editor's own sheet as a file under the id it looks for, so
            // it doesn't inject a <style> this page's CSP would refuse.
            addSheet("shared/editor.css", "me-editor-styles");
            addSheet("shared/ansi-editor-modal.css");
            editorLoading = loadScript("shared/ansi-editor.js").then(() => loadScript("shared/ansi-editor-modal.js")).then(() => {
                if (!window.AnsiEditorModal || !window.AnsiEditor) throw new Error("The ANSI editor could not be loaded.");
            }).catch((e) => { editorLoading = null; throw e; });
        }
        return editorLoading;
    }
    const EDITOR_FONT = "shared/fonts/CP437.F16";
    function editorSize(w, h) {
        return {
            width: Math.min(w, window.innerWidth), height: Math.min(h, window.innerHeight),
            minWidth: Math.min(480, window.innerWidth), minHeight: Math.min(360, window.innerHeight),
        };
    }

    function openArtEditor() {
        if (state.busy) return;
        ensureEditor().then(() => {
            if (AnsiEditorModal.isOpen) return;
            AnsiEditorModal.open(Object.assign({
                title: "ANSI Editor", className: "avatar-chat-ae", columns: 80, rows: 25, fontUrl: EDITOR_FONT,
                onDone: (editor) => {
                    const doc = editor && editor.doc;
                    if (!doc || !doc.data) return;
                    setPending({cells: doc.data, width: doc.columns, height: doc.rows});
                    sendArt();
                },
            }, editorSize(980, 620)));
        }).catch((e) => setStatus("error", String(e.message || e)));
    }

    // --- avatar ---------------------------------------------------------------------
    const FILE_COLLECTION = "__file__";
    const avatarState = {collections: [], loaded: new Map(), list: [], selected: -1};

    function avatarNote(text, isError) {
        els.avatarStatus.textContent = text || "";
        els.avatarStatus.classList.toggle("sa-error", !!isError);
    }
    function setAvatar(b64, note) {
        state.avatar = b64 || "";
        save("avatar", state.avatar);
        renderMe();
        renderMembers();
        renderAvatarCurrent();
        avatarNote(note || "");
    }
    function renderAvatarCurrent() {
        fillAvatar(els.avatarPreview, state.avatar);
        if (!state.avatar) els.avatarPreview.textContent = "none";
        els.avatarRemove.hidden = !state.avatar;
    }

    function openAvatarModal() {
        avatarNote("");
        renderAvatarCurrent();
        els.avatarLibrary.hidden = !cfg.avatars;
        openModal(els.avatarModal, els.avatarClose);
        if (cfg.avatars && !avatarState.collections.length) {
            fetch("avatars/index.json", {credentials: "same-origin"}).then((r) => r.json()).then((list) => {
                avatarState.collections = Array.isArray(list) ? list : [];
                fillCollections();
                if (avatarState.collections.length) showCollection(avatarState.collections[0].id);
            }).catch(() => avatarNote("The avatar library could not be loaded.", true));
        }
    }

    function fillCollections() {
        els.avatarCollection.replaceChildren();
        avatarState.collections.forEach((c) => {
            const o = el("option", "", (c.title || c.name) + " (" + c.count + ")");
            o.value = c.id;
            els.avatarCollection.appendChild(o);
        });
        if (avatarState.fromFile) {
            const o = el("option", "", "From your file");
            o.value = FILE_COLLECTION;
            els.avatarCollection.appendChild(o);
        }
    }

    function showCollection(id) {
        if (id === FILE_COLLECTION) { renderGrid(); return; }
        const done = (list) => {
            avatarState.list = list;
            avatarState.selected = -1;
            renderGrid();
        };
        if (avatarState.loaded.has(id)) { done(avatarState.loaded.get(id)); return; }
        avatarNote("Loading...");
        fetch("avatars/" + encodeURIComponent(id) + ".json", {credentials: "same-origin"}).then((r) => r.json()).then((c) => {
            const list = Array.isArray(c.avatars) ? c.avatars : [];
            avatarState.loaded.set(id, list);
            avatarNote("");
            if (els.avatarCollection.value === id) done(list);
        }).catch(() => avatarNote("That collection could not be loaded.", true));
    }

    function renderGrid() {
        els.avatarGrid.replaceChildren();
        avatarState.list.forEach((b64, i) => {
            const tile = el("button", "avatar-chat-avatar-tile" + (i === avatarState.selected ? " is-selected" : ""));
            tile.type = "button";
            tile.title = "Avatar " + (i + 1);
            const img = el("img");
            img.alt = "";
            avatarURL(b64).then((u) => { img.src = u; });
            tile.appendChild(img);
            tile.onclick = () => {
                avatarState.selected = i;
                els.avatarGrid.querySelectorAll(".is-selected").forEach((t) => t.classList.remove("is-selected"));
                tile.classList.add("is-selected");
                els.avatarUse.disabled = false;
            };
            tile.ondblclick = () => setAvatar(b64, "Saved. Your next message carries it.");
            els.avatarGrid.appendChild(tile);
        });
        els.avatarUse.disabled = avatarState.selected < 0;
    }

    function openAvatarEditor(seed) {
        ensureEditor().then(() => {
            closeModal(els.avatarModal);
            const A = AvatarArt;
            AnsiEditorModal.open(Object.assign({
                title: "Avatar Editor (10x6)", className: "avatar-chat-ae avatar-chat-ae-avatar",
                columns: A.AVATAR_COLS, rows: A.AVATAR_ROWS, fontUrl: EDITOR_FONT,
                onReady: (editor) => {
                    if (editor && editor.doc && typeof editor.doc.replaceAll === "function") {
                        editor.doc.replaceAll(seed, A.AVATAR_COLS, A.AVATAR_ROWS);
                        editor.columns = A.AVATAR_COLS;
                        editor.rows = A.AVATAR_ROWS;
                        if (editor._rebuildCanvas) editor._rebuildCanvas();
                        if (editor._moveCursor) editor._moveCursor(0, 0);
                        if (editor._updateStatusBar) editor._updateStatusBar();
                        if (editor._selectTool) editor._selectTool("brush");
                        if (editor._setBrushMode) editor._setBrushMode("custom_block");
                    }
                    if (editor && editor.el && editor.el.canvasContainer) editor.el.canvasContainer.style.zoom = "5";
                },
                onDone: (editor) => {
                    const doc = editor && editor.doc;
                    openModal(els.avatarModal, els.avatarClose);
                    if (!doc || doc.columns !== A.AVATAR_COLS || doc.rows !== A.AVATAR_ROWS || !doc.data) {
                        avatarNote("The avatar has to stay 10x6.", true);
                        return;
                    }
                    setAvatar(A.cellsToAvatarBase64(doc.data), "Saved. Your next message carries it.");
                },
                onCancel: () => setTimeout(() => openModal(els.avatarModal, els.avatarClose), 0),
            }, editorSize(900, 620)));
        }).catch((e) => avatarNote(String(e.message || e), true));
    }

    // An uploaded file: one 120-byte .bin is used as is; a .bin of several
    // is shown to pick from; ANSI opens in the editor, cropped to 10x6.
    function acceptAvatarFile(file) {
        const A = AvatarArt;
        avatarNote("Reading " + file.name + "...");
        readBytes(file).then((bytes) => {
            if (/\.bin$/i.test(file.name) && bytes.length >= A.AVATAR_BYTES && bytes.length % A.AVATAR_BYTES === 0) {
                if (bytes.length === A.AVATAR_BYTES) {
                    setAvatar(A.binToBase64(bytes), "Saved. Your next message carries it.");
                    return;
                }
                avatarState.list = [];
                for (let i = 0; i * A.AVATAR_BYTES < bytes.length && i < 500; i++) {
                    avatarState.list.push(A.binToBase64(bytes.subarray(i * A.AVATAR_BYTES, (i + 1) * A.AVATAR_BYTES)));
                }
                avatarState.selected = -1;
                avatarState.fromFile = true;
                els.avatarLibrary.hidden = false;
                fillCollections();
                els.avatarCollection.value = FILE_COLLECTION;
                renderGrid();
                avatarNote("Pick one from your file, then Use selected.");
                return;
            }
            avatarNote("");
            openAvatarEditor(A.ansiToAvatarCells(bytes));
        }).catch((e) => avatarNote(String(e.message || e), true));
    }

    // --- wiring -----------------------------------------------------------------------
    els.form.addEventListener("submit", (ev) => { ev.preventDefault(); submit(); });
    els.input.addEventListener("keydown", (ev) => {
        if (ev.key === "Tab" && !ev.ctrlKey && !ev.altKey && !ev.metaKey) { completeName(ev); return; }
        if (!/^(Shift|Control|Alt|Meta)$/.test(ev.key)) tab.matches = null;
        if (ev.key === "Escape" && state.pendingArt) {
            ev.preventDefault();
            ev.stopPropagation();
            setPending(null);
        }
    });
    els.input.addEventListener("blur", () => { tab.matches = null; });
    els.input.addEventListener("input", () => { tab.matches = null; });
    els.input.addEventListener("paste", (ev) => {
        const files = ev.clipboardData && ev.clipboardData.files;
        if (files && files.length) { ev.preventDefault(); acceptFile(files[0]); }
    });
    els.attach.onclick = () => { if (!state.busy) els.file.click(); };
    els.file.onchange = () => {
        const f = els.file.files && els.file.files[0];
        els.file.value = "";
        if (f) acceptFile(f);
    };
    els.draw.onclick = openArtEditor;

    let dragDepth = 0;
    const hasFiles = (ev) => ev.dataTransfer && Array.prototype.indexOf.call(ev.dataTransfer.types || [], "Files") >= 0;
    els.chatpanel.addEventListener("dragenter", (ev) => {
        if (!hasFiles(ev)) return;
        ev.preventDefault();
        dragDepth++;
        els.chatpanel.classList.add("is-dragover");
    });
    els.chatpanel.addEventListener("dragover", (ev) => { if (hasFiles(ev)) ev.preventDefault(); });
    els.chatpanel.addEventListener("dragleave", () => {
        dragDepth = Math.max(0, dragDepth - 1);
        if (!dragDepth) els.chatpanel.classList.remove("is-dragover");
    });
    els.chatpanel.addEventListener("drop", (ev) => {
        const f = ev.dataTransfer && ev.dataTransfer.files && ev.dataTransfer.files[0];
        dragDepth = 0;
        els.chatpanel.classList.remove("is-dragover");
        if (!f) return;
        ev.preventDefault();
        acceptFile(f);
    });

    els.count.onclick = () => els.members.classList.toggle("is-open");
    els.membersClose.onclick = () => els.members.classList.remove("is-open");

    els.roomClose.onclick = () => closeModal(els.roomModal);
    els.roomInput.addEventListener("input", updateRoomSubmit);
    els.roomForm.addEventListener("submit", (ev) => {
        ev.preventDefault();
        if (els.roomSubmit.disabled) return;
        joinRoom(els.roomInput.value, !!els.roomSubmit.dataset.create);
    });
    els.noRoomsMain.onclick = () => joinRoom(cfg.defaultChannel);
    els.noRoomsPick.onclick = openRoomModal;
    els.noRoomsCancel.onclick = () => closeModal(els.noRoomsModal);

    els.welcomeAvatarOpen.onclick = () => {
        state.welcomeReturn = true;
        openAvatarModal();
        closeModal(els.welcomeModal);
    };
    els.welcomeColors.onclick = () => {
        state.welcomeReturn = true;
        openSettings();
        closeModal(els.welcomeModal);
        els.settingsColors.scrollIntoView({block: "nearest"});
        els.stylePreset.focus();
    };
    els.welcomeLater.onclick = () => {
        closeModal(els.welcomeModal);
        welcomeDone();
    };

    els.nickForm.addEventListener("submit", (ev) => {
        ev.preventDefault();
        if (setNick(els.nickInput.value)) closeModal(els.nickModal);
        else els.nickError.textContent = "Letters, digits, spaces, - and _ only, up to 25.";
    });

    els.settingsOpen.onclick = openSettings;
    // A new name in the box starts from that name's own colours.
    els.settingsNick.addEventListener("input", () => {
        state.draftColors = draftName() === state.nick ? state.colors.slice() : storedColors(draftName());
        renderStyleEditor();
    });
    els.styleLetters.addEventListener("input", (ev) => {
        const i = ev.target.dataset && ev.target.dataset.letter;
        if (i === undefined) return;
        state.draftColors[Number(i)] = ev.target.value;
        ev.target.parentNode.classList.remove("is-default");
        els.stylePreview.replaceChildren(handle(draftName(), state.draftColors));
    });
    els.styleLetters.addEventListener("click", (ev) => {
        const i = ev.target.dataset && ev.target.dataset.reset;
        if (i === undefined) return;
        state.draftColors[Number(i)] = "";
        renderStyleEditor();
    });
    els.stylePreset.addEventListener("change", () => {
        const kind = els.stylePreset.value;
        if (!kind) return;
        state.draftColors = presetColors(kind, Array.from(draftName()).length, els.styleA.value, els.styleB.value);
        els.stylePreset.value = "";
        renderStyleEditor();
    });
    els.settingsStyle.onchange = () => applyStyle(els.settingsStyle.value);
    els.settingsCancel.onclick = () => closeSettings(false);
    els.settingsForm.addEventListener("submit", (ev) => {
        ev.preventDefault();
        const n = els.settingsNick.value.replace(/\s+/g, " ").trim();
        if (!validNick(n)) {
            els.settingsStatus.textContent = "Nicknames are letters, digits, spaces, - and _, up to 25.";
            els.settingsStatus.classList.add("sa-error");
            return;
        }
        state.style = els.settingsStyle.value;
        save("style", state.style === cfg.defaultStyle ? "" : state.style);
        applyStyle(state.style);
        saveColors(n, state.draftColors);
        setNick(n);
        state.colors = storedColors(n);
        renderMe();
        renderMembers();
        closeSettings(true);
    });

    els.avatarOpen.onclick = openAvatarModal;
    els.avatarClose.onclick = () => {
        closeModal(els.avatarModal);
        welcomeDone();
    };
    els.avatarDraw.onclick = () => openAvatarEditor(AvatarArt.avatarBase64ToCells(state.avatar));
    els.avatarUpload.onclick = () => els.avatarFile.click();
    els.avatarFile.onchange = () => {
        const f = els.avatarFile.files && els.avatarFile.files[0];
        els.avatarFile.value = "";
        if (f) acceptAvatarFile(f);
    };
    // Removing is a choice made: the welcome doesn't ask again.
    els.avatarRemove.onclick = () => {
        save("noavatar", "1");
        setAvatar("", "Removed. Your messages go out without an avatar.");
    };
    els.avatarCollection.onchange = () => showCollection(els.avatarCollection.value);
    els.avatarUse.onclick = () => {
        const b64 = avatarState.list[avatarState.selected];
        if (b64) setAvatar(b64, "Saved. Your next message carries it.");
    };
    els.cardClose.onclick = () => closeModal(els.cardModal);

    // Escape closes the top modal (not the nickname prompt before there is
    // a nickname, and not while the editor is open).
    document.addEventListener("keydown", (ev) => {
        if (ev.key !== "Escape" || (window.AnsiEditorModal && AnsiEditorModal.isOpen)) return;
        if (els.settingsModal.classList.contains("is-open")) { closeSettings(false); return; }
        [els.cardModal, els.avatarModal, els.welcomeModal, els.roomModal, els.noRoomsModal].some((m) => {
            if (!m.classList.contains("is-open")) return false;
            closeModal(m);
            return true;
        });
        if (state.nick && els.nickModal.classList.contains("is-open")) closeModal(els.nickModal);
        welcomeDone();
    });
    // A click on the dark overlay closes it too.
    document.querySelectorAll(".avatar-chat-modal").forEach((m) => {
        m.addEventListener("click", (ev) => {
            if (ev.target !== m || m === els.nickModal) return;
            if (m === els.settingsModal) closeSettings(false);
            else closeModal(m);
            welcomeDone();
        });
    });

    setInterval(() => { if (state.open) refreshCounts(); }, COUNTS_EVERY);

    // A socket that died while the page slept or hid is found when it's
    // back, and checked now and then while it's shown. A tick that comes
    // late means the machine slept: check at once.
    let lastTick = Date.now();
    setInterval(() => {
        const now = Date.now(), slept = now - lastTick > 15000;
        lastTick = now;
        if (document.visibilityState === "hidden" || !state.claimed) return;
        if (slept || now - state.heard >= PROBE_EVERY) probe();
    }, 5000);
    document.addEventListener("visibilitychange", wake);
    window.addEventListener("online", wake);
    window.addEventListener("pageshow", (ev) => { if (ev.persisted) wake(); });

    // --- start ---------------------------------------------------------------------
    state.style = load("style");
    applyStyle(state.style || "classic");
    fetch("config.json", {credentials: "same-origin"}).then((r) => r.json()).catch(() => ({})).then((c) => {
        Object.assign(cfg, c || {});
        if (!Array.isArray(cfg.styles) || !cfg.styles.length) cfg.styles = [{id: "classic", label: "Classic"}];
        applyStyle(state.style);
        els.input.maxLength = cfg.maxMessage;
        els.noRoomsDefault.textContent = "#" + cfg.defaultChannel;
        els.noRoomsMain.textContent = "Join #" + cfg.defaultChannel;
        els.attach.title = cfg.uploads
            ? "Attach a picture or ANSI art (or drop or paste it on the chat)"
            : "Attach ANSI art (or drop or paste it on the chat)";
        els.file.accept = [".ans", ".asc", ".nfo", ".diz", ".ice", ".txt"].concat(cfg.uploads ? ["image/png", "image/jpeg", "image/gif", "image/webp"] : []).join(",");
        state.avatar = load("avatar");
        const rooms = loadJSON("rooms", null);
        state.joined = Array.isArray(rooms) ? rooms.filter((r) => ROOM_NAME.test(r)).slice(-MAX_ROOMS) : [cfg.defaultChannel];
        const q = new URLSearchParams(location.search).get("room");
        state.view = q && ROOM_NAME.test(q) ? q : (state.joined[state.joined.length - 1] || cfg.defaultChannel);
        renderHeader();
        renderRooms();
        renderPrivate();
        const nick = load("nick");
        if (nick && validNick(nick)) setNick(nick);
        else askNick();
        renderMe();
    });

    // For the browser tests.
    window.AvatarChatPage = {state, cfg, joinRoom, leaveRoom, openView, messageTo, openThread};
})();
