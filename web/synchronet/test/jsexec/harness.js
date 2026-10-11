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
    text_dir: SANDBOX + 'text/',
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

/* ---- sidebar rooms: join, create, leave (this BBS's webprefs record) ---- */
r = api(bob, 'GET', { action: 'prefs' });
check(r.ok && r.rooms.length === 1 && r.rooms[0] === 'main' && r.style === 'classic', 'a new user has main in the sidebar and the default style', JSON.stringify(r));
r = api(bob, 'POST', { action: 'joinRoom', channel: 'LOBBY' });
check(r.ok && r.channel === 'lobby' && r.created === false && r.rooms.join(',') === 'main,lobby', 'joining an existing room uses its spelling', JSON.stringify(r));
r = api(bob, 'POST', { action: 'joinRoom', channel: 'brandnew' });
check(r.error === 'no room #brandnew yet', 'a room that does not exist is not made without create=1', JSON.stringify(r));
r = api(bob, 'GET', { action: 'channels' });
check((r.channels || []).map(function (c) { return c.name; }).indexOf('brandnew') < 0, '... and nothing was created');
r = api(bob, 'POST', { action: 'joinRoom', channel: 'brandnew', create: '1' });
check(r.ok && r.created === true && r.rooms.join(',') === 'main,lobby,brandnew', 'create=1 makes it and joins it', JSON.stringify(r));
r = api(alice, 'GET', { action: 'channels' });
check((r.channels || []).map(function (c) { return c.name; }).indexOf('brandnew') >= 0, 'the new room is public');
r = api(bob, 'POST', { action: 'joinRoom', channel: 'Alice', create: '1' });
check(r.error === 'that room is reserved', "a member's mailbox can't be joined or created as a room", JSON.stringify(r));
r = invoke(apiFn, bob, 'POST', { action: 'joinRoom', channel: 'lobby' }, {});
check(r.error === 'stale page: reload it', 'joinRoom needs the token');
r = invoke(apiFn, guest, 'POST', { action: 'joinRoom', channel: 'lobby' }, {});
check(r.error === 'authentication required', 'guests have no sidebar to change');
r = api(bob, 'POST', { action: 'leaveRoom', channel: 'LOBBY' });
check(r.ok && r.rooms.join(',') === 'main,brandnew', 'leaveRoom drops it', JSON.stringify(r));
r = api(bob, 'POST', { action: 'leaveRoom', channel: 'main' });
r = api(bob, 'POST', { action: 'leaveRoom', channel: 'brandnew' });
check(r.ok && r.rooms.length === 0, 'leaving every room leaves an empty sidebar (not main again)', JSON.stringify(r));
check(page(bob).joinedRooms.length === 0, 'the page config carries the empty list');
r = api(bob, 'POST', { action: 'send', channel: 'lobby', message: 'speaking joins' });
check(r.success === true && api(bob, 'GET', { action: 'prefs' }).rooms.join(',') === 'lobby', 'sending in a room adds it to the sidebar');
var prefsFile = SANDBOX + 'data/chat/webprefs/9002.json';
check(file_exists(prefsFile), 'kept in the sandbox data/chat/webprefs/9002.json');

/* ---- page style: one pick, saved per user, served in the page ---- */
var bp = page(bob);
check(bp.pageStyle === 'classic' && bp.pageStyles.length >= 6 && bp.pageStyles[0].id === 'classic', 'page config: the default style and the list', JSON.stringify(bp.pageStyles.map(function (x) { return x.id; })));
r = api(bob, 'POST', { action: 'savePageStyle', style: 'amber' });
check(r.ok && r.style === 'amber', 'savePageStyle stores a listed style', JSON.stringify(r));
check(page(bob).pageStyle === 'amber' && api(bob, 'GET', { action: 'prefs' }).style === 'amber', 'the next page load wears it');
check(api(bob, 'GET', { action: 'prefs' }).rooms.join(',') === 'lobby', 'saving the style keeps the sidebar');
r = api(bob, 'POST', { action: 'savePageStyle', style: 'neon"><script>' });
check(r.error === 'no such page style', 'an unlisted style is refused', JSON.stringify(r));
r = invoke(apiFn, bob, 'POST', { action: 'savePageStyle', style: 'cga' }, {});
check(r.error === 'stale page: reload it', 'savePageStyle needs the token');
check(page(guest).pageStyle === 'classic', 'guests get the default');
check(page(alice).pageStyle === 'classic', "one user's pick doesn't touch another's");
var nickstyleFile = SANDBOX + 'data/chat/nickstyle/9002.json';
check(!file_exists(nickstyleFile) || readText(nickstyleFile).indexOf('amber') < 0, 'the shared nickstyle record is not touched');
/* the sysop's list and default (web_styles, web_default_style) */
var iniPath0 = SANDBOX + 'xtrn/avatar_chat_universal/avatar_chat.ini';
var iniBefore0 = readText(iniPath0);
var iniFile0 = new File(iniPath0);
iniFile0.open('w');
iniFile0.write(iniBefore0.replace(/\s*$/, '') + '\nweb_styles = cga, win95\nweb_default_style = win95\n');
iniFile0.close();
bp = page(bob);
check(bp.pageStyle === 'win95' && bp.pageStyles.map(function (x) { return x.id; }).join(',') === 'cga,win95', 'web_styles narrows the list; a pick no longer offered falls to web_default_style', bp.pageStyle);
check(page(guest).pageStyle === 'win95', 'guests get web_default_style');
r = api(bob, 'POST', { action: 'savePageStyle', style: 'amber' });
check(r.error === 'no such page style', 'a style the sysop took off the list is refused');
iniFile0 = new File(iniPath0);
iniFile0.open('w');
iniFile0.write(iniBefore0);
iniFile0.close();
check(page(bob).pageStyle === 'amber', 'with the full list back, the saved pick returns');

/* ---- handle styles ---- */
/* The page edits colours only. A tag the shell or another site stored in
   the shared record survives a save from here, whatever the request says. */
var styleFile = SANDBOX + 'data/chat/nickstyle/9001.json';
mkpath(SANDBOX + 'data/chat/nickstyle/');
var sfw = new File(styleFile);
sfw.open('w');
sfw.write(JSON.stringify({ version: 1, colors: ['#aaaaaa'], tag: { text: 'webdev', fg: '#ffff55', bg: '#0000aa' } }));
sfw.close();
r = api(alice, 'POST', { action: 'saveStyle' }, JSON.stringify({ colors: ['#ff0000', '', '#00FF00', 'bad', '#0000ff', '#123456', '#654321'], tag: { text: 'evil', fg: '#000000', bg: '#000000' } }));
check(r.ok === true && r.style.colors[2] === '#00ff00' && r.style.colors[3] === '', 'save a style (normalized)', JSON.stringify(r));
check(r.style.tag.text === 'webdev' && r.style.tag.fg === '#ffff55', 'saving colours keeps the stored tag; the request cannot set one', JSON.stringify(r.style.tag));
check(file_exists(styleFile), 'style stored at data/chat/nickstyle/NNNN.json (the shared record)');
r = api(alice, 'GET', { action: 'style' });
check(r.style && r.style.colors.length === 7, 'style reads back', JSON.stringify(r.style));
r = invoke(apiFn, alice, 'POST', { action: 'saveStyle' }, {}, '{}');
check(r.error === 'stale page: reload it', 'style save needs the token');

/* ---- avatars (data/user/NNNN.ini [avatar], text/avatars collections) ---- */
function avatarBin(ch, attr) {
    var b = '';
    for (var n = 0; n < 60; n++) b += ch + String.fromCharCode(attr);
    return b;
}
var collDir = SANDBOX + 'text/avatars/';
mkpath(collDir);
var coll = new File(collDir + 'TESTBBS.faces.bin');
coll.open('wb');
coll.write(avatarBin('A', 0x1f) + avatarBin('B', 0x2e) + avatarBin('C', 0x4b));
coll.close();
load({}, 'sauce_lib.js').write(collDir + 'TESTBBS.faces.bin', { title: 'Test Faces', author: 'Tester', group: 'QA',
    datatype: 5, filetype: 5, cols: 10, tinfo1: 0, tinfo2: 0, tinfo3: 0, tinfo4: 2, tflags: 0, tinfos: '', comment: ['one', 'two', 'three'] });
var extract = new File(collDir + 'TESTBBS.faces.1.bin');
extract.open('wb'); extract.write(avatarBin('Z', 0x07)); extract.close();

r = invoke(apiFn, guest, 'GET', { action: 'avatar' }, {});
check(r.error === 'authentication required', 'guests have no avatar settings');
r = api(alice, 'GET', { action: 'avatar' });
check(r.ok === true && r.avatar === null && r.collections.length === 1, 'no avatar yet; one collection (per-avatar extracts skipped)', JSON.stringify(r));
var cs = r.collections[0];
check(cs.id === 'TESTBBS.faces.bin' && cs.title === 'Test Faces' && cs.count === 3 && cs.previewIndex === 1 && base64_decode(cs.preview) === avatarBin('B', 0x2e),
    'collection summary: SAUCE title, count by rows, preview from tinfo4', JSON.stringify(cs));
r = api(alice, 'GET', { action: 'avatarCollection', collection: 'TESTBBS.faces.bin' });
check(r.ok && r.collection.avatars.length === 3 && r.collection.avatars[2].label === 'three' && base64_decode(r.collection.avatars[2].data) === avatarBin('C', 0x4b),
    'collection lists its avatars with SAUCE comments', JSON.stringify(r).substr(0, 200));
r = api(alice, 'GET', { action: 'avatarCollection', collection: '../../ctrl/main.ini' });
check(r.error === 'no such collection', 'a path is not a collection id');
r = invoke(apiFn, alice, 'POST', { action: 'setAvatar', collection: 'TESTBBS.faces.bin', index: 2 }, {});
check(r.error === 'stale page: reload it', 'setting an avatar needs the token');
r = api(alice, 'POST', { action: 'setAvatar', collection: 'TESTBBS.faces.bin', index: 2 });
check(r.ok && base64_decode(r.avatar.data) === avatarBin('C', 0x4b) && r.avatar.disabled === false, 'pick #3 from the collection', JSON.stringify(r));
var aliceIni = new File(SANDBOX + 'data/user/9001.ini');
aliceIni.open('r');
var stored = aliceIni.iniGetValue('avatar', 'data', '');
aliceIni.close();
check(base64_decode(stored) === avatarBin('C', 0x4b), "saved in the user's own [avatar] record (sandbox data/user/9001.ini)");
r = api(alice, 'POST', { action: 'setAvatar', collection: 'TESTBBS.faces.bin', index: 7 });
check(r.error === 'no such avatar', 'an index past the end is refused');

var drawn = avatarBin('\xdb', 0x0c);
r = api(alice, 'POST', { action: 'setAvatar', data: base64_encode(drawn) });
check(r.ok && base64_decode(r.avatar.data) === drawn, 'a drawn 10x6 avatar is saved', JSON.stringify(r));
r = api(alice, 'POST', { action: 'setAvatar', data: base64_encode(drawn.substr(0, 100)) });
check(r.error === 'that is not a 10x6 avatar', 'a short avatar is refused');
r = api(alice, 'POST', { action: 'setAvatar', data: base64_encode(avatarBin('\x1b', 0x07)) });
check(r.error === 'that is not a 10x6 avatar', 'an avatar with ESC in it is refused');
r = api(alice, 'POST', { action: 'setAvatar', data: base64_encode(avatarBin('A', 0x8f)) });
check(r.error === 'that is not a 10x6 avatar', 'a blinking avatar is refused');

r = api(alice, 'POST', { action: 'send', channel: 'main', message: 'new face' });
r = api(bob, 'GET', { action: 'history', channel: 'main' });
m = r.messages[r.messages.length - 1];
check(m.text === 'new face' && base64_decode(m.avatar || '') === drawn, 'the next message carries the new avatar', JSON.stringify(m).substr(0, 160));
r = api(alice, 'POST', { action: 'avatarEnabled', enabled: '0' });
check(r.ok && r.avatar.disabled === true && base64_decode(r.avatar.data) === drawn, 'disable keeps the art', JSON.stringify(r));
r = api(alice, 'POST', { action: 'send', channel: 'main', message: 'faceless' });
r = api(bob, 'GET', { action: 'history', channel: 'main' });
m = r.messages[r.messages.length - 1];
check(m.text === 'faceless' && !m.avatar, 'a disabled avatar is not sent', JSON.stringify(m).substr(0, 160));
r = api(alice, 'POST', { action: 'avatarEnabled', enabled: '1' });
check(r.ok && r.avatar.disabled === false, 'enable it again');
r = api(bob, 'POST', { action: 'avatarEnabled', enabled: '0' });
check(r.error === 'no avatar saved yet', 'nothing to disable without an avatar');

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

/* the link carries the port the browser used */
function uploadVia(headers) {
    var h = { 'x-avatarchat-token': tokens[alice.number] };
    for (var k in headers) h[k] = headers[k];
    var b = invoke(uploadFn, alice, 'POST', { action: 'begin', size: pngBytes }, h);
    invoke(uploadFn, alice, 'POST', { action: 'chunk', id: b.id, seq: 0 }, h, PNG_B64);
    return invoke(uploadFn, alice, 'POST', { action: 'finish', id: b.id }, h);
}
var today = strftime('%Y-%m-%d', time());
r = uploadVia({ host: 'bbs.test:4080' });
check(r.ok && r.url.indexOf('http://bbs.test:4080/chatmedia/' + today + '/') === 0, 'Host bbs.test:4080 gives a :4080 link', r.url);
r = uploadVia({ host: 'bbs.test:80' });
check(r.ok && r.url.indexOf('http://bbs.test/chatmedia/') === 0, 'the default port is left off', r.url);
r = uploadVia({ host: 'bbs.test', 'x-forwarded-proto': 'https' });
check(r.ok && r.url.indexOf('https://bbs.test/chatmedia/') === 0, 'X-Forwarded-Proto https from a proxy is honoured', r.url);

/* web_media_base_url pins the links for a proxy that rewrites Host */
var iniPath = SANDBOX + 'xtrn/avatar_chat_universal/avatar_chat.ini';
var iniBefore = readText(iniPath);
var iniFile = new File(iniPath);
iniFile.open('w');
iniFile.write(iniBefore.replace(/\s*$/, '') + '\nweb_media_base_url = https://media.example.org/files/\n');
iniFile.close();
r = uploadVia({ host: 'inside:4080' });
check(r.ok && r.url.indexOf('https://media.example.org/files/' + today + '/') === 0, 'web_media_base_url overrides the request host', r.url);
iniFile = new File(iniPath);
iniFile.open('w');
iniFile.write(iniBefore);
iniFile.close();
r = uploadVia({ host: 'bbs.test:4080' });
check(r.ok && r.url.indexOf('http://bbs.test:4080/') === 0, 'without it the request host is back', r.url);
r = invoke(uploadFn, guest, 'POST', { action: 'begin', size: 10 }, {});
check(r.ok === false && r.error === 'authentication required', 'guests cannot upload');

writeln('');
writeln(passes + ' passed, ' + failures + ' failed');
exit(failures ? 1 : 0);
