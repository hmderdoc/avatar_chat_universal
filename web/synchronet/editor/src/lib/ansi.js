/**
 * ANSI escape sequence encoder.
 *
 * Converts a TextDocument into a Uint8Array of ANSI art bytes.
 * Uses traditional BBS conventions:
 *   FG 0-7  -> ESC[30-37m
 *   FG 8-15 -> ESC[1;30-37m   (bold = high intensity)
 *   BG 0-7  -> ESC[40-47m
 *   BG 8-15 -> ESC[5;40-47m   (blink = high intensity BG in iCE mode)
 */

/**
 * Convert CGA palette index (low 3 bits) to ANSI SGR color code.
 * CGA: 0=Black, 1=Blue, 2=Green, 3=Cyan, 4=Red, 5=Magenta, 6=Brown, 7=LightGray
 * SGR: 0=Black, 1=Red,  2=Green, 3=Yellow, 4=Blue, 5=Magenta, 6=Cyan,  7=White
 */
function cgaToAnsi(c) {
    switch (c) {
        case 1: return 4;
        case 4: return 1;
        case 3: return 6;
        case 6: return 3;
        default: return c;
    }
}

export function encodeAsAnsi(doc, opts = {}) {
    const iceColors = opts.iceColors || false;
    const out = [];

    let curBold = false;
    let curBlink = false;
    let curFg = 7;    // Default FG (as low 3 bits)
    let curBg = 0;    // Default BG (as low 3 bits)

    function pushBytes(str) {
        for (let i = 0; i < str.length; i++) out.push(str.charCodeAt(i));
    }

    function pushSGR(params) {
        out.push(0x1B, 0x5B);  // ESC [
        pushBytes(params.join(';'));
        out.push(0x6D);        // m
    }

    for (let y = 0; y < doc.rows; y++) {
        for (let x = 0; x < doc.columns; x++) {
            const block = doc.data[y * doc.columns + x];

            const wantBold  = block.fg >= 8;
            const wantFg    = block.fg & 7;
            const wantBlink = iceColors && block.bg >= 8;
            const wantBg    = iceColors ? (block.bg & 7) : Math.min(block.bg, 7);

            // Determine if reset is needed (going from bold->non or blink->non)
            const needReset = (curBold && !wantBold) || (curBlink && !wantBlink);

            if (needReset) {
                const p = [0];  // reset
                if (wantBold) p.push(1);
                if (wantBlink) p.push(5);
                p.push(30 + cgaToAnsi(wantFg));
                if (wantBg !== 0) p.push(40 + cgaToAnsi(wantBg));
                pushSGR(p);
                curBold = wantBold;
                curBlink = wantBlink;
                curFg = wantFg;
                curBg = wantBg;
            } else {
                const p = [];
                if (wantBold && !curBold)  { p.push(1); curBold = true; }
                if (wantBlink && !curBlink) { p.push(5); curBlink = true; }
                if (wantFg !== curFg)       { p.push(30 + cgaToAnsi(wantFg)); curFg = wantFg; }
                if (wantBg !== curBg)       { p.push(40 + cgaToAnsi(wantBg)); curBg = wantBg; }
                if (p.length > 0) pushSGR(p);
            }

            // Emit the character byte (CP437 code)
            out.push(block.code === 0 ? 32 : block.code);
        }

        // Line ending
        if (y < doc.rows - 1) {
            out.push(0x0D, 0x0A);  // CR LF
        }
    }

    return new Uint8Array(out);
}
