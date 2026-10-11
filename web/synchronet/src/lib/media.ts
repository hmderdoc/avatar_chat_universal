/* What an upload really is, judged by its first bytes, never by the name
 * or the browser's say-so. The stored extension comes from this table, so
 * nothing that could run under the web root (.ssjs, .xjs, .html, .svg)
 * can ever be written there. */

export interface MediaKind { ext: string; kind: 'image' | 'audio' | 'video'; }

function at(head: string, offset: number, text: string): boolean {
    return head.substr(offset, text.length) === text;
}

function byte(head: string, offset: number): number {
    return offset < head.length ? head.charCodeAt(offset) & 255 : -1;
}

/* head: the first bytes of the file as a byte string (charCode = byte). */
export function sniff(head: string): MediaKind | null {
    if (at(head, 0, '\x89PNG\r\n\x1a\n')) return { ext: 'png', kind: 'image' };
    if (byte(head, 0) === 0xff && byte(head, 1) === 0xd8 && byte(head, 2) === 0xff) return { ext: 'jpg', kind: 'image' };
    if (at(head, 0, 'GIF87a') || at(head, 0, 'GIF89a')) return { ext: 'gif', kind: 'image' };
    if (at(head, 0, 'RIFF') && at(head, 8, 'WEBP')) return { ext: 'webp', kind: 'image' };
    if (at(head, 0, 'RIFF') && at(head, 8, 'WAVE')) return { ext: 'wav', kind: 'audio' };
    if (at(head, 0, 'OggS')) return { ext: 'ogg', kind: 'audio' };
    if (at(head, 0, 'fLaC')) return { ext: 'flac', kind: 'audio' };
    if (at(head, 0, 'ID3')) return { ext: 'mp3', kind: 'audio' };
    /* A bare MPEG audio frame: 11 sync bits, layer III. */
    if (byte(head, 0) === 0xff && (byte(head, 1) & 0xe6) === 0xe2) return { ext: 'mp3', kind: 'audio' };
    if (at(head, 4, 'ftyp')) {
        const brand = head.substr(8, 4);
        if (brand === 'M4A ' || brand === 'M4B ') return { ext: 'm4a', kind: 'audio' };
        if (/^(isom|iso[2-9]|mp4[12]|avc1|M4V |MSNV|dash|mmp4|3gp[0-9])$/.test(brand)) return { ext: 'mp4', kind: 'video' };
        return null;   /* QuickTime (qt  ), HEIC (heic) and the rest: not served */
    }
    if (byte(head, 0) === 0x1a && byte(head, 1) === 0x45 && byte(head, 2) === 0xdf && byte(head, 3) === 0xa3) {
        return { ext: 'webm', kind: 'video' };
    }
    return null;
}

/* Media URLs the page builds: /chatmedia/<date>/<24 hex>.<ext>, the shape
 * the door, the shell and Futureland's site already recognise. */
export function validUploadId(id: string): boolean {
    return /^[a-f0-9]{24}$/.test(String(id || ''));
}

export function dayFolderName(name: string): boolean {
    return /^\d{4}-\d{2}-\d{2}$/.test(name);
}

/* Days between two YYYY-MM-DD folder names (b - a). */
export function daysBetween(a: string, b: string): number {
    function day(s: string): number {
        const p = s.split('-');
        return Date.UTC(parseInt(p[0]!, 10), parseInt(p[1]!, 10) - 1, parseInt(p[2]!, 10)) / 86400000;
    }
    return Math.round(day(b) - day(a));
}

/* Chunks are a multiple of 3 raw bytes so each base64 chunk decodes on
 * its own; 1998000 bytes is 2664000 base64 characters, under the 4 MiB
 * request body the Synchronet web server hands to JavaScript. */
export const CHUNK_RAW_BYTES = 1998000;

/* Pictures are stored as uploaded (there is no image library in SSJS to
 * re-encode them, as the door's media server does), so the metadata
 * segments that carry camera details and GPS positions are cut out
 * instead: JPEG APP1 (Exif, XMP) and APP13 (IPTC), and PNG's eXIf and
 * text chunks. Anything that doesn't parse cleanly is returned as is;
 * null means "leave the file alone". Bytes in, bytes out, as byte
 * strings (charCode = byte). */
export function stripJpegMetadata(data: string): string | null {
    if (data.charCodeAt(0) !== 0xff || data.charCodeAt(1) !== 0xd8) return null;
    let out = data.substr(0, 2);
    let pos = 2;
    let changed = false;
    while (pos + 4 <= data.length) {
        if (data.charCodeAt(pos) !== 0xff) return null;
        const marker = data.charCodeAt(pos + 1);
        if (marker === 0xda) {   /* start of scan: the rest is image data */
            out += data.substr(pos);
            return changed ? out : null;
        }
        if (marker === 0xd8 || marker === 0x01 || (marker >= 0xd0 && marker <= 0xd7)) {
            out += data.substr(pos, 2);
            pos += 2;
            continue;
        }
        const len = data.charCodeAt(pos + 2) * 256 + data.charCodeAt(pos + 3);
        if (len < 2 || pos + 2 + len > data.length) return null;
        if (marker === 0xe1 || marker === 0xed) {
            changed = true;
        } else {
            out += data.substr(pos, 2 + len);
        }
        pos += 2 + len;
    }
    return null;
}

const PNG_DROP: { [type: string]: boolean } = { eXIf: true, tEXt: true, iTXt: true, zTXt: true, tIME: true };

export function stripPngMetadata(data: string): string | null {
    if (data.substr(0, 8) !== '\x89PNG\r\n\x1a\n') return null;
    let out = data.substr(0, 8);
    let pos = 8;
    let changed = false;
    while (pos + 12 <= data.length) {
        const len = ((data.charCodeAt(pos) << 24) >>> 0) + (data.charCodeAt(pos + 1) << 16) + (data.charCodeAt(pos + 2) << 8) + data.charCodeAt(pos + 3);
        const type = data.substr(pos + 4, 4);
        if (pos + 12 + len > data.length) return null;
        if (PNG_DROP[type]) changed = true;
        else out += data.substr(pos, 12 + len);
        pos += 12 + len;
        if (type === 'IEND') return changed ? out : null;
    }
    return null;
}
