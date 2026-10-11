// Drives the page in headless Chromium against mock.js and checks what the
// browser code does: handle styles, mentions, inline pictures, the NSFW
// veil, link cards, escaping, the write token, live events, the style
// editor, the one-button composer (attach, drop, paste), the ANSI editor,
// the avatar chooser and editor, a chunked upload, rooms (sidebar, picker,
// join / create / leave) and the page styles.
//
//   PLAYWRIGHT=/path/to/node_modules/playwright node test/browser/check.js <port> [screenshot dir]
const { chromium } = require(process.env.PLAYWRIGHT || "playwright");
const path = require("path");
const fs = require("fs");

const PORT = process.argv[2] || "18190";
const BASE = "http://127.0.0.1:" + PORT;
const SHOTS = process.argv[3];
async function shot(target, name) {
  if (!SHOTS) return;
  fs.mkdirSync(SHOTS, { recursive: true });
  await target.screenshot({ path: path.join(SHOTS, name) });
}
let failed = 0;

function check(cond, name, detail) {
  console.log((cond ? "ok   " : "FAIL ") + name + (cond || detail === undefined ? "" : ": " + detail));
  if (!cond) failed++;
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  const dialogs = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("dialog", (d) => { dialogs.push(d.message()); d.dismiss(); });
  await page.route("https://pics.example/**", (route) => route.fulfill({
    status: 200, contentType: "image/png",
    body: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "base64"),
  }));
  await page.goto(BASE + "/?page=avatarchat.xjs");
  await page.waitForSelector(".avatar-chat-group", { timeout: 10000 });
  await page.waitForTimeout(1200);

  const carol = await page.$$eval(".avatar-chat-handle", (els) => {
    const e = els.find((x) => x.textContent === "Carol");
    return e ? Array.from(e.querySelectorAll("span")).map((s) => s.style.color) : null;
  });
  check(carol && carol.length === 4 && carol[0] === "rgb(255, 0, 0)", "per-letter handle colours", JSON.stringify(carol));
  check((await page.$$(".avatar-chat-handle-tag")).length === 0, "handle tags are not shown on this page");
  const carolText = await page.$$eval(".avatar-chat-header", (els) => els.map((e) => e.textContent).join("|"));
  check(carolText.indexOf("op") < 0 || /Carol(?!\s*op)/.test(carolText), "no tag text next to a tagged handle", carolText);

  // the composer: attach icon, editor icon, text box, Send. Nothing else,
  // and no file input visible even under bootstrap's input[type=file] rule.
  const row = await page.$$eval("#avatar-chat-form *", (els) => els
    .filter((e) => { const cs = getComputedStyle(e); return cs.display !== "none" && cs.visibility !== "hidden" && e.getClientRects().length; })
    .filter((e) => /^(BUTTON|INPUT|SELECT|TEXTAREA|LABEL)$/.test(e.tagName))
    .map((e) => e.id || e.className));
  check(JSON.stringify(row) === JSON.stringify(["avatar-chat-attach", "avatar-chat-draw", "avatar-chat-input", "avatar-chat-send"]),
    "composer row is exactly attach, draw, input, send", JSON.stringify(row));
  const fileInputs = await page.$$eval("input[type=file]", (els) => els.map((e) => getComputedStyle(e).display));
  check(fileInputs.length === 2 && fileInputs.every((d) => d === "none"), "every file input is display:none", JSON.stringify(fileInputs));
  const formText = await page.$eval("#avatar-chat-form", (e) => e.innerText.replace(/\s+/g, " ").trim());
  check(formText === "Send", "no labels or helper text in the composer", formText);
  await shot(await page.$("#avatar-chat-form"), "composer.png");
  check(await page.$(".avatar-chat-bubble.is-mention") !== null, "a mention of the viewer is highlighted");
  const mentionText = await page.$eval(".avatar-chat-bubble.is-mention", (e) => e.textContent);
  check(/@tester/.test(mentionText), "the right bubble is highlighted", mentionText);
  const imgs = await page.$$eval(".avatar-chat-rich-image img", (els) => els.map((e) => e.getAttribute("src")));
  check(imgs.indexOf("https://pics.example/cat.png") >= 0, "image link shown inline", JSON.stringify(imgs));
  check(await page.$('.avatar-chat-rich-image.is-veiled img[src="https://pics.example/nude.jpg"]') !== null, "NSFW-keyword picture is veiled");
  await page.click(".avatar-chat-rich-image.is-veiled");
  check(await page.$('.avatar-chat-rich-image.is-veiled') === null, "a click reveals it");
  await page.waitForSelector(".avatar-chat-card.is-ready", { timeout: 5000 }).catch(() => {});
  const card = await page.$eval(".avatar-chat-card.is-ready", (e) => e.textContent).catch(() => "");
  check(/Big <News> Story/.test(card) && /News Example/.test(card), "link card fetched through the BBS and escaped", card);
  check((await page.$$(".avatar-chat-card")).length === 1, "only links that resolve to a card keep one");
  const mallory = await page.$$eval(".avatar-chat-bubble", (els) => {
    const e = els.find((x) => x.textContent.indexOf("not bold") >= 0);
    return e ? { html: e.innerHTML, b: e.querySelectorAll("b").length, img: e.querySelectorAll("img").length, hover: e.querySelectorAll("[onmouseover]").length } : null;
  });
  check(mallory && mallory.b === 0 && mallory.img === 0 && mallory.hover === 0, "message HTML is escaped, a quote in a URL can't add an attribute", mallory && mallory.html);
  check(await page.$("img.avatar-chat-bitmap-image, .avatar-chat-bitmap-bubble img, .avatar-chat-bitmap-bubble canvas") !== null, "BITMAP from the door's Go encoder renders");
  const bitmapErr = await page.$eval(".avatar-chat-bitmap-bubble", (e) => e.textContent).catch(() => "");
  check(!/error|unable|invalid/i.test(bitmapErr), "no BITMAP decode error", bitmapErr);
  check(await page.$(".avatar-chat-group:not(.is-self) [data-avatar-bin] img, .avatar-chat-group:not(.is-self) [data-avatar-bin] canvas") !== null, "wire avatar drawn");

  // send: the token rides on the POST, the live event comes back
  await page.fill("#avatar-chat-input", "hello from the browser");
  await page.click(".avatar-chat-send");
  await page.waitForFunction(() => Array.from(document.querySelectorAll(".avatar-chat-bubble")).some((e) => e.textContent.indexOf("hello from the browser") >= 0), null, { timeout: 5000 }).catch(() => {});
  let st = await (await fetch(BASE + "/__state")).json();
  const send = st.posts.find((x) => x.action === "send");
  check(send && send.token === "0123456789abcdef0123456789abcdef" && send.q.message === "hello from the browser", "send carries the write token", JSON.stringify(send));
  check(await page.$eval("#avatar-chat-input", (e) => e.maxLength) === 1000, "composer capped at 1000");

  // style editor: colours only; the stored tag survives a save
  await page.click("#avatar-chat-style-open");
  await page.waitForSelector("#avatar-chat-style-letters input[type=color]");
  check((await page.$$("#avatar-chat-style-letters input[type=color]")).length === 6, "one colour per letter of the alias");
  check((await page.$$("#avatar-chat-style-modal input[type=text]")).length === 0 && !/tag/i.test(await page.$eval("#avatar-chat-style-modal", (e) => e.innerText)),
    "no tag fields in the style editor");
  await page.selectOption("#avatar-chat-style-preset", "rainbow");
  await page.click("#avatar-chat-style-save");
  await page.waitForTimeout(500);
  st = await (await fetch(BASE + "/__state")).json();
  check(st.style.colors.length === 6 && /^#[0-9a-f]{6}$/.test(st.style.colors[0]), "style saved (rainbow)", JSON.stringify(st.style));
  check(st.lastStyleBody && !("tag" in st.lastStyleBody) && st.style.tag.text === "mine", "the page sends no tag; the stored one is kept", JSON.stringify(st));
  check(st.posts.find((x) => x.action === "saveStyle").token.length === 32, "style save carries the token");
  await page.waitForTimeout(900);

  // attach an .ans: it becomes BITMAP art, held until Send
  const ans = path.join(__dirname, "..", "build", "hello.ans");
  fs.mkdirSync(path.dirname(ans), { recursive: true });
  fs.writeFileSync(ans, Buffer.from("\x1b[1;33;44mHELLO\x1b[0m world\r\n\x1b[31m\xdb\xdb\xdb\x1b[0m\r\n", "latin1"));
  await page.setInputFiles("#avatar-chat-file", ans);
  await page.waitForFunction(() => document.querySelector("#avatar-chat-input").readOnly, null, { timeout: 5000 }).catch(() => {});
  const ph = await page.$eval("#avatar-chat-input", (e) => e.placeholder);
  check(/hello\.ans .*ready/.test(ph), "an attached .ans waits in the composer", ph);
  await page.click(".avatar-chat-send");
  await page.waitForTimeout(800);
  st = await (await fetch(BASE + "/__state")).json();
  let sends = st.posts.filter((x) => x.action === "send");
  check(/^\[BITMAP\|80\|2\|Test BBS:Tester\|[0-9a-f]+\]$/.test(sends[sends.length - 1].q.message), "Send posts it as a BITMAP", sends[sends.length - 1].q.message.substr(0, 60));
  check(!(await page.$eval("#avatar-chat-input", (e) => e.readOnly)), "the composer is free again");

  // drop an .ans on the chat; Esc drops it again
  async function dropFile(name, bytes, type) {
    await page.evaluate(({ name, b64, type }) => {
      const bin = atob(b64);
      const arr = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
      const dt = new DataTransfer();
      dt.items.add(new File([arr], name, { type: type }));
      const target = document.querySelector("#avatar-chat-messages");
      for (const t of ["dragenter", "dragover", "drop"]) {
        target.dispatchEvent(new DragEvent(t, { bubbles: true, cancelable: true, dataTransfer: dt }));
      }
    }, { name: name, b64: Buffer.from(bytes).toString("base64"), type: type || "" });
  }
  await dropFile("drop.ans", fs.readFileSync(ans));
  await page.waitForFunction(() => document.querySelector("#avatar-chat-input").readOnly, null, { timeout: 5000 }).catch(() => {});
  check(/drop\.ans .*ready/.test(await page.$eval("#avatar-chat-input", (e) => e.placeholder)), "a dropped .ans is taken as art");
  await page.focus("#avatar-chat-input");
  await page.keyboard.press("Escape");
  check(!(await page.$eval("#avatar-chat-input", (e) => e.readOnly)) && (await page.$eval("#avatar-chat-input", (e) => e.placeholder)) === "Type a message...",
    "Esc drops the waiting art");

  // drop a picture: chunked upload, the link (with this site's port) lands in the composer
  await page.fill("#avatar-chat-input", "");
  await dropFile("pixel.png", Buffer.alloc(200, 7), "image/png");   // 200 bytes: seven 30-byte chunks
  await page.waitForFunction(() => /chatmedia/.test(document.querySelector("#avatar-chat-input").value), null, { timeout: 5000 }).catch(() => {});
  const composed = await page.$eval("#avatar-chat-input", (e) => e.value);
  st = await (await fetch(BASE + "/__state")).json();
  check(composed === BASE + "/chatmedia/2026-10-11/abcdefabcdefabcdefabcdef.png", "dropped picture uploaded; its link keeps the port :" + PORT, composed);
  check(st.stored.length === 1 && st.stored[0][1] === 200, "all chunks arrived in order", JSON.stringify(st.stored));
  check(st.posts.filter((x) => x.action === "upload:chunk").length === 7, "seven chunks for 200 bytes at 30 a chunk");
  await page.fill("#avatar-chat-input", "");

  // the ANSI editor: pencil opens it at 80x25, Done sends a BITMAP
  await page.click("#avatar-chat-draw");
  await page.waitForSelector(".ae-modal-window", { timeout: 10000 });
  await page.waitForTimeout(500);
  await page.keyboard.type("HI");
  await shot(page, "editor.png");
  await page.click(".ae-modal-window button:has-text('Done')");
  await page.waitForTimeout(1000);
  st = await (await fetch(BASE + "/__state")).json();
  sends = st.posts.filter((x) => x.action === "send");
  check(/^\[BITMAP\|80\|25\|Test BBS:Tester\|[0-9a-f]+\]$/.test(sends[sends.length - 1].q.message), "editor Done sends an 80x25 BITMAP", sends[sends.length - 1].q.message.substr(0, 40));
  check(await page.$(".ae-modal-window") === null, "the editor closed");

  // avatar: library pick, then the roster shows the new one for yourself
  await page.click("#avatar-chat-avatar-open");
  await page.waitForSelector(".avatar-chat-avatar-tile", { timeout: 5000 });
  check((await page.$$(".avatar-chat-avatar-tile")).length === 3, "the collection's three avatars are listed");
  await page.waitForTimeout(400);
  await shot(await page.$("#avatar-chat-avatar-modal .avatar-chat-modal-card"), "avatar-modal.png");
  await page.click('.avatar-chat-avatar-tile[data-index="1"]');
  await page.click("#avatar-chat-avatar-use");
  await page.waitForFunction(() => /Saved/.test(document.querySelector("#avatar-chat-avatar-status").textContent), null, { timeout: 5000 }).catch(() => {});
  st = await (await fetch(BASE + "/__state")).json();
  const pick = st.posts.filter((x) => x.action === "setAvatar").pop();
  check(pick && pick.q.collection === "TESTBBS.faces.bin" && pick.q.index === "1" && pick.token.length === 32, "Use selected writes collection + index with the token", JSON.stringify(pick && pick.q));
  const mineInRoster = await page.$eval('#avatar-chat-users-list [data-private-user="Tester"] [data-avatar-bin]', (e) => e.getAttribute("data-avatar-bin")).catch(() => "");
  check(mineInRoster === st.avatar.data, "the roster shows the new avatar for yourself");
  check(await page.$("#avatar-chat-avatar-preview img") !== null, "the modal previews it");

  // avatar editor at 10x6: Done saves 120 bytes
  await page.click("#avatar-chat-avatar-draw");
  await page.waitForSelector(".ae-modal-window", { timeout: 10000 });
  await page.waitForTimeout(500);
  const size = await page.evaluate(() => AnsiEditorModal.editor && [AnsiEditorModal.editor.doc.columns, AnsiEditorModal.editor.doc.rows]);
  check(size && size[0] === 10 && size[1] === 6, "the avatar editor is 10x6", JSON.stringify(size));
  const seeded = await page.evaluate(() => AnsiEditorModal.editor.doc.data[0]);
  check(seeded && seeded.code === 0xdb && seeded.fg === 0x0a, "seeded with the current avatar", JSON.stringify(seeded));
  // paint the top-left cell with the brush (a full block in white)
  const box = await page.evaluate(() => { const r = AnsiEditorModal.editor.el.canvasContainer.getBoundingClientRect(); return [r.left, r.top]; });
  await page.mouse.click(box[0] + 10, box[1] + 10);
  await page.waitForTimeout(200);
  await shot(page, "avatar-editor.png");
  await page.click(".ae-modal-window button:has-text('Done')");
  await page.waitForFunction(() => /Saved/.test(document.querySelector("#avatar-chat-avatar-status").textContent), null, { timeout: 5000 }).catch(() => {});
  st = await (await fetch(BASE + "/__state")).json();
  const drawn = st.posts.filter((x) => x.action === "setAvatar").pop();
  const bytes = drawn && drawn.q.data ? Buffer.from(drawn.q.data, "base64") : Buffer.alloc(0);
  check(bytes.length === 120 && !(bytes[0] === 0xdb && bytes[1] === 0x0a), "editor Done saves a 120-byte avatar with the painted cell", bytes.length + " " + bytes[0] + "/" + bytes[1]);
  check(await page.$eval("#avatar-chat-avatar-modal", (e) => e.classList.contains("is-open")), "back in the avatar modal after Done");

  // disable / enable
  await page.click("#avatar-chat-avatar-toggle");
  await page.waitForFunction(() => document.querySelector("#avatar-chat-avatar-toggle").textContent === "Enable", null, { timeout: 5000 }).catch(() => {});
  st = await (await fetch(BASE + "/__state")).json();
  check(st.avatar.disabled === true, "Disable writes the flag");
  await page.click("#avatar-chat-avatar-close");

  await shot(page, "page.png");
  // phone width: the composer row still fits on one line
  await page.setViewportSize({ width: 375, height: 760 });
  await page.waitForTimeout(300);
  const tops = await page.$$eval("#avatar-chat-form .avatar-chat-compose-row > *:not(input[type=file])", (els) =>
    els.filter((e) => getComputedStyle(e).display !== "none").map((e) => Math.round(e.getBoundingClientRect().top)));
  check(tops.length === 4 && tops.every((t) => t === tops[0]), "phone width: attach, draw, input, send on one row", JSON.stringify(tops));
  await shot(await page.$("#avatar-chat-form"), "composer-phone.png");
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.waitForTimeout(300);

  // ---- rooms, the way Futureland's page does them ----
  check(!/Public Rooms/i.test(await page.$eval(".avatar-chat-sidebar", (e) => e.innerText)), "no Public Rooms heading in the sidebar");
  check(await page.$eval("#avatar-chat-rooms", (e) => !e.parentNode.querySelector(".avatar-chat-sidebar-heading")), "the rooms list has no heading at all");
  check(!/Avatar Chat/.test(await page.$eval(".avatar-chat-topbar", (e) => e.innerText)), "no Avatar Chat branding in the header");
  check(await page.$("#avatar-chat-room-open") === null, "no separate create button");
  const rowsOf = () => page.$$eval("#avatar-chat-rooms [data-room-open]", (els) => els.map((e) => e.getAttribute("data-room-open") + (e.classList.contains("is-active") ? "*" : "")));
  check(JSON.stringify(await rowsOf()) === JSON.stringify(["main*"]), "sidebar starts with the joined room, highlighted", JSON.stringify(await rowsOf()));
  check(await page.$('#avatar-chat-rooms [data-room-leave="main"]') !== null, "each row has a leave x");
  let bar = await page.$eval("#avatar-chat-room-actions", (e) => e.innerText.replace(/\s+/g, " ").trim());
  check(bar === "Join a room Leave", "room bar for a joined room: Join a room, Leave", bar);
  await page.click('#avatar-chat-room-actions [data-picker-open]');
  await page.waitForSelector("#avatar-chat-room-modal.is-open #avatar-chat-room-list [data-picker-room]");
  await page.waitForTimeout(300);
  const picker = await page.$$eval("#avatar-chat-room-list [data-picker-room]", (els) => els.map((e) => e.innerText.replace(/\s+/g, " ").trim()));
  check(JSON.stringify(picker) === JSON.stringify(["#main 3 users Current", "#retro 2 users", "#lobby 0 users"]), "picker lists every room with its count and state", JSON.stringify(picker));
  const submitText = () => page.$eval("#avatar-chat-room-submit", (e) => (e.disabled ? "(disabled) " : "") + e.textContent);
  check(await submitText() === "(disabled) Join", "the submit button waits for a name", await submitText());
  await page.fill("#avatar-chat-room-input", "Retro");
  check(await submitText() === "Join #retro", "an existing name says Join #retro", await submitText());
  await page.fill("#avatar-chat-room-input", "newroom");
  check(await submitText() === "Create #newroom", "a new name says Create #newroom", await submitText());
  await shot(await page.$("#avatar-chat-room-modal .avatar-chat-modal-card"), "picker.png");
  await page.click("#avatar-chat-room-submit");
  await page.waitForFunction(() => !document.querySelector("#avatar-chat-room-modal").classList.contains("is-open"), null, { timeout: 5000 }).catch(() => {});
  st = await (await fetch(BASE + "/__state")).json();
  const created = st.posts.filter((x) => x.action === "joinRoom").pop();
  check(created && created.q.channel === "newroom" && created.q.create === "1", "Create sends joinRoom with create=1", JSON.stringify(created && created.q));
  await page.waitForTimeout(300);
  check(JSON.stringify(await rowsOf()).indexOf('"newroom*"') >= 0, "the new room is in the sidebar and open", JSON.stringify(await rowsOf()));
  check(/Created #newroom/.test(await page.$eval("#avatar-chat-status", (e) => e.innerText)), "it says the room was created", await page.$eval("#avatar-chat-status", (e) => e.innerText));
  check(await page.$eval("#avatar-chat-channel-label", (e) => e.textContent) === "#newroom", "the header shows #newroom");
  await page.click('#avatar-chat-room-actions [data-picker-open]');
  await page.waitForSelector('#avatar-chat-room-list [data-picker-room="retro"]');
  await page.click('#avatar-chat-room-list [data-picker-room="retro"]');
  await page.waitForTimeout(500);
  st = await (await fetch(BASE + "/__state")).json();
  const picked = st.posts.filter((x) => x.action === "joinRoom").pop();
  check(picked && picked.q.channel === "retro" && !picked.q.create, "clicking a room joins it (no create)", JSON.stringify(picked && picked.q));
  const rows3 = await rowsOf();
  check(rows3.length === 3 && rows3.indexOf("retro*") >= 0, "sidebar: the three joined rooms, retro open", JSON.stringify(rows3));
  await shot(await page.$(".avatar-chat-sidebar"), "sidebar.png");
  await page.click('#avatar-chat-rooms [data-room-leave="newroom"]');
  await page.waitForTimeout(400);
  check(JSON.stringify(await rowsOf()).indexOf("newroom") < 0, "x leaves a room (gone from the sidebar)", JSON.stringify(await rowsOf()));
  st = await (await fetch(BASE + "/__state")).json();
  check(st.joined.indexOf("newroom") < 0 && st.posts.some((x) => x.action === "leaveRoom" && x.q.channel === "newroom"), "leaveRoom reaches the BBS");
  await page.click('#avatar-chat-rooms [data-room-leave="retro"]');
  await page.waitForTimeout(400);
  check(await page.$eval("#avatar-chat-channel-label", (e) => e.textContent) === "#main", "leaving the open room moves to the next one", await page.$eval("#avatar-chat-channel-label", (e) => e.textContent));
  await page.click('#avatar-chat-room-actions [data-room-leave="main"]');
  await page.waitForTimeout(400);
  check(await page.$eval("#avatar-chat-norooms-modal", (e) => e.classList.contains("is-open")), "leaving the last room offers #main");
  await page.click("#avatar-chat-norooms-cancel");
  bar = await page.$eval("#avatar-chat-room-actions", (e) => e.innerText.replace(/\s+/g, " ").trim());
  check(bar === "Join #main Other rooms", "room bar for a room you are not in: Join #main, Other rooms", bar);
  await page.click("#avatar-chat-room-actions [data-room-join-this]");
  await page.waitForTimeout(400);
  check(JSON.stringify(await rowsOf()) === JSON.stringify(["main*"]), "Join #main puts it back", JSON.stringify(await rowsOf()));

  // ---- page styles: one pick in the Handle Style modal, a whole look ----
  const styleIds = await page.$$eval("#avatar-chat-page-style option", (els) => els.map((e) => e.value));
  check(styleIds.length >= 6, "the Handle Style modal offers " + styleIds.length + " page styles", JSON.stringify(styleIds));
  check((await page.$$("#avatar-chat-style-modal select")).length === 2, "one page style picker (plus the colour presets), nothing to tweak");
  await page.click("#avatar-chat-style-open");
  await page.waitForSelector("#avatar-chat-style-modal.is-open");
  await page.waitForTimeout(300);
  const looks = {};
  for (const id of styleIds) {
    await page.selectOption("#avatar-chat-page-style", id);
    await page.waitForTimeout(80);
    looks[id] = await page.evaluate(() => {
      const root = document.querySelector(".avatar-chat-web");
      const probe = document.createElement("span");
      root.appendChild(probe);
      const rgb = (v) => { probe.style.color = ""; probe.style.color = v.trim(); const m = getComputedStyle(probe).color.match(/[\d.]+/g).map(Number); return m; };
      const lum = (c) => { const f = (x) => { x /= 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); }; return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2]); };
      const ratio = (a, b) => { const la = lum(rgb(a)), lb = lum(rgb(b)); return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05); };
      const t = (n) => getComputedStyle(root).getPropertyValue("--ac-" + n);
      const pairs = { "text": ["fg", "surface"], "muted text": ["muted", "surface"], "own bubble": ["self-fg", "self-bg"], "others' bubble": ["other-fg", "other-bg"],
        "button on accent": ["on-accent", "accent"], "header": ["topbar-fg", "topbar-solid"], "links": ["link", "other-bg"], "mention": ["other-fg", "mention-bg"],
        "unread badge": ["unread-fg", "unread-bg"], "input": ["fg", "input-bg"] };
      const worst = {};
      for (const k in pairs) worst[k] = Math.round(ratio(t(pairs[k][0]), t(pairs[k][1])) * 100) / 100;
      probe.remove();
      const bubble = document.querySelector(".avatar-chat-group:not(.is-self) .avatar-chat-bubble:not(.avatar-chat-bitmap-bubble)");
      const art = document.querySelector(".avatar-chat-bitmap-bubble");
      const avatarBox = document.querySelector(".avatar-chat-avatar");
      const shell = getComputedStyle(document.querySelector(".avatar-chat-shell"));
      return {
        contrast: worst,
        shellBg: shell.backgroundColor + " " + shell.backgroundImage,
        radius: getComputedStyle(bubble).borderTopLeftRadius,
        padding: getComputedStyle(bubble).paddingTop + " " + getComputedStyle(bubble).paddingLeft,
        font: getComputedStyle(root).fontFamily,
        artPad: art ? getComputedStyle(art).paddingTop : "",
        avatarBox: avatarBox ? avatarBox.getBoundingClientRect().width + "x" + avatarBox.getBoundingClientRect().height : "",
      };
    });
    const low = Object.entries(looks[id].contrast).filter(([, v]) => v < 4.5);
    check(low.length === 0, "style " + id + ": text contrast >= 4.5:1 everywhere", JSON.stringify(low));
    await page.evaluate(() => { document.querySelector("#avatar-chat-style-modal").classList.remove("is-open"); });
    await page.waitForTimeout(60);
    await shot(page, "style-" + id + ".png");
    await page.evaluate(() => { document.querySelector("#avatar-chat-style-modal").classList.add("is-open"); });
  }
  const distinct = (k) => new Set(styleIds.map((id) => looks[id][k])).size;
  check(distinct("shellBg") === styleIds.length, "every style has its own background", distinct("shellBg") + "/" + styleIds.length);
  check(distinct("radius") >= 4 && distinct("padding") >= 4, "styles change shape too: " + distinct("radius") + " bubble radii, " + distinct("padding") + " paddings");
  check(distinct("font") >= 4, "styles change the font (" + distinct("font") + " families)");
  check(distinct("artPad") === 1 && distinct("avatarBox") === 1, "art keeps its geometry in every style", JSON.stringify(styleIds.map((id) => looks[id].artPad + " " + looks[id].avatarBox)));
  check(await page.$eval(".avatar-chat-web", (e) => e.className) === "avatar-chat-web ac-style-" + styleIds[styleIds.length - 1], "picking previews at once");
  await page.click("#avatar-chat-style-cancel");
  check(await page.$eval(".avatar-chat-web", (e) => e.className) === "avatar-chat-web ac-style-classic", "Cancel puts the saved style back");
  await page.click("#avatar-chat-style-open");
  await page.waitForSelector("#avatar-chat-style-modal.is-open");
  await page.waitForTimeout(300);
  await page.selectOption("#avatar-chat-page-style", "amber");
  await page.click("#avatar-chat-style-save");
  await page.waitForTimeout(700);
  st = await (await fetch(BASE + "/__state")).json();
  const savedStyle = st.posts.filter((x) => x.action === "savePageStyle").pop();
  check(st.pageStyle === "amber" && savedStyle && savedStyle.token.length === 32, "Save stores the page style with the token", JSON.stringify(savedStyle && savedStyle.q));
  check(await page.$eval(".avatar-chat-web", (e) => e.className) === "avatar-chat-web ac-style-amber", "the saved style stays on after the modal closes");
  const served = await (await fetch(BASE + "/?page=avatarchat.xjs")).text();
  check(/class="avatar-chat-web ac-style-amber"/.test(served), "the served HTML already wears it (no flash of the default)");
  await page.reload();
  await page.waitForSelector(".avatar-chat-group", { timeout: 10000 });
  check(await page.$eval("#avatar-chat-page-style", (e) => e.value) === "amber", "after a reload the picker shows the saved style");

  check(errors.length === 0, "no page errors", errors.join(" | "));
  check(dialogs.length === 0, "no alert() ran", dialogs.join(" | "));
  await browser.close();
  console.log(failed ? failed + " failed" : "all passed");
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
