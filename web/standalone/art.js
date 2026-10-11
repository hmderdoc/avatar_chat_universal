/* Avatar Chat, standalone page: ANSI art helpers.
 *
 * The functions between the markers below are copied unchanged from the
 * Synchronet page (web/synchronet/browser/app.js: bytesToHex through
 * prepareAnsiBitmap) so both pages turn an .ans file into the same
 * [BITMAP|...] message the door and Moebius send. Change them there and
 * copy them here again. */
(function (global) {
    'use strict';

    var MAX_BITMAP_CELLS = 40000;
    var MAX_BITMAP_HEIGHT = 255;

    function trimText(value) {
        return String(value || '').replace(/^\s+|\s+$/g, '');
    }

    /* ---- copied from web/synchronet/browser/app.js ---- */

    function bytesToHex(bytes) {
        var hex = '';
        var index = 0;
        var value = '';

        for (index = 0; index < bytes.length; index += 1) {
            value = bytes[index].toString(16);
            if (value.length < 2) {
                value = '0' + value;
            }
            hex += value;
        }

        return hex;
    }

    function cgaToXterm(color) {
        var low = color & 7;

        if (low === 1) {
            low = 4;
        } else if (low === 4) {
            low = 1;
        } else if (low === 3) {
            low = 6;
        } else if (low === 6) {
            low = 3;
        }

        return (color & 8) | low;
    }

    function encodeBitmapRaw(cells, width, height) {
        var total = width * height;
        var buffer = new Uint8Array(1 + (total * 3));
        var index = 0;
        var cell;

        buffer[0] = height & 0xff;
        for (index = 0; index < total; index += 1) {
            cell = cells[index] || { code: 32, fg: 7, bg: 0 };
            buffer[1 + index] = cgaToXterm(cell.fg || 0) & 0xff;
            buffer[1 + total + index] = cgaToXterm(cell.bg || 0) & 0xff;
            buffer[1 + (total * 2) + index] = (cell.code || 32) & 0xff;
        }

        return buffer;
    }

    function compressZlib(rawBytes) {
        var responsePromise;
        var stream;
        var writer;

        if (typeof CompressionStream !== 'function') {
            return Promise.reject(new Error('This browser cannot compress ANSI uploads.'));
        }

        stream = new CompressionStream('deflate');
        writer = stream.writable.getWriter();
        responsePromise = new Response(stream.readable).arrayBuffer();
        return Promise.all([
            responsePromise,
            writer.write(rawBytes).then(function () {
                return writer.close();
            })
        ]).then(function (results) {
            return new Uint8Array(results[0]);
        });
    }

    function sanitizeBitmapFromName(name) {
        return trimText(String(name || '').replace(/[|\[\]\x00-\x1f]/g, '')).substr(0, 60);
    }

    function buildBitmapPayload(cells, width, height, fromName) {
        var raw = encodeBitmapRaw(cells, width, height);

        return compressZlib(raw).then(function (compressed) {
            return '[BITMAP|' + width + '|' + height + '|' + sanitizeBitmapFromName(fromName) + '|' + bytesToHex(compressed) + ']';
        });
    }

    function readLittleEndian16(bytes, offset) {
        return (bytes[offset] || 0) | ((bytes[offset + 1] || 0) << 8);
    }

    function readLittleEndian32(bytes, offset) {
        return (bytes[offset] || 0) |
            ((bytes[offset + 1] || 0) << 8) |
            ((bytes[offset + 2] || 0) << 16) |
            ((bytes[offset + 3] || 0) << 24);
    }

    function decodeAsciiSlice(bytes, offset, length) {
        var chars = [];
        var index = 0;

        for (index = 0; index < length; index += 1) {
            chars.push(String.fromCharCode(bytes[offset + index] || 0));
        }

        return chars.join('').replace(/\0+$/g, '').replace(/^\s+|\s+$/g, '');
    }

    function parseSauce(bytes) {
        var offset;

        if (!bytes || bytes.length < 128) {
            return null;
        }

        offset = bytes.length - 128;
        if (bytes[offset] !== 0x53 || bytes[offset + 1] !== 0x41 || bytes[offset + 2] !== 0x55 || bytes[offset + 3] !== 0x43 || bytes[offset + 4] !== 0x45) {
            return null;
        }

        return {
            title: decodeAsciiSlice(bytes, offset + 7, 35),
            author: decodeAsciiSlice(bytes, offset + 42, 20),
            group: decodeAsciiSlice(bytes, offset + 62, 20),
            date: decodeAsciiSlice(bytes, offset + 82, 8),
            fileSize: readLittleEndian32(bytes, offset + 90),
            dataType: bytes[offset + 94] || 0,
            fileType: bytes[offset + 95] || 0,
            tInfo1: readLittleEndian16(bytes, offset + 96),
            tInfo2: readLittleEndian16(bytes, offset + 98),
            tInfo3: readLittleEndian16(bytes, offset + 100),
            tInfo4: readLittleEndian16(bytes, offset + 102),
            comments: bytes[offset + 104] || 0,
            tFlags: bytes[offset + 105] || 0,
            tInfoS: decodeAsciiSlice(bytes, offset + 106, 22)
        };
    }

    function getContentBytes(bytes, sauce) {
        var end;

        if (!sauce) {
            return bytes;
        }

        end = bytes.length - 128;
        if (sauce.comments > 0) {
            end -= 5 + (sauce.comments * 64);
        }
        if (end > 0 && bytes[end - 1] === 0x1a) {
            end -= 1;
        }

        return bytes.slice(0, end);
    }

    function ansiToCga(color) {
        if (color === 1) {
            return 4;
        }
        if (color === 4) {
            return 1;
        }
        if (color === 3) {
            return 6;
        }
        if (color === 6) {
            return 3;
        }
        return color;
    }

    function loadAnsi(data, columns, maxRows) {
        var rows = 25;
        var grid = [];
        var cx = 0;
        var cy = 0;
        var fg = 7;
        var bg = 0;
        var savedX = 0;
        var savedY = 0;
        var index = 0;

        if (!columns || columns < 1) {
            columns = 80;
        }
        if (typeof maxRows !== 'number' || maxRows < 1) {
            maxRows = 0;
        }

        function ensure(row) {
            while (grid.length < (row + 1) * columns) {
                grid.push({ code: 32, fg: 7, bg: 0 });
            }
            if (row + 1 > rows) {
                rows = row + 1;
            }
        }

        while (index < data.length) {
            var byte = data[index];

            if (byte === 0x1b && index + 1 < data.length && data[index + 1] === 0x5b) {
                var params = '';
                var cmd = '';
                var nums;
                var cursorStep = 1;
                var eraseMode = 0;
                var sgrIndex = 0;
                var value = 0;

                index += 2;
                while (index < data.length && data[index] >= 0x20 && data[index] <= 0x3f) {
                    params += String.fromCharCode(data[index]);
                    index += 1;
                }
                if (index >= data.length) {
                    break;
                }
                cmd = String.fromCharCode(data[index]);
                index += 1;
                nums = params.split(';').map(function (part) {
                    return part === '' ? undefined : parseInt(part, 10);
                });

                switch (cmd) {
                case 'A':
                    cursorStep = nums[0] || 1;
                    cy = Math.max(0, cy - cursorStep);
                    break;
                case 'B':
                    cursorStep = nums[0] || 1;
                    cy += cursorStep;
                    if (maxRows && cy >= maxRows) {
                        cy = maxRows - 1;
                    }
                    break;
                case 'C':
                    cursorStep = nums[0] || 1;
                    cx += cursorStep;
                    if (cx >= columns) {
                        cx = columns - 1;
                    }
                    break;
                case 'D':
                    cursorStep = nums[0] || 1;
                    cx = Math.max(0, cx - cursorStep);
                    break;
                case 'H':
                case 'f':
                    cy = Math.max(0, (nums[0] || 1) - 1);
                    cx = Math.max(0, (nums[1] || 1) - 1);
                    if (maxRows && cy >= maxRows) {
                        cy = maxRows - 1;
                    }
                    if (cx >= columns) {
                        cx = columns - 1;
                    }
                    break;
                case 'J':
                    eraseMode = nums[0] || 0;
                    if (eraseMode === 2) {
                        for (value = 0; value < grid.length; value += 1) {
                            grid[value] = { code: 32, fg: fg, bg: bg };
                        }
                    }
                    break;
                case 'K':
                    eraseMode = nums[0] || 0;
                    ensure(cy);
                    if (eraseMode === 0) {
                        for (value = cx; value < columns; value += 1) {
                            grid[(cy * columns) + value] = { code: 32, fg: fg, bg: bg };
                        }
                    } else if (eraseMode === 1) {
                        for (value = 0; value <= cx; value += 1) {
                            grid[(cy * columns) + value] = { code: 32, fg: fg, bg: bg };
                        }
                    } else if (eraseMode === 2) {
                        for (value = 0; value < columns; value += 1) {
                            grid[(cy * columns) + value] = { code: 32, fg: fg, bg: bg };
                        }
                    }
                    break;
                case 's':
                    savedX = cx;
                    savedY = cy;
                    break;
                case 'u':
                    cx = savedX;
                    cy = savedY;
                    break;
                case 'm':
                    if (!nums.length || (nums.length === 1 && nums[0] === undefined)) {
                        fg = 7;
                        bg = 0;
                        break;
                    }

                    for (sgrIndex = 0; sgrIndex < nums.length; sgrIndex += 1) {
                        value = nums[sgrIndex] === undefined ? 0 : nums[sgrIndex];
                        if (value === 0) {
                            fg = 7;
                            bg = 0;
                        } else if (value === 1) {
                            fg |= 8;
                        } else if (value === 5) {
                            bg |= 8;
                        } else if (value === 7) {
                            var swap = fg;
                            fg = bg;
                            bg = swap;
                        } else if (value === 22) {
                            fg &= 7;
                        } else if (value === 25) {
                            bg &= 7;
                        } else if (value >= 30 && value <= 37) {
                            fg = (fg & 8) | ansiToCga(value - 30);
                        } else if (value >= 40 && value <= 47) {
                            bg = (bg & 8) | ansiToCga(value - 40);
                        }
                    }
                    break;
                default:
                    break;
                }
                continue;
            }

            if (byte === 0x0d) {
                cx = 0;
                index += 1;
                continue;
            }

            if (byte === 0x0a) {
                cy += 1;
                if (maxRows && cy >= maxRows) {
                    cy = maxRows - 1;
                }
                index += 1;
                continue;
            }

            if (byte === 0x09) {
                cx = Math.min(columns - 1, (cx + 8) & ~7);
                index += 1;
                continue;
            }

            if (!maxRows || cy < maxRows) {
                ensure(cy);
                grid[(cy * columns) + cx] = { code: byte, fg: fg, bg: bg };
                cx += 1;
                if (cx >= columns) {
                    cx = 0;
                    cy += 1;
                    if (maxRows && cy >= maxRows) {
                        cy = maxRows - 1;
                    }
                }
            }

            index += 1;
        }

        return {
            grid: grid,
            rows: rows
        };
    }

    function rowHasAnsiContent(grid, columns, rowIndex) {
        var start = rowIndex * columns;
        var index = 0;
        var cell;

        for (index = 0; index < columns; index += 1) {
            cell = grid[start + index] || { code: 32, fg: 7, bg: 0 };
            if (cell.code !== 32 || cell.fg !== 7 || cell.bg !== 0) {
                return true;
            }
        }

        return false;
    }

    function prepareAnsiBitmap(bytes) {
        var sauce = parseSauce(bytes);
        var content = getContentBytes(bytes, sauce);
        var width = sauce && sauce.tInfo1 ? sauce.tInfo1 : 80;
        var parsed;
        var height = 0;
        var total = 0;
        var cells = [];
        var index = 0;

        if (width < 1) {
            width = 80;
        }

        parsed = loadAnsi(content, width, 0);
        if (sauce && sauce.tInfo2) {
            height = sauce.tInfo2;
        } else {
            height = parsed.rows;
            while (height > 1 && !rowHasAnsiContent(parsed.grid, width, height - 1)) {
                height -= 1;
            }
        }

        if (height < 1) {
            height = 1;
        }
        if (height > MAX_BITMAP_HEIGHT) {
            throw new Error('ANSI art is too tall. The BITMAP format supports up to 255 rows.');
        }

        total = width * height;
        if (total < 1 || total > MAX_BITMAP_CELLS) {
            throw new Error('ANSI art is too large to send as a BITMAP image.');
        }

        for (index = 0; index < total; index += 1) {
            cells.push(parsed.grid[index] || { code: 32, fg: 7, bg: 0 });
        }

        return {
            width: width,
            height: height,
            cells: cells
        };
    }
    /* ---- end of the copy ---- */

    /* Avatars: 10x6 cells, 120 bytes of character/attribute pairs. */
    var AVATAR_COLS = 10;
    var AVATAR_ROWS = 6;
    var AVATAR_BYTES = AVATAR_COLS * AVATAR_ROWS * 2;

    function binToBase64(bytes) {
        var s = '';
        var i;
        for (i = 0; i < bytes.length; i += 1) {
            s += String.fromCharCode(bytes[i]);
        }
        return btoa(s);
    }

    function cellsToAvatarBase64(cells) {
        var s = '';
        var i;
        var c;
        for (i = 0; i < AVATAR_COLS * AVATAR_ROWS; i += 1) {
            c = cells[i] || { code: 32, fg: 7, bg: 0 };
            s += String.fromCharCode((typeof c.code === 'number' ? c.code : 32) & 0xff);
            s += String.fromCharCode((((c.bg || 0) & 0x07) << 4) | ((c.fg || 0) & 0x0f));
        }
        return btoa(s);
    }

    function avatarBase64ToCells(base64) {
        var bin = '';
        var cells = [];
        var i;
        try { bin = atob(base64 || ''); } catch (_e) { bin = ''; }
        for (i = 0; i < AVATAR_COLS * AVATAR_ROWS; i += 1) {
            if (bin.length === AVATAR_BYTES) {
                var attr = bin.charCodeAt(i * 2 + 1) & 0xff;
                cells.push({ code: bin.charCodeAt(i * 2) & 0xff, fg: attr & 0x0f, bg: (attr >> 4) & 0x07 });
            } else {
                cells.push({ code: 32, fg: 7, bg: 0 });
            }
        }
        return cells;
    }

    /* The top-left 10x6 of an ANSI file, as avatar cells (blink dropped,
       which the server refuses in an avatar). */
    function ansiToAvatarCells(bytes) {
        var sauce = parseSauce(bytes);
        var content = getContentBytes(bytes, sauce);
        var width = sauce && sauce.tInfo1 ? sauce.tInfo1 : 80;
        var parsed = loadAnsi(content, width, AVATAR_ROWS);
        var cells = [];
        var x;
        var y;
        var c;
        for (y = 0; y < AVATAR_ROWS; y += 1) {
            for (x = 0; x < AVATAR_COLS; x += 1) {
                c = parsed.grid[(y * width) + x] || { code: 32, fg: 7, bg: 0 };
                cells.push({ code: c.code, fg: c.fg & 0x0f, bg: c.bg & 0x07 });
            }
        }
        return cells;
    }

    global.AvatarArt = {
        AVATAR_COLS: AVATAR_COLS,
        AVATAR_ROWS: AVATAR_ROWS,
        AVATAR_BYTES: AVATAR_BYTES,
        buildBitmapPayload: buildBitmapPayload,
        prepareAnsiBitmap: prepareAnsiBitmap,
        binToBase64: binToBase64,
        cellsToAvatarBase64: cellsToAvatarBase64,
        avatarBase64ToCells: avatarBase64ToCells,
        ansiToAvatarCells: ansiToAvatarCells
    };
})(window);
