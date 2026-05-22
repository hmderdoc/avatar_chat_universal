const events = require("events");
let visible = false;
const statuses = {ACTIVE: 0, IDLE: 1, AWAY: 2, WEB: 3};
const electron = require("electron");
const {send} = require("../../senders");
const avatar_codec = require("../../avatar_codec");
const linkify_string = require("linkify-string");
require("linkify-plugin-ticket");
let last_height = 240;
let last_date;

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
    render_blocks_canvas(blocks) {
        try {
            const doc = require("../doc");
            const libtextmode = require("../../libtextmode/libtextmode");
            const font = doc.font;
            if (!font || !blocks || !blocks.data) return null;
            const data = blocks.data.map((c) => ({code: (c.code || 0) & 0xff, fg: (c.fg || 0) & 0x0f, bg: (c.bg || 0) & 0x0f}));
            return libtextmode.render_blocks({columns: blocks.columns, rows: blocks.rows, data}, font, doc.c64_background);
        } catch (e) {
            return null;
        }
    }

    // Small portrait canvas for a base64 avatar, or null if absent/invalid.
    avatar_element(base64) {
        const canvas = this.render_blocks_canvas(avatar_codec.avatar_to_blocks(base64));
        if (!canvas) return null;
        canvas.classList.add("avatar");
        canvas.style.height = "30px";
        canvas.style.width = "auto";
        canvas.style.imageRendering = "pixelated";
        canvas.style.verticalAlign = "middle";
        canvas.style.marginRight = "6px";
        return canvas;
    }

    // Inline artwork element for a [BITMAP|...] message. Falls back to a text
    // placeholder when the font isn't ready (history replay).
    art_element(text) {
        const blocks = avatar_codec.decode_bitmap(text);
        if (!blocks) return this.create_div({classname: "text", text});
        const canvas = this.render_blocks_canvas(blocks);
        if (!canvas) return this.create_div({classname: "text", text: `[art ${blocks.columns}x${blocks.rows}${blocks.from ? " by " + blocks.from : ""}]`});
        canvas.classList.add("art");
        canvas.style.maxWidth = "100%";
        canvas.style.height = "auto";
        canvas.style.imageRendering = "pixelated";
        canvas.style.display = "block";
        canvas.style.marginTop = "3px";
        canvas.title = blocks.from ? `art by ${blocks.from} (${blocks.columns}x${blocks.rows})` : `art (${blocks.columns}x${blocks.rows})`;
        return canvas;
    }

    // Stable colour per nick so the eye can track speakers.
    nick_color(nick) {
        let h = 0;
        for (let i = 0; i < nick.length; i++) h = (h * 31 + nick.charCodeAt(i)) & 0xffff;
        return `hsl(${h % 360}, 70%, 66%)`;
    }

    chat(id, nick, group, text, time, avatar) {
        if (this.users[id] && (this.users[id] != nick || this.users[id].group != group)) {
            this.users[id].nick = nick;
            this.users[id].group = group;
            this.users[id].element.innerText = group ? `${nick} <${group}>` : nick;
        }
        const nick_div = this.create_div({classname: "nick", text: group ? `${nick} <${group}>` : nick});
        if (nick) nick_div.style.color = this.nick_color(nick);
        const container = this.create_div();
        if (time) {
            this.update_date(time);
            const date = new Date(time);
            const time_text = `${date.getHours().toString().padStart(2, "0")}:${date.getMinutes().toString().padStart(2, "0")}`;
            container.appendChild(this.create_div({classname: "time", text: time_text}));
        }
        const avatar_el = avatar ? this.avatar_element(avatar) : null;
        if (avatar_el) container.appendChild(avatar_el);
        container.appendChild(nick_div);
        if (avatar_codec.is_bitmap(text)) {
            container.appendChild(this.art_element(text));
        } else {
            container.appendChild(this.create_div({classname: "text", text, linkify: true}));
        }
        this.append(container, false);
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
        document.addEventListener("DOMContentLoaded", (event) => {
            $("chat_input").addEventListener("focus", chat_input_focus, true);
            $("chat_input").addEventListener("blur", chat_input_blur, true);
            $("chat_resizer").addEventListener("mousedown", (event) => this.mouse_down(event), true);
            document.body.addEventListener("mousemove", (event) => this.mouse_move(event), true);
            document.body.addEventListener("mouseup", () => this.mouse_up(), true);
        }, true);
    }
}

module.exports = new ChatUI();
