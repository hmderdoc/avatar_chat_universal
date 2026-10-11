// A stand-in for a Synchronet web server, for checking the browser code in
// a real browser: it renders pages/avatarchat.xjs the way webv4 does (text
// becomes write(), <?xjs ?> blocks run, <? ?> expressions are written),
// serves dist/webv4/root and the stock webv4 scripts, and answers the
// page's API with the same reply shapes src/entries/*.ts produce.
//
//   node test/browser/mock.js <port> <stock webv4 root>
const http = require("http");
const fs = require("fs");
const path = require("path");

const PKG = path.join(__dirname, "..", "..");
const DIST = path.join(PKG, "dist", "webv4");
const STOCK = process.argv[3] || "/sbbs/repo/webv4/root";
const PORT = parseInt(process.argv[2] || "18190", 10);
const TOKEN = "0123456789abcdef0123456789abcdef";

const state = {
  posts: [],         // every write the page made: { action, headers, body }
  uploads: {},
  stored: {},
  // a tag another client stored: the page must neither show nor drop it
  style: { version: 1, colors: [], tag: { text: "mine", fg: "#ffffff", bg: "#aa0000" } },
  avatar: null,
  sse: [],
  messages: [],
  // rooms on the chat service, the sidebar's joined list, the page style
  rooms: [{ name: "main", userCount: 3 }, { name: "retro", userCount: 2 }, { name: "lobby", userCount: 0 }],
  joined: ["main"],
  pageStyle: "classic",
};
const PAGE_STYLES = [
  { id: "classic", label: "Classic" }, { id: "cga", label: "CGA" }, { id: "modern-dark", label: "Modern dark" },
  { id: "modern-light", label: "Modern light" }, { id: "amber", label: "Amber phosphor" }, { id: "green", label: "Green phosphor" },
  { id: "win95", label: "Windows 95" }, { id: "compact", label: "Flat compact" },
];
const summaries = () => state.rooms.map((r) => ({ name: r.name, userCount: r.userCount, lastTimestamp: Date.now(), newCount: 0 }));
const findRoom = (n) => state.rooms.find((r) => r.name.toUpperCase() === String(n || "").toUpperCase());

function msg(sender, text, extra) {
  return Object.assign({ sender: sender, system: "Test BBS", text: text, timestamp: Date.now() - 60000, userNumber: 0, isSelf: false, private: false }, extra || {});
}

const avatarOf = (ch, attr) => Buffer.from(Array.from({ length: 120 }, (_, i) => (i % 2 ? attr : ch))).toString("base64");
const COLLECTION = { id: "TESTBBS.faces.bin", title: "Test Faces", author: "Tester", group: "QA", count: 3, previewIndex: 0,
  avatars: [0, 1, 2].map((i) => ({ index: i, label: "face " + (i + 1), data: avatarOf(0xdb, 0x09 + i) })) };
const AVATAR = Buffer.from(Array.from({ length: 120 }, (_, i) => (i % 2 ? 0x1e + (i % 7) : 0xdb))).toString("base64");
const GO_BITMAP = fs.readFileSync(process.env.GO_BITMAP_FILE, "utf8");

state.messages.push(
  msg("Carol", "styled and tagged", { style: { colors: ["#ff0000", "#00ff00", "", "#5555ff", "#ffff55"], tag: { text: "op", fg: "#000000", bg: "#55ffff" } }, avatar: AVATAR }),
  msg("Dave", "hey @tester, look: https://pics.example/cat.png and https://news.example/story"),
  msg("Eve", "nsfw pic https://pics.example/nude.jpg"),
  msg("Mallory", "<b>not bold</b> <img src=x onerror=alert(1)> https://evil.example/a\"onmouseover=\"alert(1)"),
  msg("Gopher", GO_BITMAP),
  msg("Tester", "my own line", { isSelf: true })
);

function renderXjs(file, ctx) {
  const src = fs.readFileSync(file, "utf8");
  let code = "";
  let i = 0;
  while (i < src.length) {
    const open = src.indexOf("<?", i);
    if (open < 0) { code += "write(" + JSON.stringify(src.slice(i)) + ");\n"; break; }
    code += "write(" + JSON.stringify(src.slice(i, open)) + ");\n";
    const close = src.indexOf("?>", open);
    let inner = src.slice(open + 2, close);
    if (inner.startsWith("xjs")) inner = inner.slice(3);
    code += inner + "\n";
    i = close + 2;
  }
  let out = "";
  const fn = new Function("write", "load", "settings", "AvatarChatPage", code);
  fn((t) => { out += t; }, () => {}, { web_lib: "" }, ctx);
  return out;
}

const basePageConfig = {
  server: "127.0.0.1:10088", configFound: true, defaultChannel: "main", motdChannel: "motd", canManageMotd: false,
  userAlias: "Tester", userNumber: 77, systemName: "Test BBS", isLoggedIn: true, canUseRealtime: true, token: TOKEN,
  maxText: 1000, images: true, linkPreviews: true, nsfwKeywords: ["nude", "nsfw"],
  uploads: { enabled: true, maxMb: 20, videoMaxMb: 50, types: ["png", "jpg", "gif", "webp", "mp3", "mp4"], retentionDays: 7 },
};
// as page.ts: the stored style and the sidebar rooms, read per request
const currentPageConfig = () => Object.assign({}, basePageConfig, { pageStyle: state.pageStyle, pageStyles: PAGE_STYLES, joinedRooms: state.joined.slice() });

function json(res, obj, status) {
  res.writeHead(status || 200, { "Content-Type": "application/json; charset=utf-8" });
  res.end(JSON.stringify(obj));
}

function readBody(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
  });
}

function query(url, body, req) {
  const q = Object.fromEntries(url.searchParams);
  if (req.method === "POST" && /x-www-form-urlencoded/.test(req.headers["content-type"] || "")) {
    Object.assign(q, Object.fromEntries(new URLSearchParams(body)));
  }
  return q;
}

function pushEvent(payload) {
  for (const res of state.sse) res.write("event: avatarchat\ndata: " + JSON.stringify(payload) + "\n\n");
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost");
  const body = await readBody(req);
  const p = url.pathname;

  if (p === "/" && url.searchParams.get("page") === "avatarchat.xjs") {
    const cfg = currentPageConfig();
    const html = renderXjs(path.join(DIST, "pages", "avatarchat.xjs"), {
      pageConfig: () => cfg,
      pageConfigJson: () => JSON.stringify(cfg).replace(/<\//g, "<\\/"),
    });
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    // Stock webv4's own stylesheets: bootstrap's input[type=file] rule is
    // what showed the hidden file inputs on a real site.
    res.end("<!doctype html><html><head><meta charset=utf-8><title>mock</title>" +
      '<link href="./bootstrap/css/bootstrap.min.css" rel="stylesheet"><link href="./css/style.css" rel="stylesheet">' +
      '<script src="./js/jquery.min.js"></script><script src="./js/common.js"></script></head><body style="background:#000">' + html + "</body></html>");
    return;
  }

  if (p === "/api/events.ssjs") {
    res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
    res.write(": ping\n\n");
    state.sse.push(res);
    req.on("close", () => { state.sse = state.sse.filter((r) => r !== res); });
    return;
  }

  if (p === "/api/system.ssjs") return json(res, []);

  if (p === "/api/avatarchat.ssjs") {
    const q = query(url, body, req);
    if (req.method === "POST") {
      state.posts.push({ action: q.action, token: req.headers["x-avatarchat-token"] || "", body: body, q: q });
      if (req.headers["x-avatarchat-token"] !== TOKEN) return json(res, { error: "stale page: reload it" }, 403);
    }
    switch (q.action) {
      case "sync":
        return json(res, {
          channel: "main", serverTime: Date.now(),
          channels: summaries(),
          who: { channel: "main", users: [{ nick: "Carol", system: "Test BBS", userNumber: 0, avatar: AVATAR, style: state.messages[0].style },
            { nick: "Tester", system: "Test BBS", userNumber: 77, avatar: avatarOf(0x41, 0x07) }] },
          history: q.history === "1" ? { channel: "main", messages: state.messages } : undefined,
          private: { threads: [] },
          motd: { previewText: "mock motd", timestamp: Date.now() },
          presence: [],
        });
      case "history":
        return json(res, { channel: "main", messages: state.messages, serverTime: Date.now() });
      case "who":
        return json(res, { channel: "main", users: [{ nick: "Carol", system: "Test BBS", userNumber: 0, avatar: AVATAR, style: state.messages[0].style },
          { nick: "Tester", system: "Test BBS", userNumber: 77, avatar: avatarOf(0x41, 0x07) }], serverTime: Date.now() });
      case "channels":
        return json(res, { channels: summaries(), serverTime: Date.now() });
      case "joinRoom": {
        // as api.ts: an existing room joins; a new one needs create=1
        let room = findRoom(q.channel);
        if (!room) {
          if (q.create !== "1") return json(res, { error: "no room #" + q.channel + " yet" });
          room = { name: q.channel, userCount: 0 };
          state.rooms.push(room);
        }
        if (!state.joined.some((n) => n.toUpperCase() === room.name.toUpperCase())) state.joined.push(room.name);
        return json(res, { ok: true, channel: room.name, created: q.create === "1" && state.rooms[state.rooms.length - 1] === room, rooms: state.joined });
      }
      case "leaveRoom":
        state.joined = state.joined.filter((n) => n.toUpperCase() !== String(q.channel).toUpperCase());
        return json(res, { ok: true, rooms: state.joined });
      case "savePageStyle":
        if (!PAGE_STYLES.some((x) => x.id === q.style)) return json(res, { error: "no such page style" });
        state.pageStyle = q.style;
        return json(res, { ok: true, style: q.style });
      case "motd":
        return json(res, { channel: "motd", previewText: "mock motd", timestamp: Date.now() });
      case "private":
        return json(res, { threads: [] });
      case "style":
        return json(res, { ok: true, alias: "Tester", style: state.style });
      case "saveStyle": {
        // as api.ts: colours from the page, the stored tag kept
        const b = JSON.parse(body);
        state.lastStyleBody = b;
        state.style = { version: 1, colors: b.colors || [], tag: state.style.tag };
        return json(res, { ok: true, style: state.style });
      }
      case "avatar":
        return json(res, { ok: true, avatar: state.avatar, collections: [Object.assign({}, COLLECTION, { avatars: undefined, preview: COLLECTION.avatars[0].data })] });
      case "avatarCollection":
        return q.collection === COLLECTION.id ? json(res, { ok: true, collection: COLLECTION }) : json(res, { error: "no such collection" });
      case "setAvatar": {
        let data = q.data;
        if (!data) {
          const a = COLLECTION.avatars[parseInt(q.index, 10)];
          if (q.collection !== COLLECTION.id || !a) return json(res, { error: "no such avatar" });
          data = a.data;
        }
        if (Buffer.from(data, "base64").length !== 120) return json(res, { error: "that is not a 10x6 avatar" });
        state.avatar = { data: data, disabled: false };
        return json(res, { ok: true, avatar: state.avatar });
      }
      case "avatarEnabled":
        if (!state.avatar) return json(res, { error: "no avatar saved yet" });
        state.avatar.disabled = q.enabled !== "1";
        return json(res, { ok: true, avatar: state.avatar });
      case "preview":
        if (q.url === "https://news.example/story") {
          return json(res, { url: q.url, finalUrl: q.url, kind: "site", title: "Big <News> Story", description: "Something happened.", image: "", siteName: "News Example" });
        }
        return json(res, { error: "no metadata" });
      case "send": {
        const m = msg("Tester", q.message, { isSelf: true, timestamp: Date.now(), style: state.style.colors.length ? { colors: state.style.colors, tag: state.style.tag } : undefined });
        state.messages.push(m);
        pushEvent(Object.assign({ type: "message", channel: "main" }, m));
        return json(res, { success: true, channel: "main", timestamp: m.timestamp });
      }
    }
    return json(res, { error: "unknown action" });
  }

  if (p === "/api/avatarchat-upload.ssjs") {
    const q = Object.fromEntries(url.searchParams);
    state.posts.push({ action: "upload:" + q.action, token: req.headers["x-avatarchat-token"] || "" });
    if (req.headers["x-avatarchat-token"] !== TOKEN) return json(res, { ok: false, error: "stale page: reload it" }, 403);
    if (q.action === "begin") {
      const id = "abcdefabcdefabcdefabcdef";
      state.uploads[id] = { size: parseInt(q.size, 10), parts: [] };
      return json(res, { ok: true, id: id, chunkSize: 30 });   // tiny chunks: exercise the loop
    }
    const up = state.uploads[q.id];
    if (!up) return json(res, { ok: false, error: "unknown upload" });
    if (q.action === "chunk") {
      if (parseInt(q.seq, 10) !== up.parts.length) return json(res, { ok: false, error: "chunk out of order" });
      up.parts.push(Buffer.from(body, "base64"));
      return json(res, { ok: true });
    }
    if (q.action === "finish") {
      const all = Buffer.concat(up.parts);
      if (all.length !== up.size) return json(res, { ok: false, error: "upload is incomplete" });
      state.stored["/chatmedia/2026-10-11/" + q.id + ".png"] = all;
      // as upload.ts: the link is built from the browser's own Host header
      return json(res, { ok: true, url: "http://" + req.headers.host + "/chatmedia/2026-10-11/" + q.id + ".png", kind: "image", bytes: all.length, expiresDays: 7 });
    }
    return json(res, { ok: true });
  }

  if (p === "/__state") return json(res, { joined: state.joined, rooms: state.rooms, pageStyle: state.pageStyle, posts: state.posts, stored: Object.keys(state.stored).map((k) => [k, state.stored[k].length]), style: state.style, lastStyleBody: state.lastStyleBody, avatar: state.avatar });

  if (state.stored[p]) { res.writeHead(200, { "Content-Type": "image/png" }); res.end(state.stored[p]); return; }

  // pictures in the fixture messages: a 1x1 PNG for any *.example host path
  if (/\.(png|jpe?g)$/.test(p) && p.startsWith("/fixture/")) {
    res.writeHead(200, { "Content-Type": "image/png" });
    res.end(Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "base64"));
    return;
  }

  const roots = [path.join(DIST, "root"), STOCK];
  for (const r of roots) {
    const f = path.join(r, path.normalize(p).replace(/^(\.\.[\/\\])+/, ""));
    if (f.startsWith(r) && fs.existsSync(f) && fs.statSync(f).isFile()) {
      const types = { ".js": "text/javascript", ".css": "text/css", ".png": "image/png", ".woff": "font/woff", ".ttf": "font/ttf" };
      res.writeHead(200, { "Content-Type": types[path.extname(f)] || "application/octet-stream" });
      res.end(fs.readFileSync(f));
      return;
    }
  }
  res.writeHead(404);
  res.end("not found");
});

server.listen(PORT, "127.0.0.1", () => console.log("mock on " + PORT));
