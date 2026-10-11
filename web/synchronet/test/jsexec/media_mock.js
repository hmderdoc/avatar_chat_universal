// A stand-in web server for the link-card media tests: the harness points
// http://example.com at it (see harness.js). Never listens beyond localhost.
const http = require("http");
const PORT = parseInt(process.argv[2] || "18189", 10);
const ID3 = Buffer.concat([Buffer.from("ID3\x03\x00\x00\x00\x00\x00\x00", "latin1"), Buffer.alloc(300 * 1024, 0x55)]);
const routes = {
  // futureland's files.ssjs: dynamic, so websrvr answers Range with 416
  "/api/files.ssjs?call=download-file&dir=mp3s&file=Song.mp3": { dyn: true, type: "application/octet-stream", disp: 'attachment; filename="Song.mp3"', body: ID3 },
  "/get?id=7": { type: "application/octet-stream", body: Buffer.from("ID3\x04\x00", "latin1") },
  "/clip?x=1": { type: "application/octet-stream", body: Buffer.from("\x00\x00\x00\x18ftypisom\x00\x00", "latin1") },
  "/blob": { type: "application/octet-stream", disp: "attachment; filename=notes.zip", body: Buffer.from("PK\x03\x04", "latin1") },
  "/v.webm?t=1": { type: "video/webm", body: Buffer.from([0x1a, 0x45, 0xdf, 0xa3]) },
  "/stream": { type: "audio/mpeg", body: Buffer.from([0xff, 0xfb, 0x90, 0x00]) },
  "/?page=records&track=Song.mp3": { dyn: true, type: "text/html; charset=utf-8",
    body: Buffer.from('<html><head><title>Song</title><meta property="og:title" content="Song by Someone"><meta property="og:audio" content="http://example.com/media/Song.mp3"></head></html>') },
};
http.createServer((req, res) => {
  const r = routes[req.url];
  if (!r) { res.writeHead(404); return res.end(); }
  if (r.dyn && req.headers.range) { res.writeHead(416); return res.end(); }
  const h = { "Content-Type": r.type, "Content-Length": r.body.length };
  if (r.disp) h["Content-Disposition"] = r.disp;
  res.writeHead(200, h);
  res.end(r.body);
}).listen(PORT, "127.0.0.1");
