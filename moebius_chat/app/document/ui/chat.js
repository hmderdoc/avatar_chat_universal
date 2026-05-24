const events = require("events");
let visible = false;
const statuses = {ACTIVE: 0, IDLE: 1, AWAY: 2, WEB: 3};
const electron = require("electron");
const {send} = require("../../senders");
const avatar_codec = require("../../avatar_codec");
const libtextmode = require("../../libtextmode/libtextmode");
const {Font} = require("../../libtextmode/font");
const linkify_string = require("linkify-string");
require("linkify-plugin-ticket");
let last_height = 240;
let last_date;
// Bump on each change so the running build is identifiable in the chat panel.
const BUILD = "r5";

function $(name) {
    return document.getElementById(name);
}

function set_var(name, value) {
    document.documentElement.style.setProperty(`--${name}`, `${value}px`);
}

function is_at_bottom() {
    const messages = $("messages");
    const rect = messages.getBoundingClientRect();
    return (rect.height > messages.scrollHeight) || (messages.scrollTop == messages.scrollHeight - rect.height + 1);
}

function scroll_to_bottom() {
    const messages = $("messages");
    const rect = messages.getBoundingClientRect();
    messages.scrollTop = messages.scrollHeight - rect.height + 1;
}

function show(focus = true) {
    const chat_input = $("chat_input");
    set_var("chat-height", last_height);
    chat_input.value = "";
    scroll_to_bottom();
    if (focus) chat_input.focus();
}

function hide() {
    last_height = $("chat").getBoundingClientRect().height;
    $("chat_input").blur();
    set_var("chat-height", 0);
}

function toggle(focus) {
    visible = !visible;
    if (visible) {
        show(focus);
    } else {
        hide();
    }
}

function chat_input_focus(event) {
    send("chat_input_focus");
}

function chat_input_blur(event) {
    send("chat_input_blur");
}

electron.ipcRenderer.on("chat_window_toggle", (event) => toggle());

function validate(value) {
    return /^((https?):\/\/|www.|[^@]+@[^\.]+\..+$|#\d+$)/.test(value);
}

class ChatUI extends events.EventEmitter {
    add_click_event(link) {
        return (event) => {
            event.preventDefault();
            const match = link.href.match(/^row:\/\/#(\d+)/);
            if (match) {
                this.emit("goto_row", match[1]);
            } else {
                electron.shell.openExternal(link.href);
            }
        };
    }

    linkify(element, text) {
        element.innerHTML = linkify_string(text, {className: "", formatHref: {ticket: (line_no) => `row://${line_no}`}, validate});
        const links = element.getElementsByTagName("a");
        for (const link of links) {
            link.setAttribute("tabIndex", -1);
            link.addEventListener("click", this.add_click_event(link), true);
        }
    }

    append(child, container = true) {
        const scroll = is_at_bottom();
        if (container) child = this.create_div({child});
        $("messages").appendChild(child);
        // if (scroll) scroll_to_bottom();
        scroll_to_bottom();
    }

    create_div({classname, text, parent, child, linkify = false} = {}) {
        const element = document.createElement("div");
        if (classname) element.classList.add(classname);
        if (text && linkify) {
            this.linkify(element, text);
        } else  if (text) {
            element.innerText = text;
        }
        if (parent) parent.appendChild(element);
        if (child) element.appendChild(child);
        return element;
    }

    msg_div(text) {
        const msg_div = document.createElement("div");
        this.linkify(msg_div, text);
        this.append(msg_div);
    }


    update_date(time = Date.now()) {
        const date = new Date(time);
        const date_text = date.toDateString();
        if (date_text != last_date) {
            last_date = date_text;
            this.append(this.create_div({classname: "date", text: date_text}), false);
        }
    }

    action(id, text) {
        this.update_date();
        this.append(this.create_div({classname: "nick", text: `${this.users[id].nick} has ${text}`}));
    }

    sauce(id) {
        this.action(id, "changed the sauce record");
    }

    ice_colors(id, value) {
        this.action(id, `has turned iCE colors ${value ? "on" : "off"}`);
    }

    use_9px_font(id, value) {
        this.action(id, `has turned letter spacing ${value ? "on" : "off"}`);
    }

    change_font(id, font_name) {
        this.action(id, `has changed the font to ${font_name}`);
    }

    set_canvas_size(id, columns, rows) {
        this.action(id, `has changed the size of the canvas to ${columns} × ${rows}`);
    }

    status(id, status) {
        switch (status) {
            case statuses.ACTIVE: this.users[id].element.style.backgroundImage = "url(\"../img/active_indicator.png\")"; break;
            case statuses.IDLE: this.users[id].element.style.backgroundImage = "url(\"../img/idle_indicator.png\")"; break;
            case statuses.AWAY: this.users[id].element.style.backgroundImage = "url(\"../img/away_indicator.png\")"; break;
            case statuses.WEB: this.users[id].element.style.backgroundImage = "url(\"../img/web_indicator.png\")"; break;
        }
    }

    join(id, nick, group, status, show_join = true) {
        if (nick) {
            this.users[id] = {nick, group, status, element: this.create_div({text: group ? `${nick} <${group}>` : nick, parent: $("user_list")})};
            this.users[id].element.addEventListener("click", (event) => this.emit("goto_user", id), false);
        } else {
            this.users[id] = {nick: "Guest", undefined, status, element: this.create_div({text: "Guest", classname: "guest", parent: $("user_list")})};
        }
        if (show_join) this.action(id, "joined");
        this.status(id, status);
    }

    remove_user(id) {
        $("user_list").removeChild(this.users[id].element);
        delete this.users[id];
    }

    leave(id, show_leave = true) {
        if (show_leave) this.action(id, "left");
        this.remove_user(id);
    }

    welcome(comments, chat_history) {
        for (const id of Object.keys(this.users)) this.remove_user(id);
        for (const chat of chat_history) this.chat(chat.id, chat.nick, chat.group, chat.text, chat.time, chat.avatar);
        const text = comments.split("\n")[0];
        if (text.length) this.append(this.create_div({classname: "welcome", text, linkify: true}), false);
    }

    show() {
        if (!visible) toggle(false);
        send("enable_chat_window_toggle");
    }

    // Render a libtextmode blocks object ({columns, rows, data:[{code,fg,bg}]})
    // to a fresh canvas using the document's loaded font. Returns null when no
    // font is available yet (e.g. during the initial history replay, before
    // the canvas has rendered). Palette indices are clamped to the 16-colour
    // font so 256-colour BITMAPs degrade instead of throwing.
    // Load chat's own CP437 font once, then re-render anything that was waiting.
    // Decoupled from the editor's render pipeline so backlog (replayed on
    // connect, before the canvas renders) and live messages both draw avatars
    // and art instead of falling back to placeholders.
    load_chat_font() {
        this.font = new Font();
        this.font.load({name: "IBM VGA", use_9px_font: false}).then(() => {
            this.font_loaded = true;
            this.flush_pending();
            this.update_status();
        }).catch(() => { this.font = null; this.update_status(); });
    }

    // A small self-identifying status line at the top of the user list, so the
    // running build and the avatar-chat state are visible at a glance.
    init_status() {
        const ul = $("user_list");
        if (!ul) return;
        this.status_el = this.create_div({classname: "ac-status"});
        ul.insertBefore(this.status_el, ul.firstChild);
        this.update_status();
    }

    update_status() {
        if (!this.status_el) return;
        let font = "font:loading";
        if (this.font_loaded) font = "font:ok";
        else if (!this.font) font = "font:FAILED";
        const online = (this.roster_count == null) ? "" : ` · ${this.roster_count} on chat`;
        this.status_el.innerText = `AvatarChat ${BUILD} · ${font}${online}`;
    }

    // Render a libtextmode "blocks" object to a canvas with chat's font, or null
    // if the font hasn't loaded yet. Palette indices are clamped to the 16-colour
    // font so 256-colour BITMAPs degrade instead of throwing.
    cp437_canvas(blocks) {
        if (!this.font_loaded || !this.font || !blocks || !blocks.data) return null;
        try {
            const data = blocks.data.map((c) => ({code: (c.code || 0) & 0xff, fg: (c.fg || 0) & 0x0f, bg: (c.bg || 0) & 0x0f}));
            return libtextmode.render_blocks({columns: blocks.columns, rows: blocks.rows, data}, this.font, undefined);
        } catch (e) {
            return null;
        }
    }

    style_canvas(canvas, kind, height_px) {
        if (kind === "avatar") {
            canvas.classList.add("avatar");
            canvas.style.height = `${height_px}px`;
            canvas.style.width = "auto";
        } else {
            canvas.classList.add("art");
        }
        return canvas;
    }

    // Render now if the font is ready; otherwise return `placeholder` and queue
    // it to be swapped for the real canvas once the font loads.
    deferred_canvas(blocks, kind, height_px, placeholder) {
        const canvas = this.cp437_canvas(blocks);
        if (canvas) return this.style_canvas(canvas, kind, height_px);
        this.pending.push({holder: placeholder, blocks, kind, height_px});
        return placeholder;
    }

    flush_pending() {
        const items = this.pending.splice(0);
        for (const it of items) {
            const canvas = this.cp437_canvas(it.blocks);
            if (!canvas || !it.holder.parentNode) continue;
            this.style_canvas(canvas, it.kind, it.height_px);
            it.holder.parentNode.replaceChild(canvas, it.holder);
        }
        scroll_to_bottom();
    }

    // Coloured square with the nick's initial; the no-avatar fallback, and the
    // placeholder a real avatar swaps into once the font loads.
    initial_square(nick, size_px) {
        const el = this.create_div({classname: "avatar-initial"});
        el.style.width = el.style.height = el.style.lineHeight = `${size_px}px`;
        el.style.fontSize = `${Math.floor(size_px * 0.5)}px`;
        el.style.background = nick ? this.nick_color(nick) : "#555555";
        const trimmed = (nick || "").trim();
        el.innerText = trimmed ? trimmed[0].toUpperCase() : "?";
        return el;
    }

    // Left gutter: the sender's avatar (or an initial square it swaps into).
    gutter_element(nick, avatar, size_px) {
        const gutter = this.create_div({classname: "msg-gutter"});
        const square = this.initial_square(nick, size_px);
        const blocks = avatar ? avatar_codec.avatar_to_blocks(avatar) : null;
        gutter.appendChild(blocks ? this.deferred_canvas(blocks, "avatar", size_px, square) : square);
        return gutter;
    }

    // Inline artwork for a [BITMAP|...] message.
    art_element(text) {
        const blocks = avatar_codec.decode_bitmap(text);
        if (!blocks) return this.create_div({classname: "msg-text", text});
        const label = `[art ${blocks.columns}x${blocks.rows}${blocks.from ? " by " + blocks.from : ""}]`;
        const el = this.deferred_canvas(blocks, "art", 0, this.create_div({classname: "msg-text", text: label}));
        if (el.tagName === "CANVAS") el.title = blocks.from ? `art by ${blocks.from} (${blocks.columns}x${blocks.rows})` : `art (${blocks.columns}x${blocks.rows})`;
        return el;
    }

    // Classify a URL so we can embed images/audio inline. Matches the Go
    // bridges (internal/bridgemedia/media.go): the extension can appear anywhere,
    // including a Synchronet-style ?file=name.mp3 query param, not just the path.
    media_kind(url) {
        const u = url.toLowerCase();
        for (const ext of [".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".avif"]) if (u.indexOf(ext) >= 0) return "image";
        for (const ext of [".mp3", ".ogg", ".oga", ".wav", ".m4a", ".aac", ".flac", ".opus"]) if (u.indexOf(ext) >= 0) return "audio";
        return "link";
    }

    // Message body: BITMAP art, or linkified text plus any image/audio embeds.
    body_element(text) {
        const body = this.create_div({classname: "msg-body"});
        if (avatar_codec.is_bitmap(text)) {
            body.appendChild(this.art_element(text));
            return body;
        }
        body.appendChild(this.create_div({classname: "msg-text", text, linkify: true}));
        const urls = text.match(/(https?:\/\/[^\s<>"']+)/gi) || [];
        const seen = {};
        let embeds = 0;
        for (const url of urls) {
            if (seen[url] || embeds >= 4) continue;
            seen[url] = true;
            const kind = this.media_kind(url);
            if (kind === "image") {
                const img = document.createElement("img");
                img.className = "embed-img";
                img.loading = "lazy";
                img.src = url;
                img.addEventListener("load", () => scroll_to_bottom());
                img.addEventListener("error", () => img.remove());
                img.addEventListener("click", () => electron.shell.openExternal(url));
                body.appendChild(img);
                embeds++;
            } else if (kind === "audio") {
                const audio = document.createElement("audio");
                audio.className = "embed-audio";
                audio.controls = true;
                audio.preload = "none";
                audio.src = url;
                audio.addEventListener("error", () => audio.remove());
                body.appendChild(audio);
                embeds++;
            }
        }
        return body;
    }

    // Stable colour per nick so the eye can track speakers.
    nick_color(nick) {
        let h = 0;
        for (let i = 0; i < (nick || "").length; i++) h = (h * 31 + nick.charCodeAt(i)) & 0xffff;
        return `hsl(${h % 360}, 70%, 66%)`;
    }

    chat(id, nick, group, text, time, avatar) {
        if (this.users[id] && (this.users[id].nick != nick || this.users[id].group != group)) {
            this.users[id].nick = nick;
            this.users[id].group = group;
            this.users[id].element.innerText = group ? `${nick} <${group}>` : nick;
        }
        if (nick && avatar) this.remote_avatars[nick] = avatar; // enrich the WHO roster
        if (time) this.update_date(time);

        const container = this.create_div({classname: "message"});
        container.appendChild(this.gutter_element(nick, avatar, 40));

        const main = this.create_div({classname: "msg-main"});
        const header = this.create_div({classname: "msg-header"});
        const nick_div = this.create_div({classname: "nick", text: nick || "?"});
        if (nick) nick_div.style.color = this.nick_color(nick);
        header.appendChild(nick_div);
        if (group) header.appendChild(this.create_div({classname: "system", text: group}));
        if (time) {
            const date = new Date(time);
            const time_text = `${date.getHours().toString().padStart(2, "0")}:${date.getMinutes().toString().padStart(2, "0")}`;
            header.appendChild(this.create_div({classname: "time", text: time_text}));
        }
        main.appendChild(header);
        main.appendChild(this.body_element(text));
        container.appendChild(main);
        this.append(container, false);
    }

    // Render the Avatar Chat channel roster (from WHO) into the user list, with
    // avatars where we've seen them. Kept in its own block below the Moebius
    // collaborators so the two rosters stay visually distinct.
    set_avatar_roster(users) {
        if (!Array.isArray(users)) users = [];
        this.roster_count = users.length;
        this.update_status();
        if (!this.roster_el) {
            this.roster_el = this.create_div({classname: "avatar-roster"});
            $("user_list").appendChild(this.roster_el);
        }
        const container = this.roster_el;
        while (container.firstChild) container.removeChild(container.lastChild);
        if (!users.length) return;
        container.appendChild(this.create_div({classname: "roster-header", text: `Avatar Chat (${users.length})`}));
        for (const u of users) {
            const row = this.create_div({classname: "roster-user"});
            const portrait = u.avatar || this.remote_avatars[u.nick];
            const square = this.initial_square(u.nick, 18);
            const blocks = portrait ? avatar_codec.avatar_to_blocks(portrait) : null;
            row.appendChild(blocks ? this.deferred_canvas(blocks, "avatar", 18, square) : square);
            const name = this.create_div({classname: "rname", text: u.nick});
            if (u.nick) name.style.color = this.nick_color(u.nick);
            row.appendChild(name);
            if (u.system) row.appendChild(this.create_div({classname: "rsys", text: u.system}));
            container.appendChild(row);
        }
    }

    mouse_down(event) {
        this.mouse_button = true;
        $("chat_resizer").classList.add("active");
    }

    mouse_move(event) {
        if (this.mouse_button) {
            const scroll = is_at_bottom();
            const new_height = $("chat").getBoundingClientRect().bottom - event.clientY;
            set_var("chat-height", Math.max(new_height, 96));
            // if (scroll) scroll_to_bottom();
            scroll_to_bottom();
            this.emit("update_frame");
        }
    }

    mouse_up() {
        this.mouse_button = false;
        $("chat_resizer").classList.remove("active");
    }

    constructor() {
        super();
        this.mouse_button = false;
        this.users = {};
        this.remote_avatars = {}; // nick -> base64 avatar, seen in messages
        this.roster_el = null;    // Avatar Chat WHO block inside #user_list
        this.font = null;         // chat's own CP437 font for avatars/art
        this.font_loaded = false;
        this.pending = [];        // canvases waiting for the font to load
        this.roster_count = null; // # on the avatar-chat channel (null until known)
        this.status_el = null;
        this.load_chat_font();
        document.addEventListener("DOMContentLoaded", (event) => {
            this.init_status();
            $("chat_input").addEventListener("focus", chat_input_focus, true);
            $("chat_input").addEventListener("blur", chat_input_blur, true);
            $("chat_resizer").addEventListener("mousedown", (event) => this.mouse_down(event), true);
            document.body.addEventListener("mousemove", (event) => this.mouse_move(event), true);
            document.body.addEventListener("mouseup", () => this.mouse_up(), true);
        }, true);
    }
}

module.exports = new ChatUI();
