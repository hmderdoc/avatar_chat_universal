// Drives the page in headless Chromium against mock.js and checks what the
// browser code does: handle styles, mentions, inline pictures, the NSFW
// veil, link cards, escaping, the write token, live events, the style
// editor, the one-button composer (attach, drop, paste), the ANSI editor,
// the avatar chooser and editor, and a chunked upload.
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
  check(errors.length === 0, "no page errors", errors.join(" | "));
  check(dialogs.length === 0, "no alert() ran", dialogs.join(" | "));
  await browser.close();
  console.log(failed ? failed + " failed" : "all passed");
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
