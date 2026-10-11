/* Chat handle styles: per-letter colours plus a tag, one record per account
 * in data/chat/nickstyle/NNNN.json. The same file the door (Synchronet
 * mode), the terminal shell and Futureland's site read and write; normalize
 * mirrors chat_style_lib.js and the door's nickstyle.Normalize exactly, so a
 * record saved here reads the same everywhere. */

import { isArray } from './util';

export const STYLE_VERSION = 1;
export const MAX_COLORS = 60;
export const MAX_TAG = 12;

export interface StyleTag { text: string; fg: string; bg: string; }
export interface HandleStyle { version: number; colors: string[]; tag: StyleTag; }

const HEX = /^#[0-9a-f]{6}$/;

export function hex(value: unknown): string {
    let s = String(value === undefined || value === null ? '' : value).replace(/^\s+|\s+$/g, '').toLowerCase();
    if (/^#[0-9a-f]{3}$/.test(s)) s = '#' + s.charAt(1) + s.charAt(1) + s.charAt(2) + s.charAt(2) + s.charAt(3) + s.charAt(3);
    return HEX.test(s) ? s : '';
}

function cleanTag(value: unknown): string {
    return String(value === undefined || value === null ? '' : value)
        .replace(/[^\x20-\x7e]/g, '')
        .replace(/\|/g, '')
        .replace(/^\s+|\s+$/g, '')
        .substr(0, MAX_TAG);
}

export function emptyStyle(): HandleStyle {
    return { version: STYLE_VERSION, colors: [], tag: { text: '', fg: '', bg: '' } };
}

export function normalizeStyle(raw: unknown): HandleStyle {
    const out = emptyStyle();
    const src = raw && typeof raw === 'object' ? raw as { colors?: unknown; tag?: unknown } : {};
    const list = isArray(src.colors) ? src.colors : [];
    for (let i = 0; i < list.length && i < MAX_COLORS; i++) out.colors.push(hex(list[i]));
    while (out.colors.length && !out.colors[out.colors.length - 1]!.length) out.colors.pop();
    if (src.tag && typeof src.tag === 'object') {
        const tag = src.tag as { text?: unknown; fg?: unknown; bg?: unknown };
        out.tag.text = cleanTag(tag.text);
        out.tag.fg = hex(tag.fg);
        out.tag.bg = hex(tag.bg);
    }
    if (!out.tag.text.length) out.tag.fg = out.tag.bg = '';
    return out;
}

export function styleIsEmpty(style: HandleStyle | null): boolean {
    if (!style) return true;
    for (let i = 0; i < style.colors.length; i++) if (style.colors[i]!.length) return false;
    return !style.tag.text.length;
}

/* The style trimmed to name's length, as it rides on a message nick; null
 * when there is nothing to send. */
export function wireStyle(style: HandleStyle | null, name: string): { colors: string[]; tag: StyleTag } | null {
    if (styleIsEmpty(style)) return null;
    const s = style as HandleStyle;
    const colors = s.colors.slice(0, String(name || '').length);
    while (colors.length && !colors[colors.length - 1]!.length) colors.pop();
    if (!colors.length && !s.tag.text.length) return null;
    return { colors: colors, tag: { text: s.tag.text, fg: s.tag.fg, bg: s.tag.bg } };
}

/* ---- the record on disk (Synchronet side) ---- */

function stylePath(number: number): string {
    return system.data_dir + 'chat/nickstyle/' + format('%04u', number) + '.json';
}

export function readStyle(number: number): HandleStyle {
    if (!(number > 0)) return emptyStyle();
    const path = stylePath(number);
    if (!file_exists(path)) return emptyStyle();
    const f = new File(path);
    if (!f.open('r')) return emptyStyle();
    let raw = '';
    try { raw = f.read() || ''; } finally { f.close(); }
    try { return normalizeStyle(JSON.parse(raw)); } catch (_e) { return emptyStyle(); }
}

/* Replace the record (an empty style deletes it), atomically through a
 * temp file the way chat_style_lib.js set() does. */
export function writeStyle(number: number, style: unknown): { ok: boolean; style: HandleStyle; reason?: string } {
    const clean = normalizeStyle(style);
    if (!(number > 0)) return { ok: false, style: clean, reason: 'no such user' };
    const path = stylePath(number);
    if (styleIsEmpty(clean)) {
        if (file_exists(path)) file_remove(path);
        return { ok: true, style: clean };
    }
    const dir = system.data_dir + 'chat/nickstyle/';
    if (!file_isdir(dir)) mkpath(dir);
    const tmp = path + '.tmp' + String(Math.floor(Math.random() * 1000000000));
    const f = new File(tmp);
    if (!f.open('w+')) return { ok: false, style: clean, reason: 'write failed' };
    try { f.write(JSON.stringify(clean, null, 1)); } finally { f.close(); }
    if (!file_rename(tmp, path)) {
        try { file_remove(tmp); } catch (_e) { /* nothing to undo */ }
        return { ok: false, style: clean, reason: 'write failed' };
    }
    return { ok: true, style: clean };
}
