// Avatar Chat, standalone page. Talks only to the server that served it:
// the chat over WebSocket at /chat, pictures through /img, link cards
// through /preview, uploads to /upload. See WEB.md.
"use strict";

(function () {
    const $ = (id) => document.getElementById(id);
    const els = {
        transcript: $("transcript"), roster: $("roster"), rosterList: $("roster-list"),
        input: $("input"), form: $("send-form"), counter: $("counter"), status: $("status"),
        roomName: $("room-name"), roomBtn: $("room-btn"), whoBtn: $("who-btn"), whoCount: $("who-count"),
        nickBtn: $("nick-btn"), nickName: $("nick-name"),
        nickDialog: $("nick-dialog"), nickForm: $("nick-form"), nickInput: $("nick-input"), nickError: $("nick-error"),
        roomsDialog: $("rooms-dialog"), roomsList: $("rooms-list"), createRoom: $("create-room"),
        createInput: $("create-input"), createBtn: $("create-btn"),
        dropHint: $("drop-hint"), dropVeil: $("drop-veil"),
    };

    const HISTORY = 60;          // messages loaded on joining a room
    const GROUP_MS = 2 * 60000;  // same sender within this joins one group
    const KEEP = 400;            // messages kept in the transcript

    const cfg = {defaultChannel: "main", systemName: "", maxMessage: 1000, createRooms: false, uploads: false};
    const state = {
        nick: "", room: "", ws: null, open: false, retry: 0, retryTimer: 0,
        messages: [], who: [], pendingSelf: [], whoTimer: 0, lastGroup: null,
    };

    // --- storage (per browser; may be unavailable) -----------------------
    function load(key) { try { return localStorage.getItem("avchat." + key) || ""; } catch (e) { return ""; } }
    function save(key, v) { try { localStorage.setItem("avchat." + key, v); } catch (e) { /* private mode */ } }

    // --- protocol ----------------------------------------------------------
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

    // Frames are buffered and split on newlines, so one packet may span
    // frames or one frame carry several (Synchronet's websocketservice.js
    // does both); the bundled server sends one packet per frame.
    function receive(data) {
        // A text frame (the bundled server's) is one whole packet.
        const bytes = typeof data === "string" ? enc.encode(data + "\n") : new Uint8Array(data);
        const joined = new Uint8Array(rx.length + bytes.length);
        joined.set(rx);
        joined.set(bytes, rx.length);
        let start = 0;
        for (let i = 0; i < joined.length; i++) {
            if (joined[i] !== 10) continue;
            const line = dec.decode(joined.subarray(start, i)).trim();
            start = i + 1;
            if (!line || line[0] !== "{") continue; // e.g. "Redirecting to server..."
            let pkt;
            try { pkt = JSON.parse(line); } catch (e) { continue; }
            handle(pkt);
        }
        rx = joined.slice(start);
    }

    function connect() {
        clearTimeout(state.retryTimer);
        const scheme = location.protocol === "https:" ? "wss://" : "ws://";
        const ws = new WebSocket(scheme + location.host + "/chat", ["binary"]);
        ws.binaryType = "arraybuffer";
        state.ws = ws;
        rx = new Uint8Array(0);
        setStatus(false, "connecting");
        ws.onopen = () => {
            state.open = true;
            state.retry = 0;
            setStatus(true, "connected");
            join(state.room || cfg.defaultChannel, true);
        };
        ws.onmessage = (ev) => receive(ev.data);
        ws.onclose = () => {
            if (state.ws !== ws) return;
            state.open = false;
            const wait = Math.min(30000, 1000 * Math.pow(2, state.retry++));
            setStatus(false, "reconnecting in " + Math.round(wait / 1000) + "s");
            state.retryTimer = setTimeout(connect, wait);
        };
    }

    function reconnect() {
        const old = state.ws;
        state.ws = null;
        if (old) old.close();
        connect();
    }

    function join(room, fresh) {
        room = String(room || "").replace(/^#/, "");
        if (!/^[A-Za-z0-9_-]{1,32}$/.test(room)) {
            notice("room names are letters, digits, - and _ (up to 32)", true);
            return;
        }
        if (!fresh && state.room && state.room !== room) {
            query("UNSUBSCRIBE", loc.messages(state.room), {nick: state.nick, system: cfg.systemName});
        }
        state.room = room;
        save("room", room);
        els.roomName.textContent = room;
        document.title = "#" + room + " · Avatar Chat";
        state.messages = [];
        state.lastGroup = null;
        els.transcript.textContent = "";
        query("SUBSCRIBE", loc.messages(room), {nick: state.nick, system: cfg.systemName});
        query("SLICE", loc.history(room), {data: {start: -HISTORY}, lock: 1});
        refreshWho(0);
    }

    function refreshWho(delay) {
        clearTimeout(state.whoTimer);
        state.whoTimer = setTimeout(() => query("WHO", loc.messages(state.room)), delay);
    }

    function handle(pkt) {
        if (pkt.scope === "SOCKET") {
            if (pkt.func === "PING") send({scope: "SOCKET", func: "PONG", data: pkt.data});
            return;
        }
        if (pkt.func === "ERROR") {
            const why = pkt.data && pkt.data.description ? pkt.data.description : "error";
            // A room that isn't public (any more): go home, then say why.
            if (/^no public room/.test(why) && state.room.toLowerCase() !== cfg.defaultChannel.toLowerCase()) {
                join(cfg.defaultChannel, true);
            }
            notice(why, true);
            if (/nickname/.test(why)) askNick(why);
            return;
        }
        const here = state.room && pkt.location === loc.messages(state.room);
        if (pkt.func === "RESPONSE") {
            if (pkt.oper === "SLICE" && pkt.location === loc.history(state.room)) {
                setHistory(Array.isArray(pkt.data) ? pkt.data : []);
            } else if (pkt.oper === "WHO" && here) {
                setWho(Array.isArray(pkt.data) ? pkt.data : []);
            } else if (pkt.oper === "KEYS" && pkt.location === "public_channels") {
                showRooms(pkt.data);
            }
            return;
        }
        if (pkt.func !== "UPDATE" || !here) return;
        const oper = String(pkt.oper || "").toUpperCase();
        if (oper === "WRITE") {
            const m = pkt.data;
            if (!m || typeof m !== "object" || isSelfEcho(m)) return;
            addMessage(m, true);
        } else if (oper === "SUBSCRIBE" || oper === "UNSUBSCRIBE") {
            const who = pkt.data && pkt.data.nick;
            if (who && who !== state.nick) {
                const sys = pkt.data.system ? " (" + pkt.data.system + ")" : "";
                notice(who + sys + (oper === "SUBSCRIBE" ? " joined" : " left"));
            }
            refreshWho(400);
        }
    }

    // --- messages ----------------------------------------------------------
    function plain(s) { return String(s || "").replace(/\x01./g, "").replace(/[\x00-\x08\x0b-\x1f\x7f]/g, ""); }
    function isTuner(s) { return /^\[TVTUNER\|.*\]$/.test(s); }

    function isSelfEcho(m) {
        if (!m.nick || m.nick.name !== state.nick) return false;
        const now = Date.now();
        state.pendingSelf = state.pendingSelf.filter((p) => now - p.at < 30000);
        const i = state.pendingSelf.findIndex((p) => p.str === m.str);
        if (i < 0) return false;
        state.pendingSelf.splice(i, 1);
        return true;
    }

    function setHistory(list) {
        // Notices since joining (an error, someone joining) stay after it.
        const kept = Array.from(els.transcript.querySelectorAll(".notice"));
        state.messages = [];
        state.lastGroup = null;
        els.transcript.textContent = "";
        list.forEach((m) => { if (m && typeof m === "object") addMessage(m, false); });
        if (!list.length) notice("No messages yet in #" + state.room + ". Say hello.");
        kept.forEach((n) => els.transcript.appendChild(n));
        state.lastGroup = null;
        scrollDown(true);
    }

    function addMessage(m, live) {
        const str = String(m.str || "");
        if (isTuner(str)) return;
        const nick = m.nick && typeof m.nick === "object" ? m.nick : {name: String(m.nick || "")};
        const name = plain(nick.name).trim();
        if (!name) { notice(plain(str)); return; }
        const pinned = nearBottom();
        state.messages.push(m);
        const time = Number(m.time) || Date.now();
        const self = name === state.nick && (!nick.host || nick.host === cfg.systemName);
        let g = state.lastGroup;
        if (!g || g.name !== name || g.host !== (nick.host || "") || time - g.time > GROUP_MS) {
            g = newGroup(nick, name, time, self);
            state.lastGroup = g;
        }
        g.time = time;
        g.bubbles.appendChild(bubble(str, self));
        while (els.transcript.childElementCount > KEEP) els.transcript.firstElementChild.remove();
        if (live || pinned) scrollDown(pinned);
    }

    function newGroup(nick, name, time, self) {
        const row = document.createElement("div");
        row.className = "group" + (self ? " self" : "");
        const av = document.createElement("div");
        av.className = "avatar";
        const blocks = AvatarCodec.avatarToBlocks(nick.avatar);
        if (blocks) {
            AvatarCodec.render(blocks).then((c) => av.appendChild(c)).catch(() => {});
        } else {
            const i = document.createElement("div");
            i.className = "initial";
            i.textContent = name.charAt(0).toUpperCase();
            av.appendChild(i);
        }
        const col = document.createElement("div");
        col.className = "bubbles";
        const who = document.createElement("div");
        who.className = "who";
        who.appendChild(styledName(name, nick.style));
        if (nick.host) {
            const h = document.createElement("span");
            h.className = "host";
            h.textContent = " @ " + plain(nick.host);
            who.appendChild(h);
        }
        const t = document.createElement("span");
        t.className = "time";
        t.textContent = new Date(time).toLocaleTimeString([], {hour: "2-digit", minute: "2-digit"});
        who.appendChild(t);
        col.appendChild(who);
        row.appendChild(av);
        row.appendChild(col);
        els.transcript.appendChild(row);
        return {name, host: nick.host || "", time, bubbles: col};
    }

    const HEX = /^#[0-9a-fA-F]{6}$/;
    function styledName(name, style) {
        const span = document.createElement("span");
        span.className = "name";
        const colors = style && Array.isArray(style.colors) ? style.colors : [];
        if (!colors.some((c) => HEX.test(c))) {
            span.style.color = "var(--white)";
            span.textContent = name;
        } else {
            Array.from(name).forEach((ch, i) => {
                const s = document.createElement("span");
                s.textContent = ch;
                s.style.color = HEX.test(colors[i] || "") ? colors[i] : "var(--white)";
                span.appendChild(s);
            });
        }
        const tag = style && style.tag;
        if (tag && tag.text) {
            const tg = document.createElement("span");
            tg.className = "tag";
            tg.textContent = tag.text;
            tg.style.color = HEX.test(tag.fg) ? tag.fg : "var(--black)";
            tg.style.background = HEX.test(tag.bg) ? tag.bg : "var(--lightgray)";
            span.appendChild(tg);
        }
        return span;
    }

    // The URL rules are the door's (internal/inlineimg/urls.go): links up
    // to whitespace, quotes, angle brackets or a pipe, trailing punctuation
    // dropped; pictures by the path's extension, at most three a message.
    const URL_RE = /https?:\/\/[^\s<>"'\x01|]+/gi;
    const TRAILING = /[).,;:!?]+$/;
    const IMAGE_EXT = /\.(png|jpe?g|gif|webp|bmp)(?:[?#]|$)/i;
    const NO_CARD = /\.(png|jpe?g|gif|webp|bmp|mp4|webm|ogv|m4v|mov|mp3|ogg|oga|m4a|flac|wav|ans|asc)(?:[?#]|$)/i;
    const MEDIA_PATH = /\/chatmedia\/\d{4}-\d{2}-\d{2}\/[a-f0-9]{24}\.[a-z0-9]{1,4}(?:[?#]|$)/i;

    function findURLs(text) {
        const out = [];
        (text.match(URL_RE) || []).forEach((m) => {
            const u = m.replace(TRAILING, "");
            try { if (!new URL(u).host) return; } catch (e) { return; }
            if (u && out.indexOf(u) < 0) out.push(u);
        });
        return out;
    }
    function pathOf(u) { try { return new URL(u).pathname; } catch (e) { return u; } }
    function sameOrigin(u) { try { return new URL(u).origin === location.origin; } catch (e) { return false; } }
    function imgSrc(u) { return sameOrigin(u) ? u : "img?url=" + encodeURIComponent(u); }

    function mentionsMe(text) {
        if (!state.nick) return false;
        const esc = state.nick.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
        return new RegExp("(^|[^A-Za-z0-9_])@?" + esc + "(?![A-Za-z0-9_])", "i").test(text);
    }

    function bubble(str, self) {
        const b = document.createElement("div");
        b.className = "bubble";
        if (AvatarCodec.isBitmap(str)) {
            b.classList.add("bitmap");
            b.textContent = "…";
            AvatarCodec.decodeBitmap(str).then((art) => {
                if (!art) { b.textContent = "(unreadable ANSI art)"; return; }
                return AvatarCodec.render(art).then((c) => {
                    const pinned = nearBottom();
                    b.textContent = "";
                    b.appendChild(c);
                    if (art.from) {
                        const f = document.createElement("div");
                        f.className = "from";
                        f.textContent = "art from " + art.from;
                        b.appendChild(f);
                    }
                    if (pinned) scrollDown(true);
                });
            }).catch(() => { b.textContent = "(unreadable ANSI art)"; });
            return b;
        }
        const text = plain(str);
        if (!self && mentionsMe(text)) b.classList.add("mention");
        const urls = findURLs(text);
        let rest = text;
        urls.forEach((u) => {
            const at = rest.indexOf(u);
            if (at < 0) return;
            b.appendChild(document.createTextNode(rest.slice(0, at)));
            const a = document.createElement("a");
            a.href = u;
            a.target = "_blank";
            a.rel = "noopener noreferrer";
            a.textContent = u;
            b.appendChild(a);
            rest = rest.slice(at + u.length);
        });
        b.appendChild(document.createTextNode(rest));

        const pics = urls.filter((u) => IMAGE_EXT.test(pathOf(u))).slice(0, 3);
        if (pics.length) {
            const box = document.createElement("div");
            box.className = "pics";
            pics.forEach((u) => {
                const img = document.createElement("img");
                img.loading = "lazy";
                img.alt = "picture";
                img.referrerPolicy = "no-referrer";
                img.onload = () => { if (nearBottom(img.height + 40)) scrollDown(true); };
                img.onerror = () => img.remove();
                img.src = imgSrc(u);
                box.appendChild(img);
            });
            b.appendChild(box);
        }
        const cardURL = urls.find((u) => !NO_CARD.test(pathOf(u)) && !MEDIA_PATH.test(u) && u.length <= 2048);
        if (cardURL) card(cardURL, b);
        return b;
    }

    const cards = new Map();
    function card(u, into) {
        if (!cards.has(u)) {
            cards.set(u, fetch("preview?url=" + encodeURIComponent(u), {credentials: "same-origin"})
                .then((r) => (r.ok ? r.json() : null)).catch(() => null));
        }
        cards.get(u).then((m) => {
            if (!m || (!m.title && !m.description)) return;
            const pinned = nearBottom();
            const a = document.createElement("a");
            a.className = "card";
            a.href = m.finalUrl || u;
            a.target = "_blank";
            a.rel = "noopener noreferrer";
            if (m.image) {
                const img = document.createElement("img");
                img.alt = "";
                img.referrerPolicy = "no-referrer";
                img.onerror = () => img.remove();
                img.src = imgSrc(m.image);
                a.appendChild(img);
            }
            const body = document.createElement("div");
            const add = (cls, text) => {
                if (!text) return;
                const d = document.createElement("div");
                d.className = cls;
                d.textContent = text;
                body.appendChild(d);
            };
            add("site", m.siteName || (() => { try { return new URL(a.href).host; } catch (e) { return ""; } })());
            add("title", m.title);
            add("desc", m.description);
            a.appendChild(body);
            into.appendChild(a);
            if (pinned) scrollDown(true);
        });
    }

    function notice(text, error) {
        const pinned = nearBottom();
        const d = document.createElement("div");
        d.className = "notice" + (error ? " error" : "");
        d.textContent = text;
        els.transcript.appendChild(d);
        state.lastGroup = null;
        if (pinned) scrollDown(true);
    }

    function nearBottom(slack) {
        const t = els.transcript;
        return t.scrollHeight - t.scrollTop - t.clientHeight < (slack || 80);
    }
    function scrollDown(force) {
        if (force) requestAnimationFrame(() => { els.transcript.scrollTop = els.transcript.scrollHeight; });
    }

    // --- roster and rooms ----------------------------------------------------
    function setWho(list) {
        const seen = new Set();
        const people = [];
        list.forEach((e) => {
            const nick = typeof e === "string" ? e : e && e.nick;
            const sys = typeof e === "object" && e ? e.system || "" : "";
            const key = String(nick || "").toLowerCase() + "|" + sys.toLowerCase();
            if (!nick || seen.has(key)) return;
            seen.add(key);
            people.push({nick: plain(nick), sys: plain(sys)});
        });
        people.sort((a, b) => a.nick.localeCompare(b.nick));
        els.rosterList.textContent = "";
        people.forEach((p) => {
            const li = document.createElement("li");
            li.textContent = p.nick;
            if (p.sys) {
                const s = document.createElement("span");
                s.className = "sys";
                s.textContent = " " + p.sys;
                li.appendChild(s);
            }
            els.rosterList.appendChild(li);
        });
        els.whoCount.textContent = String(people.length);
    }

    function openRooms() {
        els.roomsList.innerHTML = '<li class="hint">loading…</li>';
        els.createRoom.hidden = !cfg.createRooms;
        if (!els.roomsDialog.open) els.roomsDialog.showModal();
        query("KEYS", "public_channels", {lock: 1});
    }

    function showRooms(data) {
        let names = Array.isArray(data) ? data.slice() : data && typeof data === "object" ? Object.keys(data) : [];
        names = names.map(String).filter((n) => /^[A-Za-z0-9_-]{1,32}$/.test(n));
        const lower = names.map((n) => n.toLowerCase());
        if (lower.indexOf(cfg.defaultChannel.toLowerCase()) < 0) names.push(cfg.defaultChannel);
        names.sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()));
        els.roomsList.textContent = "";
        names.forEach((n) => {
            const li = document.createElement("li");
            const btn = document.createElement("button");
            btn.type = "button";
            btn.textContent = "#" + n;
            if (n.toLowerCase() === state.room.toLowerCase()) btn.className = "current";
            btn.onclick = () => { els.roomsDialog.close(); if (n !== state.room) join(n); };
            li.appendChild(btn);
            els.roomsList.appendChild(li);
        });
    }

    function createRoom() {
        const n = els.createInput.value.trim().replace(/^#/, "");
        if (!/^[A-Za-z0-9_-]{1,32}$/.test(n)) {
            notice("room names are letters, digits, - and _ (up to 32)", true);
            return;
        }
        query("WRITE", "public_channels." + n, {data: true, lock: 2});
        els.createInput.value = "";
        els.roomsDialog.close();
        join(n);
    }

    // --- nickname ------------------------------------------------------------
    function askNick(why) {
        els.nickInput.value = state.nick;
        els.nickError.hidden = !why;
        els.nickError.textContent = why || "";
        if (!els.nickDialog.open) els.nickDialog.showModal();
        els.nickInput.focus();
    }

    function setNick(n) {
        n = n.replace(/\s+/g, " ").trim();
        if (!/^[\p{L}\p{N} _-]{1,25}$/u.test(n)) {
            askNick("Letters, digits, spaces, - and _ only, up to 25.");
            return false;
        }
        const changed = n !== state.nick;
        state.nick = n;
        save("nick", n);
        els.nickName.textContent = n;
        if (changed && state.ws) reconnect(); // the server fixes a nick per connection
        else if (!state.ws) connect();
        return true;
    }

    // --- composer ------------------------------------------------------------
    const HELP = [
        "/join <room>  switch rooms     /rooms  list rooms",
        "/who          who's here        /nick <name>  change nickname",
    ];

    function submit(text) {
        text = text.replace(/[\r\n]+/g, " ").trim();
        if (!text) return;
        if (text[0] === "/") {
            const [cmd, ...args] = text.slice(1).split(/\s+/);
            switch (cmd.toLowerCase()) {
            case "join": case "j": if (args[0]) join(args[0]); else openRooms(); return;
            case "rooms": case "channels": case "list": openRooms(); return;
            case "who": toggleRoster(true); refreshWho(0); return;
            case "nick": if (args.length) setNick(args.join(" ")); else askNick(); return;
            case "help": case "?": HELP.forEach((l) => notice(l)); return;
            }
            if (!text.startsWith("//")) { notice("unknown command /" + cmd + " (/help)", true); return; }
            text = text.slice(1);
        }
        if (Array.from(text).length > cfg.maxMessage) {
            notice("message is longer than " + cfg.maxMessage + " characters", true);
            return;
        }
        if (!state.open) { notice("not connected yet", true); return; }
        const msg = {nick: {name: state.nick, host: cfg.systemName}, str: text, time: Date.now()};
        const lp = {data: msg, lock: 2};
        query("WRITE", loc.messages(state.room), lp);
        query("PUSH", loc.history(state.room), lp);
        state.pendingSelf.push({str: text, at: Date.now()});
        addMessage(msg, true);
        scrollDown(true);
        els.input.value = "";
        updateCounter();
    }

    function updateCounter() {
        const n = Array.from(els.input.value).length;
        els.counter.textContent = n > cfg.maxMessage * 0.8 ? n + "/" + cfg.maxMessage : "";
        els.input.style.height = "auto";
        els.input.style.height = Math.min(els.input.scrollHeight, 128) + "px";
    }

    function toggleRoster(open) {
        els.roster.classList.toggle("open", open === undefined ? !els.roster.classList.contains("open") : open);
    }

    function setStatus(on, text) {
        els.status.className = "status " + (on ? "on" : "off");
        els.status.textContent = text;
    }

    // --- uploads -------------------------------------------------------------
    async function upload(file) {
        if (!cfg.uploads || !file) return;
        notice("uploading " + file.name + "…");
        try {
            const r = await fetch("upload?name=" + encodeURIComponent(file.name), {
                method: "POST", body: file, credentials: "same-origin",
                headers: {"Content-Type": "application/octet-stream"},
            });
            const reply = await r.json().catch(() => ({}));
            if (!r.ok || !reply.url) throw new Error(reply.error || "upload failed (HTTP " + r.status + ")");
            const v = els.input.value;
            els.input.value = (v && !/\s$/.test(v) ? v + " " : v) + reply.url;
            updateCounter();
            els.input.focus();
            notice("uploaded: press Enter to send the link (kept " + (reply.expiresDays || "?") + " days)");
        } catch (e) {
            notice(String(e.message || e), true);
        }
    }

    function setupUploads() {
        if (!cfg.uploads) return;
        els.dropHint.hidden = false;
        let depth = 0;
        const hasFiles = (ev) => ev.dataTransfer && Array.from(ev.dataTransfer.types || []).indexOf("Files") >= 0;
        window.addEventListener("dragenter", (ev) => { if (hasFiles(ev)) { depth++; els.dropVeil.hidden = false; } });
        window.addEventListener("dragleave", () => { if (--depth <= 0) { depth = 0; els.dropVeil.hidden = true; } });
        window.addEventListener("dragover", (ev) => { if (hasFiles(ev)) ev.preventDefault(); });
        window.addEventListener("drop", (ev) => {
            if (!hasFiles(ev)) return;
            ev.preventDefault();
            depth = 0;
            els.dropVeil.hidden = true;
            Array.from(ev.dataTransfer.files).slice(0, 3).forEach(upload);
        });
        els.input.addEventListener("paste", (ev) => {
            const files = Array.from((ev.clipboardData && ev.clipboardData.files) || []);
            if (files.length) { ev.preventDefault(); files.slice(0, 3).forEach(upload); }
        });
    }

    // --- start -----------------------------------------------------------------
    els.form.addEventListener("submit", (ev) => { ev.preventDefault(); submit(els.input.value); });
    els.input.addEventListener("keydown", (ev) => {
        if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) { ev.preventDefault(); submit(els.input.value); }
    });
    els.input.addEventListener("input", updateCounter);
    els.roomBtn.onclick = openRooms;
    els.whoBtn.onclick = () => { toggleRoster(); refreshWho(0); };
    els.nickBtn.onclick = () => askNick();
    els.createBtn.onclick = createRoom;
    els.createInput.addEventListener("keydown", (ev) => { if (ev.key === "Enter") { ev.preventDefault(); createRoom(); } });
    els.nickForm.addEventListener("submit", (ev) => {
        ev.preventDefault();
        if (setNick(els.nickInput.value)) els.nickDialog.close();
    });
    els.nickDialog.addEventListener("cancel", (ev) => { if (!state.nick) ev.preventDefault(); });

    fetch("config.json", {credentials: "same-origin"}).then((r) => r.json()).catch(() => ({})).then((c) => {
        Object.assign(cfg, c || {});
        els.input.maxLength = cfg.maxMessage;
        const q = new URLSearchParams(location.search).get("room");
        state.room = (q && /^[A-Za-z0-9_-]{1,32}$/.test(q) ? q : load("room")) || cfg.defaultChannel;
        els.roomName.textContent = state.room;
        setupUploads();
        const nick = load("nick");
        if (nick) setNick(nick);
        else askNick();
    });

    // For the browser tests: decode with the page's own codec.
    window.AvatarChatPage = {state, cfg, submit, join};
})();
