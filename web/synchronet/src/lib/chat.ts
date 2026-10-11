/* The chat operations behind the API, talking to the JSON chat service the
 * same way the door does: live packets WRITE to channels.<room>.messages,
 * the durable copy is PUSHed to channels.<room>.history, private messages
 * go to the recipient's mailbox channels.<alias>.messages, and a public
 * room is registered under public_channels.<room>. */

import { WebConfig } from './config';
import { current as currentAvatar } from './avatars';
import { normalizeStyle, readStyle, wireStyle, HandleStyle } from './style';
import { channelKey, isArray, normalizeUpper, ownKeys, sanitizeAlias, sanitizeChannel, trimText } from './util';

export const MAX_TEXT = 1000;          /* what the server keeps of a line */
export const MAX_BITMAP_TEXT = 32000;  /* a [BITMAP|...] art message */
const WRITE_TIMEOUT_MS = 5000;

export interface Nick {
    name: string;
    host: string;
    qwkid: string;
    avatar: string;
    style: HandleStyle | null;
}

export interface ChatMessage {
    sender: string;
    system: string;
    qwkid?: string;
    text: string;
    timestamp: number;
    userNumber: number;
    isSelf: boolean;
    avatar?: string;
    style?: { colors: string[]; tag: { text: string; fg: string; bg: string } };
    private: boolean;
    peerName?: string;
    peerSystem?: string;
    peerAvatar?: string;
}

interface RawMessage {
    nick?: unknown;
    str?: unknown;
    time?: unknown;
    private?: { to?: unknown };
}

export interface Viewer {
    number: number;
    alias: string;
    authed: boolean;
    sysop: boolean;
}

export function currentViewer(): Viewer {
    const guest = typeof settings === 'object' && settings ? String(settings.guest || '') : '';
    const authed = user.number > 0 && !(guest.length && user.alias === guest);
    let sysop = false;
    if (authed) {
        if (user.is_sysop === true) sysop = true;
        else if (user.security && typeof user.security.level === 'number' && user.security.level >= 90) sysop = true;
        else {
            try { sysop = user.compare_ars('SYSOP') === true; } catch (_e) { sysop = false; }
        }
    }
    return { number: authed ? user.number : 0, alias: authed ? String(user.alias) : '', authed: authed, sysop: sysop };
}

/* ---- MOTD: only the host BBS's sysop writes it ---- */

export function canManageMotd(cfg: WebConfig, viewer: Viewer): boolean {
    if (!viewer.sysop) return false;
    const localQwkid = normalizeUpper(system.qwk_id);
    const localSystem = normalizeUpper(system.name);
    if (cfg.motdHostQwkid.length) return localQwkid === normalizeUpper(cfg.motdHostQwkid);
    if (cfg.motdHostSystem.length) return localSystem === normalizeUpper(cfg.motdHostSystem);
    const host = normalizeUpper(cfg.host);
    if (host === '127.0.0.1' || host === 'LOCALHOST' || host === '::1') return true;
    return localSystem === host;
}

export function isMotdChannel(name: string, cfg: WebConfig): boolean {
    return channelKey(name) === channelKey(cfg.motdChannel);
}

export function defaultChannel(cfg: WebConfig, viewer: Viewer): string {
    if (isMotdChannel(cfg.defaultChannel, cfg) && !canManageMotd(cfg, viewer)) return 'main';
    return cfg.defaultChannel;
}

/* A requested room, or null when the viewer may not use it. */
export function usableChannel(raw: string, cfg: WebConfig, viewer: Viewer): string | null {
    const name = sanitizeChannel(raw, defaultChannel(cfg, viewer));
    if (isMotdChannel(name, cfg) && !canManageMotd(cfg, viewer)) return null;
    if (isMailboxName(name)) return null;
    return name;
}

/* channels.<alias> is a person's mailbox, not a room; a public room never
 * shares a name with a local account. */
function isMailboxName(name: string): boolean {
    try { return system.matchuser(name) > 0; } catch (_e) { return false; }
}

/* ---- nicks and messages ---- */

export function normalizeNick(nick: unknown): Nick | null {
    if (!nick || typeof nick !== 'object') return null;
    const n = nick as { name?: unknown; host?: unknown; qwkid?: unknown; avatar?: unknown; style?: unknown };
    const name = sanitizeAlias(n.name);
    if (!name.length) return null;
    return {
        name: name,
        host: trimText(n.host),
        qwkid: normalizeUpper(n.qwkid),
        avatar: trimText(n.avatar),
        style: n.style && typeof n.style === 'object' ? normalizeStyle(n.style) : null
    };
}

function isPrivateMessage(message: RawMessage | null): boolean {
    if (!message || !message.private || typeof message.private !== 'object') return false;
    const to = normalizeNick(message.private.to);
    return !!to;
}

function samePerson(name: string, host: string, alias: string): boolean {
    return normalizeUpper(name) === normalizeUpper(alias) &&
        (!host.length || normalizeUpper(host) === normalizeUpper(system.name));
}

function peerOf(message: RawMessage, ownAlias: string): Nick | null {
    const sender = normalizeNick(message.nick);
    const recipient = normalizeNick(message.private ? message.private.to : null);
    if (!sender || !recipient) return null;
    return normalizeUpper(sender.name) === normalizeUpper(ownAlias) ? recipient : sender;
}

function localNumber(nick: Nick | null): number {
    if (!nick) return 0;
    if (nick.host.length && normalizeUpper(nick.host) !== normalizeUpper(system.name)) return 0;
    try { return system.matchuser(nick.name) || 0; } catch (_e) { return 0; }
}

export function formatMessage(raw: unknown, viewer: Viewer): ChatMessage {
    const message = (raw && typeof raw === 'object' ? raw : {}) as RawMessage;
    const nick = normalizeNick(message.nick);
    const sender = nick ? nick.name : '';
    const host = nick ? nick.host : '';
    const out: ChatMessage = {
        sender: sender,
        system: host,
        text: message.str === undefined || message.str === null ? '' : String(message.str),
        timestamp: typeof message.time === 'number' ? message.time : 0,
        userNumber: localNumber(nick),
        isSelf: viewer.authed && sender.length > 0 && samePerson(sender, host, viewer.alias),
        private: isPrivateMessage(message)
    };
    if (nick && nick.qwkid.length) out.qwkid = nick.qwkid;
    if (nick && nick.avatar.length) out.avatar = nick.avatar;
    const style = nick ? wireStyle(nick.style, sender) : null;
    if (style) out.style = style;
    if (out.private && viewer.authed) {
        const peer = peerOf(message, viewer.alias);
        if (peer) {
            out.peerName = peer.name;
            if (peer.host.length) out.peerSystem = peer.host;
            if (peer.avatar.length) out.peerAvatar = peer.avatar;
        }
    }
    return out;
}

/* What this viewer's messages carry: name, BBS, QWK id, their Synchronet
 * avatar and their handle style. */
export function ownNick(viewer: Viewer): { [k: string]: unknown } {
    const nick: { [k: string]: unknown } = {
        name: viewer.alias,
        host: system.name,
        ip: user.ip_address || '0.0.0.0'
    };
    if (trimText(system.qwk_id).length) nick['qwkid'] = system.qwk_id;
    /* A disabled avatar isn't sent: nobody sees it until it's enabled. */
    try {
        const a = currentAvatar(viewer.number);
        if (a && !a.disabled) nick['avatar'] = a.data;
    } catch (_e) { /* no avatar library: send without */ }
    const style = wireStyle(readStyle(viewer.number), viewer.alias);
    if (style) nick['style'] = style;
    return nick;
}

/* ---- the JSON service ---- */

export function withClient<T>(cfg: WebConfig, fn: (client: JSONClient) => T): T | { error: string } {
    let client: JSONClient | null = null;
    try {
        client = new JSONClient(cfg.host, cfg.port);
        client.settings.TIMEOUT = WRITE_TIMEOUT_MS;
        const result = fn(client);
        client.disconnect();
        return result;
    } catch (e) {
        log(LOG_ERR, 'avatarchat: ' + e);
        if (client) {
            try { client.disconnect(); } catch (_d) { /* already gone */ }
        }
        const err = e as { message?: string };
        return { error: err && err.message ? String(err.message) : 'chat service error' };
    }
}

function recent(client: JSONClient, location: string, count: number): RawMessage[] {
    let list: unknown = [];
    try { list = client.slice('chat', location, -count, undefined, 1) || []; } catch (_e) { list = []; }
    return isArray(list) ? list as RawMessage[] : [];
}

function ensureHistory(client: JSONClient, location: string): void {
    let existing: unknown = null;
    try { existing = client.read('chat', location, 1); } catch (_e) { existing = null; }
    if (isArray(existing)) return;
    /* The Go server keeps WRITE values apart from PUSH arrays and builds the
       array on the first PUSH, so an unset location is fine there too. */
    try { client.write('chat', location, [], 2); } catch (_e) { /* PUSH creates it */ }
}

function registerRoom(client: JSONClient, name: string): void {
    client.write('chat', 'public_channels.' + name, true, 2);
}

export function history(client: JSONClient, channel: string, count: number, viewer: Viewer): ChatMessage[] {
    const raw = recent(client, 'channels.' + channel + '.history', count);
    const out: ChatMessage[] = [];
    for (let i = 0; i < raw.length; i++) {
        if (!isPrivateMessage(raw[i]!)) out.push(formatMessage(raw[i], viewer));
    }
    return out;
}

export interface WhoUser { nick: string; system: string; userNumber: number; qwkid?: string; avatar?: string; style?: ChatMessage['style']; }

export function who(client: JSONClient, channel: string): WhoUser[] {
    const result = client.who('chat', 'channels.' + channel + '.messages') || {};
    const users: WhoUser[] = [];
    const seen: { [k: string]: number } = {};
    const keys = ownKeys(result);
    for (let i = 0; i < keys.length; i++) {
        const entry = (isArray(result) ? (result as unknown[])[i] : (result as { [k: string]: unknown })[keys[i]!]) as { nick?: unknown; system?: unknown } | null;
        const nick = normalizeNick(entry && entry.nick && typeof entry.nick === 'object' ? entry.nick : null);
        const name = nick ? nick.name : sanitizeAlias(entry && entry.nick ? entry.nick : keys[i]);
        if (!name.length) continue;
        const host = nick && nick.host.length ? nick.host : trimText(entry && entry.system ? entry.system : '');
        const qwkid = nick ? nick.qwkid : '';
        const id = normalizeUpper(name) + '|' + normalizeUpper(qwkid || host);
        const existing = seen[id];
        if (typeof existing === 'number') {
            const u = users[existing]!;
            if (!u.avatar && nick && nick.avatar) u.avatar = nick.avatar;
            if (!u.style && nick) { const s = wireStyle(nick.style, name); if (s) u.style = s; }
            continue;
        }
        seen[id] = users.length;
        const u: WhoUser = { nick: name, system: host, userNumber: localNumber(nick || { name: name, host: host, qwkid: '', avatar: '', style: null }) };
        if (qwkid.length) u.qwkid = qwkid;
        if (nick && nick.avatar.length) u.avatar = nick.avatar;
        const s = nick ? wireStyle(nick.style, name) : null;
        if (s) u.style = s;
        users.push(u);
    }
    return users;
}

function hasPublicTraffic(list: RawMessage[]): boolean {
    for (let i = 0; i < list.length; i++) if (!isPrivateMessage(list[i]!)) return true;
    return false;
}

export function roomNames(client: JSONClient, cfg: WebConfig, viewer: Viewer): string[] {
    const seen: { [k: string]: boolean } = {};
    const names: string[] = [];
    function add(value: string): void {
        const n = sanitizeChannel(value, '');
        if (!n.length || seen[n.toUpperCase()]) return;
        if (isMotdChannel(n, cfg) && !canManageMotd(cfg, viewer)) return;
        seen[n.toUpperCase()] = true;
        names.push(n);
    }
    add(defaultChannel(cfg, viewer));
    if (canManageMotd(cfg, viewer)) add(cfg.motdChannel);
    let registry: string[] = [];
    let channels: string[] = [];
    try { registry = ownKeys(client.keys('chat', 'public_channels', 1)); } catch (_e) { registry = []; }
    try { channels = ownKeys(client.keys('chat', 'channels', 1)); } catch (_e) { channels = []; }
    for (let i = 0; i < registry.length; i++) add(registry[i]!);
    for (let i = 0; i < channels.length; i++) {
        const n = sanitizeChannel(channels[i], '');
        if (!n.length || seen[n.toUpperCase()] || n !== channels[i]) continue;
        if (isMailboxName(n)) continue;
        if (hasPublicTraffic(recent(client, 'channels.' + n + '.history', 20))) add(n);
    }
    return names.sort();
}

export interface RoomSummary { name: string; userCount: number; lastTimestamp: number; newCount: number; }

export function rooms(client: JSONClient, cfg: WebConfig, viewer: Viewer, since: number): RoomSummary[] {
    const names = roomNames(client, cfg, viewer);
    const out: RoomSummary[] = [];
    for (let i = 0; i < names.length; i++) {
        const name = names[i]!;
        const list = recent(client, 'channels.' + name + '.history', Math.max(60, cfg.maxHistory));
        let last = 0;
        let fresh = 0;
        for (let j = 0; j < list.length; j++) {
            const m = list[j]!;
            if (isPrivateMessage(m)) continue;
            const t = typeof m.time === 'number' ? m.time : 0;
            if (t > last) last = t;
            const nick = normalizeNick(m.nick);
            if (since > 0 && t > since && nick && normalizeUpper(nick.name) !== normalizeUpper(viewer.alias)) fresh++;
        }
        let count = 0;
        try { count = who(client, name).length; } catch (_e) { count = 0; }
        out.push({ name: name, userCount: count, lastTimestamp: last, newCount: since > 0 ? fresh : 0 });
    }
    out.sort(function (a, b) { return (b.lastTimestamp || 0) - (a.lastTimestamp || 0); });
    return out;
}

export function latestMotd(client: JSONClient, cfg: WebConfig, viewer: Viewer): ChatMessage | null {
    const list = recent(client, 'channels.' + cfg.motdChannel + '.history', Math.max(10, cfg.maxHistory));
    for (let i = list.length - 1; i >= 0; i--) {
        if (!isPrivateMessage(list[i]!)) return formatMessage(list[i], viewer);
    }
    return null;
}

function mailboxHistory(alias: string): string { return 'channels.' + alias + '.history'; }
function mailboxLive(alias: string): string { return 'channels.' + alias + '.messages'; }

function threadKey(nick: Nick): string {
    return normalizeUpper(nick.name).replace(/[^A-Z0-9]/g, '') + '|' + (nick.qwkid || normalizeUpper(nick.host));
}

export interface ThreadSummary { name: string; system: string; avatar?: string; lastTimestamp: number; preview: string; newCount: number; }

export function privateThreads(client: JSONClient, cfg: WebConfig, viewer: Viewer, since: number): ThreadSummary[] {
    const list = recent(client, mailboxHistory(viewer.alias), Math.max(80, cfg.maxHistory * 2));
    const threads: { [k: string]: ThreadSummary } = {};
    const order: string[] = [];
    for (let i = 0; i < list.length; i++) {
        const m = list[i]!;
        if (!isPrivateMessage(m)) continue;
        const peer = peerOf(m, viewer.alias);
        if (!peer) continue;
        const key = threadKey(peer);
        const t = typeof m.time === 'number' ? m.time : 0;
        if (!threads[key]) {
            threads[key] = { name: peer.name, system: peer.host, lastTimestamp: 0, preview: '', newCount: 0 };
            order.push(key);
        }
        const th = threads[key]!;
        if (t >= th.lastTimestamp) {
            th.lastTimestamp = t;
            th.preview = m.str === undefined || m.str === null ? '' : String(m.str);
            if (peer.avatar.length) th.avatar = peer.avatar;
            if (peer.host.length) th.system = peer.host;
        }
        const sender = normalizeNick(m.nick);
        if (since > 0 && t > since && sender && normalizeUpper(sender.name) !== normalizeUpper(viewer.alias)) th.newCount++;
    }
    const out: ThreadSummary[] = [];
    for (let i = 0; i < order.length; i++) out.push(threads[order[i]!]!);
    out.sort(function (a, b) { return (b.lastTimestamp || 0) - (a.lastTimestamp || 0); });
    return out;
}

export function privateHistory(client: JSONClient, cfg: WebConfig, viewer: Viewer, target: string, targetSystem: string): { peer: { name: string; system: string; avatar?: string } | null; messages: ChatMessage[] } {
    const list = recent(client, mailboxHistory(viewer.alias), Math.max(80, cfg.maxHistory * 2));
    const wantName = normalizeUpper(target);
    const wantSystem = normalizeUpper(targetSystem);
    const messages: ChatMessage[] = [];
    let selected: Nick | null = null;
    for (let i = 0; i < list.length; i++) {
        const m = list[i]!;
        if (!isPrivateMessage(m)) continue;
        const peer = peerOf(m, viewer.alias);
        if (!peer || normalizeUpper(peer.name) !== wantName) continue;
        if (wantSystem.length && peer.host.length && normalizeUpper(peer.host) !== wantSystem) continue;
        if (!selected) selected = peer;
        messages.push(formatMessage(m, viewer));
    }
    let peerOut: { name: string; system: string; avatar?: string } | null = null;
    if (selected) {
        peerOut = { name: selected.name, system: selected.host };
        if (selected.avatar.length) peerOut.avatar = selected.avatar;
    }
    return { peer: peerOut, messages: messages };
}

function confirmLatest(client: JSONClient, location: string, packet: { time: number; str: string }): void {
    /* The last few, not just the last: someone else may have posted in
       between. */
    const latest = recent(client, location, 5);
    for (let i = latest.length - 1; i >= 0; i--) {
        const entry = latest[i]!;
        if (entry.time === packet.time && String(entry.str || '') === packet.str) return;
    }
    throw new Error('message did not persist');
}

export function sendPublic(client: JSONClient, viewer: Viewer, channel: string, text: string): { success: boolean; channel: string; timestamp: number } {
    const packet = { nick: ownNick(viewer), str: text, time: Date.now() };
    const base = 'channels.' + channel;
    ensureHistory(client, base + '.history');
    registerRoom(client, channel);
    client.write('chat', base + '.messages', packet, 2);
    client.push('chat', base + '.history', packet, 2);
    confirmLatest(client, base + '.history', packet);
    return { success: true, channel: channel, timestamp: packet.time };
}

export function sendPrivate(client: JSONClient, viewer: Viewer, target: string, targetSystem: string, text: string): { success: boolean; target: string; timestamp: number } {
    const to: { [k: string]: string } = { name: target };
    if (targetSystem.length) to['host'] = targetSystem;
    const packet = { nick: ownNick(viewer), str: text, time: Date.now(), private: { to: to } };
    ensureHistory(client, mailboxHistory(target));
    ensureHistory(client, mailboxHistory(viewer.alias));
    client.write('chat', mailboxLive(target), packet, 2);
    client.push('chat', mailboxHistory(target), packet, 2);
    client.push('chat', mailboxHistory(viewer.alias), packet, 2);
    confirmLatest(client, mailboxHistory(viewer.alias), packet);
    return { success: true, target: target, timestamp: packet.time };
}

export function createRoom(client: JSONClient, channel: string): { success: boolean; channel: string } {
    ensureHistory(client, 'channels.' + channel + '.history');
    registerRoom(client, channel);
    return { success: true, channel: channel };
}
