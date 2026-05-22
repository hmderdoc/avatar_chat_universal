// Codec for the two graphic payloads the avatar-chat ecosystem carries on a
// chat message, so Moebius interoperates with the existing JS door and the Go
// bridges instead of inventing its own format.
//
//   * AVATAR  - a 120-byte (10x6) block of CP437 (char, CGA-attr) pairs, sent
//               base64-encoded on message.nick.avatar.
//               Format/validation: /sbbs/repo/exec/load/avatar_lib.js.
//   * BITMAP  - a whole piece of ANSI art shared inline in message.str as
//               "[BITMAP|<w>|<h>|<from>|<hex zlib-deflated payload>]".
//               Payload (inflated): [height, fg*N, bg*N, char*N], N = w*h.
//               Decoders: internal/bitmap/bitmap.go, avatar_chat.js:240-348.
//
// Pure module (no DOM / Electron) so it can be unit-tested under plain node;
// the chat panel feeds the decoded blocks to libtextmode.render_blocks.

const zlib = require("zlib");

// --- avatar (10x6, 120 bytes) ---------------------------------------------

const AVATAR_W = 10;
const AVATAR_H = 6;
const AVATAR_BYTES = AVATAR_W * AVATAR_H * 2; // 120

// CP437 codes avatar_lib.js forbids in the char byte (would corrupt a CP437
// terminal); we substitute a space when encoding from a canvas.
const FORBIDDEN_CHARS = new Set([0x00, 0x07, 0x08, 0x09, 0x0a, 0x0c, 0x0d, 0x1b, 0xff]);

// avatar_to_blocks decodes a base64 avatar into a libtextmode "blocks" object
// ({columns, rows, data:[{code,fg,bg}]}), or null if it isn't a valid avatar.
function avatar_to_blocks(base64) {
    if (!base64 || typeof base64 !== "string") return null;
    let buf;
    try {
        buf = Buffer.from(base64.trim(), "base64");
    } catch (e) {
        return null;
    }
    if (buf.length !== AVATAR_BYTES) return null;
    const data = new Array(AVATAR_W * AVATAR_H);
    for (let i = 0; i < data.length; i++) {
        const code = buf[i * 2];
        const attr = buf[i * 2 + 1];
        data[i] = {code, fg: attr & 0x0f, bg: (attr >> 4) & 0x07}; // blink bit dropped
    }
    return {columns: AVATAR_W, rows: AVATAR_H, data};
}

// blocks_to_avatar takes the top-left 10x6 of a libtextmode-style blocks/doc
// ({columns, data:[{code,fg,bg}]}) and returns a base64 avatar, sanitized to
// pass avatar_lib.js validation (forbidden chars -> space, blink bit cleared).
function blocks_to_avatar(blocks) {
    const buf = Buffer.alloc(AVATAR_BYTES);
    const cols = blocks.columns || AVATAR_W;
    for (let y = 0; y < AVATAR_H; y++) {
        for (let x = 0; x < AVATAR_W; x++) {
            const src = (y < (blocks.rows || AVATAR_H) && x < cols) ? blocks.data[y * cols + x] : undefined;
            let code = src ? (src.code & 0xff) : 0x20;
            const fg = src ? (src.fg & 0x0f) : 0x07;
            const bg = src ? (src.bg & 0x07) : 0x00; // 3-bit bg, no blink
            if (FORBIDDEN_CHARS.has(code)) code = 0x20;
            const i = y * AVATAR_W + x;
            buf[i * 2] = code;
            buf[i * 2 + 1] = (fg & 0x0f) | ((bg & 0x07) << 4);
        }
    }
    return buf.toString("base64");
}

// --- BITMAP (full artwork) -------------------------------------------------

const BITMAP_PREFIX = "[BITMAP|";

function is_bitmap(str) {
    return typeof str === "string" && str.indexOf(BITMAP_PREFIX) === 0 && str.charAt(str.length - 1) === "]" && str.length > BITMAP_PREFIX.length + 1;
}

function sanitize_from(name) {
    // The envelope is pipe-delimited and travels through chat sanitizers;
    // keep `from` to safe, single-token characters.
    return String(name || "").replace(/[^A-Za-z0-9_.\-]/g, "").slice(0, 32);
}

// encode_bitmap turns a libtextmode doc/blocks ({columns, rows, data:[{code,fg,bg}]})
// into a [BITMAP|...] envelope. Throws if the canvas is too tall to encode
// (height rides in a single payload byte).
function encode_bitmap(doc, from) {
    const columns = doc.columns;
    const rows = doc.rows;
    if (!(columns >= 1 && rows >= 1)) throw new Error("encode_bitmap: empty canvas");
    if (rows > 255) throw new Error("encode_bitmap: canvas too tall (" + rows + " rows; max 255)");
    const N = columns * rows;
    const payload = Buffer.alloc(1 + 3 * N);
    payload[0] = rows;
    for (let i = 0; i < N; i++) {
        const cell = doc.data[i] || {code: 32, fg: 7, bg: 0};
        payload[1 + i] = (cell.fg || 0) & 0xff;
        payload[1 + N + i] = (cell.bg || 0) & 0xff;
        let code = (cell.code == null ? 32 : cell.code) & 0xff;
        payload[1 + 2 * N + i] = code;
    }
    const compressed = zlib.deflateSync(payload); // zlib header, matches inflateZlib/zlib.NewReader
    return `${BITMAP_PREFIX}${columns}|${rows}|${sanitize_from(from)}|${compressed.toString("hex")}]`;
}

// decode_bitmap parses a [BITMAP|...] envelope into {columns, rows, from,
// data:[{code,fg,bg}]} (a libtextmode blocks object), trusting the payload
// dimensions over the announced ones (as the door and Go decoder do). Returns
// null on anything malformed.
function decode_bitmap(str) {
    if (!is_bitmap(str)) return null;
    const inner = str.slice(1, -1);
    const parts = inner.split("|");
    if (parts.length !== 5 || parts[0] !== "BITMAP") return null;
    let width = parseInt(parts[1], 10) || 0;
    let height = parseInt(parts[2], 10) || 0;
    const from = parts[3] || "";
    const hexData = parts[4] || "";
    if (width < 1 || height < 1 || !hexData.length || hexData.length % 2 !== 0) return null;

    let decompressed;
    try {
        const compressed = Buffer.from(hexData, "hex");
        decompressed = inflate(compressed);
    } catch (e) {
        return null;
    }
    if (!decompressed || decompressed.length < 4) return null;

    const dataHeight = decompressed[0];
    if (dataHeight < 1) return null;
    const slice = Math.floor((decompressed.length - 1) / 3);
    const totalPixels = slice;
    const dataWidth = Math.floor(totalPixels / dataHeight);
    if (width * height !== totalPixels) {
        width = dataWidth;
        height = dataHeight;
    }
    if (width < 1 || height < 1) return null;

    const data = new Array(width * height);
    for (let i = 0; i < width * height; i++) data[i] = {code: 32, fg: 0, bg: 0};
    for (let i = 0; i < totalPixels; i++) {
        const y = Math.floor(i / width);
        const x = i % width;
        if (y >= height) break;
        let code = decompressed[1 + slice * 2 + i];
        if (code === 0) code = 32;
        data[y * width + x] = {
            code,
            fg: decompressed[1 + i] || 0,
            bg: decompressed[1 + slice + i] || 0,
        };
    }
    return {columns: width, rows: height, from, data};
}

// inflate prefers zlib (the door/Go default) and falls back to raw deflate.
function inflate(compressed) {
    try {
        return zlib.inflateSync(compressed);
    } catch (e) {
        return zlib.inflateRawSync(compressed);
    }
}

module.exports = {
    AVATAR_W, AVATAR_H, AVATAR_BYTES,
    avatar_to_blocks, blocks_to_avatar,
    is_bitmap, encode_bitmap, decode_bitmap, sanitize_from,
};
