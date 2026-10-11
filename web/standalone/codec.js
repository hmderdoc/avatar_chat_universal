// Browser decoder for the two graphic payloads avatar chat carries, and a
// CP437 cell renderer. The formats are moebius_chat/app/avatar_codec.js's
// (and internal/bitmap's); this is the same decode for the browser, which
// has no Buffer or zlib, so it uses atob and DecompressionStream.
//
//   AVATAR  120 bytes (10x6 cells of char, CGA attr), base64, on nick.avatar.
//   BITMAP  "[BITMAP|<w>|<h>|<from>|<hex zlib payload>]" in message.str;
//           payload [height, fg*N, bg*N, char*N] with fg/bg in the ANSI
//           palette order (1 = red), swapped here to CGA order (1 = blue).
//
// Decoded art is {columns, rows, from, data: [{code, fg, bg}]} with CGA
// colour indexes (16..255 are xterm-256 indexes, passed through).
"use strict";

(function (global) {
    const AVATAR_W = 10, AVATAR_H = 6, AVATAR_BYTES = 120;
    const BITMAP_PREFIX = "[BITMAP|";
    const PAL_SWAP = [0, 4, 2, 6, 1, 5, 3, 7, 8, 12, 10, 14, 9, 13, 11, 15];
    const swap = (i) => (i < 16 ? PAL_SWAP[i] : i);

    const CGA = [
        "#000000", "#0000aa", "#00aa00", "#00aaaa", "#aa0000", "#aa00aa", "#aa5500", "#aaaaaa",
        "#555555", "#5555ff", "#55ff55", "#55ffff", "#ff5555", "#ff55ff", "#ffff55", "#ffffff",
    ];

    // color returns the CSS colour of a CGA (0..15) or xterm-256 index.
    function color(i) {
        if (i < 16) return CGA[i];
        if (i < 232) {
            const n = i - 16, lv = [0, 95, 135, 175, 215, 255];
            const r = lv[Math.floor(n / 36)], g = lv[Math.floor(n / 6) % 6], b = lv[n % 6];
            return "rgb(" + r + "," + g + "," + b + ")";
        }
        const v = 8 + (i - 232) * 10;
        return "rgb(" + v + "," + v + "," + v + ")";
    }

    function avatarToBlocks(b64) {
        if (!b64 || typeof b64 !== "string") return null;
        let bin;
        try { bin = atob(b64.trim()); } catch (e) { return null; }
        if (bin.length !== AVATAR_BYTES) return null;
        const data = new Array(AVATAR_W * AVATAR_H);
        for (let i = 0; i < data.length; i++) {
            const code = bin.charCodeAt(i * 2), attr = bin.charCodeAt(i * 2 + 1);
            data[i] = {code, fg: attr & 0x0f, bg: (attr >> 4) & 0x07};
        }
        return {columns: AVATAR_W, rows: AVATAR_H, data};
    }

    function isBitmap(str) {
        return typeof str === "string" && str.indexOf(BITMAP_PREFIX) === 0 &&
            str.charAt(str.length - 1) === "]" && str.length > BITMAP_PREFIX.length + 1;
    }

    function hexToBytes(hex) {
        const out = new Uint8Array(hex.length / 2);
        for (let i = 0; i < out.length; i++) {
            const b = parseInt(hex.substr(i * 2, 2), 16);
            if (Number.isNaN(b)) throw new Error("bad hex");
            out[i] = b;
        }
        return out;
    }

    async function inflateWith(format, bytes) {
        const ds = new DecompressionStream(format);
        const stream = new Blob([bytes]).stream().pipeThrough(ds);
        return new Uint8Array(await new Response(stream).arrayBuffer());
    }

    // inflate prefers zlib (the door and Go default), then raw deflate.
    async function inflate(bytes) {
        try { return await inflateWith("deflate", bytes); } catch (e) { return inflateWith("deflate-raw", bytes); }
    }

    // decodeBitmap resolves to the art, or null when str is malformed. The
    // payload's own height wins over the announced size, as in the door.
    async function decodeBitmap(str) {
        if (!isBitmap(str)) return null;
        const parts = str.slice(1, -1).split("|");
        if (parts.length !== 5 || parts[0] !== "BITMAP") return null;
        let width = parseInt(parts[1], 10) || 0, height = parseInt(parts[2], 10) || 0;
        const from = parts[3] || "", hex = parts[4] || "";
        if (width < 1 || height < 1 || !hex.length || hex.length % 2 !== 0) return null;
        let d;
        try { d = await inflate(hexToBytes(hex)); } catch (e) { return null; }
        if (!d || d.length < 4) return null;
        const dataHeight = d[0];
        if (dataHeight < 1) return null;
        const slice = Math.floor((d.length - 1) / 3);
        if (width * height !== slice) {
            width = Math.floor(slice / dataHeight);
            height = dataHeight;
        }
        if (width < 1 || height < 1) return null;
        const data = new Array(width * height);
        for (let i = 0; i < data.length; i++) data[i] = {code: 32, fg: 0, bg: 0};
        for (let i = 0; i < slice; i++) {
            const y = Math.floor(i / width), x = i % width;
            if (y >= height) break;
            let code = d[1 + slice * 2 + i];
            if (code === 0) code = 32;
            data[y * width + x] = {code, fg: swap(d[1 + i] || 0), bg: swap(d[1 + slice + i] || 0)};
        }
        return {columns: width, rows: height, from, data};
    }

    // --- rendering -------------------------------------------------------

    // cp437.png is the 64x4 sheet of 8x16 glyphs (alpha mask) the Go media
    // renderer and the Synchronet web use.
    const GW = 8, GH = 16, SHEET_COLS = 64;
    let sheet = null, sheetReady = null;
    const tinted = new Map();

    function loadSheet() {
        if (!sheetReady) {
            sheetReady = new Promise((resolve, reject) => {
                const img = new Image();
                img.onload = () => { sheet = img; resolve(img); };
                img.onerror = reject;
                img.src = "cp437.png";
            });
        }
        return sheetReady;
    }

    function tintedSheet(css) {
        let c = tinted.get(css);
        if (c) return c;
        c = document.createElement("canvas");
        c.width = sheet.width;
        c.height = sheet.height;
        const ctx = c.getContext("2d");
        ctx.drawImage(sheet, 0, 0);
        ctx.globalCompositeOperation = "source-in";
        ctx.fillStyle = css;
        ctx.fillRect(0, 0, c.width, c.height);
        tinted.set(css, c);
        return c;
    }

    // render draws art into a new canvas at 8x16 pixels per cell; CSS
    // scales it (image-rendering: pixelated).
    async function render(art) {
        await loadSheet();
        const canvas = document.createElement("canvas");
        canvas.width = art.columns * GW;
        canvas.height = art.rows * GH;
        const ctx = canvas.getContext("2d");
        for (let y = 0; y < art.rows; y++) {
            for (let x = 0; x < art.columns; x++) {
                const cell = art.data[y * art.columns + x];
                if (!cell) continue;
                ctx.fillStyle = color(cell.bg);
                ctx.fillRect(x * GW, y * GH, GW, GH);
                const code = cell.code & 0xff;
                if (code === 0 || code === 32 || code === 255) continue;
                ctx.drawImage(tintedSheet(color(cell.fg)),
                    (code % SHEET_COLS) * GW, Math.floor(code / SHEET_COLS) * GH, GW, GH,
                    x * GW, y * GH, GW, GH);
            }
        }
        canvas.className = "art";
        return canvas;
    }

    global.AvatarCodec = {AVATAR_W, AVATAR_H, CGA, color, avatarToBlocks, isBitmap, decodeBitmap, render};
})(window);
