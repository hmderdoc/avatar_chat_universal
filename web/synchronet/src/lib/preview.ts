/* Link cards: the BBS fetches the page (never the browser), so the guard
 * in linkguard.ts decides what it may reach. Results are cached on disk:
 * link_preview_ttl_hours for a card, one hour for a failure.
 *
 * Limits, the same as Futureland's lib/link-preview.js: http.js buffers the
 * whole response, so a Range header asks for the first 256 KB and the
 * receive timeout bounds a server that ignores it. The address check runs
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
    error?: string;
}

function checkHostPublic(host: string): string | null {
    let target: string | null = host;
    if (!/^\d+\.\d+\.\d+\.\d+$/.test(host) && host.indexOf(':') === -1) {
        try { target = resolve_ip(host); } catch (_e) { target = null; }
        if (!target) return 'unresolvable host';
    }
    return isPrivateAddress(target) ? 'address not allowed' : null;
}

function fetchGuarded(url: string): { error?: string; finalUrl?: string; body?: string; contentType?: string } {
    let current = String(url);
    for (let hop = 0; hop <= MAX_REDIRECTS; hop++) {
        const check = validateTarget(current);
        if (!check.ok) return { error: check.error };
        const hostError = checkHostPublic(check.host || '');
        if (hostError) return { error: hostError };
        const req = new HTTPRequest(undefined, undefined,
            ['Range: bytes=0-262143', 'Accept: text/html,application/xhtml+xml;q=0.9,*/*;q=0.5'],
            RECV_TIMEOUT_SECONDS);
        let body = '';
        try {
            body = req.Get(current);
        } catch (_e) {
            return { error: 'fetch failed' };
        }
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
        return { finalUrl: current, body: body || '', contentType: contentType };
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
        result = { error: 'not html' };
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
