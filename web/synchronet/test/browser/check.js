// Drives the page in headless Chromium against mock.js and checks what the
// browser code does: handle styles, mentions, inline pictures, the NSFW
// veil, link cards, escaping, the write token, live events, the style
// editor and a chunked upload.
//
//   PLAYWRIGHT=/path/to/node_modules/playwright node test/browser/check.js <port> [screenshot.png]
const { chromium } = require(process.env.PLAYWRIGHT || "playwright");
const path = require("path");
const fs = require("fs");

const PORT = process.argv[2] || "18190";
const BASE = "http://127.0.0.1:" + PORT;
const SHOT = process.argv[3];
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
  const tag = await page.$$eval(".avatar-chat-handle-tag", (els) => els.map((e) => [e.textContent, e.style.background]));
  check(tag.some((t) => t[0] === "op" && /85, 255, 255/.test(t[1])), "handle tag with its colours", JSON.stringify(tag));
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

  // style editor
  await page.click("#avatar-chat-style-open");
  await page.waitForSelector("#avatar-chat-style-letters input[type=color]");
  check((await page.$$("#avatar-chat-style-letters input[type=color]")).length === 6, "one colour per letter of the alias");
  await page.selectOption("#avatar-chat-style-preset", "rainbow");
  await page.fill("#avatar-chat-style-tag", "web|dev");
  await page.click("#avatar-chat-style-save");
  await page.waitForTimeout(500);
  st = await (await fetch(BASE + "/__state")).json();
  check(st.style.colors.length === 6 && /^#[0-9a-f]{6}$/.test(st.style.colors[0]) && st.style.tag.text === "webdev", "style saved (rainbow, pipe dropped from tag)", JSON.stringify(st.style));
  check(st.posts.find((x) => x.action === "saveStyle").token.length === 32, "style save carries the token");
  await page.waitForTimeout(900);

  // upload: chunked, link lands in the composer
  const png = path.join(__dirname, "..", "build", "pixel.png");
  fs.mkdirSync(path.dirname(png), { recursive: true });
  fs.writeFileSync(png, Buffer.alloc(200, 7));   // 200 bytes: seven 30-byte chunks
  await page.setInputFiles("#avatar-chat-media-file", png);
  await page.waitForFunction(() => /chatmedia/.test(document.querySelector("#avatar-chat-input").value), null, { timeout: 5000 }).catch(() => {});
  const composed = await page.$eval("#avatar-chat-input", (e) => e.value);
  st = await (await fetch(BASE + "/__state")).json();
  check(/\/chatmedia\/2026-10-11\/abcdefabcdefabcdefabcdef\.png$/.test(composed), "uploaded link put in the composer", composed);
  check(st.stored.length === 1 && st.stored[0][1] === 200, "all chunks arrived in order", JSON.stringify(st.stored));
  check(st.posts.filter((x) => x.action === "upload:chunk").length === 7, "seven chunks for 200 bytes at 30 a chunk");
  check(await page.$eval("#avatar-chat-media-open", (e) => !e.hidden), "Attach button shown when uploads are on");

  if (SHOT) await page.screenshot({ path: SHOT, fullPage: false });
  check(errors.length === 0, "no page errors", errors.join(" | "));
  check(dialogs.length === 0, "no alert() ran", dialogs.join(" | "));
  await browser.close();
  console.log(failed ? failed + " failed" : "all passed");
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
