// node --test: the pure modules (src/lib) bundled to test/build/lib.js.
const test = require("node:test");
const assert = require("node:assert");
const L = require("./build/lib.js");

test("private and reserved addresses are refused", () => {
  const refused = [
    "127.0.0.1", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.1.1",
    "100.64.0.1", "0.0.0.0", "0.1.2.3", "192.0.0.8", "192.0.2.5", "198.18.0.1", "198.51.100.7",
    "203.0.113.9", "224.0.0.1", "240.0.0.1", "255.255.255.255", "1.2.3", "300.1.1.1",
    "::", "::1", "fc00::1", "fd12:3456::1", "fe80::1", "fec0::1", "ff02::1", "2001:db8::1",
    "64:ff9b:1::1", "100::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "::127.0.0.1", "[::1]", "", "nonsense",
  ];
  for (const ip of refused) assert.strictEqual(L.isPrivateAddress(ip), true, ip);
  const allowed = ["1.1.1.1", "8.8.8.8", "172.32.0.1", "100.128.0.1", "192.0.1.1", "203.0.114.1",
    "2606:4700:4700::1111", "::ffff:8.8.8.8", "2a00:1450:4001:80b::200e"];
  for (const ip of allowed) assert.strictEqual(L.isPrivateAddress(ip), false, ip);
});

test("targets: http(s) on default ports, no userinfo", () => {
  assert.ok(L.validateTarget("https://example.org/a").ok);
  assert.ok(L.validateTarget("http://example.org:80/").ok);
  assert.ok(L.validateTarget("https://example.org:443/x").ok);
  assert.ok(L.validateTarget("https://[2606:4700::1]/").ok);
  for (const bad of ["ftp://example.org/", "https://example.org:8080/", "https://user@example.org/",
    "https://a:b@example.org/", "javascript:alert(1)", "https://", "http://[::1]:22/", "https://example.org:22",
    "https://" + "a".repeat(2050)]) {
    assert.strictEqual(L.validateTarget(bad).ok, false, bad);
  }
});

test("redirect and og:image resolution never launders other schemes", () => {
  assert.strictEqual(L.resolveUrl("/b?c", "https://x.org/a/d"), "https://x.org/b?c");
  assert.strictEqual(L.resolveUrl("e.png", "https://x.org/a/d"), "https://x.org/a/e.png");
  assert.strictEqual(L.resolveUrl("//cdn.x.org/i.png", "https://x.org/"), "https://cdn.x.org/i.png");
  assert.strictEqual(L.resolveUrl("javascript:alert(1)", "https://x.org/"), null);
  assert.strictEqual(L.resolveUrl("data:text/html,hi", "https://x.org/"), null);
});

test("which link gets a card follows the door's rule", () => {
  assert.strictEqual(L.previewUrl("see https://x.org/pic.png and https://y.org/page."), "https://y.org/page");
  assert.strictEqual(L.previewUrl("https://x.org/song.mp3 https://x.org/v.webm"), "");
  assert.strictEqual(L.previewUrl("https://bbs.org/chatmedia/2026-10-11/0123456789abcdef01234567.png"), "");
  // a page whose query ends in .mp3 is a page (its og:audio makes it playable)
  assert.strictEqual(L.previewUrl("https://f.org/?page=records&track=Song.mp3"), "https://f.org/?page=records&track=Song.mp3");
  assert.deepStrictEqual(L.findUrls("a https://x.org/1, (https://x.org/2) |https://x.org/3|"), ["https://x.org/1", "https://x.org/2)", "https://x.org/3"].map((u) => u.replace(/\)$/, "")));
});

test("metadata parsing", () => {
  const html = '<html><head><title>Fallback &amp; title</title>' +
    '<meta property="og:title" content="Real &quot;Title&quot;">' +
    "<meta name='description' content='A page &#8212; with entities'>" +
    '<meta property="og:image" content="/img/card.png">' +
    '<meta property="og:audio" content="javascript:alert(1)">' +
    '<meta property="og:site_name" content="Example"></head></html>';
  const m = L.parseMetadata(html, "https://ex.org/a/b");
  assert.strictEqual(m.title, 'Real "Title"');
  assert.strictEqual(m.description, "A page \xe2\x80\x94 with entities");
  assert.strictEqual(m.image, "https://ex.org/img/card.png");
  assert.strictEqual(m.audio, "");
  assert.strictEqual(m.siteName, "Example");
  const long = L.cleanText("x".repeat(300), 200);
  assert.ok(long.length <= 200);
});

test("uploads are judged by their bytes", () => {
  const b = (arr) => String.fromCharCode.apply(null, arr);
  assert.deepStrictEqual(L.sniff("\x89PNG\r\n\x1a\n...."), { ext: "png", kind: "image" });
  assert.deepStrictEqual(L.sniff(b([0xff, 0xd8, 0xff, 0xe0])), { ext: "jpg", kind: "image" });
  assert.deepStrictEqual(L.sniff("GIF89a.."), { ext: "gif", kind: "image" });
  assert.deepStrictEqual(L.sniff("RIFF\0\0\0\0WEBPVP8 "), { ext: "webp", kind: "image" });
  assert.deepStrictEqual(L.sniff("RIFF\0\0\0\0WAVEfmt "), { ext: "wav", kind: "audio" });
  assert.deepStrictEqual(L.sniff("OggS\0"), { ext: "ogg", kind: "audio" });
  assert.deepStrictEqual(L.sniff("fLaC"), { ext: "flac", kind: "audio" });
  assert.deepStrictEqual(L.sniff("ID3\x04"), { ext: "mp3", kind: "audio" });
  assert.deepStrictEqual(L.sniff(b([0xff, 0xfb, 0x90, 0x00])), { ext: "mp3", kind: "audio" });
  assert.deepStrictEqual(L.sniff("\0\0\0\x20ftypisom"), { ext: "mp4", kind: "video" });
  assert.deepStrictEqual(L.sniff("\0\0\0\x20ftypM4A "), { ext: "m4a", kind: "audio" });
  assert.deepStrictEqual(L.sniff(b([0x1a, 0x45, 0xdf, 0xa3])), { ext: "webm", kind: "video" });
  for (const bad of ["<html><script>", "<svg xmlns=", "\0\0\0\x14ftypqt  ", "\0\0\0\x18ftypheic", "MZ\x90\0", "", "var x = 1;", "\xef\xbb\xbf<?xml"]) {
    assert.strictEqual(L.sniff(bad), null, JSON.stringify(bad));
  }
  assert.ok(L.validUploadId("0123456789abcdef01234567"));
  assert.ok(!L.validUploadId("../../etc/passwd"));
  assert.ok(!L.validUploadId("0123456789ABCDEF01234567"));
  assert.strictEqual(L.CHUNK_RAW_BYTES % 3, 0);
  assert.strictEqual(L.daysBetween("2026-10-01", "2026-10-11"), 10);
  assert.ok(L.dayFolderName("2026-10-11") && !L.dayFolderName("2026-10-11x") && !L.dayFolderName(".."));
});

test("handle styles normalize like chat_style_lib.js", () => {
  const s = L.normalizeStyle({ colors: ["#F00", "bogus", "#00ff00", "", ""], tag: { text: " sys|op\x01é ", fg: "#ff5", bg: "nope" } });
  assert.deepStrictEqual(s.colors, ["#ff0000", "", "#00ff00"]);
  assert.deepStrictEqual(s.tag, { text: "sysop", fg: "#ffff55", bg: "" });
  assert.deepStrictEqual(L.normalizeStyle({ tag: { text: "", fg: "#ffffff" } }).tag, { text: "", fg: "", bg: "" });
  assert.strictEqual(L.normalizeStyle({ colors: new Array(80).fill("#123456") }).colors.length, 60);
  assert.strictEqual(L.normalizeStyle({ tag: { text: "x".repeat(20) } }).tag.text.length, 12);
  assert.ok(L.styleIsEmpty(L.normalizeStyle(null)));
  assert.deepStrictEqual(L.wireStyle(L.normalizeStyle({ colors: ["#111111", "#222222", "#333333", "#444444"] }), "abc").colors,
    ["#111111", "#222222", "#333333"]);
  assert.strictEqual(L.wireStyle(L.normalizeStyle({ colors: ["", "", "", "#444444"] }), "abc"), null);
});

test("config reads door keys at the top and web keys in [web]", () => {
  const ini = {
    "": { host: "chat.example.org", port: "10099", default_channel: "lobby!", motd_channel: "news",
      images: "off", nsfw_keywords: "none", max_history: "9999", motd_host_qwkid: "OLDKEY" },
    web: { web_uploads: "on", web_upload_types: "png, jpeg ,mp3,exe,webm", web_upload_max_mb: "5" },
  };
  const get = (section, key) => {
    const v = ini[section === null ? "" : section];
    return v && key in v ? v[key] : null;
  };
  const c = L.buildConfig(get, "/x/avatar_chat.ini", true);
  assert.strictEqual(c.host, "chat.example.org");
  assert.strictEqual(c.port, 10099);
  assert.strictEqual(c.defaultChannel, "lobby");
  assert.strictEqual(c.motdChannel, "news");
  assert.strictEqual(c.images, false);
  assert.strictEqual(c.linkPreviews, true);
  assert.deepStrictEqual(c.nsfwKeywords, []);
  assert.strictEqual(c.maxHistory, 500);
  assert.strictEqual(c.motdHostQwkid, "OLDKEY");
  assert.strictEqual(c.uploads, true);
  assert.strictEqual(c.uploadMaxMb, 5);
  assert.deepStrictEqual(c.uploadTypes, ["png", "jpg", "mp3", "webm"]);
  const d = L.buildConfig(() => null, "/none", false);
  assert.strictEqual(d.host, "127.0.0.1");
  assert.strictEqual(d.port, 10088);
  assert.strictEqual(d.uploads, false);
  assert.deepStrictEqual(d.uploadTypes, L.DEFAULT_UPLOAD_TYPES);
  assert.deepStrictEqual(d.nsfwKeywords, L.DEFAULT_NSFW_KEYWORDS);
  assert.strictEqual(L.iniPathFor(null, "/sbbs/exec/"), "/sbbs/exec/../xtrn/avatar_chat_universal/avatar_chat.ini");
  assert.strictEqual(L.iniPathFor({ avatar_chat_ini: " /etc/ac.ini " }, "/sbbs/exec/"), "/etc/ac.ini");
});

test("message text cleaning", () => {
  assert.strictEqual(L.cleanMessageText("hi\x01Rthere\x1b[2J\tok\n", 1000), "hiRthere[2J\tok\n");
  assert.strictEqual(L.cleanMessageText("x".repeat(1200), 1000).length, 1000);
  assert.strictEqual(L.sanitizeChannel("../etc"), "etc");
  assert.strictEqual(L.sanitizeChannel("", "main"), "main");
});

test("picture metadata is cut out, the rest kept byte for byte", () => {
  const seg = (marker, body) => String.fromCharCode(0xff, marker, (body.length + 2) >> 8, (body.length + 2) & 255) + body;
  const exif = seg(0xe1, "Exif\0\0GPS 51.5N 0.1W");
  const jfif = seg(0xe0, "JFIF\0\x01\x02");
  const dqt = seg(0xdb, "\0" + "q".repeat(64));
  const scan = "\xff\xda\x00\x08scan" + "image-bytes\xff\x00more\xff\xd9";
  const jpeg = "\xff\xd8" + jfif + exif + seg(0xed, "Photoshop 3.0\0IPTC") + dqt + scan;
  const clean = L.stripJpegMetadata(jpeg);
  assert.strictEqual(clean, "\xff\xd8" + jfif + dqt + scan);
  assert.strictEqual(L.stripJpegMetadata("\xff\xd8" + jfif + dqt + scan), null, "nothing to cut: leave it");
  assert.strictEqual(L.stripJpegMetadata("\xff\xd8\xff\xe1\x7f\xffshort"), null, "truncated: leave it");

  const chunk = (type, body) => {
    const n = body.length;
    return String.fromCharCode(n >>> 24, (n >> 16) & 255, (n >> 8) & 255, n & 255) + type + body + "CRC!";
  };
  const sig = "\x89PNG\r\n\x1a\n";
  const ihdr = chunk("IHDR", "\0\0\0\x01\0\0\0\x01\x08\x06\0\0\0");
  const idat = chunk("IDAT", "pixels");
  const iend = chunk("IEND", "");
  const png = sig + ihdr + chunk("tEXt", "Comment\0hello") + chunk("eXIf", "MM\0*gps") + idat + chunk("tIME", "1234567") + iend;
  assert.strictEqual(L.stripPngMetadata(png), sig + ihdr + idat + iend);
  assert.strictEqual(L.stripPngMetadata(sig + ihdr + idat + iend), null);
  assert.strictEqual(L.stripPngMetadata("GIF89a"), null);
});

test("upload links keep the port the browser used", () => {
  const o = L.requestOrigin;
  assert.strictEqual(o({ host: "example.com:4080", vhost: "example.com", scheme: "http" }), "http://example.com:4080");
  assert.strictEqual(o({ host: "example.com:80", scheme: "http" }), "http://example.com");
  assert.strictEqual(o({ host: "example.com:443", forwardedProto: "https", scheme: "http" }), "https://example.com");
  assert.strictEqual(o({ host: "example.com", forwardedProto: "https, http", scheme: "http" }), "https://example.com");
  assert.strictEqual(o({ host: "example.com:8443", forwardedProto: "https" }), "https://example.com:8443");
  // Host wins over X-Forwarded-Host; vhost (no port) and the BBS address are fallbacks.
  assert.strictEqual(o({ host: "bbs.example:4080", forwardedHost: "evil.example" }), "http://bbs.example:4080");
  assert.strictEqual(o({ forwardedHost: "pub.example", vhost: "inside" }), "http://pub.example");
  assert.strictEqual(o({ vhost: "bbs.example" }), "http://bbs.example");
  assert.strictEqual(o({ fallbackHost: "10.0.0.2" }), "http://10.0.0.2");
  assert.strictEqual(o({ host: "[::1]:4080" }), "http://[::1]:4080");
  assert.strictEqual(o({ host: "[::1]:80" }), "http://[::1]");
  assert.strictEqual(o({ host: "x.example/\"><script>" }), "http://x.examplescript");
  assert.strictEqual(o({ host: "x.example", forwardedProto: "javascript" }), "http://x.example");
});

test("web_media_base_url, or the older web_upload_url_base, sets the link base", () => {
  const cfg = (map) => L.buildConfig((sec, key) => (map[(sec || "") + "." + key] === undefined ? null : map[(sec || "") + "." + key]), "x", true);
  assert.strictEqual(cfg({ "web.web_media_base_url": "https://m.example/files/" }).uploadUrlBase, "https://m.example/files");
  assert.strictEqual(cfg({ "web.web_upload_url_base": "https://old.example/c" }).uploadUrlBase, "https://old.example/c");
  assert.strictEqual(cfg({ "web.web_media_base_url": "https://new.example", "web.web_upload_url_base": "https://old.example" }).uploadUrlBase, "https://new.example");
  assert.strictEqual(cfg({}).uploadUrlBase, "");
});

test("page styles: one list, sysop allow-list and default, a user's pick", () => {
  const ids = L.PAGE_STYLES.map((s) => s.id);
  assert.ok(ids.length >= 6 && ids[0] === "classic");
  assert.deepStrictEqual(L.offeredStyles([]).map((s) => s.id), ids);
  assert.deepStrictEqual(L.offeredStyles(["amber", "cga", "nope"]).map((s) => s.id), ["cga", "amber"]);
  assert.deepStrictEqual(L.offeredStyles(["nope"]).map((s) => s.id), ids, "an allow-list with nothing real offers everything");
  const two = L.offeredStyles(["amber", "cga"]);
  assert.strictEqual(L.defaultStyle("amber", two), "amber");
  assert.strictEqual(L.defaultStyle("win95", two), "cga", "a default the sysop doesn't offer falls to the first offered");
  assert.strictEqual(L.defaultStyle("", L.offeredStyles([])), "classic");
  assert.strictEqual(L.resolveStyle("amber", two, "cga"), "amber");
  assert.strictEqual(L.resolveStyle("win95", two, "cga"), "cga", "a pick the sysop later removed falls back");
  const cfg = (map) => L.buildConfig((sec, key) => (map[(sec || "") + "." + key] === undefined ? null : map[(sec || "") + "." + key]), "x", true);
  const c = cfg({ "web.web_styles": "green, win95", "web.web_default_style": "WIN95" });
  assert.deepStrictEqual(c.styles.map((s) => s.id), ["green", "win95"]);
  assert.strictEqual(c.defaultStyle, "win95");
  assert.strictEqual(cfg({}).defaultStyle, "classic");
});

test("web prefs record: style and sidebar rooms, cleaned", () => {
  assert.deepStrictEqual(L.normalizePrefs(null), { version: 1, style: "", rooms: null });
  const p = L.normalizePrefs({ style: "AMBER", rooms: ["main", "Retro", "retro", "bad name", "x".repeat(40), "", 7], extra: 1 });
  assert.strictEqual(p.style, "amber");
  assert.deepStrictEqual(p.rooms, ["main", "Retro"]);
  assert.strictEqual(L.normalizePrefs({ style: "<script>" }).style, "");
  assert.deepStrictEqual(L.joinedRooms(L.normalizePrefs({}), "main"), ["main"], "never set: the default room");
  assert.deepStrictEqual(L.joinedRooms(L.normalizePrefs({ rooms: [] }), "main"), [], "left everything: nothing");
  assert.deepStrictEqual(L.addRoom(["main"], "MAIN"), ["main"]);
  assert.deepStrictEqual(L.addRoom(["main"], "lobby"), ["main", "lobby"]);
  assert.deepStrictEqual(L.removeRoom(["main", "Lobby"], "lobby"), ["main"]);
  assert.strictEqual(L.cleanRooms(Array.from({ length: 80 }, (_, i) => "r" + i)).length, 50);
});

test("web mailboxes: same rule as the door's chat.ValidMailbox", () => {
  assert.strictEqual(L.origin("futureland.today"), "futureland_today");
  assert.strictEqual(L.origin("web bbs-1_x"), "web_bbs-1_x");
  assert.strictEqual(L.origin("café"), "caf__"); // one '_' per UTF-8 byte, like Go
  assert.strictEqual(L.validMailbox("alice", "web.example", "alice@web_example"), "alice@web_example");
  assert.strictEqual(L.validMailbox("alice", "web.example", "ALICE@WEB_EXAMPLE"), "ALICE@WEB_EXAMPLE");
  assert.strictEqual(L.validMailbox("alice", "", "alice@"), "alice@");
  for (const [name, host, box] of [
    ["alice", "web.example", "Hm Derdoc"],         // someone else's plain mailbox
    ["alice", "web.example", "bob@web_example"],   // someone else's web mailbox
    ["alice", "web.example", "alice@web.example"], // a dot
    ["alice", "web.example", ""],
    ["", "web.example", "@web_example"],
  ]) assert.strictEqual(L.validMailbox(name, host, box), "", box);
});

test("media links: the door's mediaFile rule (type, then attachment name, then bytes)", () => {
  assert.strictEqual(L.dispositionName('attachment; filename="Blood_In_Bangkoks_Hollow_hm_derdoc.mp3"'), "Blood_In_Bangkoks_Hollow_hm_derdoc.mp3");
  assert.strictEqual(L.dispositionName("attachment; filename=notes.zip"), "notes.zip");
  assert.strictEqual(L.dispositionName("attachment; filename*=UTF-8''caf%C3%A9.mp3"), "café.mp3");
  assert.strictEqual(L.dispositionName('attachment; filename="../../etc/x.mp3"'), "x.mp3");
  assert.strictEqual(L.dispositionName("inline"), "");
  assert.strictEqual(L.typeForName("Song.MP3"), "audio/mpeg");
  assert.strictEqual(L.typeForName("a.webm"), "video/webm");
  assert.strictEqual(L.typeForName("notes.zip"), "");
  assert.strictEqual(L.sniffMedia("ID3\x03"), "audio/mpeg");
  assert.strictEqual(L.sniffMedia("\xff\xfb\x90"), "audio/mpeg");
  assert.strictEqual(L.sniffMedia("\xff\xe0"), "", "frame sync with no layer is not MPEG audio");
  assert.strictEqual(L.sniffMedia("OggS"), "audio/ogg");
  assert.strictEqual(L.sniffMedia("fLaC"), "audio/flac");
  assert.strictEqual(L.sniffMedia("RIFF\x00\x00\x00\x00WAVEfmt "), "audio/wav");
  assert.strictEqual(L.sniffMedia("\x00\x00\x00\x20ftypM4A "), "audio/mp4");
  assert.strictEqual(L.sniffMedia("\x00\x00\x00\x18ftypisom"), "video/mp4");
  assert.strictEqual(L.sniffMedia("\x1a\x45\xdf\xa3"), "video/webm");
  assert.strictEqual(L.sniffMedia("PK\x03\x04"), "");
  const dl = "https://futureland.today/api/files.ssjs?call=download-file&dir=mp3s&file=Song.mp3";
  const c = L.mediaCard(dl, dl, "application/octet-stream", "Song.mp3", "ID3");
  assert.deepStrictEqual([c.audio, c.audioType, c.title, c.siteName], [dl, "audio/mpeg", "Song.mp3", "futureland.today"]);
  assert.strictEqual(L.mediaCard(dl, dl, "audio/ogg; codecs=opus", "", "").audioType, "audio/ogg");
  assert.strictEqual(L.mediaCard("https://x.org/v", "https://x.org/v", "video/mp4", "", "").video, "https://x.org/v");
  assert.strictEqual(L.mediaCard("https://x.org/b", "https://x.org/b", "application/octet-stream", "notes.zip", "PK"), null);
  assert.strictEqual(L.mediaCard("https://x.org/j", "https://x.org/j", "application/json", "Song.mp3", "ID3"), null, "a named type that isn't generic decides");
});
