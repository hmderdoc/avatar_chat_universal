/* Runs the built SSJS (dist/webv4) under jsexec against a LOCAL test chat
 * server, with Synchronet's per-request globals stubbed. Each script is
 * wrapped in a function whose parameters shadow system, user, http_request,
 * http_reply, settings, write and emit, so every data path points into a
 * sandbox directory and nothing touches the live BBS's data.
 *
 *   jsexec -n test/jsexec/harness.js <sandbox dir> <dist/webv4 dir>
 *
 * The sandbox must hold xtrn/avatar_chat_universal/avatar_chat.ini pointing
 * at the test server (see run.sh). */

load('json-client.js');
load('http.js');

var SANDBOX = argv[0];
var DIST = argv[1];
var failures = 0;
var passes = 0;

function check(cond, name, detail) {
    if (cond) {
        passes++;
        writeln('ok   ' + name);
    } else {
        failures++;
        writeln('FAIL ' + name + (detail === undefined ? '' : ': ' + detail));
    }
}

function readText(path) {
    var f = new File(path);
    if (!f.open('r')) throw new Error('cannot read ' + path);
    var t = f.read();
    f.close();
    return t;
}

/* The webv4 preamble (modopts, init.js, auth.js) needs a real web server;
   the stubs below stand in for what it provides. */
function stripWebv4(text) {
    return text
        .replace("var settings = load('modopts.js', 'web') || { web_directory: '../webv4' };", '')
        .replace("load(settings.web_directory + '/lib/init.js');", '')
        .replace("load(settings.web_lib + 'auth.js');", '');
}

var PARAMS = ['system', 'user', 'http_request', 'http_reply', 'settings', 'write', 'emit'];
function compile(path, tail) {
    var args = PARAMS.slice();
    args.push(stripWebv4(readText(path)) + '\n' + (tail || ''));
    return Function.apply(null, args);
}

var apiFn = compile(DIST + 'root/api/avatarchat.ssjs');
var uploadFn = compile(DIST + 'root/api/avatarchat-upload.ssjs');
var pageFn = compile(DIST + 'lib/avatarchat-page.js', 'return AvatarChatPage;');
var eventsFn = compile(DIST + 'lib/events/avatarchat.js', 'return { cycle: cycle, dispose: dispose };');

var ACCOUNTS = { ALICE: 9001, BOB: 9002, SYSOP: 9003 };
var fakeSystem = {
    name: 'Test BBS',
    qwk_id: 'TESTBBS',
    inet_addr: 'localhost',
    data_dir: SANDBOX + 'data/',
    ctrl_dir: SANDBOX + 'ctrl/',
    exec_dir: SANDBOX + 'exec/',
    mods_dir: SANDBOX + 'mods/',
    matchuser: function (name) { return ACCOUNTS[String(name).toUpperCase()] || 0; }
};
var fakeSettings = { web_directory: SANDBOX, web_root: SANDBOX + 'webroot/', web_lib: SANDBOX + 'lib/', guest: 'Guest' };

function account(number, alias, sysop) {
    return {
        number: number, alias: alias, ip_address: '127.0.0.1', is_sysop: !!sysop,
        security: { level: sysop ? 99 : 50 },
        compare_ars: function () { return !!sysop; }
    };
}
var alice = account(9001, 'Alice');
var bob = account(9002, 'Bob');
var sysop = account(9003, 'Sysop', true);
var guest = account(0, 'Guest');

function request(method, query, headers, body) {
    var q = {};
    for (var k in query) q[k] = [String(query[k])];
    var h = { host: 'bbs.test' };
    for (var k2 in headers || {}) h[k2] = headers[k2];
    return { method: method, scheme: 'http', host: 'bbs.test', vhost: 'bbs.test', query: q, header: h, post_data: body };
}

function invoke(fn, who, method, query, headers, body) {
    var out = [];
    var reply = { status: '200 OK', header: {} };
    fn(fakeSystem, who, request(method, query, headers, body), reply, fakeSettings,
        function (t) { out.push(t); }, function () {});
    var text = out.join('');
    try {
        var parsed = JSON.parse(text);
        parsed._status = reply.status;
        return parsed;
    } catch (e) {
        return { _raw: text, _status: reply.status };
    }
}

var tokens = {};
function page(who) {
    var lib = pageFn(fakeSystem, who, request('GET', {}), { header: {} }, fakeSettings, function () {}, function () {});
    var cfg = lib.pageConfig();
    tokens[who.number] = cfg.token;
    return cfg;
}

function api(who, method, query, body, extraHeaders) {
    var headers = {};
    if (tokens[who.number]) headers['x-avatarchat-token'] = tokens[who.number];
    for (var k in extraHeaders || {}) headers[k] = extraHeaders[k];
    return invoke(apiFn, who, method, query, headers, body);
}

function upload(who, query, body) {
    return invoke(uploadFn, who, 'POST', query, { 'x-avatarchat-token': tokens[who.number] || '' }, body);
}

function lastText(list) { return list && list.length ? list[list.length - 1].text : ''; }

/* ---- page config ---- */
var g = page(guest);
check(g.isLoggedIn === false && g.token === '' && g.uploads.enabled === false, 'guest page: read-only, no token, no uploads');
var a = page(alice);
check(a.isLoggedIn && a.token.length === 32, 'alice page: logged in with a 32-hex token', a.token);
check(/^127\.0\.0\.1:\d+$/.test(a.server) && a.configFound === true, 'page reads the door ini', a.server);
check(a.uploads.enabled === true && a.uploads.maxMb === 2, 'uploads on from [web]');
check(page(alice).token === a.token, 'token is stable within a login session');
page(bob);
page(sysop);

/* ---- guests ---- */
var r = invoke(apiFn, guest, 'GET', { action: 'history', channel: 'main' }, {});
check(r.channel === 'main' && r.messages && r.messages.length === 0, 'guest can read an empty room', JSON.stringify(r));
r = invoke(apiFn, guest, 'POST', { action: 'send', channel: 'main', message: 'hi' }, {});
check(r.error === 'authentication required', 'guest cannot send', r.error);
r = invoke(apiFn, guest, 'GET', { action: 'private' }, {});
check(r.error === 'authentication required', 'guest has no private threads');
r = invoke(apiFn, guest, 'GET', { action: 'preview', url: 'https://example.com/' }, {});
check(r.error === 'authentication required', 'guest gets no link cards');

/* ---- write protection ---- */
r = invoke(apiFn, alice, 'POST', { action: 'send', channel: 'main', message: 'no token' }, {});
check(r.error === 'stale page: reload it' && r._status === '403 Forbidden', 'send without the token is refused', JSON.stringify(r));
r = api(alice, 'POST', { action: 'send', channel: 'main', message: 'cross site' }, '', { origin: 'https://evil.example' });
check(r.error === 'stale page: reload it', 'send from another origin is refused');
r = invoke(apiFn, alice, 'POST', { action: 'send', channel: 'main', message: 'bob token' }, { 'x-avatarchat-token': tokens[bob.number] });
check(r.error === 'stale page: reload it', "another user's token is refused");
r = api(alice, 'GET', { action: 'send', channel: 'main', message: 'via get' });
check(r.error === 'POST required', 'send needs POST');

/* ---- public chat ---- */
r = api(alice, 'POST', { action: 'send', channel: 'main', message: 'hello @bob see https://example.com/page' }, '', { origin: 'http://bbs.test' });
check(r.success === true && r.channel === 'main', 'alice sends to main', JSON.stringify(r));
r = api(bob, 'GET', { action: 'history', channel: 'main' });
var m = r.messages && r.messages[r.messages.length - 1];
check(m && m.sender === 'Alice' && m.system === 'Test BBS' && m.isSelf === false && m.userNumber === 9001, 'bob sees it, not as his own', JSON.stringify(m));
check(m && m.qwkid === 'TESTBBS', 'messages carry the QWK id for avatar lookups');
r = api(alice, 'GET', { action: 'history', channel: 'main' });
check(r.messages[r.messages.length - 1].isSelf === true, 'alice sees it as her own');
r = api(alice, 'POST', { action: 'send', channel: 'main', message: 'ctl\x01Rcodes\x1b[2J ' + new Array(1200).join('x') });
r = api(bob, 'GET', { action: 'history', channel: 'main' });
var t = lastText(r.messages);
check(t.indexOf('\x01') < 0 && t.indexOf('\x1b') < 0 && t.length === 1000, 'control codes stripped, capped at 1000', t.length);
r = api(alice, 'POST', { action: 'send', channel: 'main', message: '   ' });
check(r.error === 'empty message', 'blank message refused');
var bitmap = '[BITMAP|80|25|Test BBS:Alice|' + new Array(4001).join('ab') + ']';
r = api(alice, 'POST', { action: 'send', channel: 'main', message: bitmap });
r = api(bob, 'GET', { action: 'history', channel: 'main' });
check(lastText(r.messages) === bitmap, 'BITMAP art keeps its full length (' + bitmap.length + ')');

/* ---- rooms ---- */
r = api(alice, 'POST', { action: 'createChannel', channel: 'lobby' });
check(r.success === true && r.channel === 'lobby', 'create a room', JSON.stringify(r));
r = api(bob, 'GET', { action: 'channels' });
var names = (r.channels || []).map(function (c) { return c.name; });
check(names.indexOf('lobby') >= 0 && names.indexOf('main') >= 0, 'rooms list has main and lobby', names.join(','));
r = api(alice, 'POST', { action: 'send', channel: 'Bob', message: 'into a mailbox?' });
check(r.error === 'that room is reserved', "a room can't be named after an account (mailbox)", JSON.stringify(r));
r = api(alice, 'POST', { action: 'send', channel: 'motd', message: 'I am not the sysop' });
check(r.error === 'that room is reserved', 'motd is reserved for the host sysop');
r = api(sysop, 'POST', { action: 'send', channel: 'motd', message: 'Welcome to the test board' });
check(r.success === true, 'host sysop writes the motd (host is 127.0.0.1)', JSON.stringify(r));
r = api(bob, 'GET', { action: 'motd' });
check(r.previewText === 'Welcome to the test board', 'motd reads back', JSON.stringify(r));
r = api(bob, 'GET', { action: 'channels' });
names = (r.channels || []).map(function (c) { return c.name; });
check(names.indexOf('motd') < 0, 'motd room hidden from non-sysops', names.join(','));

/* ---- handle styles ---- */
r = api(alice, 'POST', { action: 'saveStyle' }, JSON.stringify({ colors: ['#ff0000', '', '#00FF00', 'bad', '#0000ff', '#123456', '#654321'], tag: { text: 'web|dev', fg: '#ffff55', bg: '#0000aa' } }));
check(r.ok === true && r.style.tag.text === 'webdev' && r.style.colors[2] === '#00ff00' && r.style.colors[3] === '', 'save a style (normalized)', JSON.stringify(r));
var styleFile = SANDBOX + 'data/chat/nickstyle/9001.json';
check(file_exists(styleFile), 'style stored at data/chat/nickstyle/NNNN.json (the shared record)');
r = api(alice, 'GET', { action: 'style' });
check(r.style && r.style.colors.length === 7, 'style reads back', JSON.stringify(r.style));
r = invoke(apiFn, alice, 'POST', { action: 'saveStyle' }, {}, '{}');
check(r.error === 'stale page: reload it', 'style save needs the token');

/* ---- live events + style on the wire ---- */
var emitted = [];
var bobEvents = eventsFn(fakeSystem, bob, request('GET', { channel: 'main' }), { header: {} }, fakeSettings,
    function () {}, function (ev) { emitted.push(ev); });
bobEvents.cycle();   /* connects and subscribes */
mswait(300);
r = api(alice, 'POST', { action: 'send', channel: 'main', message: 'styled now' });
var got = null;
for (var i = 0; i < 20 && !got; i++) {
    mswait(150);
    bobEvents.cycle();
    for (var j = 0; j < emitted.length; j++) {
        var d = JSON.parse(emitted[j].data);
        if (d.type === 'message' && d.text === 'styled now') got = d;
    }
}
check(got && got.sender === 'Alice' && got.isSelf === false, 'bob gets the message live', JSON.stringify(got));
check(got && got.style && got.style.colors.length === 5 && got.style.tag.text === 'webdev', "style rides on the message, trimmed to the alias", got && JSON.stringify(got.style));
r = api(bob, 'GET', { action: 'who', channel: 'main' });
var whoNames = (r.users || []).map(function (u) { return u.nick; });
check(whoNames.indexOf('Bob') >= 0, 'bob is in who while his event stream is open', JSON.stringify(r));

/* ---- private messages ---- */
emitted = [];
var bobMail = eventsFn(fakeSystem, bob, request('GET', { channel: 'main', mailbox: '1' }), { header: {} }, fakeSettings,
    function () {}, function (ev) { emitted.push(ev); });
bobMail.cycle();
mswait(300);
r = api(alice, 'POST', { action: 'sendPrivate', target: 'Bob', message: 'psst bob' });
check(r.success === true, 'alice sends bob a PM', JSON.stringify(r));
var pm = null;
for (i = 0; i < 20 && !pm; i++) {
    mswait(150);
    bobMail.cycle();
    for (j = 0; j < emitted.length; j++) {
        d = JSON.parse(emitted[j].data);
        if (d.type === 'private' && d.text === 'psst bob') pm = d;
    }
}
check(pm && pm.peerName === 'Alice' && pm.private === true, 'bob gets the PM live, peer is alice', JSON.stringify(pm));
r = api(bob, 'GET', { action: 'private' });
check(r.threads && r.threads.length === 1 && r.threads[0].name === 'Alice' && r.threads[0].preview === 'psst bob', "bob's threads", JSON.stringify(r));
r = api(alice, 'GET', { action: 'privateHistory', target: 'Bob' });
check(r.messages && r.messages.length === 1 && r.messages[0].isSelf === true && r.peer.name === 'Bob', "alice's copy of the thread", JSON.stringify(r));
r = api(bob, 'GET', { action: 'history', channel: 'main' });
check(lastText(r.messages) !== 'psst bob', 'the PM is not in the public room');
bobEvents.dispose();
bobMail.dispose();
mswait(300);
r = api(alice, 'GET', { action: 'who', channel: 'main' });
whoNames = (r.users || []).map(function (u) { return u.nick; });
check(whoNames.indexOf('Bob') < 0, 'dispose() unsubscribes (bob leaves who)', JSON.stringify(r));

/* ---- sync poll ---- */
r = api(bob, 'GET', { action: 'sync', channel: 'main', history: '1', presence: '1', motd: '1', since: '1' });
check(r.channels && r.who && r.history && r.private && r.motd && r.presence, 'sync bundles every section', Object.keys(r).join(','));

/* ---- link cards ---- */
r = api(alice, 'GET', { action: 'preview', url: 'http://127.0.0.1:18188/' });
check(r.error === 'port not allowed', 'card for a non-default port refused', JSON.stringify(r));
r = api(alice, 'GET', { action: 'preview', url: 'http://127.0.0.1/' });
check(r.error === 'address not allowed', 'card for loopback refused', JSON.stringify(r));
r = api(alice, 'GET', { action: 'preview', url: 'http://localhost/' });
check(r.error === 'address not allowed', 'card for a name resolving to loopback refused', JSON.stringify(r));
r = api(alice, 'GET', { action: 'preview', url: 'http://[::1]/' });
check(r.error === 'address not allowed', 'card for ::1 refused', JSON.stringify(r));
r = api(alice, 'GET', { action: 'preview', url: 'http://user@example.com/' });
check(r.error === 'invalid url', 'card with userinfo refused', JSON.stringify(r));
r = api(alice, 'GET', { action: 'preview', url: 'https://example.com/' });
if (r.error === 'unresolvable host' || r.error === 'fetch failed') {
    writeln('skip public card fetch (no outside network here): ' + r.error);
} else {
    check(r.kind === 'site' && /Example Domain/.test(r.title), 'card for a public page', JSON.stringify(r));
}

/* ---- uploads ---- */
var PNG_B64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==';
var pngBytes = base64_decode(PNG_B64).length;
r = upload(alice, { action: 'begin', size: pngBytes });
check(r.ok && /^[a-f0-9]{24}$/.test(r.id) && r.chunkSize % 3 === 0, 'begin an upload', JSON.stringify(r));
var id = r.id;
r = upload(alice, { action: 'chunk', id: id, seq: 1 }, PNG_B64);
check(r.ok === false && r.error === 'chunk out of order', 'chunks must arrive in order');
r = upload(bob, { action: 'chunk', id: id, seq: 0 }, PNG_B64);
check(r.ok === false && r.error === 'unknown upload', "bob can't write into alice's upload");
r = upload(alice, { action: 'chunk', id: id, seq: 0 }, PNG_B64);
check(r.ok && r.received === pngBytes, 'chunk stored', JSON.stringify(r));
var oldDir = SANDBOX + 'webroot/chatmedia/2020-01-01/';
mkpath(oldDir);
var oldFile = new File(oldDir + 'abcdefabcdefabcdefabcdef.png'); oldFile.open('w'); oldFile.write('x'); oldFile.close();
var keepDir = SANDBOX + 'webroot/chatmedia/keepme/';
mkpath(keepDir);
r = upload(alice, { action: 'finish', id: id });
check(r.ok && r.kind === 'image' && r.url === 'http://bbs.test/chatmedia/' + strftime('%Y-%m-%d', time()) + '/' + id + '.png', 'finish: sniffed as png, public link', JSON.stringify(r));
check(file_exists(SANDBOX + 'webroot/chatmedia/' + strftime('%Y-%m-%d', time()) + '/' + id + '.png'), 'file stored under chatmedia/<date>/');
check(!file_exists(oldDir), 'retention purge removed a 2020 folder');
check(file_isdir(keepDir), 'purge leaves folders it did not create alone');

/* a JPEG carrying an Exif segment comes out without it */
function seg(marker, body) { return String.fromCharCode(0xff, marker, (body.length + 2) >> 8, (body.length + 2) & 255) + body; }
var jpeg = '\xff\xd8' + seg(0xe0, 'JFIF\0\x01\x02') + seg(0xe1, 'Exif\0\0GPS 51.5N') + seg(0xdb, '\0qqqq') + '\xff\xda\x00\x04ab\xff\xd9';
var jpegB64 = base64_encode(jpeg);
r = upload(alice, { action: 'begin', size: jpeg.length });
id = r.id;
upload(alice, { action: 'chunk', id: id, seq: 0 }, jpegB64);
r = upload(alice, { action: 'finish', id: id });
var stored = SANDBOX + 'webroot/chatmedia/' + strftime('%Y-%m-%d', time()) + '/' + id + '.jpg';
var sf = new File(stored);
var storedBytes = '';
if (sf.open('rb')) { storedBytes = sf.read(file_size(stored)); sf.close(); }
check(r.ok && r.kind === 'image' && storedBytes.length > 0 && storedBytes.indexOf('GPS') < 0 && storedBytes.indexOf('JFIF') > 0,
    'JPEG stored without its Exif segment', JSON.stringify(r) + ' ' + storedBytes.length);
check(r.bytes === storedBytes.length && r.bytes < jpeg.length, 'reply reports the stored size');

var html = base64_encode('<html><script>alert(1)</script></html>');
r = upload(alice, { action: 'begin', size: base64_decode(html).length });
id = r.id;
upload(alice, { action: 'chunk', id: id, seq: 0 }, html);
r = upload(alice, { action: 'finish', id: id });
check(r.ok === false && r.error === 'that file type cannot be posted to chat', 'an HTML file is refused at finish', JSON.stringify(r));
check(directory(SANDBOX + 'webroot/chatmedia/*/' + id + '*').length === 0, 'nothing of it was stored');
r = upload(alice, { action: 'begin', size: 60 * 1048576 });
check(r.ok === false && /limit/.test(r.error), 'over-size upload refused at begin', JSON.stringify(r));
r = upload(alice, { action: 'begin', size: 10 });
id = r.id;
r = upload(alice, { action: 'chunk', id: id, seq: 0 }, PNG_B64);
check(r.ok === false && r.error === 'upload is larger than declared', 'more bytes than declared refused');
r = upload(alice, { action: 'chunk', id: id, seq: 0 }, 'not base64!');
check(r.ok === false, 'a dropped upload id is gone / bad chunk refused', JSON.stringify(r));
r = invoke(uploadFn, alice, 'POST', { action: 'begin', size: 10 }, {});
check(r.ok === false && r.error === 'stale page: reload it', 'upload needs the token');
r = invoke(uploadFn, guest, 'POST', { action: 'begin', size: 10 }, {});
check(r.ok === false && r.error === 'authentication required', 'guests cannot upload');

writeln('');
writeln(passes + ' passed, ' + failures + ' failed');
exit(failures ? 1 : 0);
