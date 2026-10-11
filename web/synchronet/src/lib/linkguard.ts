/* Link preview rules: which link in a message gets a card, which targets the
 * BBS may fetch, and the metadata parser. Pure, so node can test it.
 *
 * Guard rules follow the door (internal/inlineimg/fetch.go PublicAddr,
 * internal/linkprev) and Futureland's lib/link-preview.js: http(s) only on
 * the default ports, no userinfo, and every hop must resolve to a public
 * unicast address. */

export const MAX_URL_LENGTH = 2048;
const TITLE_CAP = 200;
const DESCRIPTION_CAP = 300;
const SITE_NAME_CAP = 80;
export const PARSE_CAP_BYTES = 192 * 1024;

export interface ParsedUrl { scheme: string; host: string; port: number | null; rest: string; }

export function parseUrl(url: string): ParsedUrl | null {
    const match = /^(https?):\/\/([^/?#]+)((?:[/?#]).*)?$/i.exec(String(url || ''));
    if (!match) return null;
    const authority = match[2]!;
    if (authority.indexOf('@') !== -1) return null;
    let host = authority;
    let port: number | null = null;
    const bracket = /^\[([0-9a-f:.]+)\](?::(\d+))?$/i.exec(authority);
    if (bracket) {
        host = bracket[1]!;
        port = bracket[2] ? parseInt(bracket[2], 10) : null;
    } else {
        const portMatch = /^(.*):(\d+)$/.exec(authority);
        if (portMatch) {
            host = portMatch[1]!;
            port = parseInt(portMatch[2]!, 10);
        }
        if (host.indexOf(':') !== -1) return null;
    }
    if (!host.length) return null;
    return { scheme: match[1]!.toLowerCase(), host: host.toLowerCase(), port: port, rest: match[3] || '/' };
}

export function validateTarget(url: string): { ok: boolean; host?: string; error?: string } {
    if (!url || String(url).length > MAX_URL_LENGTH) return { ok: false, error: 'invalid url' };
    const parsed = parseUrl(url);
    if (!parsed) return { ok: false, error: 'invalid url' };
    if (parsed.port !== null && parsed.port !== 80 && parsed.port !== 443) return { ok: false, error: 'port not allowed' };
    return { ok: true, host: parsed.host };
}

/* ref against base: absolute, protocol-relative, root- and path-relative.
 * Any other scheme (javascript:, data:) is refused, never laundered. */
export function resolveUrl(ref: string, base: string): string | null {
    const r = String(ref || '').replace(/^\s+|\s+$/g, '');
    if (!r.length) return null;
    if (/^https?:\/\//i.test(r)) return r;
    if (/^[a-z][a-z0-9+.-]*:/i.test(r)) return null;
    const parsed = parseUrl(base);
    if (!parsed) return null;
    const hostPart = parsed.host.indexOf(':') !== -1 ? '[' + parsed.host + ']' : parsed.host;
    const origin = parsed.scheme + '://' + hostPart + (parsed.port !== null ? ':' + parsed.port : '');
    if (/^\/\//.test(r)) return parsed.scheme + ':' + r;
    if (/^\//.test(r)) return origin + r;
    let dir = parsed.rest.replace(/[?#].*$/, '');
    dir = dir.substring(0, dir.lastIndexOf('/') + 1) || '/';
    return origin + dir + r;
}

function privateIpv4(ip: string): boolean {
    const parts = ip.split('.');
    if (parts.length !== 4) return true;
    const n: number[] = [];
    for (let i = 0; i < 4; i++) {
        if (!/^\d{1,3}$/.test(parts[i]!)) return true;
        n.push(parseInt(parts[i]!, 10));
        if (n[i]! > 255) return true;
    }
    const a = n[0]!;
    const b = n[1]!;
    if (a === 0 || a === 10 || a === 127) return true;
    if (a === 100 && b >= 64 && b <= 127) return true;   /* carrier-grade NAT */
    if (a === 169 && b === 254) return true;             /* link-local */
    if (a === 172 && b >= 16 && b <= 31) return true;
    if (a === 192 && b === 168) return true;
    if (a === 192 && b === 0 && (n[2] === 0 || n[2] === 2)) return true;   /* IETF, TEST-NET-1 */
    if (a === 198 && (b === 18 || b === 19)) return true; /* benchmarking */
    if (a === 198 && b === 51 && n[2] === 100) return true;
    if (a === 203 && b === 0 && n[2] === 113) return true;
    if (a >= 224) return true;                           /* multicast, reserved, broadcast */
    return false;
}

/* Expand an IPv6 literal to eight 16-bit groups; null when malformed. */
function ipv6Groups(value: string): number[] | null {
    let v = value;
    /* A dotted IPv4 tail (::ffff:1.2.3.4) becomes two hex groups first. */
    const v4 = /^(.*:)(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(v);
    if (v4) {
        const o: number[] = [];
        for (let i = 2; i <= 5; i++) {
            const n = parseInt(v4[i]!, 10);
            if (n > 255 || v4[i]!.length > 3) return null;
            o.push(n);
        }
        v = v4[1]! + (o[0]! * 256 + o[1]!).toString(16) + ':' + (o[2]! * 256 + o[3]!).toString(16);
    }
    const halves = v.split('::');
    if (halves.length > 2) return null;
    function groups(s: string): number[] | null {
        if (!s.length) return [];
        const out: number[] = [];
        const parts = s.split(':');
        for (let i = 0; i < parts.length; i++) {
            if (!/^[0-9a-f]{1,4}$/i.test(parts[i]!)) return null;
            out.push(parseInt(parts[i]!, 16));
        }
        return out;
    }
    const head = groups(halves[0]!);
    const rest = halves.length === 2 ? groups(halves[1]!) : [];
    if (!head || !rest) return null;
    const known = head.length + rest.length;
    if (halves.length === 1 && known !== 8) return null;
    if (halves.length === 2 && known > 7) return null;
    const out = head.slice();
    for (let i = known; i < 8; i++) out.push(0);
    return out.concat(rest);
}

export function isPrivateAddress(ip: string): boolean {
    const value = String(ip || '').toLowerCase().replace(/^\[|\]$/g, '');
    if (!value.length) return true;
    if (value.indexOf(':') === -1) return privateIpv4(value);
    const g = ipv6Groups(value);
    if (!g) return true;
    let zero = true;
    for (let i = 0; i < 7; i++) if (g[i] !== 0) zero = false;
    if (zero && (g[7] === 0 || g[7] === 1)) return true;                   /* :: and ::1 */
    if (g[0] === 0 && g[1] === 0 && g[2] === 0 && g[3] === 0 && g[4] === 0 && g[5] === 0xffff) {
        return privateIpv4(((g[6]! >> 8) & 255) + '.' + (g[6]! & 255) + '.' + ((g[7]! >> 8) & 255) + '.' + (g[7]! & 255));
    }
    const first = g[0]!;
    if ((first & 0xfe00) === 0xfc00) return true;   /* fc00::/7 unique-local */
    if ((first & 0xffc0) === 0xfe80) return true;   /* fe80::/10 link-local */
    if ((first & 0xffc0) === 0xfec0) return true;   /* fec0::/10 old site-local */
    if ((first & 0xff00) === 0xff00) return true;   /* multicast */
    if (first === 0x2001 && g[1] === 0x0db8) return true;            /* documentation */
    if (first === 0x0064 && g[1] === 0xff9b && g[2] === 0x0001) return true;   /* local-use NAT64 */
    if (first === 0x0100 && g[1] === 0 && g[2] === 0 && g[3] === 0) return true;   /* discard-only */
    if (first === 0 && g[1] === 0 && g[2] === 0 && g[3] === 0 && g[4] === 0 && g[5] === 0) return true;   /* IPv4-compatible */
    return false;
}

/* ---- which link gets a card (the door's linkprev.PreviewURL) ---- */

const URL_PATTERN = /https?:\/\/[^\s<>"'\x01|]+/gi;
const TRAILING = /[).,;:!?]+$/;
export const IMAGE_EXT = /\.(png|jpe?g|gif|webp|bmp)(?:[?#]|$)/i;
const VIDEO_EXT = /\.(mp4|webm|mov|m4v|mkv)(?:[?#]|$)/i;
const AUDIO_EXT = /\.(mp3|ogg|oga|opus|wav|flac|m4a|aac)(?:[?#]|$)/i;
const ANSI_EXT = /\.(ans|asc)(?:[?#]|$)/i;
const MEDIA_PATH = /\/chatmedia\/\d{4}-\d{2}-\d{2}\/[a-f0-9]{24}\.[a-z0-9]{1,4}(?:[?#]|$)/i;

export function findUrls(text: string): string[] {
    const out: string[] = [];
    const seen: { [u: string]: boolean } = {};
    const src = String(text || '');
    if (src.indexOf('://') === -1) return out;
    const matches = src.match(URL_PATTERN) || [];
    for (let i = 0; i < matches.length; i++) {
        const u = matches[i]!.replace(TRAILING, '');
        if (!u.length || seen[u] || !parseUrl(u)) continue;
        seen[u] = true;
        out.push(u);
    }
    return out;
}

function urlPath(u: string): string {
    return u.replace(/^https?:\/\/[^/?#]*/i, '').replace(/[?#].*$/, '');
}

export function previewUrl(text: string): string {
    const urls = findUrls(text);
    for (let i = 0; i < urls.length; i++) {
        const u = urls[i]!;
        const p = urlPath(u);
        if (IMAGE_EXT.test(p) || VIDEO_EXT.test(p) || AUDIO_EXT.test(p) || ANSI_EXT.test(p) ||
            MEDIA_PATH.test(u) || u.length > MAX_URL_LENGTH) continue;
        return u;
    }
    return '';
}

/* ---- metadata ---- */

/* Bodies stay "UTF-8 bytes in a JS string" (how HTTPRequest delivers them
 * and how write() sends them), so entities decode to UTF-8 byte runs. */
function codepointToUtf8(code: number): string {
    if (code < 0x80) return String.fromCharCode(code);
    if (code < 0x800) return String.fromCharCode(0xc0 | (code >> 6), 0x80 | (code & 0x3f));
    if (code < 0x10000) return String.fromCharCode(0xe0 | (code >> 12), 0x80 | ((code >> 6) & 0x3f), 0x80 | (code & 0x3f));
    return String.fromCharCode(0xf0 | (code >> 18), 0x80 | ((code >> 12) & 0x3f), 0x80 | ((code >> 6) & 0x3f), 0x80 | (code & 0x3f));
}

export function decodeEntities(text: string): string {
    return String(text || '')
        .replace(/&#x([0-9a-f]+);/gi, function (_m: string, h: string) {
            const code = parseInt(h, 16);
            return code > 0 && code < 0x110000 ? codepointToUtf8(code) : '';
        })
        .replace(/&#(\d+);/g, function (_m: string, d: string) {
            const code = parseInt(d, 10);
            return code > 0 && code < 0x110000 ? codepointToUtf8(code) : '';
        })
        .replace(/&nbsp;/gi, ' ')
        .replace(/&quot;/gi, '"')
        .replace(/&apos;/gi, "'")
        .replace(/&lt;/gi, '<')
        .replace(/&gt;/gi, '>')
        .replace(/&amp;/gi, '&');
}

/* Drop a UTF-8 sequence a length cap cut in half. */
function trimUtf8End(value: string): string {
    let end = value.length;
    while (end > 0 && (value.charCodeAt(end - 1) & 0xc0) === 0x80) end--;
    if (end > 0) {
        const lead = value.charCodeAt(end - 1);
        if (lead >= 0xc0) {
            const expected = lead >= 0xf0 ? 4 : lead >= 0xe0 ? 3 : 2;
            if (end - 1 + expected <= value.length) end = end - 1 + expected;
            else end--;
        }
    }
    return value.substring(0, end);
}

const UTF8_ELLIPSIS = '\xe2\x80\xa6';

export function cleanText(text: string, cap: number): string {
    const value = decodeEntities(text)
        .replace(/[\x00-\x1f\x7f]/g, ' ')
        .replace(/\s+/g, ' ')
        .replace(/^\s+|\s+$/g, '');
    if (value.length <= cap) return value;
    return trimUtf8End(value.substring(0, cap - UTF8_ELLIPSIS.length)) + UTF8_ELLIPSIS;
}

function attrFromTag(tag: string, name: string): string | null {
    const match = new RegExp('\\b' + name + '\\s*=\\s*(?:"([^"]*)"|\'([^\']*)\'|([^\\s>]+))', 'i').exec(tag);
    if (!match) return null;
    if (match[1] !== undefined) return match[1];
    if (match[2] !== undefined) return match[2];
    return match[3] === undefined ? null : match[3];
}

export interface PageMeta { title: string; description: string; image: string; siteName: string; audio: string; }

export function parseMetadata(html: string, baseUrl: string): PageMeta {
    const source = String(html || '').substring(0, PARSE_CAP_BYTES);
    const tags = source.match(/<meta\b[^>]*>/gi) || [];
    const meta: { [k: string]: string } = {};
    for (let i = 0; i < tags.length; i++) {
        const tag = tags[i]!;
        let key = attrFromTag(tag, 'property') || attrFromTag(tag, 'name');
        const content = attrFromTag(tag, 'content');
        if (!key || content === null) continue;
        key = key.toLowerCase();
        if (meta[key] === undefined) meta[key] = content;
    }
    const titleMatch = /<title[^>]*>([\s\S]*?)<\/title>/i.exec(source);
    function absolute(raw: string | undefined): string {
        if (!raw) return '';
        const r = resolveUrl(decodeEntities(raw), baseUrl);
        return r && /^https?:\/\//i.test(r) ? r : '';
    }
    return {
        title: cleanText(meta['og:title'] || meta['twitter:title'] || (titleMatch ? titleMatch[1]! : ''), TITLE_CAP),
        description: cleanText(meta['og:description'] || meta['twitter:description'] || meta['description'] || '', DESCRIPTION_CAP),
        image: absolute(meta['og:image'] || meta['og:image:url'] || meta['twitter:image']),
        siteName: cleanText(meta['og:site_name'] || '', SITE_NAME_CAP),
        audio: absolute(meta['og:audio'] || meta['og:audio:url'] || meta['og:audio:secure_url'])
    };
}

export function urlBasename(url: string): string {
    const segments = urlPath(url).split('/');
    return segments[segments.length - 1] || '';
}
