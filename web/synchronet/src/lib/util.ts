/* Small string helpers shared by every entry point. Pure: no Synchronet
 * globals, so the node tests can load them. */

export function trimText(value: unknown): string {
    return String(value === undefined || value === null ? '' : value).replace(/^\s+|\s+$/g, '');
}

export function normalizeUpper(value: unknown): string {
    return trimText(value).toUpperCase();
}

/* Channel names are letters, digits, dash and underscore, as the door and
 * the shell allow. */
export function sanitizeChannel(raw: unknown, fallback?: string): string {
    const channel = String(raw || fallback || 'main').replace(/[^a-zA-Z0-9_-]/g, '').substr(0, 32);
    return channel.length ? channel : (fallback || 'main');
}

export function channelKey(value: unknown): string {
    return String(value || '').replace(/[^a-zA-Z0-9_-]/g, '').toUpperCase();
}

export function sanitizeAlias(raw: unknown): string {
    return trimText(String(raw || '')).replace(/[\x00-\x1f]/g, '').substr(0, 60);
}

/* What may go into a chat line: no control characters other than tab and
 * newline (CTRL-A colour codes and ANSI escapes would reach terminals). */
export function cleanMessageText(text: string, max: number): string {
    const out = String(text || '').replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g, '');
    return out.length > max ? out.substr(0, max) : out;
}

export function parseBool(value: unknown, fallback: boolean): boolean {
    const s = trimText(value).toLowerCase();
    if (s === 'true' || s === 'yes' || s === 'on' || s === '1') return true;
    if (s === 'false' || s === 'no' || s === 'off' || s === '0') return false;
    return fallback;
}

export function parseIntOr(value: unknown, fallback: number): number {
    const n = parseInt(trimText(value), 10);
    return isNaN(n) ? fallback : n;
}

export function splitList(value: unknown): string[] {
    const out: string[] = [];
    const parts = trimText(value).split(',');
    for (let i = 0; i < parts.length; i++) {
        const p = trimText(parts[i]).toLowerCase();
        if (p.length) out.push(p);
    }
    return out;
}

export function ownKeys(obj: unknown): string[] {
    const keys: string[] = [];
    if (Object.prototype.toString.call(obj) === '[object Array]') {
        const list = obj as unknown[];
        for (let i = 0; i < list.length; i++) keys.push(String(list[i]));
        return keys;
    }
    if (!obj || typeof obj !== 'object') return keys;
    for (const k in obj as object) {
        if (Object.prototype.hasOwnProperty.call(obj, k)) keys.push(k);
    }
    return keys;
}

export function isArray(value: unknown): value is unknown[] {
    return Object.prototype.toString.call(value) === '[object Array]';
}
