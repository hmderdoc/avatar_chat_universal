(function () {
    'use strict';

    /* Everything from the BBS: pages/avatarchat.xjs writes it from
       lib/avatarchat-page.js. */
    var pageConfig = window.AvatarChatConfig || {};
    var uploadConfig = pageConfig.uploads || { enabled: false, types: [] };
    var params = new URLSearchParams(window.location.search);
    var apiBase = './api/avatarchat.ssjs';
    /* A ?channel= link opens that room; otherwise the first room in your
       sidebar (the default room when you have none). */
    var initialChannel = sanitizeChannelName(params.get('channel') ||
        (Array.isArray(pageConfig.joinedRooms) && pageConfig.joinedRooms.length ? pageConfig.joinedRooms[0] : '') ||
        pageConfig.defaultChannel || 'main');
    var initialPrivate = trimText(params.get('private') || '');
    var initialPrivateSystem = trimText(params.get('system') || '');
    if (isMotdChannelName(initialChannel) && !pageConfig.canManageMotd) {
        initialChannel = getDefaultPublicChannel();
    }
    var currentPublicChannel = initialChannel;
    var activeView = initialPrivate.length
        ? { type: 'private', name: initialPrivate, system: initialPrivateSystem }
        : { type: 'channel', name: currentPublicChannel };
    var messages = [];
    var users = [];
    var rooms = [];
    var privateThreads = [];
    var mailboxes = {}; /* thread key -> a web user's mailbox; see rememberMailbox */
    var motdState = {
        text: '',
        timestamp: 0
    };
    var motdRenderSignature = '';
    var onlinePeerKeys = {};
    var onlinePeerNames = {};
    var unreadChannels = {};
    var unreadPrivate = {};
    var eventSource = null;
    var graphicsReady = false;
    var avatarsReady = false;
    var converter = null;
    var bitmapRecords = {};
    var bitmapSpritesheet = null;
    var bitmapSpritesheetCanvas = null;
    var bitmapSpritesheetContext = null;
    var bitmapGlyphCache = {};
    var bitmapSpritesheetLoading = false;
    var bitmapSpritesheetLoadFailed = false;
    var bitmapSpritesheetCallbacks = [];
    var BITMAP_FONT_WIDTH = 8;
    var BITMAP_FONT_HEIGHT = 16;
    var BITMAP_SPRITESHEET_COLUMNS = 64;
    var MAX_BITMAP_CELLS = 40000;
    var MAX_BITMAP_HEIGHT = 255;
    var LENGTH_BASE = [3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258];
    var LENGTH_EXTRA = [0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0];
    var DISTANCE_BASE = [1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577];
    var DISTANCE_EXTRA = [0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13];
    var XTERM_COLORS = buildXtermPalette();

    // --- Avatar localStorage cache ---
    var AVATAR_CACHE_KEY = 'avatarchat_avatars';

    function loadAvatarCache() {
        try {
            var raw = localStorage.getItem(AVATAR_CACHE_KEY);
            return raw ? JSON.parse(raw) : {};
        } catch (_e) {
            return {};
        }
    }

    function saveAvatarCache(cache) {
        try {
            localStorage.setItem(AVATAR_CACHE_KEY, JSON.stringify(cache));
        } catch (_e) {}
    }

    function cacheAvatar(name, system, bin) {
        if (!name || !bin) {
            return;
        }
        var key = normalizeUpper(name) + '|' + normalizeUpper(system || '');
        var cache = loadAvatarCache();
        cache[key] = bin;
        saveAvatarCache(cache);
    }

    function getCachedAvatar(name, system) {
        if (!name) {
            return null;
        }
        var key = normalizeUpper(name) + '|' + normalizeUpper(system || '');
        var cache = loadAvatarCache();
        return cache[key] || null;
    }

    function cacheAvatarsFromMessages(msgs) {
        if (!Array.isArray(msgs) || !msgs.length) {
            return;
        }
        var cache = loadAvatarCache();
        var changed = false;
        msgs.forEach(function (msg) {
            if (msg.sender && msg.avatar) {
                var key = normalizeUpper(msg.sender) + '|' + normalizeUpper(msg.system || '');
                if (!cache[key]) {
                    cache[key] = msg.avatar;
                    changed = true;
                }
            }
        });
        if (changed) {
            saveAvatarCache(cache);
        }
    }
    function cacheAvatarByNumber(userNum, bin) {
        if (!userNum || !bin) return;
        var key = 'num:' + String(userNum);
        var cache = loadAvatarCache();
        cache[key] = bin;
        saveAvatarCache(cache);
    }

    function getCachedAvatarByNumber(userNum) {
        if (!userNum) return null;
        var key = 'num:' + String(userNum);
        var cache = loadAvatarCache();
        return cache[key] || null;
    }
    // --- End avatar cache ---

    /* Set when the user changes their avatar on this page: the new one in
       base64, or '' when they disabled it. null until then. */
    var ownAvatarOverride = null;

    function isOwnName(name, system) {
        if (!pageConfig.userAlias || normalizeUpper(name) !== normalizeUpper(pageConfig.userAlias)) {
            return false;
        }
        return !system || normalizeUpper(system) === normalizeUpper(pageConfig.systemName || '');
    }
    var reconnectTimer = 0;
    var reconcileTimer = 0;
    var sendRefreshTimer = 0;
    var reconnectAttemptCount = 0;
    var realtimeHealthy = false;
    var usersRefreshTick = 0;
    var lastRoomPollAt = 0;
    var lastPrivatePollAt = 0;
    var historyRequestToken = 0;
    var usersRequestToken = 0;
    var RECONCILE_INTERVAL_MS = 15000;
    var RECONNECT_DELAY_MS = 4000;
    var statusState = {
        type: '',
        message: ''
    };

    var channelLabel = document.getElementById('avatar-chat-channel-label');
    var countButton = document.getElementById('avatar-chat-count');
    var statusBar = document.getElementById('avatar-chat-status');
    var statusText = document.getElementById('avatar-chat-status-text');
    var statusRetry = document.getElementById('avatar-chat-status-retry');
    var roomCountLabel = document.getElementById('avatar-chat-room-count');
    var serverLabel = document.getElementById('avatar-chat-server');
    var motdBar = document.getElementById('avatar-chat-motd');
    var motdWindow = document.getElementById('avatar-chat-motd-window');
    var motdTrack = document.getElementById('avatar-chat-motd-track');
    var motdPrimary = document.getElementById('avatar-chat-motd-primary');
    var motdClone = document.getElementById('avatar-chat-motd-clone');
    var usersModal = document.getElementById('avatar-chat-users-modal');
    var usersPanel = document.getElementById('avatar-chat-users');
    var usersTitle = document.getElementById('avatar-chat-users-title');
    var usersList = document.getElementById('avatar-chat-users-list');
    var usersClose = document.getElementById('avatar-chat-users-close');
    var roomsList = document.getElementById('avatar-chat-rooms');
    var privateList = document.getElementById('avatar-chat-private');
    var roomModal = document.getElementById('avatar-chat-room-modal');
    var roomClose = document.getElementById('avatar-chat-room-close');
    var roomForm = document.getElementById('avatar-chat-room-form');
    var roomInput = document.getElementById('avatar-chat-room-input');
    var roomSubmit = document.getElementById('avatar-chat-room-submit');
    var roomPickerList = document.getElementById('avatar-chat-room-list');
    var roomActions = document.getElementById('avatar-chat-room-actions');
    var noRoomsModal = document.getElementById('avatar-chat-norooms-modal');
    var noRoomsMain = document.getElementById('avatar-chat-norooms-main');
    var noRoomsPick = document.getElementById('avatar-chat-norooms-pick');
    var noRoomsCancel = document.getElementById('avatar-chat-norooms-cancel');
    /* The sidebar's rooms: the ones this user joined, kept by the BBS
       (webprefs record), as the page was served. */
    var joinedRooms = Array.isArray(pageConfig.joinedRooms) ? pageConfig.joinedRooms.slice() : [];
    var messageContainer = document.getElementById('avatar-chat-messages');
    var form = document.getElementById('avatar-chat-form');
    var fileInput = document.getElementById('avatar-chat-file');
    var attachButton = document.getElementById('avatar-chat-attach');
    var drawButton = document.getElementById('avatar-chat-draw');
    var input = document.getElementById('avatar-chat-input');
    var usersPanelRoom = currentPublicChannel;
    var panelUsers = Array.isArray(users) ? users.slice() : [];
    var usersPanelRequestToken = 0;
    var pendingAnsiUpload = null;
    var ansiUploadBusy = false;
    var ansiUploadMessage = '';
    var ansiUploadMessageType = '';
    var inputDefaultPlaceholder = input && input.getAttribute('placeholder')
        ? input.getAttribute('placeholder')
        : 'Type a message...';

    function trimText(value) {
        return String(value || '').replace(/^\s+|\s+$/g, '');
    }

    function normalizeUpper(value) {
        return trimText(value).toUpperCase();
    }

    function sanitizeChannelName(raw) {
        var channel = String(raw || 'main').replace(/[^a-zA-Z0-9_-]/g, '');
        return channel.length ? channel : 'main';
    }

    function normalizeChannelKey(value) {
        return String(value || '').replace(/[^a-zA-Z0-9_-]/g, '').toUpperCase();
    }

    function isMotdChannelName(value) {
        return normalizeChannelKey(value) === normalizeChannelKey(pageConfig.motdChannel || 'motd');
    }

    function getDefaultPublicChannel() {
        var fallback = sanitizeChannelName(pageConfig.defaultChannel || 'main');

        if (isMotdChannelName(fallback) && !pageConfig.canManageMotd) {
            return 'main';
        }

        return fallback;
    }

    function sanitizeAlias(raw) {
        return trimText(String(raw || '')).replace(/[\x00-\x1f]/g, '').substr(0, 60);
    }

    function isBitmapMessageText(text) {
        var value = String(text || '');
        return value.indexOf('[BITMAP|') === 0 && value.charAt(value.length - 1) === ']';
    }

    function parseBitmapMessage(text) {
        var value = String(text || '');
        var inner = '';
        var parts = [];
        var width = 0;
        var height = 0;

        if (!isBitmapMessageText(value)) {
            return null;
        }

        inner = value.slice(1, -1);
        parts = inner.split('|');

        if (parts.length !== 5 || parts[0] !== 'BITMAP') {
            return null;
        }

        width = parseInt(parts[1] || '', 10) || 0;
        height = parseInt(parts[2] || '', 10) || 0;

        if (width < 1 || height < 1 || !(parts[4] || '').length || ((parts[4] || '').length % 2) !== 0) {
            return null;
        }

        return {
            width: width,
            height: height,
            fromName: parts[3] || '',
            hexData: parts[4] || ''
        };
    }

    function buildBitmapPreview(parsed) {
        if (!parsed) {
            return '[image]';
        }

        return '[image ' + String(parsed.width || 0) + 'x' + String(parsed.height || 0) + ']';
    }

    function buildMessagePreview(text) {
        var parsed = parseBitmapMessage(text);

        if (parsed) {
            return buildBitmapPreview(parsed);
        }

        return String(text || '');
    }

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

    function readFileBytes(file) {
        return new Promise(function (resolve, reject) {
            var reader = new FileReader();

            reader.onload = function () {
                resolve(new Uint8Array(reader.result));
            };
            reader.onerror = function () {
                reject(new Error('Unable to read the ANSI file.'));
            };
            reader.readAsArrayBuffer(file);
        });
    }

    function buildBitmapFromName() {
        var alias = trimText(pageConfig.userAlias || '');
        var systemName = trimText(pageConfig.systemName || '');

        if (systemName.length && alias.length) {
            return systemName + ':' + alias;
        }
        return alias || systemName || '';
    }

    /* The composer has no status area of its own: what an attachment is
       doing shows as the message box's placeholder, and errors go to the
       status bar. A prepared ANSI file holds the box (read-only) until Send
       posts it or Esc drops it. */
    function renderAnsiUploadState() {
        var hasUpload = !!pendingAnsiUpload;
        var placeholder = inputDefaultPlaceholder;

        if (ansiUploadBusy) {
            placeholder = ansiUploadMessage || 'Working...';
        } else if (hasUpload) {
            placeholder = pendingAnsiUpload.fileName + ' (' + pendingAnsiUpload.width + 'x' + pendingAnsiUpload.height +
                ') ready: Send posts it, Esc drops it';
        }
        if (attachButton) {
            attachButton.disabled = ansiUploadBusy;
        }
        if (drawButton) {
            drawButton.disabled = ansiUploadBusy;
        }
        if (fileInput) {
            fileInput.disabled = ansiUploadBusy;
        }
        if (form) {
            form.classList.toggle('is-busy', ansiUploadBusy);
            form.classList.toggle('has-art', hasUpload);
        }
        if (input) {
            input.disabled = ansiUploadBusy;
            input.readOnly = hasUpload;
            input.placeholder = placeholder;
            input.title = hasUpload || ansiUploadBusy ? placeholder : '';
        }
    }

    function clearPendingAnsiUpload() {
        pendingAnsiUpload = null;
        ansiUploadBusy = false;
        ansiUploadMessage = '';
        ansiUploadMessageType = '';
        if (fileInput) {
            fileInput.value = '';
        }
        renderAnsiUploadState();
    }

    function queueAnsiUpload(file) {
        var safeName = trimText(file && file.name ? file.name : 'ansi.ans');

        if (!file) {
            return;
        }

        ansiUploadBusy = true;
        pendingAnsiUpload = null;
        ansiUploadMessage = 'Encoding ' + safeName + '...';
        ansiUploadMessageType = 'busy';
        renderAnsiUploadState();

        readFileBytes(file).then(function (bytes) {
            var prepared = prepareAnsiBitmap(bytes);

            return buildBitmapPayload(prepared.cells, prepared.width, prepared.height, buildBitmapFromName()).then(function (payload) {
                return {
                    fileName: safeName || 'ansi.ans',
                    width: prepared.width,
                    height: prepared.height,
                    payload: payload
                };
            });
        }).then(function (upload) {
            pendingAnsiUpload = upload;
            ansiUploadBusy = false;
            ansiUploadMessage = '';
            ansiUploadMessageType = 'ready';
            renderAnsiUploadState();
            if (input) {
                input.focus();
            }
        }).catch(function (error) {
            pendingAnsiUpload = null;
            ansiUploadBusy = false;
            ansiUploadMessage = '';
            ansiUploadMessageType = '';
            renderAnsiUploadState();
            setStatus('error', error && error.message ? String(error.message) : 'Unable to encode the ANSI file.', false);
        });
    }

    function buildBitmapKey(text) {
        var value = String(text || '');
        var hash = 5381;
        var index = 0;
        var key;
        var suffix = 1;
        var base;

        for (index = 0; index < value.length; index += 1) {
            hash = (((hash << 5) + hash) + value.charCodeAt(index)) >>> 0;
        }

        base = 'bmp-' + hash.toString(16) + '-' + String(value.length);
        key = base;

        while (bitmapRecords[key] && bitmapRecords[key].sourceText !== value) {
            suffix += 1;
            key = base + '-' + String(suffix);
        }

        return key;
    }

    function ensureBitmapRecord(text) {
        var parsed = parseBitmapMessage(text);
        var key;

        if (!parsed) {
            return null;
        }

        key = buildBitmapKey(text);
        if (!bitmapRecords[key]) {
            bitmapRecords[key] = {
                key: key,
                sourceText: String(text || ''),
                fromName: parsed.fromName || '',
                width: parsed.width || 0,
                height: parsed.height || 0,
                actualWidth: 0,
                actualHeight: 0,
                previewText: buildBitmapPreview(parsed),
                bitmap: null,
                dataURL: '',
                renderPending: false,
                error: ''
            };
        }

        return bitmapRecords[key];
    }

    function copyOwnProperties(source) {
        var target = {};
        var key;

        if (!source) {
            return target;
        }

        for (key in source) {
            if (Object.prototype.hasOwnProperty.call(source, key)) {
                target[key] = source[key];
            }
        }

        return target;
    }

    function normalizeThreadSummary(summary) {
        var next = copyOwnProperties(summary);

        if (typeof next.preview === 'string' && next.preview.length) {
            next.preview = buildMessagePreview(next.preview);
        }

        return next;
    }

    function normalizeMessage(message) {
        var next = copyOwnProperties(message);
        var text = typeof next.text === 'string' ? next.text : '';
        var record;

        next.text = text;
        next.previewText = buildMessagePreview(text);
        next.kind = 'text';
        rememberMailbox(next.sender, next.system, next.mailbox);
        rememberMailbox(next.peerName, next.peerSystem, next.peerMailbox);

        record = ensureBitmapRecord(text);
        if (record) {
            next.kind = 'bitmap';
            next.bitmapKey = record.key;
            next.bitmapWidth = record.width || 0;
            next.bitmapHeight = record.height || 0;
            next.bitmapFromName = record.fromName || '';
            next.previewText = record.previewText;
        }

        return next;
    }

    /* Where a PM to a standalone web user goes (their name@origin mailbox,
       re-checked by the server), keyed like threads. Data only: never put
       on screen. (mailboxes is declared with the other state at the top.) */

    function rememberMailbox(name, system, mailbox) {
        if (name && typeof mailbox === 'string' && mailbox.length) {
            mailboxes[buildThreadKey(name, system || '')] = mailbox;
        }
    }

    function mailboxFor(name, system) {
        return mailboxes[buildThreadKey(name, system || '')] || '';
    }

    function normalizeMessages(list) {
        return (list || []).map(function (message) {
            return normalizeMessage(message);
        });
    }

    function buildNameKey(name) {
        return normalizeUpper(name).replace(/[^A-Z0-9]/g, '');
    }

    function buildThreadKey(name, system) {
        return buildNameKey(name) + '|' + normalizeUpper(system);
    }

    function currentPrivateThreadKey() {
        if (activeView.type !== 'private') {
            return '';
        }
        return buildThreadKey(activeView.name, activeView.system || '');
    }

    function buildXtermPalette() {
        var palette = [
            '#000000', '#800000', '#008000', '#808000',
            '#000080', '#800080', '#008080', '#c0c0c0',
            '#808080', '#ff0000', '#00ff00', '#ffff00',
            '#0000ff', '#ff00ff', '#00ffff', '#ffffff'
        ];
        var cube = [0, 95, 135, 175, 215, 255];
        var r = 0;
        var g = 0;
        var b = 0;
        var index = 0;

        function componentToHex(value) {
            var out = value.toString(16);
            return out.length < 2 ? ('0' + out) : out;
        }

        for (r = 0; r < 6; r += 1) {
            for (g = 0; g < 6; g += 1) {
                for (b = 0; b < 6; b += 1) {
                    palette.push('#' + componentToHex(cube[r]) + componentToHex(cube[g]) + componentToHex(cube[b]));
                }
            }
        }

        for (index = 0; index < 24; index += 1) {
            var level = 8 + (index * 10);
            var hex = componentToHex(level);
            palette.push('#' + hex + hex + hex);
        }

        return palette;
    }

    function clampColor256(value) {
        var num = Number(value);

        if (!isFinite(num)) {
            return 0;
        }
        if (num < 0) {
            return 0;
        }
        if (num > 255) {
            return 255;
        }

        return num | 0;
    }

    function color256ToCss(value) {
        return XTERM_COLORS[clampColor256(value)] || XTERM_COLORS[0];
    }

    function hexToBytes(hex) {
        var bytes = [];
        var index = 0;

        for (index = 0; index < hex.length; index += 2) {
            bytes.push(parseInt(hex.substr(index, 2), 16) || 0);
        }

        return bytes;
    }

    function createInflateState(bytes, offset) {
        return {
            bytes: bytes,
            position: offset,
            bitBuffer: 0,
            bitCount: 0
        };
    }

    function readByte(state) {
        var value = state.bytes[state.position];
        state.position += 1;
        return value === undefined ? 0 : value;
    }

    function readBits(state, count) {
        var buffer = state.bitBuffer;
        var available = state.bitCount;
        var out = 0;

        while (available < count) {
            buffer |= readByte(state) << available;
            available += 8;
        }

        out = buffer & ((1 << count) - 1);
        state.bitBuffer = buffer >>> count;
        state.bitCount = available - count;
        return out;
    }

    function alignByte(state) {
        state.bitBuffer = 0;
        state.bitCount = 0;
    }

    function reverseBits(value, count) {
        var result = 0;
        var index = 0;

        for (index = 0; index < count; index += 1) {
            result = (result << 1) | (value & 1);
            value >>= 1;
        }

        return result;
    }

    function buildHuffmanTable(codeLengths) {
        var table = {
            maxBits: 0,
            map: {}
        };
        var counts = [];
        var nextCodes = [];
        var code = 0;
        var index = 0;

        for (index = 0; index < codeLengths.length; index += 1) {
            if ((codeLengths[index] || 0) > table.maxBits) {
                table.maxBits = codeLengths[index] || 0;
            }
        }

        for (index = 0; index <= table.maxBits; index += 1) {
            counts[index] = 0;
        }

        for (index = 0; index < codeLengths.length; index += 1) {
            counts[codeLengths[index] || 0] = (counts[codeLengths[index] || 0] || 0) + 1;
        }

        counts[0] = 0;
        for (index = 1; index <= table.maxBits; index += 1) {
            code = (code + (counts[index - 1] || 0)) << 1;
            nextCodes[index] = code;
        }

        for (index = 0; index < codeLengths.length; index += 1) {
            var length = codeLengths[index] || 0;
            var nextCode;
            var key;

            if (!length) {
                continue;
            }

            nextCode = nextCodes[length] || 0;
            key = String(reverseBits(nextCode, length) | (length << 16));
            table.map[key] = index;
            nextCodes[length] = nextCode + 1;
        }

        return table;
    }

    function readHuffmanCode(table, state) {
        var code = 0;
        var length = 0;
        var key;

        for (length = 1; length <= table.maxBits; length += 1) {
            code |= readBits(state, 1) << (length - 1);
            key = String(code | (length << 16));
            if (table.map[key] !== undefined) {
                return table.map[key] || 0;
            }
        }

        throw new Error('Huffman decode failed');
    }

    function buildFixedLiteralTable() {
        var lengths = [];
        var index = 0;

        for (index = 0; index <= 287; index += 1) {
            lengths[index] = 0;
        }
        for (index = 0; index <= 143; index += 1) {
            lengths[index] = 8;
        }
        for (index = 144; index <= 255; index += 1) {
            lengths[index] = 9;
        }
        for (index = 256; index <= 279; index += 1) {
            lengths[index] = 7;
        }
        for (index = 280; index <= 287; index += 1) {
            lengths[index] = 8;
        }

        return buildHuffmanTable(lengths);
    }

    function buildFixedDistanceTable() {
        var lengths = [];
        var index = 0;

        for (index = 0; index < 32; index += 1) {
            lengths[index] = 5;
        }

        return buildHuffmanTable(lengths);
    }

    function decodeDynamicTables(state) {
        var hlit = readBits(state, 5) + 257;
        var hdist = readBits(state, 5) + 1;
        var hclen = readBits(state, 4) + 4;
        var order = [16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15];
        var codeLengths = [];
        var index = 0;
        var codeTable;

        for (index = 0; index < 19; index += 1) {
            codeLengths[index] = 0;
        }

        for (index = 0; index < hclen; index += 1) {
            codeLengths[order[index] || 0] = readBits(state, 3);
        }

        codeTable = buildHuffmanTable(codeLengths);

        function readLengths(count) {
            var out = [];
            var previous = 0;

            while (out.length < count) {
                var symbol = readHuffmanCode(codeTable, state);
                var repeat = 0;
                var repeatIndex = 0;

                if (symbol <= 15) {
                    out.push(symbol);
                    previous = symbol;
                    continue;
                }

                if (symbol === 16) {
                    repeat = 3 + readBits(state, 2);
                    for (repeatIndex = 0; repeatIndex < repeat; repeatIndex += 1) {
                        out.push(previous);
                    }
                    continue;
                }

                if (symbol === 17) {
                    repeat = 3 + readBits(state, 3);
                    previous = 0;
                    for (repeatIndex = 0; repeatIndex < repeat; repeatIndex += 1) {
                        out.push(0);
                    }
                    continue;
                }

                if (symbol === 18) {
                    repeat = 11 + readBits(state, 7);
                    previous = 0;
                    for (repeatIndex = 0; repeatIndex < repeat; repeatIndex += 1) {
                        out.push(0);
                    }
                    continue;
                }

                throw new Error('Bad RLE in code lengths');
            }

            return out;
        }

        return {
            lit: buildHuffmanTable(readLengths(hlit)),
            dist: buildHuffmanTable(readLengths(hdist))
        };
    }

    function inflateRaw(bytes, start) {
        var state = createInflateState(bytes, start);
        var output = [];
        var fixedLiterals = buildFixedLiteralTable();
        var fixedDistances = buildFixedDistanceTable();
        var done = false;

        while (!done) {
            var isFinal = readBits(state, 1);
            var blockType = readBits(state, 2);
            var literalTable = null;
            var distanceTable = null;
            var length = 0;
            var notLength = 0;
            var index = 0;

            if (blockType === 0) {
                alignByte(state);
                length = readByte(state) | (readByte(state) << 8);
                notLength = readByte(state) | (readByte(state) << 8);
                if ((length ^ 65535) !== notLength) {
                    throw new Error('Stored block length mismatch');
                }
                for (index = 0; index < length; index += 1) {
                    output.push(readByte(state));
                }
            } else {
                if (blockType === 1) {
                    literalTable = fixedLiterals;
                    distanceTable = fixedDistances;
                } else if (blockType === 2) {
                    var tables = decodeDynamicTables(state);
                    literalTable = tables.lit;
                    distanceTable = tables.dist;
                } else {
                    throw new Error('Invalid DEFLATE block type');
                }

                while (literalTable && distanceTable) {
                    var symbol = readHuffmanCode(literalTable, state);
                    var lengthIndex;
                    var distanceSymbol;
                    var distance;
                    var base;
                    var copyIndex;

                    if (symbol < 256) {
                        output.push(symbol);
                        continue;
                    }
                    if (symbol === 256) {
                        break;
                    }

                    lengthIndex = symbol - 257;
                    length = (LENGTH_BASE[lengthIndex] || 0) + ((LENGTH_EXTRA[lengthIndex] || 0) ? readBits(state, LENGTH_EXTRA[lengthIndex] || 0) : 0);
                    distanceSymbol = readHuffmanCode(distanceTable, state);
                    distance = (DISTANCE_BASE[distanceSymbol] || 0) + ((DISTANCE_EXTRA[distanceSymbol] || 0) ? readBits(state, DISTANCE_EXTRA[distanceSymbol] || 0) : 0);
                    base = output.length - distance;

                    if (base < 0) {
                        throw new Error('Invalid DEFLATE distance');
                    }

                    for (copyIndex = 0; copyIndex < length; copyIndex += 1) {
                        output.push(output[base + copyIndex] || 0);
                    }
                }
            }

            if (isFinal) {
                done = true;
            }
        }

        return output;
    }

    function inflateZlib(bytes, offset) {
        var position = offset || 0;
        var cmf = bytes[position] || 0;
        var flg = bytes[position + 1] || 0;

        position += 2;
        if ((cmf & 15) !== 8) {
            throw new Error('Unsupported zlib compression method');
        }
        if (flg & 32) {
            position += 4;
        }

        return inflateRaw(bytes, position);
    }

    function decodeBitmap(hexData, expectedWidth, expectedHeight) {
        var compressed = hexToBytes(hexData);
        var decompressed = inflateZlib(compressed, 0);
        var bitmap = [];
        var dataHeight = 0;
        var dataLength = 0;
        var slicePoint = 0;
        var totalPixels = 0;
        var dataWidth = 0;
        var width = 0;
        var height = 0;
        var index = 0;

        if (decompressed.length < 4) {
            return {
                bitmap: bitmap,
                width: 0,
                height: 0,
                actualWidth: 0,
                actualHeight: 0
            };
        }

        dataHeight = decompressed[0] || 0;
        if (dataHeight < 1) {
            return {
                bitmap: bitmap,
                width: 0,
                height: 0,
                actualWidth: 0,
                actualHeight: 0
            };
        }

        dataLength = decompressed.length - 1;
        slicePoint = Math.floor(dataLength / 3);
        totalPixels = slicePoint;
        dataWidth = Math.floor(totalPixels / dataHeight);
        width = expectedWidth || dataWidth;
        height = expectedHeight || dataHeight;

        if (totalPixels < 1 || totalPixels > MAX_BITMAP_CELLS) {
            throw new Error('Bitmap exceeds supported size');
        }

        if (width * height !== totalPixels) {
            width = dataWidth;
            height = dataHeight;
        }

        for (index = 0; index < totalPixels; index += 1) {
            bitmap.push({
                charCode: decompressed[1 + slicePoint * 2 + index] || 32,
                fg: decompressed[1 + index] || 0,
                bg: decompressed[1 + slicePoint + index] || 0
            });
        }

        return {
            bitmap: bitmap,
            width: width,
            height: height,
            actualWidth: dataWidth,
            actualHeight: dataHeight
        };
    }

    function decodeBitmapRecord(record) {
        var parsed;
        var decoded;

        if (!record) {
            return null;
        }

        if (record.bitmap && record.width > 0 && record.height > 0) {
            return record;
        }

        parsed = parseBitmapMessage(record.sourceText);
        if (!parsed) {
            throw new Error('Invalid bitmap payload');
        }

        decoded = decodeBitmap(parsed.hexData, parsed.width, parsed.height);
        if (!decoded.bitmap.length || !decoded.width || !decoded.height) {
            throw new Error('Decoded bitmap was empty');
        }

        record.bitmap = decoded.bitmap;
        record.width = decoded.width || parsed.width || 0;
        record.height = decoded.height || parsed.height || 0;
        record.actualWidth = decoded.actualWidth || record.width;
        record.actualHeight = decoded.actualHeight || record.height;
        record.previewText = buildBitmapPreview({ width: record.width, height: record.height });
        return record;
    }

    function escapeHtml(text) {
        var div = document.createElement('div');
        div.appendChild(document.createTextNode(text || ''));
        return div.innerHTML;
    }

    function escapeAttr(text) {
        return escapeHtml(text || '').replace(/"/g, '&quot;');
    }

    function rebuildOnlinePresence(entries) {
        var nextKeys = {};
        var nextNames = {};

        (entries || []).forEach(function (entry) {
            var name = sanitizeAlias(entry && (entry.nick || entry.name) || '');
            var systemName = trimText(entry && entry.system || '');
            var nameKey = buildNameKey(name);

            if (!nameKey.length) {
                return;
            }

            nextKeys[buildThreadKey(name, systemName)] = true;
            nextNames[nameKey] = true;
        });

        onlinePeerKeys = nextKeys;
        onlinePeerNames = nextNames;
        renderPrivateList();
    }

    function isThreadOnline(name, system) {
        var exactKey = buildThreadKey(name, system || '');
        var nameKey = buildNameKey(name);

        if (onlinePeerKeys[exactKey]) {
            return true;
        }

        if (!trimText(system).length && onlinePeerNames[nameKey]) {
            return true;
        }

        return false;
    }

    /* ---- Rich text: links, inline pictures, players, link cards ----
       The same rules as the door (internal/inlineimg, internal/linkprev):
       a link is http(s) up to whitespace, quotes, angle brackets or a pipe,
       trailing punctuation trimmed; a picture is a link whose path ends in an
       image extension; the first other link that isn't media gets a card,
       which this BBS fetches (./api/avatarchat.ssjs?action=preview). */
    var URL_PATTERN = /https?:\/\/[^\s<>"'\x01|]+/gi;
    var URL_TRAILING = /[).,;:!?]+$/;
    var IMAGE_EXT = /\.(png|jpe?g|gif|webp|bmp)$/i;
    var AUDIO_EXT = /\.(mp3|ogg|oga|opus|wav|flac|m4a|aac)$/i;
    var VIDEO_EXT = /\.(mp4|webm|mov|m4v)$/i;
    var ANSI_EXT = /\.(ans|asc)$/i;
    var MEDIA_PATH = /\/chatmedia\/\d{4}-\d{2}-\d{2}\/[a-f0-9]{24}\.[a-z0-9]{1,4}$/i;
    var MAX_INLINE_IMAGES = 3;
    var cardCache = {};
    var cardWaiting = {};

    function urlPath(url) {
        return String(url).replace(/^https?:\/\/[^\/?#]*/i, '').replace(/[?#].*$/, '');
    }

    function findUrls(text) {
        var out = [];
        var seen = {};
        String(text || '').replace(URL_PATTERN, function (match) {
            var url = match.replace(URL_TRAILING, '');
            if (url.length && !seen[url] && /^https?:\/\/[^\/?#@]+/i.test(url)) {
                seen[url] = true;
                out.push(url);
            }
            return match;
        });
        return out;
    }

    function looksNsfw(text) {
        var words = pageConfig.nsfwKeywords || [];
        var lower = String(text || '').toLowerCase();
        var i;
        for (i = 0; i < words.length; i += 1) {
            if (new RegExp('(^|[^a-z0-9])' + words[i].replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '([^a-z0-9]|$)').test(lower)) {
                return true;
            }
        }
        return false;
    }

    /* Escaped text with its links made clickable. Splitting on the pattern
       (rather than rewriting escaped HTML) keeps a quote in a URL from
       ever reaching an attribute. */
    function linkify(text) {
        var src = String(text || '');
        var html = '';
        var last = 0;
        src.replace(URL_PATTERN, function (match, offset) {
            var url = match.replace(URL_TRAILING, '');
            html += escapeHtml(src.substring(last, offset));
            html += '<a href="' + escapeAttr(url) + '" target="_blank" rel="noopener noreferrer">' + escapeHtml(url) + '</a>';
            last = offset + url.length;
            return match;
        });
        return html + escapeHtml(src.substring(last));
    }

    function renderRichText(text) {
        var html = '<div class="avatar-chat-rich-text">' + linkify(text) + '</div>';
        var urls = findUrls(text);
        var veiled = looksNsfw(text);
        var pictures = 0;
        var card = '';
        var media = '';

        urls.forEach(function (url) {
            var path = urlPath(url);
            if (IMAGE_EXT.test(path)) {
                if (!pageConfig.images || pictures >= MAX_INLINE_IMAGES) {
                    return;
                }
                pictures += 1;
                media += '<a class="avatar-chat-rich-image' + (veiled || looksNsfw(url) ? ' is-veiled' : '') + '" href="' + escapeAttr(url) + '" target="_blank" rel="noopener noreferrer">' +
                    '<img src="' + escapeAttr(url) + '" alt="" loading="lazy" referrerpolicy="no-referrer"></a>';
            } else if (AUDIO_EXT.test(path)) {
                media += '<audio class="avatar-chat-rich-audio" controls preload="none" src="' + escapeAttr(url) + '"></audio>';
            } else if (VIDEO_EXT.test(path)) {
                media += '<video class="avatar-chat-rich-video" controls preload="none" src="' + escapeAttr(url) + '"></video>';
            } else if (!card.length && pageConfig.linkPreviews && !ANSI_EXT.test(path) && !MEDIA_PATH.test(url) && url.length <= 2048) {
                card = '<div class="avatar-chat-card" data-card-url="' + escapeAttr(url) + '"></div>';
            }
        });

        return html + (media.length ? '<div class="avatar-chat-rich-media">' + media + '</div>' : '') + card;
    }

    function cardHtml(url, data) {
        var html = '';
        if (!data || data.error || !(data.title || data.description || data.image || data.audio || data.video)) {
            return '';
        }
        if (data.image && /^https?:\/\//i.test(data.image)) {
            html += '<img class="avatar-chat-card-image' + (looksNsfw(data.title + ' ' + data.description) ? ' is-veiled' : '') + '" src="' + escapeAttr(data.image) + '" alt="" loading="lazy" referrerpolicy="no-referrer">';
        }
        html += '<div class="avatar-chat-card-body">';
        if (data.siteName) {
            html += '<div class="avatar-chat-card-site">' + escapeHtml(data.siteName) + '</div>';
        }
        html += '<a class="avatar-chat-card-title" href="' + escapeAttr(url) + '" target="_blank" rel="noopener noreferrer">' + escapeHtml(data.title || url) + '</a>';
        if (data.description) {
            html += '<div class="avatar-chat-card-text">' + escapeHtml(data.description) + '</div>';
        }
        if (data.audio && /^https?:\/\//i.test(data.audio)) {
            html += '<audio class="avatar-chat-rich-audio" controls preload="none" src="' + escapeAttr(data.audio) + '"></audio>';
        }
        if (data.video && /^https?:\/\//i.test(data.video)) {
            html += '<video class="avatar-chat-rich-video" controls preload="none" src="' + escapeAttr(data.video) + '"></video>';
        }
        return html + '</div>';
    }

    function fillCards(root, url) {
        var html = cardHtml(url, cardCache[url]);
        root.querySelectorAll('.avatar-chat-card[data-card-url]').forEach(function (el) {
            if (el.getAttribute('data-card-url') !== url) {
                return;
            }
            el.removeAttribute('data-card-url');
            if (html.length) {
                el.innerHTML = html;
                el.classList.add('is-ready');
            } else {
                el.remove();
            }
        });
    }

    function hydrateRichText(root) {
        root.querySelectorAll('.avatar-chat-card[data-card-url]').forEach(function (el) {
            var url = el.getAttribute('data-card-url');
            if (Object.prototype.hasOwnProperty.call(cardCache, url)) {
                fillCards(root, url);
                return;
            }
            if (cardWaiting[url]) {
                return;
            }
            cardWaiting[url] = true;
            fetchJSON(apiBase + '?action=preview&url=' + encodeURIComponent(url)).then(function (data) {
                cardCache[url] = data;
            }).catch(function () {
                cardCache[url] = { error: 'unavailable' };
            }).then(function () {
                delete cardWaiting[url];
                fillCards(messageContainer, url);
            });
        });
    }

    /* ---- Handle styles and mentions ---- */
    var HEX_COLOR = /^#[0-9a-f]{6}$/i;

    function renderHandle(name, style) {
        var text = String(name || '');
        var colors = style && Array.isArray(style.colors) ? style.colors : [];
        var html = '<strong class="avatar-chat-handle">';
        var i;
        if (!colors.length) {
            html += escapeHtml(text);
        } else {
            for (i = 0; i < text.length; i += 1) {
                if (HEX_COLOR.test(colors[i] || '')) {
                    html += '<span style="color:' + colors[i] + '">' + escapeHtml(text.charAt(i)) + '</span>';
                } else {
                    html += escapeHtml(text.charAt(i));
                }
            }
        }
        html += '</strong>';
        return html;
    }

    /* The signed-in user's alias as a whole word, optional @, any case. */
    function mentionsMe(text) {
        var me = String(pageConfig.userAlias || '');
        if (!me.length) {
            return false;
        }
        return new RegExp('(^|[^a-z0-9])@?' + me.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '([^a-z0-9]|$)', 'i').test(String(text || ''));
    }

    function formatTime(timestamp) {
        if (!timestamp) {
            return '';
        }
        var date = new Date(timestamp);
        var hours = date.getHours();
        var minutes = date.getMinutes();
        var ampm = hours >= 12 ? 'PM' : 'AM';

        hours = hours % 12 || 12;
        return String(hours) + ':' + (minutes < 10 ? '0' : '') + String(minutes) + ' ' + ampm;
    }

    /* Every write carries the page's token (the API refuses it otherwise);
       an error reply's message is surfaced rather than a bare status. */
    function fetchJSON(url, options) {
        var opts = options || {};
        opts.credentials = 'same-origin';
        if (opts.method === 'POST') {
            opts.headers = opts.headers || {};
            opts.headers['X-AvatarChat-Token'] = pageConfig.token || '';
        }
        return fetch(url, opts).then(function (response) {
            if (!response.ok) {
                return response.json().catch(function () { return {}; }).then(function (body) {
                    throw new Error(body && body.error ? String(body.error) : 'HTTP ' + String(response.status));
                });
            }
            return response.json();
        });
    }

    function loadScript(src, done) {
        var script = document.createElement('script');
        script.src = src;
        script.onload = done;
        script.onerror = done;
        document.head.appendChild(script);
    }

    function ensureDependencies(callback) {
        function done() {
            graphicsReady = typeof GraphicsConverter !== 'undefined';
            avatarsReady = typeof Avatars !== 'undefined' && typeof Avatars.draw === 'function';
            if (graphicsReady && !converter) {
                converter = new GraphicsConverter('./images/cp437-ibm-vga8.png', 8, 16, 64, 4);
            }
            callback();
        }

        if (typeof GraphicsConverter === 'undefined') {
            loadScript('./js/graphics-converter.js', function () {
                if (typeof Avatars === 'undefined') {
                    loadScript('./js/avatars.js', done);
                    return;
                }
                done();
            });
            return;
        }

        if (typeof Avatars === 'undefined') {
            loadScript('./js/avatars.js', done);
            return;
        }

        done();
    }

    function ensureBitmapSpritesheet(callback) {
        var callbacks;
        var img;

        if (bitmapSpritesheet && bitmapSpritesheetContext) {
            callback(true);
            return;
        }

        if (bitmapSpritesheetLoadFailed) {
            callback(false);
            return;
        }

        bitmapSpritesheetCallbacks.push(callback);
        if (bitmapSpritesheetLoading) {
            return;
        }

        bitmapSpritesheetLoading = true;
        img = new Image();
        img.onload = function () {
            bitmapSpritesheet = img;
            bitmapSpritesheetCanvas = document.createElement('canvas');
            bitmapSpritesheetCanvas.width = img.width;
            bitmapSpritesheetCanvas.height = img.height;
            bitmapSpritesheetContext = bitmapSpritesheetCanvas.getContext('2d', { willReadFrequently: true });
            bitmapSpritesheetContext.drawImage(img, 0, 0);
            bitmapGlyphCache = {};
            bitmapSpritesheetLoading = false;
            callbacks = bitmapSpritesheetCallbacks.slice();
            bitmapSpritesheetCallbacks = [];
            callbacks.forEach(function (fn) {
                fn(true);
            });
        };
        img.onerror = function () {
            bitmapSpritesheetLoading = false;
            bitmapSpritesheetLoadFailed = true;
            callbacks = bitmapSpritesheetCallbacks.slice();
            bitmapSpritesheetCallbacks = [];
            callbacks.forEach(function (fn) {
                fn(false);
            });
        };
        img.src = './images/cp437-ibm-vga8.png';
    }

    function getBitmapGlyph(charCode) {
        var key = String(charCode & 0xff);
        var x = 0;
        var y = 0;

        if (!bitmapGlyphCache[key]) {
            x = ((charCode & 0xff) * BITMAP_FONT_WIDTH) % (BITMAP_FONT_WIDTH * BITMAP_SPRITESHEET_COLUMNS);
            y = BITMAP_FONT_HEIGHT * Math.floor((charCode & 0xff) / BITMAP_SPRITESHEET_COLUMNS);
            bitmapGlyphCache[key] = bitmapSpritesheetContext.getImageData(x, y, BITMAP_FONT_WIDTH, BITMAP_FONT_HEIGHT);
        }

        return bitmapGlyphCache[key];
    }

    function putBitmapGlyph(ctx, glyph, x, y, fg, bg) {
        ctx.globalCompositeOperation = 'source-over';
        ctx.putImageData(glyph, x, y);
        ctx.fillStyle = fg;
        ctx.globalCompositeOperation = 'source-atop';
        ctx.fillRect(x, y, BITMAP_FONT_WIDTH, BITMAP_FONT_HEIGHT);
        ctx.globalCompositeOperation = 'destination-over';
        ctx.fillStyle = bg;
        ctx.fillRect(x, y, BITMAP_FONT_WIDTH, BITMAP_FONT_HEIGHT);
    }

    function renderBitmapCells(cells, cols, rows, callback) {
        ensureBitmapSpritesheet(function (ready) {
            var canvas;
            var ctx;
            var total;
            var index = 0;

            if (!ready) {
                callback('');
                return;
            }

            canvas = document.createElement('canvas');
            canvas.width = cols * BITMAP_FONT_WIDTH;
            canvas.height = rows * BITMAP_FONT_HEIGHT;
            ctx = canvas.getContext('2d', { willReadFrequently: true });
            ctx.fillStyle = '#000000';
            ctx.fillRect(0, 0, canvas.width, canvas.height);

            total = cols * rows;
            for (index = 0; index < total; index += 1) {
                var cell = Array.isArray(cells) ? (cells[index] || null) : null;
                var charCode = cell && typeof cell.charCode === 'number' ? (cell.charCode & 0xff) : 32;
                var x = (index % cols) * BITMAP_FONT_WIDTH;
                var y = Math.floor(index / cols) * BITMAP_FONT_HEIGHT;

                putBitmapGlyph(
                    ctx,
                    getBitmapGlyph(charCode),
                    x,
                    y,
                    color256ToCss(cell && cell.fg),
                    color256ToCss(cell && cell.bg)
                );
            }

            callback(canvas.toDataURL());
        });
    }

    function updateBitmapElement(el, record) {
        var text;
        var placeholder;
        var img;

        if (!el) {
            return;
        }

        while (el.firstChild) {
            el.removeChild(el.firstChild);
        }

        el.classList.remove('is-loading', 'is-ready', 'is-error');
        text = el.getAttribute('data-avatar-chat-bitmap-alt') || (record && record.previewText) || '[image]';

        if (record && record.width > 0 && record.height > 0) {
            el.style.aspectRatio = String(record.width * BITMAP_FONT_WIDTH) + ' / ' + String(record.height * BITMAP_FONT_HEIGHT);
            if (
                el.parentNode &&
                el.parentNode.parentNode &&
                el.parentNode.parentNode.classList &&
                el.parentNode.parentNode.classList.contains('avatar-chat-bitmap-shell')
            ) {
                el.parentNode.parentNode.style.maxWidth = String(record.width * BITMAP_FONT_WIDTH) + 'px';
            }
        }

        if (record && record.dataURL) {
            img = new Image();
            img.className = 'avatar-chat-bitmap-image';
            img.alt = text;
            img.addEventListener('load', scrollToBottom);
            img.src = record.dataURL;
            el.classList.add('is-ready');
            el.appendChild(img);
            return;
        }

        placeholder = document.createElement('div');
        placeholder.className = 'avatar-chat-bitmap-placeholder';

        if (!record || record.error) {
            placeholder.textContent = 'Image unavailable';
            el.classList.add('is-error');
        } else {
            placeholder.textContent = 'Rendering ' + text + '...';
            el.classList.add('is-loading');
        }

        el.appendChild(placeholder);
    }

    function refreshBitmapElements(key, root) {
        var scope = root || document;
        var elements;

        if (!key) {
            return;
        }

        elements = scope.querySelectorAll('[data-avatar-chat-bitmap-key="' + key + '"]');
        elements.forEach(function (el) {
            updateBitmapElement(el, bitmapRecords[key] || null);
        });
    }

    function renderBitmapRecord(record) {
        if (!record || record.dataURL || record.renderPending || record.error) {
            return;
        }

        try {
            decodeBitmapRecord(record);
        } catch (err) {
            record.error = err && err.message ? err.message : 'Decode failed';
            refreshBitmapElements(record.key);
            return;
        }

        record.renderPending = true;
        renderBitmapCells(record.bitmap, record.width, record.height, function (dataURL) {
            record.renderPending = false;
            record.dataURL = dataURL || '';
            if (!record.dataURL && !record.error) {
                record.error = 'Render failed';
            }
            refreshBitmapElements(record.key);
        });
    }

    function renderEmbeddedBitmaps(root) {
        var scope = root || document;
        var elements = scope.querySelectorAll('[data-avatar-chat-bitmap-key]');

        if (!elements.length) {
            return;
        }

        elements.forEach(function (el) {
            var key = el.getAttribute('data-avatar-chat-bitmap-key');
            var record = key ? bitmapRecords[key] : null;

            updateBitmapElement(el, record);
            if (record && !record.dataURL && !record.renderPending && !record.error) {
                renderBitmapRecord(record);
            }
        });
    }

    function setStatus(type, message, showRetry) {
        if (!statusBar || !statusText) {
            return;
        }

        statusState.type = type || '';
        statusState.message = message || '';

        statusBar.classList.remove('is-info', 'is-warning', 'is-error', 'is-visible');
        if (!message) {
            statusText.textContent = '';
            if (statusRetry) {
                statusRetry.style.display = 'none';
            }
            return;
        }

        statusBar.classList.add('is-visible');
        if (type === 'warning') {
            statusBar.classList.add('is-warning');
        } else if (type === 'error') {
            statusBar.classList.add('is-error');
        } else {
            statusBar.classList.add('is-info');
        }

        statusText.textContent = message;
        if (statusRetry) {
            statusRetry.style.display = showRetry ? '' : 'none';
        }
    }

    function clearStatusIfMatch(type) {
        if (!statusState.message) {
            return;
        }
        if (!type || statusState.type === type) {
            setStatus('', '', false);
        }
    }

    function explainServiceError() {
        return 'Avatar Chat cannot reach the JSON service right now. Check avatar_chat.ini host/port and make sure the JSON service is running.';
    }

    function explainRealtimeError() {
        if (reconnectAttemptCount > 2) {
            return 'Realtime updates are still unavailable. Retrying in the background. Recent messages will continue syncing from history.';
        }
        return 'Realtime updates were interrupted. Retrying automatically while history continues to refresh in the background.';
    }

    function closeEventSource() {
        if (!eventSource) {
            return;
        }
        try { eventSource.close(); } catch (_closeError) {}
        eventSource = null;
    }

    function buildEventUrl() {
        var url = './api/events.ssjs?subscribe=avatarchat&channel=' + encodeURIComponent(currentPublicChannel);
        if (pageConfig.isLoggedIn) {
            url += '&mailbox=1';
        }
        return url;
    }

    function scheduleReconnect() {
        if (reconnectTimer) {
            return;
        }
        reconnectTimer = window.setTimeout(function () {
            reconnectTimer = 0;
            connectEvents(true);
        }, RECONNECT_DELAY_MS);
    }

    function scheduleHistoryRefresh() {
        if (sendRefreshTimer) {
            window.clearTimeout(sendRefreshTimer);
        }
        sendRefreshTimer = window.setTimeout(function () {
            sendRefreshTimer = 0;
            loadActiveHistory(true);
            loadRoomSummaries(true);
            loadPrivateThreads(true);
            loadMotd(true);
        }, 1000);
    }

    function updateQueryString() {
        var next = new URL(window.location.href);

        if (activeView.type === 'private') {
            next.searchParams.set('private', activeView.name);
            if (activeView.system) {
                next.searchParams.set('system', activeView.system);
            } else {
                next.searchParams.delete('system');
            }
            next.searchParams.set('channel', currentPublicChannel);
        } else {
            next.searchParams.set('channel', activeView.name);
            next.searchParams.delete('private');
            next.searchParams.delete('system');
        }

        window.history.replaceState({}, '', next.toString());
    }

    function findRoomSummary(name) {
        var key = normalizeUpper(name);
        var index = 0;

        for (index = 0; index < rooms.length; index += 1) {
            if (normalizeUpper(rooms[index].name) === key) {
                return rooms[index];
            }
        }

        return null;
    }

    function ensureRoomSummary(name) {
        var existing = findRoomSummary(name);

        if (existing) {
            return existing;
        }

        existing = {
            name: name,
            userCount: 0,
            lastTimestamp: 0,
            newCount: 0
        };
        rooms.push(existing);
        return existing;
    }

    function upsertPrivateThread(summary) {
        if (summary) {
            rememberMailbox(summary.name, summary.system, summary.mailbox);
        }
        summary = normalizeThreadSummary(summary);
        var key = buildThreadKey(summary.name, summary.system || '');
        var index = 0;

        for (index = 0; index < privateThreads.length; index += 1) {
            if (buildThreadKey(privateThreads[index].name, privateThreads[index].system || '') === key) {
                privateThreads[index].system = summary.system || privateThreads[index].system || '';
                privateThreads[index].avatar = summary.avatar || privateThreads[index].avatar;
                privateThreads[index].lastTimestamp = Math.max(privateThreads[index].lastTimestamp || 0, summary.lastTimestamp || 0);
                if (summary.preview) {
                    privateThreads[index].preview = summary.preview;
                }
                return privateThreads[index];
            }
        }

        privateThreads.push({
            name: summary.name,
            system: summary.system || '',
            avatar: summary.avatar || undefined,
            lastTimestamp: summary.lastTimestamp || 0,
            preview: summary.preview || ''
        });
        return privateThreads[privateThreads.length - 1];
    }

    function syncMotdTicker() {
        var primaryWidth = 0;
        var windowWidth = 0;
        var gap = 40;
        var distance = 0;
        var duration = 0;

        if (!motdBar || !motdWindow || !motdTrack || !motdPrimary || !motdClone) {
            return;
        }

        motdBar.classList.remove('is-scrolling', 'is-empty');
        motdTrack.style.removeProperty('--motd-scroll-width');
        motdTrack.style.removeProperty('--motd-duration');
        motdClone.style.display = 'none';

        if (!trimText(motdState.text || '').length) {
            motdBar.classList.add('is-empty');
            return;
        }

        primaryWidth = motdPrimary.scrollWidth || 0;
        windowWidth = motdWindow.clientWidth || 0;

        if (primaryWidth <= windowWidth + 8) {
            return;
        }

        distance = primaryWidth + gap;
        duration = Math.max(12, Math.round(distance / 42));
        motdClone.style.display = 'inline-block';
        motdBar.classList.add('is-scrolling');
        motdTrack.style.setProperty('--motd-scroll-width', String(distance) + 'px');
        motdTrack.style.setProperty('--motd-duration', String(duration) + 's');
    }

    function renderMotdBar() {
        var text = trimText(motdState.text || '');
        var primaryText = text.length ? text : 'No MOTD set.';
        var cloneText = text.length ? text : '';
        var signature = primaryText + '|' + cloneText + '|' + String(motdWindow ? motdWindow.clientWidth || 0 : 0);

        if (!motdPrimary || !motdClone) {
            return;
        }

        if (motdRenderSignature === signature) {
            return;
        }

        motdRenderSignature = signature;
        motdPrimary.textContent = primaryText;
        motdClone.textContent = cloneText;
        window.requestAnimationFrame(syncMotdTicker);
    }

    function shouldShowRoom(room) {
        if (!room || !room.name) {
            return false;
        }

        if (isMotdChannelName(room.name) && !pageConfig.canManageMotd) {
            return false;
        }

        return normalizeUpper(currentPublicChannel) === normalizeUpper(room.name) ||
            (room.userCount || 0) > 0 ||
            (unreadChannels[normalizeUpper(room.name)] || 0) > 0 ||
            (pageConfig.canManageMotd && isMotdChannelName(room.name));
    }

    function renderHeaderBar() {
        var count = 0;
        var summary = findRoomSummary(currentPublicChannel);
        var roomCount = rooms.filter(shouldShowRoom).length;

        if (channelLabel) {
            if (activeView.type === 'private') {
                channelLabel.textContent = '@' + activeView.name;
            } else {
                channelLabel.textContent = '#' + activeView.name;
            }
        }

        if (summary && typeof summary.userCount === 'number') {
            count = summary.userCount;
        } else if (Array.isArray(users)) {
            count = users.length;
        }

        if (countButton) {
            if (activeView.type === 'private') {
                countButton.textContent = 'Direct';
                countButton.disabled = true;
            } else {
                countButton.textContent = String(count) + ' user' + (count === 1 ? '' : 's');
                countButton.disabled = false;
            }
        }

        if (roomCountLabel) {
            roomCountLabel.textContent = 'rooms ' + String(roomCount > 0 ? roomCount : 1);
        }

        if (serverLabel) {
            serverLabel.textContent = String(pageConfig.server || '');
        }

        renderRoomActions();
        renderMotdBar();
    }

    function isJoined(name) {
        var key = normalizeUpper(name);
        var i;
        for (i = 0; i < joinedRooms.length; i += 1) {
            if (normalizeUpper(joinedRooms[i]) === key) {
                return true;
            }
        }
        return false;
    }

    /* Beside the MOTD, where it is clear what they act on: Join a room /
       Leave for a joined room; Join #name / Other rooms for one you are
       only looking at. Nothing in a private thread or for guests. */
    function renderRoomActions() {
        var html = '';
        var name = activeView.name;

        if (!roomActions) {
            return;
        }
        if (pageConfig.isLoggedIn && activeView.type === 'channel') {
            if (isJoined(name)) {
                html = '<button type="button" class="avatar-chat-room-action" data-picker-open>Join a room</button>' +
                    '<button type="button" class="avatar-chat-room-action is-leave" data-room-leave="' + escapeAttr(name) + '" title="Leave #' + escapeAttr(name) + '">Leave</button>';
            } else {
                html = '<button type="button" class="avatar-chat-room-action is-primary" data-room-join-this="' + escapeAttr(name) + '">Join #' + escapeHtml(name) + '</button>' +
                    '<button type="button" class="avatar-chat-room-action" data-picker-open>Other rooms</button>';
            }
        }
        if (roomActions.innerHTML !== html) {
            roomActions.innerHTML = html;
        }
    }

    function updateChannelLabel() {
        renderHeaderBar();
    }

    function updateUserCountButton() {
        renderHeaderBar();
    }

    function renderUsersTitle() {
        if (!usersTitle) {
            return;
        }

        usersTitle.textContent = 'Users in #' + sanitizeChannelName(usersPanelRoom || currentPublicChannel);
    }

    function loadUsersPanelRoom(roomName, silent) {
        var requestedChannel = sanitizeChannelName(roomName || currentPublicChannel);
        var requestToken = ++usersPanelRequestToken;

        if (isMotdChannelName(requestedChannel) && !pageConfig.canManageMotd) {
            requestedChannel = getDefaultPublicChannel();
        }

        return fetchJSON(apiBase + '?action=who&channel=' + encodeURIComponent(requestedChannel)).then(function (response) {
            if (response && response.error) {
                throw new Error(String(response.error));
            }
            if (
                requestToken !== usersPanelRequestToken ||
                normalizeUpper(usersPanelRoom) !== normalizeUpper(requestedChannel)
            ) {
                return false;
            }

            panelUsers = response && Array.isArray(response.users) ? response.users : [];
            renderUsers();
            return true;
        }).catch(function () {
            if (!silent) {
                setStatus('error', explainServiceError(), true);
            }
            return false;
        });
    }

    function openUsersPanelForRoom(roomName) {
        usersPanelRoom = sanitizeChannelName(roomName || currentPublicChannel);
        if (isMotdChannelName(usersPanelRoom) && !pageConfig.canManageMotd) {
            usersPanelRoom = getDefaultPublicChannel();
        }
        renderUsersTitle();
        if (usersModal) {
            usersModal.classList.add('is-open');
            usersModal.setAttribute('aria-hidden', 'false');
        }

        if (normalizeUpper(usersPanelRoom) === normalizeUpper(currentPublicChannel)) {
            panelUsers = Array.isArray(users) ? users.slice() : [];
            renderUsers();
        }

        loadUsersPanelRoom(usersPanelRoom, true);
    }

    function closeUsersModal() {
        if (!usersModal) {
            return;
        }

        usersModal.classList.remove('is-open');
        usersModal.setAttribute('aria-hidden', 'true');
    }

    /* The room picker: every public room with its user count and whether
       you are in it; click one to join it. The name box below it says what
       it will do: Join #x for a room that exists, Create #x for a new one. */
    function pickerRooms() {
        var list = rooms.filter(function (room) {
            return room && room.name && !(isMotdChannelName(room.name) && !pageConfig.canManageMotd);
        }).slice();
        list.sort(function (a, b) {
            if ((a.userCount || 0) !== (b.userCount || 0)) {
                return (b.userCount || 0) - (a.userCount || 0);
            }
            return String(a.name).localeCompare(String(b.name));
        });
        return list;
    }

    function renderRoomPicker() {
        var list = pickerRooms();
        var html = '';

        if (!roomPickerList) {
            return;
        }
        if (!list.length) {
            html = '<div class="avatar-chat-nav-empty">No public rooms yet. Type a name below to create one.</div>';
        }
        list.forEach(function (room) {
            var isCurrent = activeView.type === 'channel' && normalizeUpper(room.name) === normalizeUpper(activeView.name);
            var state = isCurrent ? 'Current' : (isJoined(room.name) ? 'Joined' : '');
            html += '<button type="button" class="avatar-chat-picker-room' + (isCurrent ? ' is-current' : '') + '" data-picker-room="' + escapeAttr(room.name) + '">' +
                '<span class="avatar-chat-picker-room-name">#' + escapeHtml(room.name) + '</span>' +
                '<span class="avatar-chat-picker-room-count">' + escapeHtml(String(room.userCount || 0)) + ' user' + ((room.userCount || 0) === 1 ? '' : 's') + '</span>' +
                (state ? '<span class="avatar-chat-nav-state">' + state + '</span>' : '') +
                '</button>';
        });
        roomPickerList.innerHTML = html;
        updateRoomSubmit();
    }

    /* Join #x when x is a room, Create #x when it would be a new one. */
    function updateRoomSubmit() {
        var name = roomInput ? sanitizeChannelName(trimText(roomInput.value).replace(/^#/, '')) : '';
        var typed = roomInput ? trimText(roomInput.value).replace(/^#/, '') : '';

        if (!roomSubmit) {
            return;
        }
        if (!typed.length || !name.length) {
            roomSubmit.textContent = 'Join';
            roomSubmit.disabled = true;
            roomSubmit.removeAttribute('data-create');
            return;
        }
        var existing = findRoomSummary(name);
        roomSubmit.disabled = false;
        if (existing) {
            roomSubmit.textContent = 'Join #' + existing.name;
            roomSubmit.removeAttribute('data-create');
        } else {
            roomSubmit.textContent = 'Create #' + name;
            roomSubmit.setAttribute('data-create', '1');
        }
    }

    function openRoomModal() {
        if (!roomModal) {
            return;
        }
        closeNoRoomsModal();
        if (roomInput) {
            roomInput.value = '';
        }
        renderRoomPicker();
        roomModal.classList.add('is-open');
        roomModal.setAttribute('aria-hidden', 'false');
        if (roomInput) {
            roomInput.focus();
        }
        loadRoomSummaries(true).then(function () {
            if (roomModal.classList.contains('is-open')) {
                renderRoomPicker();
            }
        });
    }

    function closeRoomModal() {
        if (!roomModal) {
            return;
        }
        roomModal.classList.remove('is-open');
        roomModal.setAttribute('aria-hidden', 'true');
        if (roomInput) {
            roomInput.value = '';
        }
    }

    function openNoRoomsModal() {
        if (!noRoomsModal) {
            return;
        }
        noRoomsModal.classList.add('is-open');
        noRoomsModal.setAttribute('aria-hidden', 'false');
        if (noRoomsMain) {
            noRoomsMain.focus();
        }
    }

    function closeNoRoomsModal() {
        if (!noRoomsModal) {
            return;
        }
        noRoomsModal.classList.remove('is-open');
        noRoomsModal.setAttribute('aria-hidden', 'true');
    }

    function postRoom(action, name, create) {
        var body = new URLSearchParams();
        body.set('action', action);
        body.set('channel', name);
        if (create) {
            body.set('create', '1');
        }
        return fetchJSON(apiBase, {
            method: 'POST',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
            body: body.toString()
        }).then(function (reply) {
            if (!reply || reply.error) {
                throw new Error(reply && reply.error ? String(reply.error) : 'could not ' + (action === 'leaveRoom' ? 'leave' : 'join'));
            }
            if (Array.isArray(reply.rooms)) {
                joinedRooms = reply.rooms.slice();
            }
            return reply;
        });
    }

    /* Join (create=true: create first) and open the room. */
    function joinRoom(name, create) {
        var next = sanitizeChannelName(String(name || '').replace(/^#/, ''));
        if (!next.length) {
            return Promise.resolve(false);
        }
        return postRoom('joinRoom', next, create).then(function (reply) {
            closeRoomModal();
            closeNoRoomsModal();
            ensureRoomSummary(reply.channel);
            setActiveChannel(reply.channel, true);
            if (reply.created) {
                setStatus('info', 'Created #' + reply.channel + ' and joined it.', false);
                loadRoomSummaries(true);
            }
            return true;
        }).catch(function (error) {
            setStatus('error', error && error.message ? String(error.message) : explainServiceError(), false);
            return false;
        });
    }

    /* Leave a room: it drops out of the sidebar. Leaving the one you are
       looking at moves you to your most recent other room; leaving the last
       one offers the default room. */
    function leaveRoom(name) {
        return postRoom('leaveRoom', name, false).then(function () {
            var leavingCurrent = activeView.type === 'channel' && normalizeUpper(activeView.name) === normalizeUpper(name);
            if (!joinedRooms.length) {
                renderRoomList();
                renderHeaderBar();
                openNoRoomsModal();
                return;
            }
            if (leavingCurrent) {
                var remaining = joinedRooms.map(function (n) { return findRoomSummary(n) || { name: n, lastTimestamp: 0 }; });
                remaining.sort(function (a, b) { return (b.lastTimestamp || 0) - (a.lastTimestamp || 0); });
                setActiveChannel(remaining[0].name, true);
                return;
            }
            renderRoomList();
            renderHeaderBar();
        }).catch(function (error) {
            setStatus('error', error && error.message ? String(error.message) : explainServiceError(), false);
        });
    }

    function openPrivateThread(targetName, targetSystem, targetAvatar) {
        var safeName = sanitizeAlias(targetName);

        if (!safeName.length) {
            return;
        }

        if (!pageConfig.canUseRealtime) {
            setStatus('info', 'Log in from the web interface to open private chats.', false);
            return;
        }

        upsertPrivateThread({
            name: safeName,
            system: trimText(targetSystem),
            avatar: trimText(targetAvatar),
            lastTimestamp: 0,
            preview: ''
        });

        activeView = {
            type: 'private',
            name: safeName,
            system: trimText(targetSystem),
            avatar: trimText(targetAvatar)
        };

        unreadPrivate[currentPrivateThreadKey()] = 0;
        updateChannelLabel();
        updateQueryString();
        renderRoomList();
        renderPrivateList();
        loadActiveHistory(false);
        loadPresenceMap(true);
    }

    function setActiveChannel(nextChannel, reconnectStream) {
        var safeChannel = sanitizeChannelName(nextChannel || getDefaultPublicChannel());
        var changed = normalizeUpper(currentPublicChannel) !== normalizeUpper(safeChannel);

        if (isMotdChannelName(safeChannel) && !pageConfig.canManageMotd) {
            safeChannel = getDefaultPublicChannel();
            changed = normalizeUpper(currentPublicChannel) !== normalizeUpper(safeChannel);
        }

        currentPublicChannel = safeChannel;
        activeView = {
            type: 'channel',
            name: safeChannel
        };
        unreadChannels[normalizeUpper(safeChannel)] = 0;
        ensureRoomSummary(safeChannel);
        updateChannelLabel();
        updateQueryString();
        renderRoomList();
        renderPrivateList();
        loadActiveHistory(false);
        loadUsers(false);
        loadPresenceMap(true);

        if (pageConfig.canUseRealtime && (changed || reconnectStream)) {
            connectEvents(changed || reconnectStream);
        }
    }

    function renderEmbeddedAvatars(root) {
        var scope = root || document;
        var els = scope.querySelectorAll('div[data-avatar-bin]:empty');

        if (!els.length || !graphicsReady || !converter) {
            return;
        }

        els.forEach(function (el) {
            var bin = el.getAttribute('data-avatar-bin');
            if (!bin) {
                return;
            }

            try {
                converter.from_bin(atob(bin), 10, 6, function (dataURL) {
                    var img = new Image();
                    img.addEventListener('load', function () {
                        if (!el.hasChildNodes()) {
                            el.appendChild(img);
                        }
                    });
                    img.src = dataURL;
                }, true);
            } catch (_error) {}
        });
    }

    function renderLocalAvatars(root) {
        if (!avatarsReady) {
            return;
        }

        var scope = root || document;
        var avatarEls = scope.querySelectorAll('div[data-avatar]');
        var ids = [];

        avatarEls.forEach(function (el) {
            var id = el.getAttribute('data-avatar');
            if (id && id !== '0') {
                ids.push(id);
            }
        });

        if (ids.length) {
            Avatars.draw(ids);
        }
    }

    function renderAvatarFallbacks(root) {
        var scope = root || document;
        var placeholders = scope.querySelectorAll('.avatar-chat-avatar-placeholder[data-initial]');

        placeholders.forEach(function (el) {
            if (el.parentNode && el.parentNode.children.length > 1) {
                el.remove();
            }
        });
    }

    var AVATAR_FALLBACK_IMG = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAFAAAABgCAYAAACKa/UAAAABiUlEQVR4Xu3c0YqCUBiFUXtKX1Gfcga6TPBgn6LDrG5zSy3377EQX8uy/ExeXwu8AH5t9w4CbH4Aox9AgFUg5p0Dnw44z3P8iC2+rmvbwSB9eQMBxuMHEOCugBGOBQEIcF/AKhwbAvC/Ad592TLyPruRpy8iAEeHcPA+QICHBIzwIa7txgABHhOwCh/z2mwNEGAUiHENBBgFYlwDAUaBGNdAgFEgxjUQYBSIcQ0EGAViXAMBRoEY10CAUSDGNRBgFIhxDQQYBWJcAwFGgRjXQIBRIMYf38DP73f33Vpng31+v9PvjQEYRwQgwEMCRvgQ13ZjgAD3BR6/Ct99mRILNFXgPMIA44N3AAJMZwEjHJ+pABBgmkCrcOObAD4e8K9fplwNPFxEAO4/+QjgoKKjn3oAAdazXPs3RwM1UAOvFYh7t4gAjAIxroEAo0CMayDAKBDjGggwCsS4BgKMAjGugQCjQIxrIMAoEOMaCDAKxLgGAowCMa6BAKNAjGsgwCgQ4xoIMArE+KiBv6PnKB+V8OyaAAAAAElFTkSuQmCC';

    function fetchMissingAvatars(root) {
        var scope = root || document;
        var els = scope.querySelectorAll('div[data-avatar]:empty');
        if (!els.length || !graphicsReady || !converter) return;

        var idMap = {};
        els.forEach(function (el) {
            var id = el.getAttribute('data-avatar');
            if (id && id !== '0') idMap[id] = true;
        });
        var ids = Object.keys(idMap);
        if (!ids.length) return;

        var url = './api/system.ssjs?call=get-avatar&' + ids.map(function (id) {
            return 'user=' + encodeURIComponent(id);
        }).join('&');

        fetchJSON(url).then(function (results) {
            if (!Array.isArray(results)) return;
            results.forEach(function (entry) {
                if (!entry) return;
                var id = String(entry.user);
                var targets = scope.querySelectorAll('div[data-avatar="' + (window.CSS && CSS.escape ? CSS.escape(id) : id.replace(/["\\]/g, '\\$&')) + '"]:empty');
                if (entry.data) {
                    cacheAvatarByNumber(String(entry.user), entry.data);
                    try {
                        converter.from_bin(atob(entry.data), 10, 6, function (dataURL) {
                            var img = new Image();
                            img.addEventListener('load', function () {
                                targets.forEach(function (el) {
                                    if (!el.hasChildNodes()) el.appendChild(img.cloneNode(true));
                                });
                            });
                            img.src = dataURL;
                        }, true);
                    } catch (_e) {}
                } else {
                    var fallback = new Image();
                    fallback.addEventListener('load', function () {
                        targets.forEach(function (el) {
                            if (!el.hasChildNodes()) el.appendChild(fallback.cloneNode(true));
                        });
                    });
                    fallback.src = AVATAR_FALLBACK_IMG;
                }
            });
        }).catch(function () {});
    }

    /* The sidebar is the switcher: the rooms you joined, unread first, then
       most recent; the open one highlighted. Click a row to view it, x to
       leave it. */
    function renderRoomsMarkup() {
        var items = joinedRooms.filter(function (name) {
            return !(isMotdChannelName(name) && !pageConfig.canManageMotd);
        }).map(function (name) {
            return findRoomSummary(name) || { name: name, userCount: 0, lastTimestamp: 0 };
        });
        var html = '';

        items.sort(function (a, b) {
            var aUnread = (unreadChannels[normalizeUpper(a.name)] || 0) > 0 ? 1 : 0;
            var bUnread = (unreadChannels[normalizeUpper(b.name)] || 0) > 0 ? 1 : 0;

            if (aUnread !== bUnread) {
                return bUnread - aUnread;
            }
            if ((a.lastTimestamp || 0) !== (b.lastTimestamp || 0)) {
                return (b.lastTimestamp || 0) - (a.lastTimestamp || 0);
            }
            return String(a.name).localeCompare(String(b.name));
        });

        if (!items.length) {
            roomsList.innerHTML = '<div class="avatar-chat-nav-empty">No rooms joined.</div>';
            return;
        }

        items.forEach(function (room) {
            var unread = unreadChannels[normalizeUpper(room.name)] || 0;
            var isActive =
                activeView.type === 'channel' &&
                normalizeUpper(currentPublicChannel) === normalizeUpper(room.name);

            html += '<div class="avatar-chat-nav-item avatar-chat-room' + (isActive ? ' is-active' : '') +
                '" role="button" tabindex="0" data-room-open="' + escapeAttr(room.name) + '">';
            html += '<div class="avatar-chat-nav-top">';
            html += '<span class="avatar-chat-nav-title">#' + escapeHtml(room.name) + '</span>';
            if (unread > 0) {
                html += '<span class="avatar-chat-badge is-unread">' + escapeHtml(String(unread)) + '</span>';
            }
            if (pageConfig.isLoggedIn) {
                html += '<button type="button" class="avatar-chat-nav-icon is-leave" data-room-leave="' + escapeAttr(room.name) +
                    '" title="Leave #' + escapeAttr(room.name) + '" aria-label="Leave #' + escapeAttr(room.name) + '">&times;</button>';
            }
            html += '</div>';
            html += '<div class="avatar-chat-nav-secondary">' + escapeHtml(String(room.userCount || 0)) + ' user' + ((room.userCount || 0) === 1 ? '' : 's') +
                (isActive ? ' \u00b7 Current' : '') + '</div>';
            html += '</div>';
        });

        roomsList.innerHTML = html;
    }

    function renderPrivateMarkup() {
        var items = privateThreads.slice();
        var html = '';

        items.sort(function (a, b) {
            var aKey = buildThreadKey(a.name, a.system || '');
            var bKey = buildThreadKey(b.name, b.system || '');
            var aActive = activeView.type === 'private' && aKey === currentPrivateThreadKey() ? 1 : 0;
            var bActive = activeView.type === 'private' && bKey === currentPrivateThreadKey() ? 1 : 0;
            var aOnline = isThreadOnline(a.name, a.system || '') ? 1 : 0;
            var bOnline = isThreadOnline(b.name, b.system || '') ? 1 : 0;
            var aUnread = unreadPrivate[aKey] || 0;
            var bUnread = unreadPrivate[bKey] || 0;

            if (aOnline !== bOnline) {
                return bOnline - aOnline;
            }
            if (aActive !== bActive) {
                return bActive - aActive;
            }
            if (aUnread !== bUnread) {
                return bUnread - aUnread;
            }
            return (b.lastTimestamp || 0) - (a.lastTimestamp || 0);
        });

        if (!items.length) {
            privateList.innerHTML = '<div class="avatar-chat-nav-empty">No private chats yet.</div>';
            return;
        }

        items.forEach(function (thread) {
            var key = buildThreadKey(thread.name, thread.system || '');
            var unread = unreadPrivate[key] || 0;
            var isActive = activeView.type === 'private' && key === currentPrivateThreadKey();
            var isOnline = isThreadOnline(thread.name, thread.system || '');
            var initial = thread.name ? thread.name.charAt(0).toUpperCase() : '?';

            html += '<button type="button" class="avatar-chat-nav-item' + (isActive ? ' is-active' : '') + '" data-private-nav="' + escapeAttr(thread.name) + '" data-private-system="' + escapeAttr(thread.system || '') + '" data-private-avatar="' + escapeAttr(thread.avatar || '') + '">';
            html += '<div class="avatar-chat-thread-row">';
            html += '<span class="avatar-chat-presence-dot ' + (isOnline ? 'is-online' : 'is-offline') + '" aria-hidden="true"></span>';
            html += '<div class="avatar-chat-thread-avatar">';
            if (thread.avatar) {
                html += '<div data-avatar-bin="' + escapeAttr(thread.avatar) + '"></div>';
            } else {
                html += '<div class="avatar-chat-avatar-placeholder" data-initial="' + escapeAttr(initial) + '">' + escapeHtml(initial) + '</div>';
            }
            html += '</div>';
            html += '<div>';
            html += '<div class="avatar-chat-nav-top">';
            html += '<span class="avatar-chat-nav-title">@' + escapeHtml(thread.name) + '</span>';
            if (unread > 0) {
                html += '<span class="avatar-chat-badge is-unread">' + escapeHtml(String(unread)) + '</span>';
            }
            html += '</div>';
            if (thread.system) {
                html += '<div class="avatar-chat-nav-secondary">' + escapeHtml(thread.system) + '</div>';
            }
            if (thread.preview) {
                html += '<div class="avatar-chat-nav-preview">' + escapeHtml(thread.preview) + '</div>';
            }
            html += '</div>';
            html += '</div>';
            html += '</button>';
        });

        privateList.innerHTML = html;
        renderEmbeddedAvatars(privateList);
        renderAvatarFallbacks(privateList);
    }

    function renderRoomList() {
        renderRoomsMarkup();
        updateUserCountButton();
    }

    function renderPrivateList() {
        renderPrivateMarkup();
    }

    function renderUsers() {
        var html = '';
        var visibleUsers = Array.isArray(panelUsers) ? panelUsers : [];

        visibleUsers.forEach(function (entry) {
            rememberMailbox(entry && entry.nick, entry && entry.system, entry && entry.mailbox);
        });

        if (!usersList) {
            return;
        }

        renderUsersTitle();

        if (!visibleUsers.length) {
            usersList.innerHTML = '<div class="avatar-chat-empty">No users in #' + escapeHtml(sanitizeChannelName(usersPanelRoom || currentPublicChannel)) + '.</div>';
            return;
        }

        visibleUsers.forEach(function (entry) {
            var initial = entry.nick ? entry.nick.charAt(0).toUpperCase() : '?';

            html += '<button type="button" class="avatar-chat-user" data-private-user="' + escapeAttr(entry.nick || '') + '" data-private-system="' + escapeAttr(entry.system || '') + '" data-private-avatar="' + escapeAttr(entry.avatar || '') + '">';
            html += '<div class="avatar-chat-user-avatar">';
            /* The roster's nick is from when that page joined: after an
               avatar change here, show the new one for yourself. */
            var mine = ownAvatarOverride !== null && isOwnName(entry.nick, entry.system);
            var displayAvatar = mine ? ownAvatarOverride : (entry.avatar || getCachedAvatar(entry.nick, entry.system) || (entry.userNumber > 0 && getCachedAvatarByNumber(entry.userNumber)) || null);
            if (displayAvatar) {
                html += '<div data-avatar-bin="' + escapeAttr(displayAvatar) + '"></div>';
            } else if (!mine && entry.userNumber && entry.userNumber > 0) {
                html += '<div data-avatar="' + String(entry.userNumber) + '"></div>';
            } else if (!mine && entry.qwkid && entry.nick) {
                html += '<div data-avatar="' + escapeAttr(entry.nick + '@' + entry.qwkid) + '"></div>';
            } else {
                html += '<div class="avatar-chat-avatar-placeholder" data-initial="' + escapeAttr(initial) + '">' + escapeHtml(initial) + '</div>';
            }
            html += '</div>';
            html += '<div>';
            html += '<div>' + renderHandle(entry.nick || 'Unknown', entry.style) + '</div>';
            if (entry.system) {
                html += '<div class="avatar-chat-channel">' + escapeHtml(entry.system) + '</div>';
            }
            html += '</div></button>';
        });

        usersList.innerHTML = html;
        renderEmbeddedAvatars(usersList);
        fetchMissingAvatars(usersList);
        renderAvatarFallbacks(usersList);
    }

    function buildActorAttrs(message, isSelf) {
        if (isSelf || !message.sender) {
            return '';
        }
        return ' data-private-user="' + escapeAttr(message.sender) + '"' +
            ' data-private-system="' + escapeAttr(message.system || '') + '"' +
            ' data-private-avatar="' + escapeAttr(message.avatar || '') + '"';
    }

    function renderBitmapBubble(message) {
        var cols = Math.max(1, message.bitmapWidth || 80);
        var rows = Math.max(1, message.bitmapHeight || 25);
        var pixelWidth = cols * BITMAP_FONT_WIDTH;
        var pixelHeight = rows * BITMAP_FONT_HEIGHT;
        var label = 'image ' + cols + 'x' + rows;

        return '<div class="avatar-chat-bitmap-shell" style="' + escapeAttr('max-width:' + pixelWidth + 'px;') + '">' +
            '<div class="avatar-chat-bitmap-frame">' +
            '<div class="avatar-chat-bitmap-stage" data-avatar-chat-bitmap-key="' + escapeAttr(message.bitmapKey || '') + '" data-avatar-chat-bitmap-alt="' + escapeAttr(label) + '" style="' + escapeAttr('aspect-ratio:' + pixelWidth + ' / ' + pixelHeight + ';') + '">' +
            '<div class="avatar-chat-bitmap-placeholder">Rendering ' + escapeHtml(label) + '...</div>' +
            '</div>' +
            '</div>' +
            '</div>';
    }

    function renderMessages() {
        var html = '';
        var previousSender = '';
        var previousTime = 0;
        var groupWindow = 120000;

        if (!messages.length) {
            messageContainer.innerHTML = '<div class="avatar-chat-empty">No messages yet. Say hello.</div>';
            return;
        }

        messages.forEach(function (message) {
            var displaySender = message.sender || message.bitmapFromName || '';
            if (!displaySender && message.kind === 'bitmap' && message.bitmapKey) {
                displaySender = 'Image';
            }
            var isSystem = !displaySender;
            var isSelf = !!message.isSelf;
            var sameGroup = displaySender === previousSender && Math.abs((message.timestamp || 0) - previousTime) < groupWindow;
            var avatarInitial = displaySender ? displaySender.charAt(0).toUpperCase() : '?';

            if (!isSelf && pageConfig.userAlias) {
                isSelf =
                    normalizeUpper(displaySender) === normalizeUpper(pageConfig.userAlias) &&
                    (!message.system || !pageConfig.systemName || normalizeUpper(message.system) === normalizeUpper(pageConfig.systemName));
            }

            var actorAttrs = buildActorAttrs(message, isSelf);

            if (isSystem) {
                html += '<div class="avatar-chat-system">' + escapeHtml(message.text || '') + '</div>';
                previousSender = '';
                previousTime = 0;
                return;
            }

            if (!sameGroup) {
                if (previousSender.length) {
                    html += '</div></div>';
                }

                html += '<div class="avatar-chat-group' + (isSelf ? ' is-self' : '') + '">';
                if (actorAttrs.length) {
                    html += '<button type="button" class="avatar-chat-avatar avatar-chat-actor"' + actorAttrs + '>';
                } else {
                    html += '<div class="avatar-chat-avatar">';
                }
                var msgAvatar = message.avatar || getCachedAvatar(message.sender, message.system) || (message.userNumber > 0 && getCachedAvatarByNumber(message.userNumber)) || null;
                if (msgAvatar) {
                    html += '<div data-avatar-bin="' + escapeAttr(msgAvatar) + '"></div>';
                } else if (message.userNumber && message.userNumber > 0) {
                    html += '<div data-avatar="' + String(message.userNumber) + '"></div>';
                } else if (message.qwkid && message.sender) {
                    /* A remote user: stock webv4 looks up alias@QWKID. */
                    html += '<div data-avatar="' + escapeAttr(message.sender + '@' + message.qwkid) + '"></div>';
                } else {
                    html += '<div class="avatar-chat-avatar-placeholder" data-initial="' + escapeAttr(avatarInitial) + '">' + escapeHtml(avatarInitial) + '</div>';
                }
                html += actorAttrs.length ? '</button>' : '</div>';
                html += '<div class="avatar-chat-content">';
                html += '<div class="avatar-chat-header">';
                if (actorAttrs.length) {
                    html += '<button type="button" class="avatar-chat-sender-button"' + actorAttrs + '>';
                }
                html += renderHandle(displaySender, message.style);
                if (message.system) {
                    html += ' <span class="avatar-chat-system-name">- ' + escapeHtml(message.system) + '</span>';
                }
                html += ' <span class="avatar-chat-time">' + escapeHtml(formatTime(message.timestamp)) + '</span>';
                if (actorAttrs.length) {
                    html += '</button>';
                }
                html += '</div>';
            }

            if (message.kind === 'bitmap' && message.bitmapKey) {
                html += '<div class="avatar-chat-bubble avatar-chat-bitmap-bubble">' + renderBitmapBubble(message) + '</div>';
            } else {
                html += '<div class="avatar-chat-bubble' + (!isSelf && mentionsMe(message.text || '') && activeView.type !== 'private' ? ' is-mention' : '') + '">' +
                    renderRichText(message.text || '') + '</div>';
            }
            previousSender = displaySender;
            previousTime = message.timestamp || 0;
        });

        if (previousSender.length) {
            html += '</div></div>';
        }

        messageContainer.innerHTML = html;
        hydrateRichText(messageContainer);
        renderEmbeddedAvatars(messageContainer);
        renderEmbeddedBitmaps(messageContainer);
        fetchMissingAvatars(messageContainer);
        renderAvatarFallbacks(messageContainer);
        scrollToBottom();
    }

    function scrollToBottom() {
        requestAnimationFrame(function () {
            messageContainer.scrollTop = messageContainer.scrollHeight;
        });
    }

    function applyRoomSummaries(summaries, serverTime) {
        summaries = Array.isArray(summaries) ? summaries : [];
        serverTime = serverTime || Date.now();
        var nextRooms = [];

        summaries.forEach(function (summary) {
            var room = ensureRoomSummary(summary.name);
            room.userCount = summary.userCount || 0;
            room.lastTimestamp = summary.lastTimestamp || 0;
            room.newCount = summary.newCount || 0;

            if (!realtimeHealthy && (summary.newCount || 0) > 0 && !(activeView.type === 'channel' && normalizeUpper(activeView.name) === normalizeUpper(summary.name))) {
                unreadChannels[normalizeUpper(summary.name)] = (unreadChannels[normalizeUpper(summary.name)] || 0) + summary.newCount;
            }

            if (activeView.type === 'channel' && normalizeUpper(activeView.name) === normalizeUpper(summary.name)) {
                unreadChannels[normalizeUpper(summary.name)] = 0;
            }

            nextRooms.push(room);
        });

        rooms = nextRooms.length ? nextRooms : [ensureRoomSummary(currentPublicChannel)];
        ensureRoomSummary(currentPublicChannel);
        lastRoomPollAt = serverTime;
        renderRoomList();
    }

    function loadRoomSummaries(silent) {
        var url = apiBase + '?action=channels';
        return fetchJSON(url + (lastRoomPollAt > 0 ? '&since=' + encodeURIComponent(String(lastRoomPollAt)) : '')).then(function (response) {
            applyRoomSummaries(response && response.channels, response && response.serverTime);
            return true;
        }).catch(function (_error) {
            if (!silent) {
                setStatus('error', explainServiceError(), true);
            }
            return false;
        });
    }

    function loadPrivateThreads(silent) {
        if (!pageConfig.canUseRealtime) {
            privateThreads = [];
            renderPrivateList();
            renderHeaderBar();
            return Promise.resolve(true);
        }

        var url = apiBase + '?action=private';
        return fetchJSON(url + (lastPrivatePollAt > 0 ? '&since=' + encodeURIComponent(String(lastPrivatePollAt)) : '')).then(function (response) {
            applyPrivateThreads(response && response.threads, response && response.serverTime);
            return true;
        }).catch(function (_error) {
            if (!silent) {
                setStatus('error', explainServiceError(), true);
            }
            return false;
        });
    }

    function applyPrivateThreads(threads, serverTime) {
        threads = Array.isArray(threads) ? threads : [];
        serverTime = serverTime || Date.now();
        var nextThreads = [];
        var seen = {};

        threads.forEach(function (summary) {
            var thread = upsertPrivateThread(summary);
            var key = buildThreadKey(thread.name, thread.system || '');

            if (!realtimeHealthy && (summary.newCount || 0) > 0 && !(activeView.type === 'private' && key === currentPrivateThreadKey())) {
                unreadPrivate[key] = (unreadPrivate[key] || 0) + summary.newCount;
            }

            if (activeView.type === 'private' && key === currentPrivateThreadKey()) {
                unreadPrivate[key] = 0;
            }

            if (!seen[key]) {
                seen[key] = true;
                nextThreads.push(thread);
            }
        });

        if (activeView.type === 'private') {
            upsertPrivateThread({
                name: activeView.name,
                system: activeView.system || '',
                avatar: activeView.avatar || undefined,
                lastTimestamp: 0,
                preview: ''
            });
            if (!nextThreads.length || !nextThreads.some(function (thread) {
                return buildThreadKey(thread.name, thread.system || '') === currentPrivateThreadKey();
            })) {
                nextThreads.push({
                    name: activeView.name,
                    system: activeView.system || '',
                    avatar: activeView.avatar || undefined,
                    lastTimestamp: 0,
                    preview: ''
                });
            }
        }

        privateThreads = nextThreads;
        lastPrivatePollAt = serverTime;
        renderPrivateList();
        renderHeaderBar();
    }

    function applyMotd(response) {
        motdState.text = trimText(response && response.previewText ? response.previewText : '');
        motdState.timestamp = response && response.timestamp ? response.timestamp : 0;
        renderHeaderBar();
    }

    function loadMotd(silent) {
        return fetchJSON(apiBase + '?action=motd').then(function (response) {
            if (response && response.error) {
                throw new Error(String(response.error));
            }
            applyMotd(response);
            return true;
        }).catch(function () {
            if (!silent) {
                setStatus('error', explainServiceError(), true);
            }
            return false;
        });
    }

    function loadPublicHistory(silent) {
        var requestedChannel = currentPublicChannel;
        var requestToken = ++historyRequestToken;

        return fetchJSON(apiBase + '?action=history&channel=' + encodeURIComponent(requestedChannel)).then(function (response) {
            if (response && response.error) {
                throw new Error(String(response.error));
            }
            return applyPublicHistory(response, requestedChannel, requestToken, silent);
        }).catch(function (_error) {
            setStatus('error', explainServiceError(), true);
            return false;
        });
    }

    function applyPublicHistory(response, requestedChannel, requestToken, silent) {
        if (
            requestToken !== historyRequestToken ||
            activeView.type !== 'channel' ||
            normalizeUpper(currentPublicChannel) !== normalizeUpper(requestedChannel)
        ) {
            return false;
        }
        messages = normalizeMessages(response && Array.isArray(response.messages) ? response.messages : []);
        cacheAvatarsFromMessages(messages);
        unreadChannels[normalizeUpper(requestedChannel)] = 0;
        renderMessages();
        if (!realtimeHealthy && reconnectAttemptCount > 0 && !silent) {
            setStatus('warning', 'Realtime updates are not fully healthy, but the current room refreshed successfully.', true);
        }
        return true;
    }

    function loadPrivateHistory(silent) {
        var requestedName = activeView.name;
        var requestedSystem = activeView.system || '';
        var requestToken = ++historyRequestToken;
        var url = apiBase + '?action=privateHistory&target=' + encodeURIComponent(requestedName);
        if (requestedSystem) {
            url += '&system=' + encodeURIComponent(requestedSystem);
        }

        return fetchJSON(url).then(function (response) {
            var key;

            if (response && response.error) {
                throw new Error(String(response.error));
            }
            if (
                requestToken !== historyRequestToken ||
                activeView.type !== 'private' ||
                normalizeUpper(activeView.name || '') !== normalizeUpper(requestedName) ||
                normalizeUpper(activeView.system || '') !== normalizeUpper(requestedSystem)
            ) {
                return false;
            }

            messages = response && Array.isArray(response.messages) ? response.messages : [];
            messages = normalizeMessages(messages);
            cacheAvatarsFromMessages(messages);
            if (response && response.peer) {
                activeView.system = response.peer.system || activeView.system || '';
                activeView.avatar = response.peer.avatar || activeView.avatar || '';
                upsertPrivateThread(response.peer);
            }
            key = currentPrivateThreadKey();
            unreadPrivate[key] = 0;
            renderPrivateList();
            renderMessages();
            if (!realtimeHealthy && reconnectAttemptCount > 0 && !silent) {
                setStatus('warning', 'Realtime updates are not fully healthy, but the private thread refreshed successfully.', true);
            }
            return true;
        }).catch(function (_error) {
            setStatus('error', explainServiceError(), true);
            return false;
        });
    }

    function loadActiveHistory(silent) {
        if (activeView.type === 'private') {
            return loadPrivateHistory(silent);
        }
        return loadPublicHistory(silent);
    }

    function loadUsers(silent) {
        var requestedChannel = currentPublicChannel;
        var requestToken = ++usersRequestToken;

        return fetchJSON(apiBase + '?action=who&channel=' + encodeURIComponent(requestedChannel)).then(function (response) {
            if (response && response.error) {
                throw new Error(String(response.error));
            }
            return applyUsers(response && response.users, requestedChannel, requestToken, silent);
        }).catch(function (_error) {
            if (!silent) {
                setStatus('error', explainServiceError(), true);
            }
            return false;
        });
    }

    function applyUsers(usersArr, requestedChannel, requestToken, silent) {
        if (
            requestToken !== usersRequestToken ||
            normalizeUpper(currentPublicChannel) !== normalizeUpper(requestedChannel)
        ) {
            return false;
        }
        users = Array.isArray(usersArr) ? usersArr : [];
        if (normalizeUpper(usersPanelRoom) === normalizeUpper(requestedChannel)) {
            panelUsers = users.slice();
            renderUsers();
        }
        updateUserCountButton();
        if (!realtimeHealthy && reconnectAttemptCount > 0 && !silent) {
            setStatus('warning', 'Realtime updates are not fully healthy, but the roster refreshed successfully.', true);
        }
        return true;
    }

    function applyPresence(combined, silent) {
        rebuildOnlinePresence(Array.isArray(combined) ? combined : []);
        if (!realtimeHealthy && reconnectAttemptCount > 0 && !silent) {
            setStatus('warning', 'Realtime updates are not fully healthy, but private chat presence refreshed successfully.', true);
        }
    }

    function loadPresenceMap(silent) {
        var targets = rooms.filter(function (room) {
            return !!room && !!room.name && ((room.userCount || 0) > 0 || normalizeUpper(room.name) === normalizeUpper(currentPublicChannel));
        });
        var requests;

        if (!targets.length) {
            rebuildOnlinePresence([]);
            return Promise.resolve(true);
        }

        requests = targets.map(function (room) {
            return fetchJSON(apiBase + '?action=who&channel=' + encodeURIComponent(room.name)).then(function (response) {
                return response && Array.isArray(response.users) ? response.users : [];
            }).catch(function () {
                return [];
            });
        });

        return Promise.all(requests).then(function (results) {
            var combined = [];

            results.forEach(function (entries) {
                if (Array.isArray(entries) && entries.length) {
                    combined = combined.concat(entries);
                }
            });

            applyPresence(combined, silent);
            return true;
        }).catch(function () {
            if (!silent) {
                setStatus('error', explainServiceError(), true);
            }
            return false;
        });
    }

    function sendPublicMessage(text) {
        var body = new URLSearchParams();
        var channel = currentPublicChannel;
        body.set('action', 'send');
        body.set('channel', channel);
        body.set('message', text);

        return fetchJSON(apiBase, {
            method: 'POST',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
            body: body.toString()
        }).then(function (reply) {
            /* speaking in a room joins it (the BBS records it too) */
            if (reply && reply.success && !isJoined(channel)) {
                joinedRooms.push(channel);
                renderRoomList();
                renderHeaderBar();
            }
            return reply;
        });
    }

    function sendPrivateMessage(text) {
        var body = new URLSearchParams();
        body.set('action', 'sendPrivate');
        body.set('target', activeView.name);
        body.set('message', text);
        if (activeView.system) {
            body.set('system', activeView.system);
        }
        if (mailboxFor(activeView.name, activeView.system)) {
            body.set('mailbox', mailboxFor(activeView.name, activeView.system));
        }

        return fetchJSON(apiBase, {
            method: 'POST',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
            body: body.toString()
        });
    }

    function sendActiveMessage(text) {
        if (activeView.type === 'private') {
            return sendPrivateMessage(text);
        }
        return sendPublicMessage(text);
    }

    function reconcileState(forceUsers) {
        // One combined ?action=sync request per tick instead of the old fan-out
        // (channels + private + history + presence(one who PER room) + motd + who).
        // The server bundles them over a single JSONClient connection. A roster
        // modal open on a non-active room, and an open DM's history, are the only
        // pieces still fetched separately.
        var isPrivateView = activeView.type === 'private';
        var requestedChannel = currentPublicChannel;
        var historyToken = ++historyRequestToken;
        var usersToken = ++usersRequestToken;

        var usersPanelOpen = !!(usersModal && usersModal.classList.contains('is-open'));
        usersRefreshTick += 1;
        var wantUsers = forceUsers || (usersPanelOpen && normalizeUpper(usersPanelRoom) === normalizeUpper(currentPublicChannel)) || usersRefreshTick >= 2;
        if (wantUsers) {
            usersRefreshTick = 0;
        }

        var since = Math.min(lastRoomPollAt || 0, lastPrivatePollAt || 0);
        var url = apiBase + '?action=sync'
            + '&channel=' + encodeURIComponent(requestedChannel)
            + '&who=' + (wantUsers ? '1' : '0')
            + '&presence=1'
            + '&motd=1'
            + '&history=' + (isPrivateView ? '0' : '1')
            + (since > 0 ? '&since=' + encodeURIComponent(String(since)) : '');

        return fetchJSON(url).then(function (response) {
            if (!response || response.error) {
                return false;
            }

            applyRoomSummaries(response.channels, response.serverTime);

            if (response.private) {
                applyPrivateThreads(response.private.threads, response.serverTime);
            } else if (!pageConfig.canUseRealtime) {
                applyPrivateThreads([], response.serverTime);
            }

            if (response.motd) {
                applyMotd(response.motd);
            }
            if (response.presence) {
                applyPresence(response.presence, true);
            }
            if (wantUsers && response.who) {
                applyUsers(response.who.users, requestedChannel, usersToken, true);
            }
            if (!isPrivateView && response.history) {
                applyPublicHistory(response.history, requestedChannel, historyToken, true);
            }

            var extra = [];
            if (usersPanelOpen && normalizeUpper(usersPanelRoom) !== normalizeUpper(currentPublicChannel)) {
                extra.push(loadUsersPanelRoom(usersPanelRoom, true));
            }
            if (isPrivateView) {
                extra.push(loadActiveHistory(true));
            }
            if (extra.length) {
                return Promise.all(extra).then(function () { return true; }).catch(function () { return false; });
            }
            return true;
        }).catch(function () {
            return false;
        });
    }

    function startReconcileLoop() {
        if (reconcileTimer) {
            return;
        }
        reconcileTimer = window.setInterval(function () {
            reconcileState(false);
        }, RECONCILE_INTERVAL_MS);
    }

    function connectEvents(isReconnect) {
        if (!pageConfig.canUseRealtime) {
            return;
        }

        if (!window.EventSource) {
            setStatus('warning', 'This browser does not support realtime updates. Background refresh will continue, but updates may be delayed.', false);
            return;
        }

        closeEventSource();
        eventSource = new EventSource(buildEventUrl());
        if (isReconnect) {
            setStatus('warning', 'Reconnecting realtime updates...', true);
        }

        eventSource.onopen = function () {
            realtimeHealthy = true;
            reconnectAttemptCount = 0;
            clearStatusIfMatch('warning');
        };

        eventSource.addEventListener('avatarchat', function (event) {
            var payload = null;
            var roomSummary;
            var thread;
            var threadKey;

            try {
                payload = JSON.parse(event.data);
            } catch (_parseError) {
                return;
            }

            if (!payload) {
                return;
            }

            if (payload.type === 'message') {
                payload = normalizeMessage(payload);
                if (payload.sender && payload.avatar) {
                    cacheAvatar(payload.sender, payload.system, payload.avatar);
                }
                if (isMotdChannelName(payload.channel || '')) {
                    motdState.text = trimText(payload.previewText || payload.text || '');
                    motdState.timestamp = payload.timestamp || Date.now();
                    renderHeaderBar();
                }
                roomSummary = ensureRoomSummary(payload.channel || currentPublicChannel);
                roomSummary.lastTimestamp = Math.max(roomSummary.lastTimestamp || 0, payload.timestamp || 0);

                if (activeView.type === 'channel' && normalizeUpper(activeView.name) === normalizeUpper(payload.channel || currentPublicChannel)) {
                    messages.push(payload);
                    if (messages.length > 200) {
                        messages.shift();
                    }
                    unreadChannels[normalizeUpper(payload.channel || currentPublicChannel)] = 0;
                    renderMessages();
                } else {
                    unreadChannels[normalizeUpper(payload.channel || currentPublicChannel)] = (unreadChannels[normalizeUpper(payload.channel || currentPublicChannel)] || 0) + 1;
                }

                renderRoomList();
                return;
            }

            if (payload.type === 'join' || payload.type === 'part') {
                loadUsers(true);
                loadRoomSummaries(true).then(function () {
                    loadPresenceMap(true);
                });
                return;
            }

            if (payload.type === 'private') {
                payload = normalizeMessage(payload);
                if (payload.sender && payload.avatar) {
                    cacheAvatar(payload.sender, payload.system, payload.avatar);
                }
                if (payload.peerName && payload.peerAvatar) {
                    cacheAvatar(payload.peerName, payload.peerSystem, payload.peerAvatar);
                }
                thread = upsertPrivateThread({
                    name: payload.peerName || payload.sender,
                    system: payload.peerSystem || payload.system || '',
                    avatar: payload.peerAvatar || payload.avatar || undefined,
                    lastTimestamp: payload.timestamp || Date.now(),
                    preview: payload.previewText || payload.text || ''
                });
                threadKey = buildThreadKey(thread.name, thread.system || '');

                if (activeView.type === 'private' && threadKey === currentPrivateThreadKey()) {
                    messages.push(normalizeMessage({
                        sender: payload.sender,
                        system: payload.system,
                        text: payload.text,
                        timestamp: payload.timestamp,
                        userNumber: payload.userNumber,
                        isSelf: payload.isSelf,
                        avatar: payload.avatar,
                        qwkid: payload.qwkid,
                        style: payload.style
                    }));
                    if (messages.length > 200) {
                        messages.shift();
                    }
                    unreadPrivate[threadKey] = 0;
                    renderMessages();
                } else {
                    unreadPrivate[threadKey] = (unreadPrivate[threadKey] || 0) + 1;
                }

                lastPrivatePollAt = Math.max(lastPrivatePollAt, payload.timestamp || 0);
                renderPrivateList();
            }
        });

        eventSource.onerror = function () {
            realtimeHealthy = false;
            reconnectAttemptCount += 1;
            setStatus('warning', explainRealtimeError(), true);
            closeEventSource();
            scheduleReconnect();
        };
    }

    if (usersClose) {
        usersClose.addEventListener('click', closeUsersModal);
    }

    if (statusRetry) {
        statusRetry.addEventListener('click', function () {
            reconnectAttemptCount = 0;
            closeEventSource();
            reconcileState(true);
            if (pageConfig.canUseRealtime) {
                connectEvents(true);
            }
        });
    }

    if (countButton) {
        countButton.addEventListener('click', function () {
            if (activeView.type !== 'channel' || countButton.disabled) {
                return;
            }
            openUsersPanelForRoom(currentPublicChannel);
        });
    }

    if (roomClose) {
        roomClose.addEventListener('click', function () {
            closeRoomModal();
        });
    }

    if (roomModal) {
        roomModal.addEventListener('click', function (event) {
            var pick = event.target.closest('[data-picker-room]');
            if (event.target === roomModal) {
                closeRoomModal();
                return;
            }
            if (pick) {
                joinRoom(pick.getAttribute('data-picker-room'), false);
            }
        });
    }

    if (roomInput) {
        roomInput.addEventListener('input', updateRoomSubmit);
    }

    if (roomActions) {
        roomActions.addEventListener('click', function (event) {
            var target = event.target.closest('button');
            if (!target) {
                return;
            }
            if (target.hasAttribute('data-picker-open')) {
                openRoomModal();
            } else if (target.hasAttribute('data-room-join-this')) {
                joinRoom(target.getAttribute('data-room-join-this'), false);
            } else if (target.hasAttribute('data-room-leave')) {
                leaveRoom(target.getAttribute('data-room-leave'));
            }
        });
    }

    if (noRoomsModal) {
        if (noRoomsMain) {
            noRoomsMain.addEventListener('click', function () {
                joinRoom(getDefaultPublicChannel(), false);
            });
        }
        if (noRoomsPick) {
            noRoomsPick.addEventListener('click', function () {
                openRoomModal();
            });
        }
        if (noRoomsCancel) {
            noRoomsCancel.addEventListener('click', closeNoRoomsModal);
        }
        noRoomsModal.addEventListener('click', function (event) {
            if (event.target === noRoomsModal) {
                closeNoRoomsModal();
            }
        });
    }

    if (usersModal) {
        usersModal.addEventListener('click', function (event) {
            if (event.target === usersModal) {
                closeUsersModal();
            }
        });
    }

    if (roomsList) {
        roomsList.addEventListener('click', function (event) {
            var leave = event.target.closest('[data-room-leave]');
            var row = event.target.closest('[data-room-open]');

            if (leave) {
                event.stopPropagation();
                leaveRoom(leave.getAttribute('data-room-leave'));
                return;
            }
            if (row) {
                setActiveChannel(row.getAttribute('data-room-open'), true);
            }
        });
        roomsList.addEventListener('keydown', function (event) {
            var row = event.target.closest('[data-room-open]');
            if (row && event.target === row && (event.key === 'Enter' || event.key === ' ')) {
                event.preventDefault();
                setActiveChannel(row.getAttribute('data-room-open'), true);
            }
        });
    }

    if (privateList) {
        privateList.addEventListener('click', function (event) {
            var button = event.target.closest('[data-private-nav]');
            if (!button) {
                return;
            }
            openPrivateThread(
                button.getAttribute('data-private-nav'),
                button.getAttribute('data-private-system'),
                button.getAttribute('data-private-avatar')
            );
        });
    }

    if (usersList) {
        usersList.addEventListener('click', function (event) {
            var button = event.target.closest('[data-private-user]');
            if (!button) {
                return;
            }
            openPrivateThread(
                button.getAttribute('data-private-user'),
                button.getAttribute('data-private-system'),
                button.getAttribute('data-private-avatar')
            );
        });
    }

    if (messageContainer) {
        messageContainer.addEventListener('click', function (event) {
            var button = event.target.closest('[data-private-user]');
            if (!button) {
                return;
            }
            openPrivateThread(
                button.getAttribute('data-private-user'),
                button.getAttribute('data-private-system'),
                button.getAttribute('data-private-avatar')
            );
        });
    }

    if (roomForm) {
        roomForm.addEventListener('submit', function (event) {
            var rawRoom = roomInput ? trimText(roomInput.value) : '';
            var nextRoom = sanitizeChannelName(rawRoom);

            event.preventDefault();
            if (!rawRoom.length || !nextRoom.length) {
                return;
            }

            updateRoomSubmit();
            joinRoom(nextRoom, !!(roomSubmit && roomSubmit.getAttribute('data-create')));
        });
    }

    /* ---- Attachments: pictures, sound and video stored on this BBS ----
       Only when the sysop turned web_uploads on. The file goes up in base64
       chunks (the web server hands scripts at most 4 MiB of body), the BBS
       checks what it really is, and the link lands in the composer: nothing
       is sent until the user presses Send. ANSI art keeps its own path: it
       becomes a [BITMAP|...] message right here in the browser, uploads on
       or off. One attach button, plus drop and paste anywhere on the chat;
       the file decides which path it takes. */
    var uploadEndpoint = './api/avatarchat-upload.ssjs';
    var ART_EXTENSIONS = /\.(ans|asc|nfo|diz|ice|txt)$/i;
    var ART_MAX_BYTES = 1048576;
    var MEDIA_KINDS = {
        png: 'image', jpg: 'image', jpeg: 'image', gif: 'image', webp: 'image',
        mp3: 'audio', ogg: 'audio', wav: 'audio', flac: 'audio', m4a: 'audio',
        mp4: 'video', m4v: 'video', webm: 'video'
    };

    function mediaKindOf(file) {
        var match = /\.([a-z0-9]{1,4})$/i.exec(String(file && file.name || ''));
        var ext = match ? match[1].toLowerCase() : '';
        var kind = MEDIA_KINDS[ext];
        var stored = ext === 'jpeg' ? 'jpg' : (ext === 'm4v' ? 'mp4' : ext);
        if (!kind || (uploadConfig.types || []).indexOf(stored) < 0) {
            return '';
        }
        return kind;
    }

    /* Busy notes ride in the composer's placeholder; errors go to the
       status bar. */
    function setUploadNote(text, type) {
        if (type === 'error') {
            ansiUploadMessage = '';
            ansiUploadMessageType = '';
            renderAnsiUploadState();
            setStatus('error', text, false);
            return;
        }
        ansiUploadMessage = type === 'busy' ? text : '';
        ansiUploadMessageType = type || '';
        renderAnsiUploadState();
    }

    function readChunkBase64(blob) {
        return new Promise(function (resolve, reject) {
            var reader = new FileReader();
            reader.onload = function () {
                var url = String(reader.result || '');
                resolve(url.substring(url.indexOf(',') + 1));
            };
            reader.onerror = function () {
                reject(new Error('could not read the file'));
            };
            reader.readAsDataURL(blob);
        });
    }

    function uploadCall(query, body) {
        return fetchJSON(uploadEndpoint + '?' + query, {
            method: 'POST',
            headers: { 'Content-Type': 'text/plain' },
            body: body || ''
        }).then(function (reply) {
            if (!reply || reply.ok === false) {
                throw new Error(reply && reply.error ? String(reply.error) : 'upload failed');
            }
            return reply;
        });
    }

    function uploadMedia(file) {
        var kind = mediaKindOf(file);
        var limitMb = kind === 'video' ? uploadConfig.videoMaxMb : uploadConfig.maxMb;
        var name = String(file.name || 'file');
        var id = '';

        if (!kind) {
            setUploadNote(name + ' is not a type this board takes.', 'error');
            return;
        }
        if (file.size > limitMb * 1048576) {
            setUploadNote(name + ' is over the ' + limitMb + ' MB limit.', 'error');
            return;
        }
        ansiUploadBusy = true;
        setUploadNote('Uploading ' + name + '...', 'busy');

        uploadCall('action=begin&size=' + String(file.size)).then(function (reply) {
            var chunk = reply.chunkSize;
            var seq = 0;
            id = reply.id;
            function next(offset) {
                if (offset >= file.size) {
                    return uploadCall('action=finish&id=' + encodeURIComponent(id));
                }
                return readChunkBase64(file.slice(offset, offset + chunk)).then(function (b64) {
                    return uploadCall('action=chunk&id=' + encodeURIComponent(id) + '&seq=' + String(seq), b64);
                }).then(function () {
                    seq += 1;
                    setUploadNote('Uploading ' + name + ' (' + Math.min(100, Math.round((offset + chunk) * 100 / file.size)) + '%)...', 'busy');
                    return next(offset + chunk);
                });
            }
            return next(0);
        }).then(function (done) {
            ansiUploadBusy = false;
            input.value = trimText(input.value).length ? trimText(input.value) + ' ' + done.url : done.url;
            setUploadNote('', 'ready');
            clearStatusIfMatch('error');
            input.focus();
        }).catch(function (error) {
            ansiUploadBusy = false;
            if (id) {
                uploadCall('action=abort&id=' + encodeURIComponent(id)).catch(function () {});
            }
            setUploadNote(error && error.message ? String(error.message) : 'Upload failed.', 'error');
        });
    }

    /* A dropped, pasted or chosen file. ANSI art by its name, or anything
       that isn't a picture, sound or video by its type, is tried as art;
       media goes up when the sysop turned uploads on. */
    function looksLikeArt(file) {
        var name = String(file.name || '');
        if (ART_EXTENSIONS.test(name)) {
            return true;
        }
        return !/^(image|audio|video)\//.test(String(file.type || '')) && !mediaKindOf(file) && file.size <= ART_MAX_BYTES;
    }

    function acceptFile(file) {
        if (!file || ansiUploadBusy) {
            return;
        }
        clearStatusIfMatch('error');
        if (looksLikeArt(file)) {
            queueAnsiUpload(file);
            return;
        }
        if (!uploadConfig.enabled) {
            setUploadNote('This board only takes ANSI art here; picture, sound and video uploads are off.', 'error');
            return;
        }
        uploadMedia(file);
    }

    if (fileInput && attachButton) {
        fileInput.setAttribute('accept', ['.ans', '.asc', '.nfo', '.diz', '.ice', '.txt'].concat(
            uploadConfig.enabled ? (uploadConfig.types || []).map(function (t) { return '.' + t; }) : [],
            uploadConfig.enabled && (uploadConfig.types || []).indexOf('jpg') >= 0 ? ['.jpeg'] : []).join(','));
        attachButton.title = uploadConfig.enabled
            ? 'Attach a picture, sound, video or ANSI art (or drop or paste it on the chat)'
            : 'Attach ANSI art (or drop or paste it on the chat)';
        attachButton.addEventListener('click', function () {
            if (!ansiUploadBusy) {
                fileInput.click();
            }
        });
        fileInput.addEventListener('change', function () {
            var file = fileInput.files && fileInput.files.length ? fileInput.files[0] : null;
            fileInput.value = '';
            if (file) {
                acceptFile(file);
            }
        });
    }

    if (form && input) {
        input.addEventListener('paste', function (event) {
            var files = event.clipboardData && event.clipboardData.files;
            if (files && files.length) {
                event.preventDefault();
                acceptFile(files[0]);
            }
        });
        input.addEventListener('keydown', function (event) {
            if (event.key === 'Escape' && pendingAnsiUpload) {
                event.preventDefault();
                event.stopPropagation();
                clearPendingAnsiUpload();
            }
        });
    }

    /* Drop anywhere on the chat panel: no drop box, just an outline while a
       file is over it. */
    var dropTarget = form && form.closest ? form.closest('.avatar-chat-chatpanel') : null;
    var dragDepth = 0;

    function dragHasFiles(event) {
        var types = event.dataTransfer && event.dataTransfer.types;
        return !!types && Array.prototype.indexOf.call(types, 'Files') >= 0;
    }

    if (dropTarget) {
        dropTarget.addEventListener('dragenter', function (event) {
            if (!dragHasFiles(event)) {
                return;
            }
            event.preventDefault();
            dragDepth += 1;
            dropTarget.classList.add('is-dragover');
        });
        dropTarget.addEventListener('dragover', function (event) {
            if (dragHasFiles(event)) {
                event.preventDefault();
            }
        });
        dropTarget.addEventListener('dragleave', function () {
            dragDepth = Math.max(0, dragDepth - 1);
            if (!dragDepth) {
                dropTarget.classList.remove('is-dragover');
            }
        });
        dropTarget.addEventListener('drop', function (event) {
            var file = event.dataTransfer && event.dataTransfer.files && event.dataTransfer.files.length
                ? event.dataTransfer.files[0]
                : null;
            dragDepth = 0;
            dropTarget.classList.remove('is-dragover');
            if (!file) {
                return;
            }
            event.preventDefault();
            acceptFile(file);
        });
    }

    /* A veiled picture (NSFW keywords) shows on the first click. */
    messageContainer.addEventListener('click', function (event) {
        var veiled = event.target && event.target.closest ? event.target.closest('.is-veiled') : null;
        if (veiled) {
            event.preventDefault();
            veiled.classList.remove('is-veiled');
        }
    });

    /* ---- Handle style editor ----
       The record every client shares (data/chat/nickstyle/NNNN.json): one
       colour per letter of the alias ('' = default). Presets match the
       door's and the shell's (solid, gradient, rainbow). This page edits the
       colours only; anything else in the record is kept as it is. */
    var styleOpen = document.getElementById('avatar-chat-style-open');
    var styleModal = document.getElementById('avatar-chat-style-modal');
    var styleLetters = document.getElementById('avatar-chat-style-letters');
    var stylePreview = document.getElementById('avatar-chat-style-preview');
    var stylePreset = document.getElementById('avatar-chat-style-preset');
    var styleA = document.getElementById('avatar-chat-style-a');
    var styleB = document.getElementById('avatar-chat-style-b');
    var styleStatus = document.getElementById('avatar-chat-style-status');
    var styleSave = document.getElementById('avatar-chat-style-save');
    var styleCancel = document.getElementById('avatar-chat-style-cancel');
    var styleState = { colors: [] };
    /* Page style: one pick from the sysop's list, a whole look for this
       page (colours and shape). Picking previews it at once; Save keeps it,
       closing without saving puts the saved one back. */
    var pageStyleSelect = document.getElementById('avatar-chat-page-style');
    var appRoot = document.querySelector('.avatar-chat-web');
    var savedPageStyle = String(pageConfig.pageStyle || '');

    function applyPageStyle(id) {
        if (!appRoot || !id) {
            return;
        }
        Array.prototype.slice.call(appRoot.classList).forEach(function (c) {
            if (c.indexOf('ac-style-') === 0) {
                appRoot.classList.remove(c);
            }
        });
        appRoot.classList.add('ac-style-' + id);
        window.requestAnimationFrame(syncMotdTicker);
    }

    function savePageStyle(id) {
        var body = new URLSearchParams();
        if (!id || id === savedPageStyle) {
            return Promise.resolve(true);
        }
        body.set('action', 'savePageStyle');
        body.set('style', id);
        return fetchJSON(apiBase, {
            method: 'POST',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
            body: body.toString()
        }).then(function (reply) {
            if (!reply || reply.error) {
                throw new Error(reply && reply.error ? reply.error : 'could not save the page style');
            }
            savedPageStyle = id;
            return true;
        });
    }

    function hexChannel(v) {
        var s = Math.max(0, Math.min(255, Math.round(v))).toString(16);
        return s.length < 2 ? '0' + s : s;
    }

    function hexRgb(h) {
        return HEX_COLOR.test(h || '') ? [parseInt(h.substr(1, 2), 16), parseInt(h.substr(3, 2), 16), parseInt(h.substr(5, 2), 16)] : null;
    }

    function hsvHex(h, s, v) {
        var c = v * s;
        var x = c * (1 - Math.abs((h / 60) % 2 - 1));
        var m = v - c;
        var rgb = h < 60 ? [c, x, 0] : h < 120 ? [x, c, 0] : h < 180 ? [0, c, x] : h < 240 ? [0, x, c] : h < 300 ? [x, 0, c] : [c, 0, x];
        return '#' + hexChannel((rgb[0] + m) * 255) + hexChannel((rgb[1] + m) * 255) + hexChannel((rgb[2] + m) * 255);
    }

    function presetColors(kind, n, a, b) {
        var out = [];
        var i;
        var ra = hexRgb(a);
        var rb = hexRgb(b);
        for (i = 0; i < n; i += 1) {
            if (kind === 'solid') {
                out.push(a);
            } else if (kind === 'gradient' && ra && rb) {
                var t = n > 1 ? i / (n - 1) : 0;
                out.push('#' + hexChannel(ra[0] + (rb[0] - ra[0]) * t) + hexChannel(ra[1] + (rb[1] - ra[1]) * t) + hexChannel(ra[2] + (rb[2] - ra[2]) * t));
            } else if (kind === 'rainbow') {
                out.push(hsvHex(i / Math.max(1, n) * 360, 0.85, 1));
            } else {
                out.push('');
            }
        }
        return out;
    }

    function renderStyleEditor() {
        var alias = String(pageConfig.userAlias || '');
        var html = '';
        var i;
        for (i = 0; i < alias.length; i += 1) {
            var color = styleState.colors[i] || '';
            html += '<span class="avatar-chat-style-letter' + (color ? '' : ' is-default') + '">' +
                '<span class="avatar-chat-style-char">' + escapeHtml(alias.charAt(i)) + '</span>' +
                '<input type="color" data-letter="' + i + '" value="' + escapeAttr(color || '#aaaaaa') + '" aria-label="Colour of letter ' + (i + 1) + '">' +
                '<button type="button" data-reset="' + i + '" title="Default colour">x</button></span>';
        }
        styleLetters.innerHTML = html;
        stylePreview.innerHTML = renderHandle(alias, styleState);
    }

    function closeStyleModal() {
        styleModal.classList.remove('is-open');
        styleModal.setAttribute('aria-hidden', 'true');
        applyPageStyle(savedPageStyle);
    }

    if (styleOpen && styleModal) {
        styleOpen.addEventListener('click', function () {
            styleStatus.textContent = 'Loading...';
            if (pageStyleSelect) {
                pageStyleSelect.value = savedPageStyle;
            }
            styleModal.classList.add('is-open');
            styleModal.setAttribute('aria-hidden', 'false');
            fetchJSON(apiBase + '?action=style').then(function (reply) {
                var st = reply && reply.style ? reply.style : {};
                var alias = String(pageConfig.userAlias || '');
                var colors = Array.isArray(st.colors) ? st.colors : [];
                var i;
                styleState.colors = [];
                for (i = 0; i < alias.length; i += 1) {
                    styleState.colors.push(HEX_COLOR.test(colors[i] || '') ? colors[i] : '');
                }
                styleStatus.textContent = '';
                renderStyleEditor();
            }).catch(function (error) {
                styleStatus.textContent = error && error.message ? String(error.message) : 'Could not load your style.';
            });
        });
        styleLetters.addEventListener('input', function (event) {
            var i = event.target.getAttribute('data-letter');
            if (i !== null) {
                styleState.colors[parseInt(i, 10)] = event.target.value;
                event.target.parentNode.classList.remove('is-default');
                stylePreview.innerHTML = renderHandle(String(pageConfig.userAlias || ''), styleState);
            }
        });
        styleLetters.addEventListener('click', function (event) {
            var i = event.target.getAttribute('data-reset');
            if (i !== null) {
                styleState.colors[parseInt(i, 10)] = '';
                renderStyleEditor();
            }
        });
        stylePreset.addEventListener('change', function () {
            var kind = stylePreset.value;
            if (kind) {
                styleState.colors = presetColors(kind, String(pageConfig.userAlias || '').length, styleA.value, styleB.value);
                stylePreset.value = '';
                renderStyleEditor();
            }
        });
        if (pageStyleSelect) {
            pageStyleSelect.addEventListener('change', function () {
                applyPageStyle(pageStyleSelect.value);
            });
        }
        styleSave.addEventListener('click', function () {
            styleStatus.textContent = 'Saving...';
            fetchJSON(apiBase + '?action=saveStyle', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ colors: styleState.colors })
            }).then(function (reply) {
                if (!reply || reply.error) {
                    throw new Error(reply && reply.error ? reply.error : 'could not save');
                }
                return savePageStyle(pageStyleSelect ? pageStyleSelect.value : '');
            }).then(function () {
                styleStatus.textContent = 'Saved. Your next message carries it.';
                setTimeout(closeStyleModal, 900);
            }).catch(function (error) {
                styleStatus.textContent = error && error.message ? String(error.message) : 'Could not save.';
            });
        });
        styleCancel.addEventListener('click', closeStyleModal);
    }

    /* ---- ANSI editor (Futureland's, shipped in ./avatarchat/) ----
       Loaded on first use: the pencil in the composer draws an 80x25
       picture that Done sends as a [BITMAP|...] message, and the avatar
       modal opens it at 10x6. */
    var EDITOR_BASE = './avatarchat/';
    var EDITOR_FONT = EDITOR_BASE + 'fonts/CP437.F16';
    var editorLoading = null;

    function ensureEditor() {
        if (window.AnsiEditorModal && window.AnsiEditor) {
            return Promise.resolve();
        }
        if (!editorLoading) {
            editorLoading = new Promise(function (resolve, reject) {
                var css = document.createElement('link');
                css.rel = 'stylesheet';
                css.href = EDITOR_BASE + 'ansi-editor-modal.css';
                document.head.appendChild(css);
                loadScript(EDITOR_BASE + 'ansi-editor.js', function () {
                    loadScript(EDITOR_BASE + 'ansi-editor-modal.js', function () {
                        if (window.AnsiEditorModal && window.AnsiEditor) {
                            resolve();
                        } else {
                            editorLoading = null;
                            reject(new Error('The ANSI editor could not be loaded.'));
                        }
                    });
                });
            });
        }
        return editorLoading;
    }

    function openArtEditor() {
        if (ansiUploadBusy) {
            return;
        }
        ensureEditor().then(function () {
            if (AnsiEditorModal.isOpen) {
                return;
            }
            AnsiEditorModal.open({
                title: 'ANSI Editor',
                className: 'avatar-chat-ae',
                columns: 80,
                rows: 25,
                width: Math.min(980, window.innerWidth),
                height: Math.min(620, window.innerHeight),
                minWidth: Math.min(480, window.innerWidth),
                minHeight: Math.min(360, window.innerHeight),
                fontUrl: EDITOR_FONT,
                onDone: function (editor) {
                    var doc = editor && editor.doc;
                    if (!doc || !doc.data) {
                        return;
                    }
                    ansiUploadBusy = true;
                    setUploadNote('Sending your drawing...', 'busy');
                    buildBitmapPayload(doc.data, doc.columns, doc.rows, buildBitmapFromName()).then(function (payload) {
                        return sendActiveMessage(payload);
                    }).then(function (response) {
                        ansiUploadBusy = false;
                        setUploadNote('', '');
                        if (response && response.error) {
                            setStatus('error', String(response.error), false);
                            return;
                        }
                        scheduleHistoryRefresh();
                    }).catch(function (error) {
                        ansiUploadBusy = false;
                        setUploadNote(error && error.message ? String(error.message) : explainServiceError(), 'error');
                    });
                }
            });
        }).catch(function (error) {
            setStatus('error', String(error.message || error), false);
        });
    }

    if (drawButton) {
        drawButton.addEventListener('click', openArtEditor);
    }

    /* ---- Avatar: the user's real BBS avatar ----
       Pick one from the sysop's shared collections, draw or edit one at
       10x6, upload a .bin (one avatar, or a collection to pick from) or an
       .ans (opened in the editor to check before saving), or disable it.
       Saved through ./api/avatarchat.ssjs into the same record the door,
       the terminal chooser and the rest of the site use. */
    var AVATAR_COLS = 10;
    var AVATAR_ROWS = 6;
    var AVATAR_BYTES = AVATAR_COLS * AVATAR_ROWS * 2;
    var avatarOpen = document.getElementById('avatar-chat-avatar-open');
    var avatarModal = document.getElementById('avatar-chat-avatar-modal');
    var avatarPreview = document.getElementById('avatar-chat-avatar-preview');
    var avatarDraw = document.getElementById('avatar-chat-avatar-draw');
    var avatarUpload = document.getElementById('avatar-chat-avatar-upload');
    var avatarToggle = document.getElementById('avatar-chat-avatar-toggle');
    var avatarFile = document.getElementById('avatar-chat-avatar-file');
    var avatarCollection = document.getElementById('avatar-chat-avatar-collection');
    var avatarGrid = document.getElementById('avatar-chat-avatar-grid');
    var avatarStatus = document.getElementById('avatar-chat-avatar-status');
    var avatarUse = document.getElementById('avatar-chat-avatar-use');
    var avatarClose = document.getElementById('avatar-chat-avatar-close');
    var avatarState = { current: null, collections: [], loaded: {}, list: [], selected: -1, fromFile: false };
    var FILE_COLLECTION = '__file__';

    function avatarNote(text, isError) {
        avatarStatus.textContent = text || '';
        avatarStatus.classList.toggle('is-error', !!isError);
    }

    function avatarTile(base64) {
        return '<div data-avatar-bin="' + escapeAttr(base64) + '"></div>';
    }

    function renderAvatarCurrent() {
        var cur = avatarState.current;
        avatarPreview.innerHTML = cur && cur.data
            ? avatarTile(cur.data)
            : '<div class="avatar-chat-avatar-placeholder">none</div>';
        avatarPreview.classList.toggle('is-disabled', !!(cur && cur.disabled));
        avatarToggle.hidden = !(cur && cur.data);
        avatarToggle.textContent = cur && cur.disabled ? 'Enable' : 'Disable';
        renderEmbeddedAvatars(avatarPreview);
    }

    function renderAvatarGrid() {
        var html = '';
        avatarState.list.forEach(function (a, i) {
            html += '<button type="button" class="avatar-chat-avatar-tile' + (i === avatarState.selected ? ' is-selected' : '') +
                '" data-index="' + i + '" title="' + escapeAttr(a.label || ('#' + (i + 1))) + '">' + avatarTile(a.data) + '</button>';
        });
        avatarGrid.innerHTML = html || '<div class="avatar-chat-empty">No avatars here.</div>';
        avatarUse.disabled = avatarState.selected < 0;
        renderEmbeddedAvatars(avatarGrid);
    }

    function fillCollectionSelect() {
        var html = '';
        if (avatarState.fromFile) {
            html += '<option value="' + FILE_COLLECTION + '">From your file (' + avatarState.list.length + ')</option>';
        }
        avatarState.collections.forEach(function (c) {
            html += '<option value="' + escapeAttr(c.id) + '">' + escapeHtml(c.title + ' (' + c.count + ')') + '</option>';
        });
        avatarCollection.innerHTML = html || '<option value="">No shared collections</option>';
        avatarCollection.disabled = !html;
    }

    function showCollection(id) {
        if (id === FILE_COLLECTION) {
            renderAvatarGrid();
            return Promise.resolve();
        }
        avatarState.fromFile = false;
        if (!id) {
            avatarState.list = [];
            avatarState.selected = -1;
            renderAvatarGrid();
            return Promise.resolve();
        }
        var cached = avatarState.loaded[id];
        var pending = cached ? Promise.resolve(cached) : fetchJSON(apiBase + '?action=avatarCollection&collection=' + encodeURIComponent(id)).then(function (reply) {
            if (!reply || !reply.collection) {
                throw new Error(reply && reply.error ? reply.error : 'Could not load that collection.');
            }
            avatarState.loaded[id] = reply.collection;
            return reply.collection;
        });
        avatarGrid.innerHTML = '<div class="avatar-chat-empty">Loading...</div>';
        return pending.then(function (c) {
            avatarState.list = c.avatars || [];
            avatarState.selected = -1;
            renderAvatarGrid();
        }).catch(function (error) {
            avatarState.list = [];
            renderAvatarGrid();
            avatarNote(String(error.message || error), true);
        });
    }

    /* After a save: the modal, the roster and the browser's avatar cache
       all show the new one; the next message carries it from the BBS. */
    function applyOwnAvatar(avatar) {
        avatarState.current = avatar || null;
        ownAvatarOverride = avatar && avatar.data && !avatar.disabled ? avatar.data : '';
        if (ownAvatarOverride) {
            cacheAvatar(pageConfig.userAlias, pageConfig.systemName, ownAvatarOverride);
            cacheAvatar(pageConfig.userAlias, '', ownAvatarOverride);
            cacheAvatarByNumber(pageConfig.userNumber, ownAvatarOverride);
        }
        renderAvatarCurrent();
        renderUsers();
        renderEmbeddedAvatars(usersList);
    }

    function avatarWrite(query, okText) {
        avatarNote('Saving...');
        return fetchJSON(apiBase + '?action=' + query, { method: 'POST' }).then(function (reply) {
            if (!reply || !reply.ok) {
                throw new Error(reply && reply.error ? reply.error : 'Could not save.');
            }
            applyOwnAvatar(reply.avatar);
            avatarNote(okText);
        }).catch(function (error) {
            avatarNote(String(error.message || error), true);
        });
    }

    function showAvatarModal() {
        avatarModal.classList.add('is-open');
        avatarModal.setAttribute('aria-hidden', 'false');
    }

    function openAvatarModal() {
        showAvatarModal();
        avatarNote('Loading...');
        fetchJSON(apiBase + '?action=avatar').then(function (reply) {
            if (!reply || !reply.ok) {
                throw new Error(reply && reply.error ? reply.error : 'Could not load your avatar.');
            }
            avatarState.current = reply.avatar || null;
            avatarState.collections = reply.collections || [];
            renderAvatarCurrent();
            if (!avatarState.fromFile) {
                fillCollectionSelect();
                showCollection(avatarCollection.value);
            }
            avatarNote('');
        }).catch(function (error) {
            avatarNote(String(error.message || error), true);
        });
    }

    function closeAvatarModal() {
        avatarModal.classList.remove('is-open');
        avatarModal.setAttribute('aria-hidden', 'true');
    }

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

    /* The editor at a fixed 10x6, seeded with cells, zoomed up so the
       cells are big enough to paint. Done saves; Cancel goes back. */
    function openAvatarEditor(seedCells) {
        ensureEditor().then(function () {
            closeAvatarModal();
            AnsiEditorModal.open({
                title: 'Avatar Editor (10x6)',
                className: 'avatar-chat-ae avatar-chat-ae-avatar',
                columns: AVATAR_COLS,
                rows: AVATAR_ROWS,
                width: Math.min(900, window.innerWidth),
                height: Math.min(620, window.innerHeight),
                minWidth: Math.min(480, window.innerWidth),
                minHeight: Math.min(360, window.innerHeight),
                fontUrl: EDITOR_FONT,
                onReady: function (editor) {
                    if (editor && editor.doc && typeof editor.doc.replaceAll === 'function') {
                        editor.doc.replaceAll(seedCells, AVATAR_COLS, AVATAR_ROWS);
                        editor.columns = AVATAR_COLS;
                        editor.rows = AVATAR_ROWS;
                        if (editor._rebuildCanvas) editor._rebuildCanvas();
                        if (editor._moveCursor) editor._moveCursor(0, 0);
                        if (editor._updateStatusBar) editor._updateStatusBar();
                        if (editor._selectTool) editor._selectTool('brush');
                        if (editor._setBrushMode) editor._setBrushMode('custom_block');
                    }
                    if (editor && editor.el && editor.el.canvasContainer) {
                        editor.el.canvasContainer.style.zoom = '5';
                    }
                },
                onDone: function (editor) {
                    var doc = editor && editor.doc;
                    showAvatarModal();
                    if (!doc || doc.columns !== AVATAR_COLS || doc.rows !== AVATAR_ROWS || !doc.data) {
                        avatarNote('The avatar has to stay 10x6.', true);
                        return;
                    }
                    avatarWrite('setAvatar&data=' + encodeURIComponent(cellsToAvatarBase64(doc.data)), 'Saved. Your next message carries it.');
                },
                onCancel: function () {
                    /* After this Escape has finished, or the page's own
                       Escape handler would close the modal again. */
                    window.setTimeout(showAvatarModal, 0);
                }
            });
        }).catch(function (error) {
            avatarNote(String(error.message || error), true);
        });
    }

    /* An uploaded file: one 120-byte avatar is saved as is; a .bin of
       several (a collection) is shown to pick from; ANSI is cropped to 10x6
       and opened in the editor to look at before saving. */
    function acceptAvatarFile(file) {
        avatarNote('Reading ' + String(file.name || 'file') + '...');
        readFileBytes(file).then(function (bytes) {
            var name = String(file.name || '');
            if (/\.bin$/i.test(name) && bytes.length >= AVATAR_BYTES && bytes.length % AVATAR_BYTES === 0) {
                if (bytes.length === AVATAR_BYTES) {
                    return avatarWrite('setAvatar&data=' + encodeURIComponent(binToBase64(bytes)), 'Saved. Your next message carries it.');
                }
                avatarState.list = [];
                for (var i = 0; i * AVATAR_BYTES < bytes.length && i < 500; i += 1) {
                    avatarState.list.push({ label: name + ' #' + (i + 1), data: binToBase64(bytes.subarray(i * AVATAR_BYTES, (i + 1) * AVATAR_BYTES)) });
                }
                avatarState.selected = -1;
                avatarState.fromFile = true;
                fillCollectionSelect();
                avatarCollection.value = FILE_COLLECTION;
                renderAvatarGrid();
                avatarNote('Pick one from your file, then Use selected.');
                return null;
            }
            if (/\.bin$/i.test(name)) {
                throw new Error('A .bin avatar is 120 bytes (10x6 cells); that file is ' + bytes.length + '.');
            }
            var art = prepareAnsiBitmap(bytes);
            var cells = [];
            for (var y = 0; y < AVATAR_ROWS; y += 1) {
                for (var x = 0; x < AVATAR_COLS; x += 1) {
                    cells.push(y < art.height && x < art.width ? art.cells[y * art.width + x] : { code: 32, fg: 7, bg: 0 });
                }
            }
            avatarNote('');
            openAvatarEditor(cells);
            return null;
        }).catch(function (error) {
            avatarNote(String(error.message || error), true);
        });
    }

    if (avatarOpen && avatarModal) {
        avatarOpen.addEventListener('click', openAvatarModal);
        avatarClose.addEventListener('click', closeAvatarModal);
        avatarCollection.addEventListener('change', function () {
            showCollection(avatarCollection.value);
        });
        avatarGrid.addEventListener('click', function (event) {
            var tile = event.target && event.target.closest ? event.target.closest('[data-index]') : null;
            if (!tile) {
                return;
            }
            avatarState.selected = parseInt(tile.getAttribute('data-index'), 10);
            Array.prototype.forEach.call(avatarGrid.querySelectorAll('.avatar-chat-avatar-tile'), function (el) {
                el.classList.toggle('is-selected', el === tile);
            });
            avatarUse.disabled = false;
        });
        avatarGrid.addEventListener('dblclick', function () {
            if (!avatarUse.disabled) {
                avatarUse.click();
            }
        });
        avatarUse.addEventListener('click', function () {
            var picked = avatarState.list[avatarState.selected];
            if (!picked) {
                return;
            }
            if (avatarCollection.value === FILE_COLLECTION) {
                avatarWrite('setAvatar&data=' + encodeURIComponent(picked.data), 'Saved. Your next message carries it.');
            } else {
                avatarWrite('setAvatar&collection=' + encodeURIComponent(avatarCollection.value) + '&index=' + String(picked.index), 'Saved. Your next message carries it.');
            }
        });
        avatarDraw.addEventListener('click', function () {
            openAvatarEditor(avatarBase64ToCells(avatarState.current ? avatarState.current.data : ''));
        });
        avatarUpload.addEventListener('click', function () {
            avatarFile.click();
        });
        avatarFile.addEventListener('change', function () {
            var file = avatarFile.files && avatarFile.files.length ? avatarFile.files[0] : null;
            avatarFile.value = '';
            if (file) {
                acceptAvatarFile(file);
            }
        });
        avatarToggle.addEventListener('click', function () {
            var cur = avatarState.current;
            if (!cur) {
                return;
            }
            avatarWrite('avatarEnabled&enabled=' + (cur.disabled ? '1' : '0'),
                cur.disabled ? 'Enabled.' : 'Disabled: nobody sees an avatar for you until you enable it.');
        });
    }

    renderAnsiUploadState();

    if (form) {
        form.addEventListener('submit', function (event) {
            var text = input.value.replace(/^\s+|\s+$/g, '');
            var pendingUpload = pendingAnsiUpload;
            var outgoing = '';

            event.preventDefault();
            if (ansiUploadBusy) {
                return;
            }

            if (!pendingUpload && !text.length) {
                return;
            }

            outgoing = pendingUpload ? pendingUpload.payload : text;
            if (pendingUpload) {
                ansiUploadBusy = true;
                ansiUploadMessage = 'Sending ' + pendingUpload.fileName + '...';
                ansiUploadMessageType = 'busy';
                renderAnsiUploadState();
            }

            sendActiveMessage(outgoing).then(function (response) {
                if (response && response.error) {
                    if (pendingUpload) {
                        ansiUploadBusy = false;
                        ansiUploadMessage = '';
                        ansiUploadMessageType = 'ready';
                        renderAnsiUploadState();
                    }
                    setStatus('error', String(response.error), true);
                    return;
                }
                if (pendingUpload) {
                    clearPendingAnsiUpload();
                } else {
                    input.value = '';
                }
                scheduleHistoryRefresh();
            }).catch(function () {
                if (pendingUpload) {
                    ansiUploadBusy = false;
                    ansiUploadMessage = '';
                    ansiUploadMessageType = 'ready';
                    renderAnsiUploadState();
                }
                setStatus('error', explainServiceError(), true);
            });
        });
    }

    updateChannelLabel();
    renderUsersTitle();
    updateQueryString();
    renderHeaderBar();

    ensureDependencies(function () {
        ensureRoomSummary(currentPublicChannel);
        updateChannelLabel();
        renderRoomList();
        renderPrivateList();
        loadMotd(false);
        loadRoomSummaries(false).then(function () {
            loadPresenceMap(true);
        });
        loadPrivateThreads(false);
        loadActiveHistory(false);
        loadUsers(false);
        if (pageConfig.canUseRealtime) {
            connectEvents();
            startReconcileLoop();
        }
    });

    window.addEventListener('beforeunload', function () {
        if (reconnectTimer) {
            window.clearTimeout(reconnectTimer);
            reconnectTimer = 0;
        }
        if (reconcileTimer) {
            window.clearInterval(reconcileTimer);
            reconcileTimer = 0;
        }
        if (sendRefreshTimer) {
            window.clearTimeout(sendRefreshTimer);
            sendRefreshTimer = 0;
        }
        closeEventSource();
    });

    window.addEventListener('resize', function () {
        window.requestAnimationFrame(syncMotdTicker);
    });

    window.addEventListener('keydown', function (event) {
        if (event.key === 'Escape' && usersModal && usersModal.classList.contains('is-open')) {
            closeUsersModal();
            return;
        }
        if (event.key === 'Escape' && styleModal && styleModal.classList.contains('is-open')) {
            closeStyleModal();
            return;
        }
        if (event.key === 'Escape' && avatarModal && avatarModal.classList.contains('is-open') &&
            !(window.AnsiEditorModal && AnsiEditorModal.isOpen)) {
            closeAvatarModal();
            return;
        }
        if (event.key === 'Escape' && roomModal && roomModal.classList.contains('is-open')) {
            closeRoomModal();
            return;
        }
        if (event.key === 'Escape' && noRoomsModal && noRoomsModal.classList.contains('is-open')) {
            closeNoRoomsModal();
        }
    });
})();
