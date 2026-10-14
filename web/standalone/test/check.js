// Drives the standalone page in headless Chromium against a real
// avatar_chat_server (run on scratch ports, the page served from this
// checkout with -web-dir) through a TCP proxy that can freeze a WebSocket:
// it stops passing bytes either way but never closes, the way a socket is
// left after a laptop sleeps or a phone backgrounds the tab. Checks:
//
//   - a message sent on a socket not heard from lately waits for a probe,
//     the probe fails, the page reconnects and the message goes then, once;
//   - a message sent into a dead socket that still looked alive is not
//     shown as sent: it fails and its text is back in the box;
//   - a reader scrolled up keeps their place across a reconnect, one
//     pinned to the bottom stays there, and nothing is shown twice;
//   - a server ERROR on a send (too long, too fast) shows as not sent;
//   - a refused name claim for a BBS user's name says so.
//
//   node test/check.js <avatar_chat_server binary>     (from web/standalone)
//   PLAYWRIGHT=/path/to/node_modules/playwright overrides where Playwright is.
const { chromium } = require(process.env.PLAYWRIGHT || "/sbbs/webv4_custom/node_modules/playwright");
const net = require("net");
const fs = require("fs");
const os = require("os");
const path = require("path");
const { spawn } = require("child_process");

const SRV = process.argv[2];
const PAGE_DIR = process.env.PAGE_DIR || path.resolve(__dirname, "..");
let failed = 0;
function check(cond, name, detail) {
  console.log((cond ? "ok   " : "FAIL ") + name + (cond || detail === undefined ? "" : ": " + detail));
  if (!cond) failed++;
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function freePort() {
  return new Promise((resolve) => {
    const s = net.createServer().listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => resolve(p)); });
  });
}
async function waitPort(port) {
  for (let i = 0; i < 100; i++) {
    const ok = await new Promise((r) => { const c = net.connect(port, "127.0.0.1", () => { c.destroy(); r(true); }); c.on("error", () => r(false)); });
    if (ok) return;
    await sleep(100);
  }
  throw new Error("server didn't come up on " + port);
}

// The proxy: every connection is passed through until frozen.
function proxy(listen, target) {
  const conns = [];
  const server = net.createServer((client) => {
    const up = net.connect(target, "127.0.0.1");
    const c = { client, up, frozen: false, ws: false };
    conns.push(c);
    client.on("data", (d) => {
      if (!c.ws && /upgrade:\s*websocket/i.test(d.toString("latin1"))) c.ws = true;
      if (!c.frozen) up.write(d);
    });
    up.on("data", (d) => { if (!c.frozen) client.write(d); });
    const end = () => { client.destroy(); up.destroy(); };
    client.on("error", end); up.on("error", end); client.on("close", end); up.on("close", end);
  });
  server.listen(listen, "127.0.0.1");
  return {
    server, conns,
    wsCount: () => conns.filter((c) => c.ws).length,
    freeze: () => conns.forEach((c) => { if (c.ws) c.frozen = true; }),
  };
}

// A TCP chat client, like a door.
function tcpClient(port) {
  const sock = net.connect(port, "127.0.0.1");
  const got = [];
  let buf = "";
  sock.on("data", (d) => {
    buf += d.toString("utf8");
    let i;
    while ((i = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (line) { try { got.push(JSON.parse(line)); } catch (e) { /* skip */ } }
    }
  });
  const send = (p) => sock.write(JSON.stringify(p) + "\r\n");
  return { sock, got, send, ready: new Promise((r) => sock.on("connect", r)) };
}
async function history(port, room) {
  const c = tcpClient(port);
  await c.ready;
  c.send({ scope: "chat", func: "QUERY", oper: "SLICE", location: "channels." + room + ".history", data: { start: -500 }, lock: 1, timeout: 5000, nick: "Checker", system: "BBS" });
  for (let i = 0; i < 50 && !c.got.some((p) => p.func === "RESPONSE"); i++) await sleep(50);
  c.sock.destroy();
  const r = c.got.find((p) => p.func === "RESPONSE");
  return r && Array.isArray(r.data) ? r.data : [];
}
const countIn = (list, str) => list.filter((m) => m && m.str === str).length;

(async () => {
  if (!SRV) { console.error("usage: node test/check.js <avatar_chat_server binary>"); process.exit(2); }
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "acs-check-"));
  const [chatPort, webPort, proxyPort] = [await freePort(), await freePort(), await freePort()];
  // frozen sockets stay open on the server (it drops them after ~75 s), hence the per-IP cap
  const srv = spawn(SRV, ["-addr", "127.0.0.1:" + chatPort, "-web-addr", "127.0.0.1:" + webPort, "-store", "off",
    "-web-dir", PAGE_DIR, "-web-claims-file", "", "-web-cache-dir", "", "-web-max-per-ip", "20", "-system-name", "TestWeb"], { cwd: tmp, stdio: ["ignore", "ignore", "pipe"] });
  let srvLog = "";
  srv.stderr.on("data", (d) => { srvLog += d; });
  const px = proxy(proxyPort, webPort);
  let browser;
  try {
    await waitPort(webPort);

    // 80 messages of history, a few per connection (flood control).
    const t0 = Date.now() - 3600 * 1000;
    for (let k = 0; k < 10; k++) {
      const c = tcpClient(chatPort);
      await c.ready;
      for (let j = 0; j < 8; j++) {
        const n = k * 8 + j;
        c.send({ scope: "chat", func: "QUERY", oper: "PUSH", location: "channels.main.history", lock: 2, timeout: -1, nick: "Seeder", system: "BBS",
          data: { nick: { name: "Seeder", host: "BBS" }, str: "seed message " + n, time: t0 + n * 300000 } });
      }
      await sleep(50);
      c.sock.destroy();
    }

    browser = await chromium.launch();
    const ctx = await browser.newContext({ viewport: { width: 1100, height: 700 } });
    await ctx.addInitScript(() => {
      localStorage.setItem("avchat.nick", "Tester");
      localStorage.setItem("avchat.noavatar", "1");
      localStorage.setItem("avchat.rooms", JSON.stringify(["main"]));
    });
    const page = await ctx.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    await page.goto("http://127.0.0.1:" + proxyPort + "/");
    await page.waitForFunction(() => document.querySelectorAll("#messages .avatar-chat-bubble").length >= 60, null, { timeout: 10000 });
    await page.waitForFunction(() => window.AvatarChatPage.state.claimed && !window.AvatarChatPage.state.holdOutbox);
    await sleep(500);

    const bubbles = () => page.$$eval("#messages .avatar-chat-bubble", (l) => l.map((b) => ({ text: b.innerText.trim(), cls: b.className })));
    const status = () => page.$eval("#status-text", (e) => e.textContent);
    const inputValue = () => page.$eval("#input", (e) => e.value);
    const reconnected = (before) => page.waitForFunction((n) => {
      const s = window.AvatarChatPage.state;
      return s.claimed && !s.holdOutbox && window.__wsOpened > n;
    }, before, { timeout: 20000 });
    // count WebSockets the page opens
    await page.evaluate(() => {
      window.__wsOpened = 0;
      const W = window.WebSocket;
      window.WebSocket = function (...a) { window.__wsOpened++; return new W(...a); };
      Object.assign(window.WebSocket, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 });
    });
    const wake = () => page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));

    // --- (b) a reader scrolled up keeps their place across a reconnect ---
    await page.evaluate(() => { document.getElementById("messages").scrollTop = 400; });
    await sleep(200);
    const anchor = await page.evaluate(() => {
      const box = document.getElementById("messages");
      const top = box.getBoundingClientRect().top;
      const b = Array.from(box.querySelectorAll(".avatar-chat-bubble")).find((n) => n.getBoundingClientRect().bottom > top);
      return { text: b.innerText.trim(), offset: b.getBoundingClientRect().top - top, scrollTop: box.scrollTop };
    });
    const countBefore = (await bubbles()).length;
    px.freeze();
    // a message arrives while the socket is dead
    const door = tcpClient(chatPort);
    await door.ready;
    door.send({ scope: "chat", func: "QUERY", oper: "WRITE", location: "channels.main.messages", lock: 2, timeout: -1, nick: "Seeder", system: "BBS",
      data: { nick: { name: "Seeder", host: "BBS" }, str: "said while you slept", time: Date.now() } });
    door.send({ scope: "chat", func: "QUERY", oper: "PUSH", location: "channels.main.history", lock: 2, timeout: -1, nick: "Seeder", system: "BBS",
      data: { nick: { name: "Seeder", host: "BBS" }, str: "said while you slept", time: Date.now() } });
    let opened = await page.evaluate(() => window.__wsOpened);
    const tWake = Date.now();
    await wake();
    await reconnected(opened);
    const tBack = Date.now() - tWake;
    await sleep(400);
    const after = await page.evaluate((text) => {
      const box = document.getElementById("messages");
      const top = box.getBoundingClientRect().top;
      const b = Array.from(box.querySelectorAll(".avatar-chat-bubble")).find((n) => n.innerText.trim() === text);
      return b ? { offset: b.getBoundingClientRect().top - top, bottomGap: box.scrollHeight - box.scrollTop - box.clientHeight } : null;
    }, anchor.text);
    check(tBack < 9000, "dead socket found by a probe on wake and replaced", tBack + " ms");
    check(after && Math.abs(after.offset - anchor.offset) <= 2, "scrolled-up reader keeps their anchor message in place across a reconnect",
      JSON.stringify({ anchor, after }));
    check(after && after.bottomGap > 200, "and isn't jumped to the bottom", JSON.stringify(after));
    let list = await bubbles();
    const texts = list.map((b) => b.text);
    check(new Set(texts).size === texts.length, "no message shown twice after the reload", texts.length + " shown, " + new Set(texts).size + " distinct");
    check(texts.includes("said while you slept") && list.length >= countBefore, "what was said during the outage is there after the reload");

    // pinned reader stays pinned
    await page.evaluate(() => { const b = document.getElementById("messages"); b.scrollTop = b.scrollHeight; });
    await sleep(200);
    px.freeze();
    door.send({ scope: "chat", func: "QUERY", oper: "PUSH", location: "channels.main.history", lock: 2, timeout: -1, nick: "Seeder", system: "BBS",
      data: { nick: { name: "Seeder", host: "BBS" }, str: "second outage news", time: Date.now() } });
    opened = await page.evaluate(() => window.__wsOpened);
    await wake();
    await reconnected(opened);
    await sleep(400);
    const pin = await page.evaluate(() => {
      const b = document.getElementById("messages");
      const last = Array.from(b.querySelectorAll(".avatar-chat-bubble")).pop();
      return { gap: b.scrollHeight - b.scrollTop - b.clientHeight, last: last && last.innerText.trim() };
    });
    check(pin.gap < 5 && pin.last === "second outage news", "a reader at the bottom stays pinned across a reconnect", JSON.stringify(pin));

    // --- (a) sending after a sleep: probe first, then send on the new socket ---
    px.freeze();
    await page.evaluate(() => { window.AvatarChatPage.state.heard = Date.now() - 60000; });
    opened = await page.evaluate(() => window.__wsOpened);
    await page.fill("#input", "hello after sleep");
    await page.press("#input", "Enter");
    list = await bubbles();
    const pend = list.filter((b) => b.text === "hello after sleep");
    check(pend.length === 1 && /is-sending/.test(pend[0].cls), "a message on a socket not heard from lately shows as sending, not sent", JSON.stringify(pend));
    await reconnected(opened);
    await page.waitForFunction(() => {
      const b = Array.from(document.querySelectorAll("#messages .avatar-chat-bubble")).filter((n) => n.innerText.trim() === "hello after sleep");
      return b.length === 1 && !b[0].classList.contains("is-sending");
    }, null, { timeout: 10000 }).catch(() => {});
    list = await bubbles();
    const sent = list.filter((b) => b.text === "hello after sleep");
    check(sent.length === 1 && !/is-sending|is-failed/.test(sent[0].cls), "after the reconnect it is sent and shown once", JSON.stringify(sent));
    check(countIn(await history(chatPort, "main"), "hello after sleep") === 1, "the server has it exactly once");

    // --- (a) sending into a dead socket that still looks alive ---
    px.freeze();
    await page.evaluate(() => { window.AvatarChatPage.state.heard = Date.now(); });
    opened = await page.evaluate(() => window.__wsOpened);
    await page.fill("#input", "into the void");
    await page.press("#input", "Enter");
    const pend2 = (await bubbles()).filter((b) => b.text === "into the void");
    check(pend2.length === 1 && /is-sending/.test(pend2[0].cls), "a message into a dead socket shows as sending first", JSON.stringify(pend2));
    await reconnected(opened);
    await sleep(500);
    const st = await status(), val = await inputValue();
    list = await bubbles();
    check(val === "into the void", "no ack: its text is back in the box", JSON.stringify(val));
    check(/^Not sent: .*may not have gone through/.test(st), "and the status says it wasn't sent", st);
    check(!list.some((b) => b.text === "into the void"), "and it isn't shown as sent");
    check(countIn(await history(chatPort, "main"), "into the void") === 0, "(the server never got it)");
    // sending it again works on the new socket
    await page.press("#input", "Enter");
    await page.waitForFunction(() => Array.from(document.querySelectorAll("#messages .avatar-chat-bubble"))
      .some((n) => n.innerText.trim() === "into the void" && !n.classList.contains("is-sending")), null, { timeout: 8000 }).catch(() => {});
    check(countIn(await history(chatPort, "main"), "into the void") === 1 && await inputValue() === "", "resent from the box, it goes through once");

    // --- (c) a server ERROR on a send ---
    await page.evaluate(() => { window.AvatarChatPage.cfg.maxMessage = 5000; document.getElementById("input").maxLength = 5000; });
    const long = "x".repeat(1200);
    await page.fill("#input", long);
    await page.press("#input", "Enter");
    await page.waitForFunction(() => /Not sent/.test(document.getElementById("status-text").textContent), null, { timeout: 5000 }).catch(() => {});
    check(/^Not sent: message is too long\./.test(await status()), "a policy refusal shows as not sent", await status());
    check(await inputValue() === long, "and the refused text is back in the box");
    check(!(await bubbles()).some((b) => b.text === long), "and no bubble is left looking sent");
    await page.fill("#input", "");
    await sleep(11000); // flood control's bucket fills again

    // flood control: 14 at once, burst 10
    await page.evaluate(() => {
      const f = document.getElementById("form"), i = document.getElementById("input");
      for (let n = 0; n < 14; n++) { i.value = "burst " + n; f.requestSubmit(); }
    });
    await sleep(1500);
    list = await bubbles();
    const hist = await history(chatPort, "main");
    const through = Array.from({ length: 14 }, (_, n) => countIn(hist, "burst " + n)).reduce((a, b) => a + b, 0);
    const failedBubbles = list.filter((b) => /^burst \d+/.test(b.text) && /is-failed/.test(b.cls)).length;
    const okBubbles = list.filter((b) => /^burst \d+/.test(b.text) && !/is-failed|is-sending/.test(b.cls)).length;
    const boxed = /^burst \d+$/.test(await inputValue()) ? 1 : 0;
    check(/slow down/.test(await status()) && /^Not sent/.test(await status()), "a flood-control ERROR shows as not sent", await status());
    check(through < 14 && okBubbles === through && failedBubbles + boxed === 14 - through,
      "every refused message is marked or back in the box, every sent one shown as sent",
      JSON.stringify({ through, okBubbles, failedBubbles, boxed }));
    if (failedBubbles) {
      await page.fill("#input", "");
      const first = await page.$("#messages .avatar-chat-bubble.is-failed");
      const t = await first.innerText();
      await first.click();
      check(await inputValue() === t.replace(/\s*Not sent\s*$/, "").trim(), "clicking a failed message puts it back in the box", await inputValue());
    }
    check(errors.length === 0, "no page errors", errors.join("; "));

    // --- a name a door uses is refused, and the page says why ---
    const bbs = tcpClient(chatPort);
    await bbs.ready;
    bbs.send({ scope: "chat", func: "QUERY", oper: "SUBSCRIBE", location: "channels.main.messages", lock: 0, timeout: -1, nick: "DoorUser", system: "BBS" });
    await sleep(300);
    const ctx2 = await browser.newContext();
    await ctx2.addInitScript(() => {
      localStorage.setItem("avchat.nick", "DoorUser");
      localStorage.setItem("avchat.noavatar", "1");
      localStorage.setItem("avchat.rooms", JSON.stringify(["main"]));
    });
    const p2 = await ctx2.newPage();
    await p2.goto("http://127.0.0.1:" + proxyPort + "/");
    await p2.waitForSelector("#nick-modal.is-open", { timeout: 8000 }).catch(() => {});
    const why = await p2.$eval("#nick-error", (e) => e.textContent);
    check(why === "That name belongs to a BBS user. Pick another.", "a BBS user's name: the prompt says so",
      JSON.stringify(why) + " " + JSON.stringify(await p2.evaluate(() => { const s = window.AvatarChatPage.state; return { claimed: s.claimed, refused: s.refused, nick: s.nick }; })));
    bbs.sock.destroy();
    door.sock.destroy();
  } catch (e) {
    console.log("FAIL " + (e && e.stack || e));
    failed++;
  } finally {
    if (browser) await browser.close();
    px.conns.forEach((c) => { c.client.destroy(); c.up.destroy(); });
    px.server.close();
    srv.kill();
    fs.rmSync(tmp, { recursive: true, force: true });
  }
  if (failed) console.log("server log:\n" + srvLog.split("\n").slice(-30).join("\n"));
  console.log(failed ? failed + " failed" : "all ok");
  process.exit(failed ? 1 : 0);
})();
