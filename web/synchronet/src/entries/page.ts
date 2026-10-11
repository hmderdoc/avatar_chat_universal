/* lib/avatarchat-page.js - what pages/avatarchat.xjs hands the browser:
 * who is looking, which room to open, what the sysop turned on, and the
 * write token for this login session. Loaded with load() from the page, so
 * it runs with the page's user and settings. */

import { loadConfig } from '../lib/config';
import { pageToken } from '../lib/csrf';
import * as chat from '../lib/chat';

export function pageConfig(): { [k: string]: unknown } {
    const cfg = loadConfig();
    const viewer = chat.currentViewer();
    const motd = chat.canManageMotd(cfg, viewer);
    return {
        server: cfg.host + ':' + cfg.port,
        configFound: cfg.iniFound,
        defaultChannel: chat.defaultChannel(cfg, viewer),
        motdChannel: cfg.motdChannel,
        canManageMotd: motd,
        userAlias: viewer.alias,
        userNumber: viewer.number,
        systemName: String(system.name || ''),
        isLoggedIn: viewer.authed,
        canUseRealtime: viewer.authed,
        token: viewer.authed ? pageToken(viewer.number) : '',
        maxText: chat.MAX_TEXT,
        images: cfg.images,
        linkPreviews: cfg.linkPreviews && viewer.authed,
        nsfwKeywords: cfg.nsfwKeywords,
        uploads: {
            enabled: cfg.uploads && viewer.authed,
            maxMb: cfg.uploadMaxMb,
            videoMaxMb: cfg.uploadVideoMaxMb,
            types: cfg.uploadTypes,
            retentionDays: cfg.uploadRetentionDays
        }
    };
}

/* JSON for a <script> block: "</" can't end the script early. */
export function pageConfigJson(): string {
    return JSON.stringify(pageConfig()).replace(/<\//g, '<\\/');
}
