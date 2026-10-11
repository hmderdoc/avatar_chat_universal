/**
 * SAUCE (Standard Architecture for Universal Comment Extensions) support.
 * Parses SAUCE records from ANS/BIN files, loads ANSI and BIN formats.
 *
 * SAUCE record: 128-byte trailer at end of file.
 *   Offset 0:   "SAUCE" (5 bytes)
 *   Offset 5:   "00" (version, 2 bytes)
 *   Offset 7:   Title (35 bytes)
 *   Offset 42:  Author (20 bytes)
 *   Offset 62:  Group (20 bytes)
 *   Offset 82:  Date (8 bytes, CCYYMMDD)
 *   Offset 90:  FileSize (4 bytes, little-endian, original file size without SAUCE)
 *   Offset 94:  DataType (1 byte)
 *   Offset 95:  FileType (1 byte)
 *   Offset 96:  TInfo1 (2 bytes LE) — width for Character types
 *   Offset 98:  TInfo2 (2 bytes LE) — height for Character types
 *   Offset 100: TInfo3 (2 bytes LE)
 *   Offset 102: TInfo4 (2 bytes LE)
 *   Offset 104: Comments (1 byte, number of comment lines)
 *   Offset 105: TFlags (1 byte)
 *   Offset 106: TInfoS (22 bytes, font name / info string)
 */

/**
 * Parse SAUCE record from the tail of a byte array.
 * @param {Uint8Array} bytes — complete file bytes
 * @returns {object|null} parsed SAUCE record or null if not found
 */
export function parseSauce(bytes) {
    if (bytes.length < 128) return null;
    const offset = bytes.length - 128;
    // Check magic "SAUCE"
    if (bytes[offset] !== 0x53 || bytes[offset + 1] !== 0x41 ||
        bytes[offset + 2] !== 0x55 || bytes[offset + 3] !== 0x43 ||
        bytes[offset + 4] !== 0x45) return null;

    const decoder = new TextDecoder('ascii');
    const str = (off, len) => decoder.decode(bytes.slice(off, off + len)).replace(/\0+$/, '').trim();
    const u16 = (off) => bytes[off] | (bytes[off + 1] << 8);
    const u32 = (off) => bytes[off] | (bytes[off + 1] << 8) | (bytes[off + 2] << 16) | (bytes[off + 3] << 24);

    return {
        title: str(offset + 7, 35),
        author: str(offset + 42, 20),
        group: str(offset + 62, 20),
        date: str(offset + 82, 8),
        fileSize: u32(offset + 90),
        dataType: bytes[offset + 94],
        fileType: bytes[offset + 95],
        tInfo1: u16(offset + 96),   // width
        tInfo2: u16(offset + 98),   // height (number of lines)
        tInfo3: u16(offset + 100),
        tInfo4: u16(offset + 102),
        comments: bytes[offset + 104],
        tFlags: bytes[offset + 105],
        tInfoS: str(offset + 106, 22),
    };
}

/**
 * Get the content portion of a file (strip SAUCE/EOF/COMNT).
 */
export function getContentBytes(bytes, sauce) {
    if (!sauce) return bytes;
    // Look for EOF (0x1A) marker
    let end = bytes.length - 128;
    // COMNT block: 5-byte "COMNT" + sauce.comments * 64 bytes, before SAUCE record
    if (sauce.comments > 0) {
        end -= 5 + sauce.comments * 64;
    }
    // Strip trailing EOF (0x1A)
    if (end > 0 && bytes[end - 1] === 0x1A) end--;
    return bytes.slice(0, end);
}

/**
 * Convert ANSI SGR color index (0-7) to CGA/BIN palette index.
 * ANSI SGR: 0=Black, 1=Red, 2=Green, 3=Yellow, 4=Blue, 5=Magenta, 6=Cyan, 7=White
 * CGA/BIN:  0=Black, 1=Blue, 2=Green, 3=Cyan,   4=Red,  5=Magenta, 6=Brown, 7=LightGray
 * Swaps: 1<->4 (Red<->Blue), 3<->6 (Yellow<->Cyan)
 */
function ansiToCga(c) {
    switch (c) {
        case 1: return 4;
        case 4: return 1;
        case 3: return 6;
        case 6: return 3;
        default: return c;
    }
}

/**
 * Load an ANS file: parse ANSI escape sequences into a grid.
 * Uses a virtual screen approach — processes bytes sequentially,
 * maintaining cursor position and current attributes.
 *
 * @param {Uint8Array} data — raw ANS content bytes (SAUCE-stripped)
 * @param {number} columns — canvas width
 * @param {number} maxRows — max rows to allow (0 = unlimited)
 * @returns {{ grid: Array, rows: number }} grid of {code, fg, bg} blocks
 */
export function loadAnsi(data, columns, maxRows) {
    columns = columns || 80;
    maxRows = maxRows || 1000;
    let rows = 25;
    const grid = [];
    const ensure = (r) => {
        while (grid.length < (r + 1) * columns) {
            grid.push({ code: 32, fg: 7, bg: 0 });
        }
        if (r + 1 > rows) rows = r + 1;
    };

    let cx = 0, cy = 0;
    let fg = 7, bg = 0;
    let savedX = 0, savedY = 0;
    let i = 0;

    while (i < data.length) {
        const b = data[i];

        // ESC sequence
        if (b === 0x1B && i + 1 < data.length && data[i + 1] === 0x5B) {
            // CSI sequence: ESC [ params letter
            i += 2;
            let params = '';
            while (i < data.length && data[i] >= 0x20 && data[i] <= 0x3F) {
                params += String.fromCharCode(data[i]);
                i++;
            }
            if (i >= data.length) break;
            const cmd = String.fromCharCode(data[i]);
            i++;

            const nums = params.split(';').map(s => s === '' ? undefined : parseInt(s, 10));

            switch (cmd) {
                case 'A': // Cursor Up
                    cy = Math.max(0, cy - (nums[0] || 1));
                    break;
                case 'B': // Cursor Down
                    cy += (nums[0] || 1);
                    if (maxRows && cy >= maxRows) cy = maxRows - 1;
                    break;
                case 'C': // Cursor Forward
                    cx += (nums[0] || 1);
                    if (cx >= columns) cx = columns - 1;
                    break;
                case 'D': // Cursor Back
                    cx = Math.max(0, cx - (nums[0] || 1));
                    break;
                case 'H': case 'f': // Cursor Position
                    cy = Math.max(0, (nums[0] || 1) - 1);
                    cx = Math.max(0, (nums[1] || 1) - 1);
                    if (cx >= columns) cx = columns - 1;
                    break;
                case 'J': // Erase Display
                    {
                        const n = nums[0] || 0;
                        if (n === 2) {
                            // Clear entire screen
                            for (let j = 0; j < grid.length; j++) {
                                grid[j] = { code: 32, fg, bg };
                            }
                        }
                    }
                    break;
                case 'K': // Erase in Line
                    {
                        const n = nums[0] || 0;
                        ensure(cy);
                        if (n === 0) {
                            // Clear from cursor to end of line
                            for (let x = cx; x < columns; x++) {
                                grid[cy * columns + x] = { code: 32, fg, bg };
                            }
                        } else if (n === 1) {
                            // Clear from start of line to cursor
                            for (let x = 0; x <= cx; x++) {
                                grid[cy * columns + x] = { code: 32, fg, bg };
                            }
                        } else if (n === 2) {
                            // Clear entire line
                            for (let x = 0; x < columns; x++) {
                                grid[cy * columns + x] = { code: 32, fg, bg };
                            }
                        }
                    }
                    break;
                case 's': // Save cursor
                    savedX = cx;
                    savedY = cy;
                    break;
                case 'u': // Restore cursor
                    cx = savedX;
                    cy = savedY;
                    break;
                case 'm': // SGR — Set Graphic Rendition
                    if (nums.length === 0 || (nums.length === 1 && nums[0] === undefined)) {
                        fg = 7; bg = 0;
                    } else {
                        for (let j = 0; j < nums.length; j++) {
                            const n = nums[j] === undefined ? 0 : nums[j];
                            if (n === 0) { fg = 7; bg = 0; }
                            else if (n === 1) { fg |= 8; }     // bold → high-intensity fg
                            else if (n === 5) { bg |= 8; }     // blink → high-intensity bg (iCE colors)
                            else if (n === 7) { const t = fg; fg = bg; bg = t; } // reverse
                            else if (n === 22) { fg &= 7; }    // normal intensity
                            else if (n === 25) { bg &= 7; }    // blink off
                            else if (n >= 30 && n <= 37) { fg = (fg & 8) | ansiToCga(n - 30); }
                            else if (n >= 40 && n <= 47) { bg = (bg & 8) | ansiToCga(n - 40); }
                        }
                    }
                    break;
                default:
                    // Unknown CSI sequence — ignore
                    break;
            }
            continue;
        }

        // CR (carriage return)
        if (b === 0x0D) {
            cx = 0;
            i++;
            continue;
        }

        // LF (line feed)
        if (b === 0x0A) {
            cy++;
            if (maxRows && cy >= maxRows) cy = maxRows - 1;
            i++;
            continue;
        }

        // TAB
        if (b === 0x09) {
            cx = Math.min(columns - 1, (cx + 8) & ~7);
            i++;
            continue;
        }

        // Regular printable byte (CP437)
        if (cy < maxRows) {
            ensure(cy);
            grid[cy * columns + cx] = { code: b, fg, bg };
            cx++;
            if (cx >= columns) {
                cx = 0;
                cy++;
                if (maxRows && cy >= maxRows) cy = maxRows - 1;
            }
        }
        i++;
    }

    return { grid, rows };
}

/**
 * Load a BIN file: byte-pairs of (charCode, attribute).
 * Attribute byte: low nibble = fg (0–15), high nibble = bg (0–15).
 *
 * @param {Uint8Array} data — raw BIN content bytes
 * @param {number} columns — canvas width (from SAUCE TInfo1 or default 160)
 * @returns {{ grid: Array, rows: number }}
 */
export function loadBin(data, columns) {
    columns = columns || 160;
    const grid = [];
    const totalCells = Math.floor(data.length / 2);
    for (let i = 0; i < totalCells; i++) {
        const code = data[i * 2];
        const attr = data[i * 2 + 1];
        grid.push({
            code,
            fg: attr & 0x0F,
            bg: (attr >> 4) & 0x0F,
        });
    }
    const rows = Math.ceil(totalCells / columns);
    // Pad to full rows
    while (grid.length < rows * columns) {
        grid.push({ code: 32, fg: 7, bg: 0 });
    }
    return { grid, rows };
}

/**
 * Detect file type from filename extension and optional SAUCE record.
 * @param {string} filename
 * @param {Uint8Array} bytes
 * @returns {{ type: string, sauce: object|null, columns: number, rows: number|null }}
 */
export function detectFileType(filename, bytes) {
    const sauce = parseSauce(bytes);
    const ext = (filename || '').split('.').pop().toLowerCase();

    // SAUCE datatype 5 = BinaryText (BIN)
    if (sauce && sauce.dataType === 5) {
        return {
            type: 'bin',
            sauce,
            columns: sauce.tInfo1 * 2 || 160,  // SAUCE TInfo1 for BIN is width/2
            rows: null,
        };
    }

    // SAUCE datatype 1 = Character (ANS, ASC, etc.)
    if (sauce && sauce.dataType === 1) {
        return {
            type: 'ans',
            sauce,
            columns: sauce.tInfo1 || 80,
            rows: sauce.tInfo2 || null,
        };
    }

    // Fall back to extension
    if (ext === 'bin') {
        return { type: 'bin', sauce, columns: 160, rows: null };
    }

    // Default: treat as ANS
    return { type: 'ans', sauce, columns: 80, rows: null };
}
