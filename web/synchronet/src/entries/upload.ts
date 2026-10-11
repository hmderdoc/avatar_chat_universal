/* avatarchat-upload.ssjs - pictures, audio and video dropped on the page,
 * stored on this BBS's own web server. Off unless web_uploads = on.
 *
 * The web server hands JavaScript a request body only up to 4 MiB, so the
 * page sends a file in base64 chunks:
 *
 *   POST ?action=begin&size=N              -> { id, chunkSize }
 *   POST ?action=chunk&id=..&seq=..        body: raw base64
 *   POST ?action=finish&id=..              -> { url, kind, bytes, expiresDays }
 *   POST ?action=abort&id=..
 *
 * Nothing about the file's name is trusted: at finish its first bytes say
 * what it is (media.ts sniff) and pick the stored extension, and the name is
 * a fresh 24-hex id under <web_root>/chatmedia/<date>/. The link goes in the
 * composer; the user decides whether to send it. Folders older than
 * web_upload_retention_days are removed when someone uploads. */

import { loadConfig, WebConfig } from '../lib/config';
import { writeAllowed } from '../lib/csrf';
import { currentViewer, Viewer } from '../lib/chat';
import { CHUNK_RAW_BYTES, dayFolderName, daysBetween, sniff, stripJpegMetadata, stripPngMetadata, validUploadId } from '../lib/media';
import { trimText } from '../lib/util';

const STAGE_STALE_SECONDS = 3600;
const MAX_OPEN_UPLOADS = 3;

interface Meta { id: string; user: number; size: number; received: number; nextSeq: number; started: number; }

function respond(payload: { [k: string]: unknown }): void {
    write(JSON.stringify(payload));
}

function param(name: string): string {
    const v = http_request.query[name];
    return v && v.length ? String(v[0]) : '';
}

function intParam(name: string): number {
    const n = parseInt(param(name), 10);
    return isNaN(n) ? -1 : n;
}

function readJson(path: string): Meta | null {
    const f = new File(path);
    if (!f.open('r')) return null;
    let text = '';
    try { text = f.read(); } finally { f.close(); }
    try { return JSON.parse(text) as Meta; } catch (_e) { return null; }
}

function writeJson(path: string, value: unknown): boolean {
    const f = new File(path);
    if (!f.open('w')) return false;
    try { f.write(JSON.stringify(value)); } finally { f.close(); }
    return true;
}

function newId(): string {
    let id = '';
    while (id.length < 24) id += format('%02x', random(256));
    return id.substr(0, 24);
}

function ensureDir(path: string): boolean {
    if (!file_isdir(path)) mkpath(path);
    return file_isdir(path);
}

function slash(path: string): string {
    return /[\/\\]$/.test(path) ? path : path + '/';
}

function mediaDir(cfg: WebConfig): string {
    return slash(cfg.uploadDir.length ? cfg.uploadDir : backslash(settings.web_root) + 'chatmedia');
}

/* The link has to be the public one: behind a proxy the request itself is
 * plain HTTP on some inside port. */
function publicBase(cfg: WebConfig): string {
    if (cfg.uploadUrlBase.length) return cfg.uploadUrlBase;
    const proto = trimText(http_request.header['x-forwarded-proto']) || http_request.scheme || 'http';
    const host = trimText(http_request.header['x-forwarded-host']) || trimText(http_request.vhost) ||
        trimText(http_request.header['host']) || system.inet_addr;
    return trimText(proto.split(',')[0]) + '://' + trimText(host.split(',')[0]) + '/chatmedia';
}

function today(): string { return strftime('%Y-%m-%d', time()); }

class Uploads {
    readonly stage: string;
    readonly media: string;
    constructor(readonly cfg: WebConfig, readonly viewer: Viewer) {
        this.stage = system.data_dir + 'avatarchat/upload/';
        this.media = mediaDir(cfg);
    }
    part(id: string): string { return this.stage + id + '.part'; }
    meta(id: string): string { return this.stage + id + '.json'; }

    drop(id: string): void {
        if (file_exists(this.part(id))) file_remove(this.part(id));
        if (file_exists(this.meta(id))) file_remove(this.meta(id));
    }

    /* Open uploads of this user; abandoned ones are swept on the way. */
    openCount(): number {
        const names = directory(this.stage + '*.json');
        let count = 0;
        for (let i = 0; i < names.length; i++) {
            const m = readJson(names[i]!);
            if (time() - file_date(names[i]!) > STAGE_STALE_SECONDS) {
                if (m && validUploadId(m.id)) this.drop(m.id);
                else file_remove(names[i]!);
                continue;
            }
            if (m && m.user === this.viewer.number) count++;
        }
        return count;
    }

    /* Uploads finished by this user in the last hour: one file of
       timestamps per user. */
    recentCount(record: boolean): number {
        const path = this.stage + 'rate-' + format('%04u', this.viewer.number) + '.json';
        let list: number[] = [];
        const f = new File(path);
        if (f.open('r')) {
            try { list = JSON.parse(f.read() || '[]'); } catch (_e) { list = []; } finally { f.close(); }
        }
        const now = time();
        const kept: number[] = [];
        for (let i = 0; i < list.length; i++) if (typeof list[i] === 'number' && now - list[i]! < 3600) kept.push(list[i]!);
        if (record) {
            kept.push(now);
            writeJson(path, kept);
        }
        return kept.length;
    }

    /* Remove day folders past the retention window. Only folders named
       YYYY-MM-DD and files named <24hex>.<ext> are ever touched. */
    purge(): void {
        const days = this.cfg.uploadRetentionDays;
        if (days <= 0) return;
        const now = today();
        const dirs = directory(this.media + '*');
        for (let i = 0; i < dirs.length; i++) {
            const d = dirs[i]!.replace(/[\/\\]+$/, '');
            const name = file_getname(d);
            if (!dayFolderName(name) || !file_isdir(d) || daysBetween(name, now) <= days) continue;
            const files = directory(slash(d) + '*');
            for (let j = 0; j < files.length; j++) {
                if (/^[a-f0-9]{24}\.[a-z0-9]{1,4}$/.test(file_getname(files[j]!))) file_remove(files[j]!);
            }
            rmdir(d);
        }
    }

    begin(): { [k: string]: unknown } {
        const size = intParam('size');
        const cap = Math.max(this.cfg.uploadMaxMb, this.cfg.uploadVideoMaxMb) * 1048576;
        if (size <= 0) return { ok: false, error: 'missing file size' };
        if (size > cap) return { ok: false, error: 'that file is over the ' + Math.round(cap / 1048576) + ' MB limit' };
        if (this.openCount() >= MAX_OPEN_UPLOADS) return { ok: false, error: 'too many uploads in progress; let those finish first' };
        if (this.cfg.uploadsPerHour > 0 && this.recentCount(false) >= this.cfg.uploadsPerHour) {
            return { ok: false, error: 'upload limit reached; try again in an hour' };
        }
        const id = newId();
        const f = new File(this.part(id));
        if (!f.open('wb')) return { ok: false, error: 'upload storage unavailable' };
        f.close();
        const meta: Meta = { id: id, user: this.viewer.number, size: size, received: 0, nextSeq: 0, started: time() };
        writeJson(this.meta(id), meta);
        return { ok: true, id: id, chunkSize: CHUNK_RAW_BYTES };
    }

    load(): Meta | { error: string } {
        const id = param('id');
        if (!validUploadId(id)) return { error: 'bad upload id' };
        const m = readJson(this.meta(id));
        if (!m || m.user !== this.viewer.number || m.id !== id) return { error: 'unknown upload' };
        return m;
    }

    chunk(): { [k: string]: unknown } {
        const m = this.load();
        if ('error' in m) return { ok: false, error: m.error };
        const seq = intParam('seq');
        const body = typeof http_request.post_data === 'string' ? http_request.post_data : '';
        if (!body.length) return { ok: false, error: 'empty chunk' };
        if (!/^[A-Za-z0-9+/=\r\n]+$/.test(body)) return { ok: false, error: 'chunk is not base64' };
        if (seq !== m.nextSeq) return { ok: false, error: 'chunk out of order', expected: m.nextSeq };
        const before = file_size(this.part(m.id));
        const f = new File(this.part(m.id));
        if (!f.open('ab')) return { ok: false, error: 'cannot append to upload' };
        f.base64 = true;   /* write() decodes */
        f.write(body);
        f.close();
        const after = file_size(this.part(m.id));
        if (after <= before) { this.drop(m.id); return { ok: false, error: 'chunk could not be decoded' }; }
        if (after > m.size) { this.drop(m.id); return { ok: false, error: 'upload is larger than declared' }; }
        m.received = after;
        m.nextSeq = seq + 1;
        writeJson(this.meta(m.id), m);
        return { ok: true, received: after, nextSeq: m.nextSeq };
    }

    finish(): { [k: string]: unknown } {
        const m = this.load();
        if ('error' in m) return { ok: false, error: m.error };
        const bytes = file_size(this.part(m.id));
        if (bytes <= 0 || bytes !== m.size) { this.drop(m.id); return { ok: false, error: 'upload is incomplete' }; }
        const f = new File(this.part(m.id));
        let head = '';
        if (f.open('rb')) {
            try { head = f.read(64); } finally { f.close(); }
        }
        const kind = sniff(head);
        if (!kind || this.cfg.uploadTypes.indexOf(kind.ext) < 0) {
            this.drop(m.id);
            return { ok: false, error: 'that file type cannot be posted to chat' };
        }
        const cap = (kind.kind === 'video' ? this.cfg.uploadVideoMaxMb : this.cfg.uploadMaxMb) * 1048576;
        if (bytes > cap) {
            this.drop(m.id);
            return { ok: false, error: 'that ' + kind.kind + ' is over the ' + Math.round(cap / 1048576) + ' MB limit' };
        }
        if (kind.ext === 'jpg' || kind.ext === 'png') this.stripMetadata(this.part(m.id), kind.ext);
        const folder = today();
        const dir = this.media + folder + '/';
        if (!ensureDir(dir)) { this.drop(m.id); return { ok: false, error: 'media storage unavailable' }; }
        const name = m.id + '.' + kind.ext;
        if (!file_rename(this.part(m.id), dir + name)) { this.drop(m.id); return { ok: false, error: 'could not store the upload' }; }
        this.drop(m.id);
        this.recentCount(true);
        this.purge();
        return {
            ok: true,
            url: publicBase(this.cfg) + '/' + folder + '/' + name,
            kind: kind.kind,
            bytes: file_size(dir + name),
            expiresDays: this.cfg.uploadRetentionDays
        };
    }

    /* Cut camera and location metadata out of a picture (media.ts). */
    stripMetadata(path: string, ext: string): void {
        const f = new File(path);
        if (!f.open('rb')) return;
        let data = '';
        try { data = f.read(file_size(path)); } finally { f.close(); }
        const clean = ext === 'jpg' ? stripJpegMetadata(data) : stripPngMetadata(data);
        if (clean === null) return;
        const out = new File(path);
        if (!out.open('wb')) return;
        try { out.write(clean); } finally { out.close(); }
    }

    abort(): { [k: string]: unknown } {
        const m = this.load();
        if (!('error' in m)) this.drop(m.id);
        return { ok: true };
    }
}

http_reply.header['Content-Type'] = 'application/json; charset=utf-8';
http_reply.header['Cache-Control'] = 'no-store';
http_reply.header['X-Content-Type-Options'] = 'nosniff';

(function () {
    const cfg = loadConfig();
    const viewer = currentViewer();
    if (!cfg.uploads) return respond({ ok: false, error: 'uploads are off on this board' });
    if (!viewer.authed) return respond({ ok: false, error: 'authentication required' });
    if (http_request.method !== 'POST') return respond({ ok: false, error: 'POST required' });
    if (!writeAllowed(viewer.number)) {
        http_reply.status = '403 Forbidden';
        return respond({ ok: false, error: 'stale page: reload it' });
    }
    const up = new Uploads(cfg, viewer);
    if (!ensureDir(up.stage) || !ensureDir(up.media)) return respond({ ok: false, error: 'upload storage unavailable' });
    try {
        switch (param('action')) {
            case 'begin': return respond(up.begin());
            case 'chunk': return respond(up.chunk());
            case 'finish': return respond(up.finish());
            case 'abort': return respond(up.abort());
        }
        respond({ ok: false, error: 'unknown action' });
    } catch (e) {
        log(LOG_ERR, 'avatarchat-upload.ssjs: ' + e);
        respond({ ok: false, error: 'internal error' });
    }
})();
