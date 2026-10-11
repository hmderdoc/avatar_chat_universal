/* The web mailbox convention: a standalone web user (the bundled server's
 * page, no BBS login) receives private messages at channels.<mailbox>,
 * where mailbox is name@origin(host), and says so in nick.mailbox. A BBS
 * user has no mailbox field and keeps the plain-name mailbox.
 *
 * A mailbox is honoured only when it is exactly name + '@' + origin(host)
 * (any case) with no '.', so a forged one can't redirect replies; anything
 * else falls back to the plain name. Same rule as the door's
 * internal/chat/mailbox.go. The page never shows a mailbox string. */

/* host with every byte outside [A-Za-z0-9_-] replaced by '_', counting
 * bytes as Go does on the UTF-8 the JSON carried (so a non-ASCII character
 * becomes one '_' per UTF-8 byte), matching chat.Origin. */
export function origin(host: string): string {
    const h = String(host === undefined || host === null ? '' : host);
    let out = '';
    for (let i = 0; i < h.length; i++) {
        const c = h.charCodeAt(i);
        if ((c >= 0x30 && c <= 0x39) || (c >= 0x41 && c <= 0x5a) || (c >= 0x61 && c <= 0x7a) || c === 0x5f || c === 0x2d) out += h.charAt(i);
        else if (c < 0x80) out += '_';
        else if (c < 0x800) out += '__';
        else if (c >= 0xd800 && c <= 0xdbff) { out += '____'; i++; } /* a surrogate pair: 4 bytes */
        else out += '___';
    }
    return out;
}

/* The mailbox for name@host when `mailbox` is a valid one for them, else ''. */
export function validMailbox(name: string, host: string, mailbox: unknown): string {
    const m = String(mailbox === undefined || mailbox === null ? '' : mailbox);
    if (!m.length || !name.length || m.indexOf('.') >= 0) return '';
    return m.toLowerCase() === (name + '@' + origin(host)).toLowerCase() ? m : '';
}
