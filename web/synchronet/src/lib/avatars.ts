/* The logged-in user's real Synchronet avatar: read it, pick one from the
 * sysop's shared collections (text/avatars/*.bin, as exec/avatar_chooser.js
 * reads them), save a drawn or uploaded one, disable or re-enable it. The
 * same [avatar] section in data/user/NNNN.ini the terminal chooser, the
 * door and Futureland's site write, through Synchronet's own avatar_lib.
 *
 * The libraries are loaded with this script's `system`, so everything
 * follows whatever system object the caller runs under. */

const COLLECTION_NAME = /^[A-Za-z0-9][A-Za-z0-9 ._+()-]{0,79}\.bin$/;

export interface AvatarState { data: string; disabled: boolean; }
export interface CollectionSummary {
    id: string; title: string; author: string; group: string;
    count: number; previewIndex: number; preview: string; description: string;
}
export interface CollectionAvatar { index: number; label: string; data: string; }

function avatarLib(): AvatarLib {
    return load({ system: system }, 'avatar_lib.js') as AvatarLib;
}

function sauceLib(): SauceLib {
    return load({ system: system }, 'sauce_lib.js') as SauceLib;
}

function isDisabled(obj: AvatarObject): boolean {
    const d = obj.disabled;
    return d === true || d === 'true' || d === 1 || d === '1';
}

export function current(number: number): AvatarState | null {
    if (!(number > 0)) return null;
    const obj = avatarLib().read_localuser(number);
    if (!obj || !obj.data) return null;
    return { data: String(obj.data), disabled: isDisabled(obj) };
}

function libraryDir(): string {
    return backslash(system.text_dir + 'avatars/');
}

/* Collection files, without the per-avatar extracts (name.N.bin). */
function collectionFiles(): string[] {
    const files = directory(libraryDir() + '*.bin') || [];
    const out: string[] = [];
    for (let i = 0; i < files.length; i++) {
        const name = file_getname(files[i]!);
        if (/\.\d+\.bin$/i.test(name) || !COLLECTION_NAME.test(name)) continue;
        out.push(files[i]!);
    }
    out.sort();
    return out;
}

function collectionFile(id: string): string {
    if (!COLLECTION_NAME.test(id) || file_getname(id) !== id) return '';
    const files = collectionFiles();
    for (let i = 0; i < files.length; i++) if (file_getname(files[i]!) === id) return files[i]!;
    return '';
}

/* Avatars in a file, counted as avatar_chooser.js does: SAUCE rows over the
 * avatar height, so the SAUCE comment block isn't read as more avatars. */
function countIn(path: string, lib: AvatarLib, sauce: SauceRecord | null | false): number {
    const f = new File(path);
    if (!f.open('rb')) return 0;
    let n = Math.floor(f.length / lib.size);
    f.close();
    if (sauce && sauce.rows && sauce.rows > 0) n = Math.min(n, Math.floor(sauce.rows / lib.defs.height));
    return n;
}

function chunk(path: string, index: number, lib: AvatarLib, count: number): string {
    if (index < 0 || index >= count) return '';
    const f = new File(path);
    if (!f.open('rb')) return '';
    let bin = '';
    try {
        f.position = index * lib.size;
        bin = f.read(lib.size);
    } finally {
        f.close();
    }
    return lib.is_valid(bin) ? bin : '';
}

function summarize(path: string, lib: AvatarLib, sauce: SauceRecord | null | false): CollectionSummary | null {
    if (!sauce) return null;
    const count = countIn(path, lib, sauce);
    if (count < 1) return null;
    let previewIndex = 0;
    const t4 = parseInt(String(sauce.tinfo4 === undefined ? '' : sauce.tinfo4), 10) - 1;
    if (!isNaN(t4) && t4 >= 0 && t4 < count) previewIndex = t4;
    const bin = chunk(path, previewIndex, lib, count);
    if (!bin.length) return null;
    const comments = sauce.comment && sauce.comment.length !== undefined ? sauce.comment : [];
    const id = file_getname(path);
    return {
        id: id,
        title: sauce.title && String(sauce.title).length ? String(sauce.title) : id.replace(/\.bin$/i, ''),
        author: sauce.author && String(sauce.author).length ? String(sauce.author) : 'Unknown',
        group: sauce.group && String(sauce.group).length ? String(sauce.group) : 'Unknown',
        count: count,
        previewIndex: previewIndex,
        preview: base64_encode(bin),
        description: String(comments[previewIndex] || '')
    };
}

export function collections(): CollectionSummary[] {
    const lib = avatarLib();
    const sl = sauceLib();
    const files = collectionFiles();
    const out: CollectionSummary[] = [];
    for (let i = 0; i < files.length; i++) {
        const s = summarize(files[i]!, lib, sl.read(files[i]!));
        if (s) out.push(s);
    }
    return out;
}

export function collection(id: string): (CollectionSummary & { avatars: CollectionAvatar[] }) | null {
    const path = collectionFile(id);
    if (!path.length) return null;
    const lib = avatarLib();
    const sauce = sauceLib().read(path);
    const s = summarize(path, lib, sauce);
    if (!s || !sauce) return null;
    const comments = sauce.comment && sauce.comment.length !== undefined ? sauce.comment : [];
    const avatars: CollectionAvatar[] = [];
    for (let i = 0; i < s.count; i++) {
        const bin = chunk(path, i, lib, s.count);
        if (bin.length) avatars.push({ index: i, label: String(comments[i] || ''), data: base64_encode(bin) });
    }
    return { id: s.id, title: s.title, author: s.author, group: s.group, count: s.count,
        previewIndex: s.previewIndex, preview: s.preview, description: s.description, avatars: avatars };
}

function save(number: number, bin: string, lib: AvatarLib): { ok: boolean; avatar?: AvatarState | null; error?: string } {
    if (!lib.update_localuser(number, base64_encode(bin))) return { ok: false, error: 'could not save the avatar' };
    const now = lib.read_localuser(number);
    if (now && isDisabled(now)) lib.enable_localuser(number, true);
    return { ok: true, avatar: current(number) };
}

export function useFromCollection(number: number, id: string, index: number): { ok: boolean; avatar?: AvatarState | null; error?: string } {
    const path = collectionFile(id);
    if (!path.length) return { ok: false, error: 'no such collection' };
    const lib = avatarLib();
    const count = countIn(path, lib, sauceLib().read(path));
    const bin = chunk(path, index, lib, count);
    if (!bin.length) return { ok: false, error: 'no such avatar' };
    return save(number, bin, lib);
}

/* A drawn or uploaded avatar: base64 of exactly 120 bytes (10x6 cells,
 * char then attribute), no control characters, no blink. */
export function useData(number: number, data: string): { ok: boolean; avatar?: AvatarState | null; error?: string } {
    const lib = avatarLib();
    let bin: string | null = null;
    if (/^[A-Za-z0-9+\/]+={0,2}$/.test(data) && data.length <= 400) {
        try { bin = base64_decode(data); } catch (_e) { bin = null; }
    }
    if (!bin || bin.length !== lib.size || !lib.is_valid(bin)) return { ok: false, error: 'that is not a 10x6 avatar' };
    return save(number, bin, lib);
}

export function setEnabled(number: number, enabled: boolean): { ok: boolean; avatar?: AvatarState | null; error?: string } {
    const lib = avatarLib();
    const now = lib.read_localuser(number);
    if (!now || !now.data) return { ok: false, error: 'no avatar saved yet' };
    /* enable_localuser returns false when nothing changes; that's fine. */
    if (isDisabled(now) === enabled) lib.enable_localuser(number, enabled);
    const after = current(number);
    if (!after || after.disabled === enabled) return { ok: false, error: 'could not save the avatar state' };
    return { ok: true, avatar: after };
}
