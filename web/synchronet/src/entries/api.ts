/* avatarchat.ssjs - the page's API. The browser only ever talks to this
 * BBS; the BBS talks to the chat service.
 *
 *   GET  ?action=history&channel=main[&count=N]   public room history
 *   GET  ?action=who&channel=main                 who is in a room
 *   GET  ?action=channels[&since=ms]              room summaries
 *   GET  ?action=motd                             latest message of the day
 *   GET  ?action=sync&channel=..&since=..[&history=1&presence=1&motd=1]
 *                                                 one poll, one connection
 *   GET  ?action=private[&since=ms]               your private threads   (login)
 *   GET  ?action=privateHistory&target=Alias[&system=BBS]                (login)
 *   GET  ?action=style                            your handle style      (login)
 *   GET  ?action=preview&url=..                   a link card            (login)
 *   POST ?action=send&channel=..&message=..                              (login, token)
 *   POST ?action=sendPrivate&target=..[&system=..]&message=..           (login, token)
 *   POST ?action=createChannel&channel=..                                (login, token)
 *   POST ?action=saveStyle   body: JSON {colors, tag}                    (login, token)
 *
 * Writes need the X-AvatarChat-Token header the page was given (csrf.ts).
 * Guests can read public rooms and nothing else, as before. */

import { loadConfig, WebConfig } from '../lib/config';
import { writeAllowed } from '../lib/csrf';
import * as chat from '../lib/chat';
import { getCard } from '../lib/preview';
import { readStyle, writeStyle } from '../lib/style';
import { cleanMessageText, sanitizeAlias, trimText } from '../lib/util';

type Reply = { [k: string]: unknown };

function param(name: string): string {
    const v = http_request.query[name];
    return v && v.length ? String(v[0]) : '';
}

function has(name: string): boolean {
    const v = http_request.query[name];
    return !!(v && v.length);
}

function timestampParam(name: string): number {
    const n = parseInt(param(name), 10);
    return isNaN(n) || n < 1 ? 0 : n;
}

function flagOn(name: string, def: boolean): boolean {
    return has(name) ? param(name) !== '0' : def;
}

function countParam(cfg: WebConfig): number {
    const n = parseInt(param('count'), 10);
    return !isNaN(n) && n > 0 && n <= Math.max(60, cfg.maxHistory) ? n : 60;
}

function messageParam(): string {
    const raw = param('message');
    return cleanMessageText(raw, raw.indexOf('[BITMAP|') === 0 ? chat.MAX_BITMAP_TEXT : chat.MAX_TEXT);
}

function needLogin(viewer: chat.Viewer): Reply | null {
    return viewer.authed ? null : { error: 'authentication required' };
}

function needWrite(viewer: chat.Viewer): Reply | null {
    if (http_request.method !== 'POST') return { error: 'POST required' };
    if (!viewer.authed) return { error: 'authentication required' };
    if (!writeAllowed(viewer.number)) {
        http_reply.status = '403 Forbidden';
        return { error: 'stale page: reload it' };
    }
    return null;
}

function run(cfg: WebConfig, viewer: chat.Viewer, action: string): Reply {
    const requested = param('channel');
    const channel = chat.usableChannel(requested || chat.defaultChannel(cfg, viewer), cfg, viewer);
    let gate: Reply | null;

    switch (action) {
        case 'history':
            if (!channel) return { error: 'channel unavailable' };
            return chat.withClient(cfg, function (c) {
                return { channel: channel, messages: chat.history(c, channel, countParam(cfg), viewer), serverTime: Date.now() };
            });

        case 'who':
            if (!channel) return { error: 'channel unavailable' };
            return chat.withClient(cfg, function (c) {
                return { channel: channel, users: chat.who(c, channel), serverTime: Date.now() };
            });

        case 'channels':
            return chat.withClient(cfg, function (c) {
                return { channels: chat.rooms(c, cfg, viewer, timestampParam('since')), serverTime: Date.now() };
            });

        case 'motd':
            return chat.withClient(cfg, function (c) {
                const m = chat.latestMotd(c, cfg, viewer);
                return {
                    channel: cfg.motdChannel,
                    message: m,
                    previewText: m ? trimText(m.text) : '',
                    timestamp: m ? m.timestamp : 0,
                    serverTime: Date.now()
                };
            });

        case 'sync': {
            const active = channel || chat.defaultChannel(cfg, viewer);
            const since = timestampParam('since');
            return chat.withClient(cfg, function (c) {
                const out: Reply = { channel: active, serverTime: Date.now() };
                let summaries: chat.RoomSummary[] | null = null;
                if (flagOn('channels', true)) out['channels'] = summaries = chat.rooms(c, cfg, viewer, since);
                if (flagOn('who', true)) out['who'] = { channel: active, users: chat.who(c, active) };
                if (flagOn('history', false)) out['history'] = { channel: active, messages: chat.history(c, active, countParam(cfg), viewer) };
                if (flagOn('private', true) && viewer.authed) out['private'] = { threads: chat.privateThreads(c, cfg, viewer, since) };
                if (flagOn('motd', false)) {
                    const m = chat.latestMotd(c, cfg, viewer);
                    out['motd'] = { previewText: m ? trimText(m.text) : '', timestamp: m ? m.timestamp : 0 };
                }
                if (flagOn('presence', false)) {
                    /* who across occupied rooms plus the active one, all on
                       this connection. */
                    const names: string[] = [];
                    const seen: { [k: string]: boolean } = {};
                    const add = function (n: string): void {
                        const k = n.toUpperCase();
                        if (!n || seen[k]) return;
                        seen[k] = true;
                        names.push(n);
                    };
                    if (summaries) {
                        for (let i = 0; i < summaries.length; i++) if (summaries[i]!.userCount > 0) add(summaries[i]!.name);
                    } else {
                        const all = chat.roomNames(c, cfg, viewer);
                        for (let i = 0; i < all.length; i++) add(all[i]!);
                    }
                    add(active);
                    let people: chat.WhoUser[] = [];
                    for (let i = 0; i < names.length; i++) people = people.concat(chat.who(c, names[i]!));
                    out['presence'] = people;
                }
                return out;
            });
        }

        case 'private':
            if ((gate = needLogin(viewer))) return gate;
            return chat.withClient(cfg, function (c) {
                return { threads: chat.privateThreads(c, cfg, viewer, timestampParam('since')), serverTime: Date.now() };
            });

        case 'privateHistory': {
            if ((gate = needLogin(viewer))) return gate;
            const target = sanitizeAlias(param('target'));
            if (!target.length) return { error: 'target required' };
            return chat.withClient(cfg, function (c) {
                const r: Reply = chat.privateHistory(c, cfg, viewer, target, trimText(param('system')));
                r['serverTime'] = Date.now();
                return r;
            });
        }

        case 'createChannel':
            if ((gate = needWrite(viewer))) return gate;
            if (!channel) return { error: 'that room is reserved' };
            return chat.withClient(cfg, function (c) { return chat.createRoom(c, channel); });

        case 'send': {
            if ((gate = needWrite(viewer))) return gate;
            if (!channel) return { error: 'that room is reserved' };
            const text = messageParam();
            if (!trimText(text).length) return { error: 'empty message' };
            return chat.withClient(cfg, function (c) { return chat.sendPublic(c, viewer, channel, text); });
        }

        case 'sendPrivate': {
            if ((gate = needWrite(viewer))) return gate;
            const target = sanitizeAlias(param('target'));
            const text = messageParam();
            if (!target.length) return { error: 'target required' };
            if (!trimText(text).length) return { error: 'empty message' };
            return chat.withClient(cfg, function (c) {
                return chat.sendPrivate(c, viewer, target, trimText(param('system')), text);
            });
        }

        case 'style':
            if ((gate = needLogin(viewer))) return gate;
            return { ok: true, alias: viewer.alias, style: readStyle(viewer.number) };

        case 'saveStyle': {
            if ((gate = needWrite(viewer))) return gate;
            let body: { colors?: unknown; tag?: unknown } | null = null;
            try { body = JSON.parse(http_request.post_data || '{}'); } catch (_e) { body = null; }
            if (!body || typeof body !== 'object') return { error: 'bad request' };
            const saved = writeStyle(viewer.number, { colors: body.colors, tag: body.tag });
            return saved.ok ? { ok: true, style: saved.style } : { error: saved.reason || 'could not save' };
        }

        case 'preview': {
            /* The BBS makes the outbound request, so guests don't get to. */
            if ((gate = needLogin(viewer))) return gate;
            if (!cfg.linkPreviews) return { error: 'link previews are off' };
            const url = param('url');
            if (!url.length) return { error: 'missing url' };
            http_reply.header['Cache-Control'] = 'private, max-age=3600';
            return getCard(url, system.data_dir + 'avatarchat/previews/', cfg.linkPreviewTtlHours) as Reply;
        }
    }
    return { error: 'unknown action: ' + action };
}

http_reply.header['Content-Type'] = 'application/json; charset=utf-8';
http_reply.header['Cache-Control'] = 'no-store';
http_reply.header['X-Content-Type-Options'] = 'nosniff';

(function () {
    const cfg = loadConfig();
    const viewer = chat.currentViewer();
    let reply: Reply;
    try {
        reply = run(cfg, viewer, param('action'));
    } catch (e) {
        log(LOG_ERR, 'avatarchat.ssjs: ' + e);
        reply = { error: 'internal error' };
    }
    write(JSON.stringify(reply));
})();
