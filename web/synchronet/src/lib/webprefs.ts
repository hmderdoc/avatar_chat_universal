/* The web page's own per-user settings: which page style the user picked
 * and which rooms are in their sidebar. One small record per account in
 * data/chat/webprefs/NNNN.json, kept apart from Synchronet's user records
 * and from the shared nickstyle record (the door, the shell and Futureland's
 * site never read this one). */

import { channelKey, isArray, sanitizeChannel, trimText } from './util';

/* Page styles: each one a complete look (colours and shape), picked from
 * one list. The CSS for each is .ac-style-<id> in browser/app.css. */
export interface PageStyle { id: string; label: string; }
export const PAGE_STYLES: PageStyle[] = [
    { id: 'classic', label: 'Classic' },
    { id: 'cga', label: 'CGA' },
    { id: 'modern-dark', label: 'Modern dark' },
    { id: 'modern-light', label: 'Modern light' },
    { id: 'amber', label: 'Amber phosphor' },
    { id: 'green', label: 'Green phosphor' },
    { id: 'win95', label: 'Windows 95' },
    { id: 'compact', label: 'Flat compact' }
];
export const DEFAULT_PAGE_STYLE = 'classic';

export const PREFS_VERSION = 1;
export const MAX_ROOMS = 50;

export interface WebPrefs {
    version: number;
    style: string;          /* '' = the sysop's default */
    rooms: string[] | null; /* null = never set: the default room only */
}

export function emptyPrefs(): WebPrefs {
    return { version: PREFS_VERSION, style: '', rooms: null };
}

function knownStyle(id: string): boolean {
    for (let i = 0; i < PAGE_STYLES.length; i++) if (PAGE_STYLES[i]!.id === id) return true;
    return false;
}

/* The styles a sysop offers: web_styles (a list of ids) or all of them,
 * in this file's order; never empty. */
export function offeredStyles(allow: string[]): PageStyle[] {
    const out: PageStyle[] = [];
    for (let i = 0; i < PAGE_STYLES.length; i++) {
        if (!allow.length || allow.indexOf(PAGE_STYLES[i]!.id) >= 0) out.push(PAGE_STYLES[i]!);
    }
    return out.length ? out : PAGE_STYLES.slice();
}

/* The sysop's default: web_default_style when it is offered, else the
 * first offered one. */
export function defaultStyle(wanted: string, offered: PageStyle[]): string {
    for (let i = 0; i < offered.length; i++) if (offered[i]!.id === wanted) return wanted;
    for (let i = 0; i < offered.length; i++) if (offered[i]!.id === DEFAULT_PAGE_STYLE) return DEFAULT_PAGE_STYLE;
    return offered[0]!.id;
}

/* What the page wears: the user's pick when the sysop still offers it. */
export function resolveStyle(picked: string, offered: PageStyle[], fallback: string): string {
    for (let i = 0; i < offered.length; i++) if (offered[i]!.id === picked) return picked;
    return fallback;
}

export function cleanRooms(list: unknown): string[] {
    const out: string[] = [];
    const seen: { [k: string]: boolean } = {};
    const src = isArray(list) ? list : [];
    for (let i = 0; i < src.length && out.length < MAX_ROOMS; i++) {
        if (typeof src[i] !== 'string') continue;
        const name = sanitizeChannel(src[i], '');
        const key = channelKey(name);
        /* only names stored as they are spelled: nothing to strip or cut */
        if (!name.length || seen[key] || src[i] !== name) continue;
        seen[key] = true;
        out.push(name);
    }
    return out;
}

export function normalizePrefs(raw: unknown): WebPrefs {
    const out = emptyPrefs();
    const src = raw && typeof raw === 'object' ? raw as { style?: unknown; rooms?: unknown } : {};
    const style = trimText(src.style).toLowerCase();
    out.style = knownStyle(style) ? style : '';
    out.rooms = isArray(src.rooms) ? cleanRooms(src.rooms) : null;
    return out;
}

/* The sidebar's rooms: the stored list, or the default room for someone
 * who never joined or left anything. */
export function joinedRooms(prefs: WebPrefs, defaultRoom: string): string[] {
    return prefs.rooms === null ? [defaultRoom] : prefs.rooms.slice();
}

export function addRoom(rooms: string[], name: string): string[] {
    const key = channelKey(name);
    for (let i = 0; i < rooms.length; i++) if (channelKey(rooms[i]) === key) return rooms.slice();
    const out = rooms.slice();
    if (out.length < MAX_ROOMS) out.push(name);
    return out;
}

export function removeRoom(rooms: string[], name: string): string[] {
    const key = channelKey(name);
    const out: string[] = [];
    for (let i = 0; i < rooms.length; i++) if (channelKey(rooms[i]) !== key) out.push(rooms[i]!);
    return out;
}

/* ---- the record on disk (Synchronet side) ---- */

function prefsPath(number: number): string {
    return system.data_dir + 'chat/webprefs/' + format('%04u', number) + '.json';
}

export function readPrefs(number: number): WebPrefs {
    if (!(number > 0)) return emptyPrefs();
    const path = prefsPath(number);
    if (!file_exists(path)) return emptyPrefs();
    const f = new File(path);
    if (!f.open('r')) return emptyPrefs();
    let raw = '';
    try { raw = f.read() || ''; } finally { f.close(); }
    try { return normalizePrefs(JSON.parse(raw)); } catch (_e) { return emptyPrefs(); }
}

/* Replace the record atomically through a temp file, like style.ts. */
export function writePrefs(number: number, prefs: WebPrefs): boolean {
    if (!(number > 0)) return false;
    const clean = normalizePrefs(prefs);
    const dir = system.data_dir + 'chat/webprefs/';
    if (!file_isdir(dir)) mkpath(dir);
    const path = prefsPath(number);
    const tmp = path + '.tmp' + String(Math.floor(Math.random() * 1000000000));
    const f = new File(tmp);
    if (!f.open('w+')) return false;
    try { f.write(JSON.stringify(clean, null, 1)); } finally { f.close(); }
    if (!file_rename(tmp, path)) {
        try { file_remove(tmp); } catch (_e) { /* nothing to undo */ }
        return false;
    }
    return true;
}
