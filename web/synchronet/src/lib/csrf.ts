/* Cross-site request protection for every write (send, PM, room, style,
 * upload, preview). The stock webv4 validateCsrfToken() compares against a
 * session token that stock login never sets, so this package keeps its own:
 * the page mints one per login session into the user's data/user/NNNN.web
 * file and every write must echo it in an X-AvatarChat-Token header. A
 * browser won't attach a custom header to a cross-site request without a
 * CORS preflight, which the web server never grants. */

import { trimText } from './util';

const KEY = 'avatarchat_token';

function sessionPath(number: number): string {
    return format('%suser/%04d.web', system.data_dir, number);
}

function newToken(): string {
    let out = '';
    while (out.length < 32) out += format('%02x', random(256));
    return out;
}

function readToken(number: number): string {
    const f = new File(sessionPath(number));
    if (!f.open('r')) return '';
    try {
        const v = f.iniGetValue(null, KEY, '');
        return v === null || v === undefined ? '' : String(v);
    } finally {
        f.close();
    }
}

/* The session's token, minted on first use. Rotates with the login: the
 * web session key changes at every login, so the token is bound to it. */
export function pageToken(number: number): string {
    if (!(number > 0)) return '';
    const f = new File(sessionPath(number));
    if (!f.open(file_exists(sessionPath(number)) ? 'r+' : 'w+')) return '';
    try {
        const session = String(f.iniGetValue(null, 'key', '') || '');
        const stored = String(f.iniGetValue(null, KEY, '') || '');
        const bound = String(f.iniGetValue(null, KEY + '_session', '') || '');
        if (stored.length === 32 && bound === session) return stored;
        const token = newToken();
        f.iniSetValue(null, KEY, token);
        f.iniSetValue(null, KEY + '_session', session);
        return token;
    } finally {
        f.close();
    }
}

/* Origin, when the browser sends it, must be this site. */
function sameOrigin(): boolean {
    const origin = trimText(http_request.header['origin']);
    if (!origin.length) return true;
    const host = trimText(http_request.header['x-forwarded-host']) || trimText(http_request.header['host']) || trimText(http_request.host);
    const m = /^https?:\/\/([^/]+)$/i.exec(origin);
    if (!m) return false;
    return m[1]!.toLowerCase() === host.split(',')[0]!.replace(/^\s+|\s+$/g, '').toLowerCase();
}

export function writeAllowed(number: number): boolean {
    if (!(number > 0) || !sameOrigin()) return false;
    const sent = trimText(http_request.header['x-avatarchat-token']);
    const stored = readToken(number);
    return sent.length === 32 && sent === stored;
}
