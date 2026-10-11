/* lib/events/avatarchat.js - live updates for the page through webv4's
 * event stream (./api/events.ssjs?subscribe=avatarchat). events.ssjs calls
 * cycle() about once a second and, in newer webv4, dispose() when the
 * browser goes away; js.on_exit covers the older one that doesn't.
 *
 * One JSON-service connection per open page, subscribed to the room and,
 * for a logged-in user, their mailbox. Guests get nothing: stock webv4
 * already refuses them this module, and the check here holds either way. */

import { loadConfig } from '../lib/config';
import * as chat from '../lib/chat';

const cfg = loadConfig();
const viewer = chat.currentViewer();

function query(name: string): string {
    const v = typeof http_request !== 'undefined' && http_request.query ? http_request.query[name] : undefined;
    return v && v.length ? String(v[0]) : '';
}

const channel = chat.usableChannel(query('channel') || chat.defaultChannel(cfg, viewer), cfg, viewer) || chat.defaultChannel(cfg, viewer);
const channelPath = 'channels.' + channel + '.messages';
const mailboxPath = viewer.authed && query('mailbox') !== '0' ? 'channels.' + viewer.alias + '.messages' : '';

let client: JSONClient | null = null;
let lastReconnect = 0;
const RECONNECT_SECONDS = 5;

function connect(): boolean {
    try {
        client = new JSONClient(cfg.host, cfg.port);
        client.subscribe('chat', channelPath);
        if (mailboxPath.length) client.subscribe('chat', mailboxPath);
        return true;
    } catch (e) {
        log(LOG_WARNING, 'avatarchat event: connect failed: ' + e);
        client = null;
        return false;
    }
}

export function dispose(): void {
    if (!client) return;
    const c = client;
    client = null;
    try { c.unsubscribe('chat', channelPath); } catch (_e) { /* closing anyway */ }
    if (mailboxPath.length) {
        try { c.unsubscribe('chat', mailboxPath); } catch (_e) { /* closing anyway */ }
    }
    try { c.disconnect(); } catch (_e) { /* already gone */ }
}

function send(payload: { [k: string]: unknown }): void {
    emit({ event: 'avatarchat', data: JSON.stringify(payload) });
}

function handle(packet: JsonPacket): void {
    if (!packet || !packet.location || !packet.oper) return;
    const oper = String(packet.oper).toUpperCase();
    const data = (packet.data || {}) as { nick?: unknown };
    if (packet.location === channelPath) {
        if (oper === 'WRITE') {
            const m = chat.formatMessage(data, viewer);
            if (m.private) return;
            const out: { [k: string]: unknown } = { type: 'message', channel: channel };
            for (const k in m) if (Object.prototype.hasOwnProperty.call(m, k)) out[k] = (m as unknown as { [k: string]: unknown })[k];
            send(out);
        } else if (oper === 'SUBSCRIBE' || oper === 'UNSUBSCRIBE') {
            const nick = chat.normalizeNick(data.nick);
            send({
                type: oper === 'SUBSCRIBE' ? 'join' : 'part',
                channel: channel,
                sender: nick ? nick.name : '',
                system: nick ? nick.host : '',
                timestamp: Date.now()
            });
        }
        return;
    }
    if (mailboxPath.length && packet.location === mailboxPath && oper === 'WRITE') {
        const m = chat.formatMessage(data, viewer);
        if (!m.private) return;
        const out: { [k: string]: unknown } = { type: 'private' };
        for (const k in m) if (Object.prototype.hasOwnProperty.call(m, k)) out[k] = (m as unknown as { [k: string]: unknown })[k];
        if (!out['peerName']) out['peerName'] = m.sender;
        send(out);
    }
}

export function cycle(): void {
    if (!viewer.authed) return;
    if (!client || !client.connected) {
        client = null;
        if (time() - lastReconnect < RECONNECT_SECONDS) return;
        lastReconnect = time();
        if (!connect()) return;
    }
    try {
        const c = client as JSONClient;
        c.cycle();
        while (c.updates.length) handle(c.updates.shift() as JsonPacket);
    } catch (e) {
        log(LOG_WARNING, 'avatarchat event: ' + e);
        dispose();
    }
}
