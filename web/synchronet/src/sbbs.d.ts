/* The parts of the Synchronet SSJS runtime this package uses (web server,
 * jsexec). Types only: nothing here exists at run time except as the
 * Synchronet globals themselves. */

declare var LOG_ERR: number;
declare var LOG_WARNING: number;
declare var LOG_INFO: number;
declare var LOG_DEBUG: number;

interface SbbsSystem {
    name: string;
    qwk_id: string;
    inet_addr: string;
    data_dir: string;
    ctrl_dir: string;
    exec_dir: string;
    mods_dir: string;
    matchuser(name: string): number;
}

interface SbbsUserSecurity { level: number; }

interface SbbsUser {
    number: number;
    alias: string;
    ip_address: string;
    is_sysop: boolean;
    security: SbbsUserSecurity;
    compare_ars(ars: string): boolean;
}

interface SbbsHttpRequest {
    method: string;
    scheme: string;
    host: string;
    vhost: string;
    query: { [key: string]: string[] };
    header: { [key: string]: string };
    post_data?: string;
}

interface SbbsHttpReply {
    status: string;
    header: { [key: string]: string };
}

interface SbbsJs {
    exec_dir: string;
    on_exit(code: string): void;
    gc(): void;
}

declare class File {
    constructor(path: string);
    open(mode: string): boolean;
    close(): void;
    read(count?: number): string;
    write(text: string): boolean;
    readAll(): string[];
    iniGetValue(section: string | null, key: string, def?: string | number | boolean): string | number | boolean | null;
    iniGetSections(prefix?: string): string[];
    iniSetValue(section: string | null, key: string, value: string): boolean;
    base64: boolean;
    exists: boolean;
    length: number;
    position: number;
}

declare class JSONClient {
    constructor(host: string, port: number);
    settings: { TIMEOUT: number };
    connected: boolean;
    updates: JsonPacket[];
    read(scope: string, location: string, lock: number): unknown;
    write(scope: string, location: string, data: unknown, lock: number): unknown;
    push(scope: string, location: string, data: unknown, lock: number): unknown;
    slice(scope: string, location: string, start: number, end: number | undefined, lock: number): unknown;
    keys(scope: string, location: string, lock: number): unknown;
    who(scope: string, location: string): unknown;
    subscribe(scope: string, location: string): unknown;
    unsubscribe(scope: string, location: string): unknown;
    cycle(): void;
    disconnect(): void;
}

interface JsonPacket {
    scope?: string;
    func?: string;
    oper?: string;
    location?: string;
    data?: unknown;
}

declare class HTTPRequest {
    constructor(user?: string, password?: string, headers?: string[], timeout?: number);
    Get(url: string): string;
    response_code: number;
    response_headers_parsed: { [key: string]: string[] };
}

interface AvatarObject { data?: string; }
interface AvatarLib {
    read_localuser(number: number): AvatarObject | null;
    read_netuser(name: string, netaddr: string): AvatarObject | null;
}

interface WebSettings {
    web_directory: string;
    web_root: string;
    web_lib: string;
    guest: string;
    avatar_chat_ini?: string;
    [key: string]: unknown;
}

declare var system: SbbsSystem;
declare var user: SbbsUser;
declare var http_request: SbbsHttpRequest;
declare var http_reply: SbbsHttpReply;
declare var js: SbbsJs;
declare var settings: WebSettings;

declare function load(path: string, ...args: unknown[]): any;
declare function load(scope: object, path: string, ...args: unknown[]): any;
declare function log(level: number, text: string): void;
declare function write(text: string): void;
declare function exit(code?: number): void;
declare function time(): number;
declare function strftime(fmt: string, t: number): string;
declare function format(fmt: string, ...args: unknown[]): string;
declare function random(n: number): number;
declare function mswait(ms: number): void;
declare function file_exists(path: string): boolean;
declare function file_isdir(path: string): boolean;
declare function file_remove(path: string): boolean;
declare function file_rename(from: string, to: string): boolean;
declare function file_size(path: string): number;
declare function file_date(path: string): number;
declare function file_getname(path: string): string;
declare function backslash(path: string): string;
declare function directory(pattern: string, flags?: number): string[];
declare function mkdir(path: string): boolean;
declare function mkpath(path: string): boolean;
declare function rmdir(path: string): boolean;
declare function resolve_ip(host: string): string | null;
declare function md5_calc(text: string, hex?: boolean): string;
/* events.ssjs defines emit() in the scope its modules run in. */
declare function emit(event: { [key: string]: string | object }): void;
