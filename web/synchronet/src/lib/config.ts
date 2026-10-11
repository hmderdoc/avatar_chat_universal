/* Settings for the web page, read from the door's own avatar_chat.ini so
 * the page and the door always point at the same chat server and rooms.
 *
 * Door keys are read as the door reads them (top of the file). Keys only
 * the web page uses live in a [web] section, prefixed web_ so the door's
 * flat parser can never mistake one for its own. The [web] section has to
 * come last in the file: Synchronet's INI reader puts every key after a
 * section header into that section. */

import { parseBool, parseIntOr, sanitizeChannel, splitList, trimText } from './util';

export interface WebConfig {
    iniPath: string;
    iniFound: boolean;
    host: string;
    port: number;
    defaultChannel: string;
    motdChannel: string;
    motdHostQwkid: string;
    motdHostSystem: string;
    maxHistory: number;
    images: boolean;
    linkPreviews: boolean;
    linkPreviewTtlHours: number;
    nsfwKeywords: string[];
    uploads: boolean;
    uploadMaxMb: number;
    uploadVideoMaxMb: number;
    uploadTypes: string[];
    uploadRetentionDays: number;
    uploadDir: string;
    uploadUrlBase: string;
    uploadsPerHour: number;
}

/* The door's DefaultNSFWKeywords (internal/inlineimg/urls.go). */
export const DEFAULT_NSFW_KEYWORDS = [
    'nude', 'nudes', 'nudity', 'naked', 'nsfw', 'topless', 'sex', 'sexy', 'sexual', 'porn', 'porno',
    'xxx', 'erotic', 'erotica', 'boobs', 'breasts', 'tits', 'nipples', 'nipple', 'genitals', 'penis', 'vagina',
    'pussy', 'dick', 'cock', 'orgy', 'lingerie', 'thong', 'stripper', 'striptease', 'undressed', 'undressing'
];

/* Every stored extension the upload endpoint knows. Unless the sysop lists
 * types in web_upload_types, only those stock ctrl/mime_types.ini already
 * serves with the right Content-Type are taken; webp, ogg, flac, m4a and
 * webm need a mime_types.ini line first (README.md). */
export const ALL_UPLOAD_TYPES = ['png', 'jpg', 'gif', 'webp', 'mp3', 'ogg', 'wav', 'flac', 'm4a', 'mp4', 'webm'];
export const DEFAULT_UPLOAD_TYPES = ['png', 'jpg', 'gif', 'mp3', 'wav', 'mp4'];

/* get(section, key) returns the raw value or null; section null is the top
 * of the file. Kept apart from File so the tests can feed a map. */
export type IniGetter = (section: string | null, key: string) => string | null;

export function buildConfig(get: IniGetter, iniPath: string, iniFound: boolean): WebConfig {
    function top(key: string, def: string): string {
        const v = get(null, key);
        return v === null || !trimText(v).length ? def : trimText(v);
    }
    /* [web] first; a key left at the top (the old bundle's motd_host_*)
       still counts. */
    function web(key: string, def: string): string {
        let v = get('web', key);
        if (v === null || !trimText(v).length) v = get(null, key);
        return v === null || !trimText(v).length ? def : trimText(v);
    }

    const nsfw = top('nsfw_keywords', '');
    let keywords = DEFAULT_NSFW_KEYWORDS.slice();
    if (nsfw.toLowerCase() === 'none') keywords = [];
    else if (nsfw.length) keywords = splitList(nsfw);

    const allowed: string[] = [];
    const wanted = splitList(web('web_upload_types', ''));
    for (let i = 0; i < ALL_UPLOAD_TYPES.length; i++) {
        const t = ALL_UPLOAD_TYPES[i]!;
        const list = wanted.length ? wanted : DEFAULT_UPLOAD_TYPES;
        if (list.indexOf(t) >= 0 || (t === 'jpg' && list.indexOf('jpeg') >= 0)) allowed.push(t);
    }

    return {
        iniPath: iniPath,
        iniFound: iniFound,
        host: top('host', '127.0.0.1'),
        port: parseIntOr(top('port', '10088'), 10088),
        defaultChannel: sanitizeChannel(top('default_channel', 'main'), 'main'),
        motdChannel: sanitizeChannel(top('motd_channel', 'motd'), 'motd'),
        motdHostQwkid: web('web_motd_host_qwkid', web('motd_host_qwkid', '')).toUpperCase(),
        motdHostSystem: web('web_motd_host_system', web('motd_host_system', '')),
        maxHistory: Math.max(20, Math.min(500, parseIntOr(top('max_history', '200'), 200))),
        images: parseBool(top('images', 'on'), true),
        linkPreviews: parseBool(top('link_previews', 'on'), true),
        linkPreviewTtlHours: Math.max(1, parseIntOr(top('link_preview_ttl_hours', '24'), 24)),
        nsfwKeywords: keywords,
        uploads: parseBool(web('web_uploads', 'off'), false),
        uploadMaxMb: Math.max(1, parseIntOr(web('web_upload_max_mb', '20'), 20)),
        uploadVideoMaxMb: Math.max(1, parseIntOr(web('web_upload_video_max_mb', '50'), 50)),
        uploadTypes: allowed,
        uploadRetentionDays: Math.max(0, parseIntOr(web('web_upload_retention_days', '7'), 7)),
        uploadDir: web('web_upload_dir', ''),
        uploadUrlBase: web('web_upload_url_base', '').replace(/\/+$/, ''),
        uploadsPerHour: Math.max(0, parseIntOr(web('web_uploads_per_hour', '30'), 30))
    };
}

/* Where avatar_chat.ini is: [web] avatar_chat_ini in modopts.ini, else the
 * door's usual install directory next to exec/. */
export function iniPathFor(webSettings: { avatar_chat_ini?: unknown } | null, execDir: string): string {
    const override = webSettings ? trimText(webSettings.avatar_chat_ini) : '';
    if (override.length) return override;
    return execDir + '../xtrn/avatar_chat_universal/avatar_chat.ini';
}

/* Synchronet side: open the file and read it through File.iniGetValue. */
export function loadConfig(): WebConfig {
    const webSettings = typeof settings === 'object' && settings ? settings : null;
    const path = iniPathFor(webSettings, system.exec_dir);
    const f = new File(path);
    if (!f.open('r')) {
        log(LOG_WARNING, 'avatarchat: cannot read ' + path + '; using defaults (127.0.0.1:10088)');
        return buildConfig(function () { return null; }, path, false);
    }
    try {
        return buildConfig(function (section, key) {
            const v = f.iniGetValue(section, key, '');
            return v === null || v === undefined ? null : String(v);
        }, path, true);
    } finally {
        f.close();
    }
}
