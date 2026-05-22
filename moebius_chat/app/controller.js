const electron = require("electron");
const {on, send, send_sync, msg_box, save_box} = require("./senders");
const doc = require("./document/doc");
const avatar_codec = require("./avatar_codec");
const {tools} = require("./document/ui/ui");
const {HourlySaver} = require("./hourly_saver");
const {remove_ice_colors} = require("./libtextmode/libtextmode");
let hourly_saver, backup_folder;
require("./document/ui/canvas");
require("./document/tools/select");
require("./document/tools/brush");
require("./document/tools/shifter");
require("./document/tools/line");
require("./document/tools/rectangle_filled");
require("./document/tools/rectangle_outline");
require("./document/tools/ellipse_filled");
require("./document/tools/ellipse_outline");
require("./document/tools/fill");
require("./document/tools/sample");

doc.on("start_rendering", () => send_sync("show_rendering_modal"));
doc.on("end_rendering", () => send("close_modal"));
doc.on("connecting", () => send_sync("show_connecting_modal"));
doc.on("connected", () => send("close_modal"));
doc.on("unable_to_connect", () => {
    const choice = msg_box("Connect to Server", "Cannot connect to Server", {buttons: ["Retry", "Cancel"], defaultId: 0, cancelId: 1});
    if (choice == 1) send("destroy");
    doc.connect_to_server(doc.connection.server, doc.connection.pass);
});
doc.on("refused", () => {
    msg_box("Connect to Server", "Incorrect password!");
    send("destroy");
});
doc.on("disconnected", () => {
    const choice = msg_box("Disconnected", "You were disconnected from the server.", {buttons: ["Retry", "Cancel"], defaultId: 0, cancelId: 1});
    if (choice == 1) send("destroy");
    doc.connect_to_server(doc.connection.server, doc.connection.pass);
});
doc.on("ready", () => {
    send("ready");
    tools.start(tools.modes.SELECT);
});

async function process_save(method = 'save', destroy_when_done = false, ignore_controlcharacters = false) {
    var ctrl = false;
    doc.data.forEach((block, index) => {
        if (block.code == 9 || block.code == 10 || block.code == 13 || block.code == 26) ctrl = true;
    });
    if (ctrl && ignore_controlcharacters == false) {
        send("show_controlcharacters", {method, destroy_when_done});
    } else {
        switch (method) {
            case "save_as":
                save_as(destroy_when_done);
                break;
            case "save_without_sauce":
                save_without_sauce();
                break;
            default:
                save(destroy_when_done);
                break;
        }
    }
}

function save(destroy_when_done = false, save_without_sauce = false) {
    if (doc.file) {
        doc.edited = false;
        doc.save(save_without_sauce);
        if (destroy_when_done) send("destroy");
    } else {
        save_as(destroy_when_done);
    }
}

function save_as(destroy_when_done = false) {
    const file = save_box(doc.file, "ans", {filters: [{name: "ANSI Art", extensions: ["ans", "asc", "diz", "nfo", "txt"]}, {name: "XBin", extensions: ["xb"]}, {name: "Binary Text", extensions: ["bin"]}]});
    if (file) {
        doc.file = file;
        doc.edited = false;
        save(destroy_when_done);
    }
}

function save_without_sauce() {
    const file = save_box(doc.file, "ans", {filters: [{name: "ANSI Art", extensions: ["ans", "asc", "diz", "nfo", "txt"]}, {name: "XBin", extensions: ["xb"]}, {name: "Binary Text", extensions: ["bin"]}]});
    if (file) {
        doc.file = file;
        doc.edited = false;
        save(false, true);
    }
}

async function share_online() {
    const url = await doc.share_online();
    if (url) electron.shell.openExternal(url);
}

// Avatar-chat packets are capped at 131072 bytes; leave headroom for the
// JSON envelope around the encoded artwork.
const CHAT_MAX_BYTES = 120000;

// Encode the current canvas as a [BITMAP|...] envelope and post it to chat.
// Chat is mirrored server-side, so this only works while joined to a server.
function send_to_chat() {
    if (!doc.connection) {
        msg_box("Send to Chat", "Connect to a server first (File menu, Connect to Server). Chat is mirrored through the server you join.");
        return;
    }
    let envelope;
    try {
        envelope = avatar_codec.encode_bitmap({columns: doc.columns, rows: doc.rows, data: doc.data}, doc.author || doc.title || "moebius");
    } catch (err) {
        msg_box("Send to Chat", "Couldn't encode this canvas: " + err.message);
        return;
    }
    if (envelope.length > CHAT_MAX_BYTES) {
        msg_box("Send to Chat", "This artwork is too large to send to chat (" + Math.round(envelope.length / 1024) + " KB). Try a smaller canvas.");
        return;
    }
    doc.connection.chat(envelope);
}

// Set the user's chat avatar from the top-left 10x6 of the current canvas.
function set_avatar_from_canvas() {
    const base64 = avatar_codec.blocks_to_avatar({columns: doc.columns, rows: doc.rows, data: doc.data});
    doc.set_avatar(base64);
    msg_box("Set Avatar", "Your chat avatar is now the top-left " + avatar_codec.AVATAR_W + "x" + avatar_codec.AVATAR_H + " of this canvas. It will ride on chat messages you send.");
}

function check_before_closing() {
    const choice = msg_box("Save this document?", "This document contains unsaved changes.", {buttons: ["Save", "Cancel", "Don't Save"], defaultId: 0, cancelId: 1});
    if (choice == 0) {
        save(true);
    } else if (choice == 2) {
        send("destroy");
    }
}

function export_as_utf8() {
    const file = save_box(doc.file, "utf8ans", {filters: [{name: "ANSI Art ", extensions: ["utf8ans"]}]});
    if (file) doc.export_as_utf8(file);
}

function export_as_png() {
    const file = save_box(doc.file, "png", {filters: [{name: "Portable Network Graphics ", extensions: ["png"]}]});
    if (file) doc.export_as_png(file);
}

function export_as_apng() {
    const file = save_box(doc.file, "png", {filters: [{name: "Animated Portable Network Graphics ", extensions: ["png"]}]});
    if (file) doc.export_as_apng(file);
}

function hourly_save() {
    if (doc.connection && !doc.connection.connected) return;
    const file = (doc.connection) ? `${doc.connection.server}.ans` : (doc.file ? doc.file : "Untitled.ans");
    const timestamped_file = hourly_saver.filename(backup_folder, file);
    doc.save_backup(timestamped_file);
    hourly_saver.keep_if_changes(timestamped_file);
}

function use_backup(value) {
    if (value) {
        hourly_saver = new HourlySaver();
        hourly_saver.start();
        hourly_saver.on("save", hourly_save);
    } else if (hourly_saver) {
        hourly_saver.stop();
    }
}

on("new_document", (event, opts) => doc.new_document(opts));
on("revert_to_last_save", (event, opts) => doc.open(doc.file));
on("show_file_in_folder", (event, opts) => electron.shell.showItemInFolder(doc.file));
on("duplicate", (event, opts) => send("new_document", {columns: doc.columns, rows: doc.rows, data: doc.data, palette: doc.palette, font_name: doc.font_name, use_9px_font: doc.use_9px_font, ice_colors: doc.ice_colors}));
on("process_save", (event, {method, destroy_when_done, ignore_controlcharacters}) => process_save(method, destroy_when_done, ignore_controlcharacters));
on("save", (event, opts) => {
    if (doc.connection) {
        process_save('save_as');
    } else {
        process_save('save');
    }
});
on("save_as", (event, opts) => process_save('save_as'));
on("save_without_sauce", (event, opts) => process_save('save_without_sauce'));
on("share_online", (event, opts) => share_online());
on("send_to_chat", (event) => send_to_chat());
on("set_avatar_from_canvas", (event) => set_avatar_from_canvas());
on("open_file", (event, file) => doc.open(file));
on("check_before_closing", (event) => check_before_closing());
on("export_as_utf8", (event) => export_as_utf8());
on("export_as_png", (event) => export_as_png());
on("export_as_apng", (event) => export_as_apng());
on("remove_ice_colors", (event) => send("new_document", remove_ice_colors(doc)));
on("connect_to_server", (event, {server, pass}) => doc.connect_to_server(server, pass));
on("backup_folder", (event, folder) => backup_folder = folder);
on("use_backup", (event, value) => use_backup(value));
