/* Link cards: the BBS fetches the page (never the browser), so the guard
 * in linkguard.ts decides what it may reach. Results are cached on disk:
 * link_preview_ttl_hours for a card, one hour for a failure.
 *
 * Limits: the body is read only up to 256 KB and the socket closed (http.js
 * would buffer a whole download), with a Range header for servers that
 * honour it; websrvr answers Range on a dynamic page with 416, so that is
 * retried without one. The address check runs
 * before HTTPRequest resolves the name again, so a DNS answer that changes
 * between the two (rebinding) is not caught; the door, which dials the
 * checked address itself, does not have that gap. */

import { parseMetadata, resolveUrl, urlBasename, validateTarget, isPrivateAddress } from './linkguard';

const MAX_REDIRECTS = 3;
const RECV_TIMEOUT_SECONDS = 8;
const FAIL_TTL_SECONDS = 60 * 60;

export interface Card {
    url?: string;
    finalUrl?: string;
    kind?: string;
    title?: string;
    description?: string;
    image?: string;
    siteName?: string;
    audio?: string;
    audioType?: string;
    video?: string;
    error?: string;
}

const MAX_BODY = 262144;

function checkHostPublic(host: string): string | null {
    let target: string | null = host;
    if (!/^\d+\.\d+\.\d+\.\d+$/.test(host) && host.indexOf(':') === -1) {
        try { target = resolve_ip(host); } catch (_e) { target = null; }
        if (!target) return 'unresolvable host';
    }
    return isPrivateAddress(target) ? 'address not allowed' : null;
}

/* One GET, reading at most MAX_BODY bytes of the body. */
function getCapped(url: string, withRange: boolean): { req: HTTPRequest; body: string } {
    const headers: { [name: string]: string } = {};
    if (withRange) headers['Range'] = 'bytes=0-' + (MAX_BODY - 1);
    const req = new HTTPRequest(undefined, undefined, headers, RECV_TIMEOUT_SECONDS);
    req.SetupGet(url);
    req.SendRequest();
    let body = '';
    try {
        req.ReadStatus();
        req.ReadHeaders();
        const code = req.response_code || 0;
        if (code < 300 || code >= 400) {
            let chunk: string | null;
            while (body.length < MAX_BODY && req.sock && (chunk = req.sock.recv(Math.min(16384, MAX_BODY - body.length), RECV_TIMEOUT_SECONDS)) !== null && chunk !== '') {
                body += chunk;
            }
        }
    } finally {
        if (req.sock) req.sock.close();
    }
    return { req: req, body: body };
}

/* The Content-Disposition file name, base name only, or ''. */
export function dispositionName(value: string): string {
    const v = String(value || '');
    let m = /filename\*\s*=\s*(?:UTF-8|utf-8)?'[^']*'([^;]+)/.exec(v);
    let name = '';
    if (m) {
        try { name = decodeURIComponent(m[1]!.trim()); } catch (_e) { name = m[1]!.trim(); }
    } else if ((m = /filename\s*=\s*"([^"]*)"/.exec(v)) || (m = /filename\s*=\s*([^;\s]+)/.exec(v))) {
        name = m[1]!;
    }
    const parts = name.split(/[\\/]/);
    return parts[parts.length - 1] || '';
}

function fetchGuarded(url: string): { error?: string; finalUrl?: string; body?: string; contentType?: string; fileName?: string } {
    let current = String(url);
    for (let hop = 0; hop <= MAX_REDIRECTS; hop++) {
        const check = validateTarget(current);
        if (!check.ok) return { error: check.error };
        const hostError = checkHostPublic(check.host || '');
        if (hostError) return { error: hostError };
        let got: { req: HTTPRequest; body: string };
        try {
            got = getCapped(current, true);
            if ((got.req.response_code || 0) === 416) got = getCapped(current, false);
        } catch (_e) {
            return { error: 'fetch failed' };
        }
        const req = got.req;
        const body = got.body;
        const code = req.response_code || 0;
        const headers = req.response_headers_parsed || {};
        if (code >= 300 && code < 400) {
            const location = headers['location'] && headers['location'][0];
            if (!location) return { error: 'bad redirect' };
            const next = resolveUrl(location, current);
            if (!next) return { error: 'bad redirect' };
            current = next;
            continue;
        }
        if (code !== 200 && code !== 206) return { error: 'http ' + code };
        const contentType = String((headers['content-type'] && headers['content-type'][0]) || '').toLowerCase();
        const disposition = String((headers['content-disposition'] && headers['content-disposition'][0]) || '');
        return { finalUrl: current, body: body || '', contentType: contentType, fileName: dispositionName(disposition) };
    }
    return { error: 'too many redirects' };
}

function readCache(path: string, ttlSeconds: number): Card | null {
    if (!file_exists(path)) return null;
    const f = new File(path);
    if (!f.open('r')) return null;
    let raw = '';
    try { raw = f.read(); } finally { f.close(); }
    let parsed: { fetched?: number; data?: Card } | null = null;
    try { parsed = JSON.parse(raw); } catch (_e) { return null; }
    if (!parsed || typeof parsed.fetched !== 'number' || !parsed.data) return null;
    const ttl = parsed.data.error ? FAIL_TTL_SECONDS : ttlSeconds;
    return time() - parsed.fetched > ttl ? null : parsed.data;
}

function writeCache(dir: string, path: string, data: Card): void {
    if (!file_isdir(dir)) mkpath(dir);
    const f = new File(path);
    if (!f.open('w')) return;
    try { f.write(JSON.stringify({ fetched: time(), data: data })); } finally { f.close(); }
}

export function getCard(url: string, cacheDir: string, ttlHours: number): Card {
    const check = validateTarget(url);
    if (!check.ok) return { error: check.error };
    const path = cacheDir + md5_calc(String(url), true) + '.json';
    const cached = readCache(path, ttlHours * 3600);
    if (cached) return cached;
    const fetched = fetchGuarded(url);
    let result: Card;
    if (fetched.error) {
        result = { error: fetched.error };
    } else if ((fetched.contentType || '').indexOf('image/') === 0) {
        result = { url: url, finalUrl: fetched.finalUrl, kind: 'image', title: urlBasename(fetched.finalUrl || url), description: '', image: fetched.finalUrl, siteName: '' };
    } else if ((fetched.contentType || '').indexOf('text/html') !== 0 && (fetched.contentType || '').indexOf('application/xhtml') !== 0) {
        result = mediaCard(url, fetched.finalUrl || url, fetched.contentType || '', fetched.fileName || '', fetched.body || '') || { error: 'not html' };
    } else {
        const meta = parseMetadata(fetched.body || '', fetched.finalUrl || url);
        if (!meta.title.length && !meta.description.length && !meta.image && !meta.audio) {
            result = { error: 'no metadata' };
        } else {
            result = {
                url: url, finalUrl: fetched.finalUrl, kind: 'site',
                title: meta.title, description: meta.description,
                image: meta.image, siteName: meta.siteName, audio: meta.audio
            };
        }
    }
    writeCache(cacheDir, path, result);
    return result;
}

/* ---- links that serve an audio or video file themselves ----
 * Same rule as the door's internal/linkprev mediaFile: a download link
 * such as files.ssjs?call=download-file&file=Song.mp3 names its file only
 * in the query and Content-Disposition and is often served as
 * application/octet-stream. audio/* or video/* by type; otherwise, for a
 * generic type, the attachment's file name, then the first bytes. */

const GENERIC_TYPES = ['', 'application/octet-stream', 'binary/octet-stream', 'application/x-download', 'application/download', 'application/force-download'];

const NAME_TYPES: { [ext: string]: string } = {
    '.mp3': 'audio/mpeg', '.ogg': 'audio/ogg', '.oga': 'audio/ogg', '.opus': 'audio/opus',
    '.flac': 'audio/flac', '.wav': 'audio/wav', '.m4a': 'audio/mp4', '.aac': 'audio/aac',
    '.mp4': 'video/mp4', '.m4v': 'video/mp4', '.webm': 'video/webm', '.mov': 'video/quicktime', '.mkv': 'video/x-matroska'
};

export function typeForName(name: string): string {
    const m = /(\.[^.\\/]+)$/.exec(String(name || '').toLowerCase());
    return m ? (NAME_TYPES[m[1]!] || '') : '';
}

/* The common audio and video containers by their first bytes (the body is
 * a binary string: one char per byte), '' when it is none of them. */
export function sniffMedia(b: string): string {
    const at = function (i: number): number { return b.charCodeAt(i) & 0xff; };
    if (b.length >= 3 && b.substr(0, 3) === 'ID3') return 'audio/mpeg';
    if (b.length >= 2 && at(0) === 0xff && (at(1) & 0xe0) === 0xe0 && (at(1) & 0x06) !== 0) return 'audio/mpeg';
    if (b.length >= 4 && b.substr(0, 4) === 'OggS') return 'audio/ogg';
    if (b.length >= 4 && b.substr(0, 4) === 'fLaC') return 'audio/flac';
    if (b.length >= 12 && b.substr(0, 4) === 'RIFF' && b.substr(8, 4) === 'WAVE') return 'audio/wav';
    if (b.length >= 12 && b.substr(4, 4) === 'ftyp') return b.substr(8, 3) === 'M4A' ? 'audio/mp4' : 'video/mp4';
    if (b.length >= 4 && at(0) === 0x1a && at(1) === 0x45 && at(2) === 0xdf && at(3) === 0xa3) return 'video/webm';
    return '';
}

export function mediaCard(url: string, finalUrl: string, contentType: string, fileName: string, body: string): Card | null {
    const ct = String(contentType || '').split(';')[0]!.trim().toLowerCase();
    let typ = '';
    if (ct.indexOf('audio/') === 0 || ct.indexOf('video/') === 0) typ = ct;
    else if (GENERIC_TYPES.indexOf(ct) >= 0) typ = typeForName(fileName) || sniffMedia(body);
    const name = fileName && fileName !== '.' && fileName !== '/' ? fileName : urlBasename(finalUrl);
    let siteName = '';
    const hm = /^https?:\/\/(?:[^@\/]*@)?(\[[^\]]+\]|[^:\/?#]+)/i.exec(finalUrl);
    if (hm) siteName = hm[1]!.replace(/^\[|\]$/g, '');
    const card: Card = { url: url, finalUrl: finalUrl, kind: 'site', title: name, description: '', siteName: siteName };
    if (typ.indexOf('audio/') === 0) {
        card.audio = finalUrl;
        card.audioType = typ;
    } else if (typ.indexOf('video/') === 0) {
        card.video = finalUrl;
    } else {
        return null;
    }
    return card;
}
