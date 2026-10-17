// A static WordPress stand-in for the migration wizard spec. It serves the
// fictional Example Press REST answers from internal/wpmigrate/testdata/example-press
// exactly like the Go test helper (internal/wpmigrate/helpers_test.go):
// routes.json maps a canonical "path?sorted-query" key to {file, status,
// headers}; https://example.org in bodies is rewritten to this server's URL;
// any /wp-content/uploads/* answers a tiny valid file of the extension's type.
//
// Run standalone: node fixtures/wp-fixture-server.ts [port]
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { readFileSync } from 'node:fs';
import { dirname, extname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { deflateSync } from 'node:zlib';

const here = dirname(fileURLToPath(import.meta.url));
export const DEFAULT_FIXTURE_DIR = resolve(here, '../../internal/wpmigrate/testdata/example-press');

interface Route {
  file: string;
  status?: number;
  headers?: Record<string, string>;
}

export interface WPFixture {
  url: string;
  hits: Map<string, number>;
  close(): Promise<void>;
}

// goQueryEscape mirrors Go's url.QueryEscape (space -> '+', only
// [A-Za-z0-9-_.~] kept), so keys match the recorder's CanonicalKey.
function goQueryEscape(s: string): string {
  return Array.from(new TextEncoder().encode(s))
    .map((b) => {
      const c = String.fromCharCode(b);
      if (/[A-Za-z0-9\-_.~]/.test(c)) return c;
      if (c === ' ') return '+';
      return '%' + b.toString(16).toUpperCase().padStart(2, '0');
    })
    .join('');
}

// canonicalKey mirrors canonicalKey in internal/wpmigrate/helpers_test.go:
// drop per_page, _embed, _fields, status, the default orderby=id&order=asc
// and page=1, sort the remaining keys.
export function canonicalKey(u: URL): string {
  const q = new URLSearchParams(u.search);
  q.delete('per_page');
  q.delete('_embed');
  q.delete('_fields');
  q.delete('status');
  if (q.get('orderby') === 'id' && q.get('order') === 'asc') {
    q.delete('orderby');
    q.delete('order');
  }
  if (q.get('page') === '1') q.delete('page');
  const keys = Array.from(new Set(Array.from(q.keys()))).sort();
  const parts: string[] = [];
  for (const k of keys) for (const v of q.getAll(k)) parts.push(goQueryEscape(k) + '=' + goQueryEscape(v));
  return parts.length ? `${u.pathname}?${parts.join('&')}` : u.pathname;
}

// crc32 for the PNG chunks (tiny, table-free).
function crc32(buf: Buffer): number {
  let c = ~0;
  for (const b of buf) {
    c ^= b;
    for (let k = 0; k < 8; k++) c = c & 1 ? (c >>> 1) ^ 0xedb88320 : c >>> 1;
  }
  return ~c >>> 0;
}

function chunk(type: string, data: Buffer): Buffer {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const td = Buffer.concat([Buffer.from(type, 'ascii'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(td));
  return Buffer.concat([len, td, crc]);
}

// tinyPNG returns a valid w×h RGBA PNG filled with one colour.
export function tinyPNG(w = 2, h = 2, rgba: [number, number, number, number] = [200, 0, 0, 255]): Buffer {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 6; // RGBA
  const row = Buffer.concat([Buffer.from([0]), Buffer.from(Array.from({ length: w }, () => rgba).flat())]);
  const raw = Buffer.concat(Array.from({ length: h }, () => row));
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

function uploadBody(path: string): { type: string; data: Buffer } | null {
  switch (extname(path).toLowerCase()) {
    case '.png':
    case '.jpg':
    case '.jpeg':
      return { type: 'image/png', data: tinyPNG() };
    case '.gif':
      return { type: 'image/gif', data: Buffer.from('R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7', 'base64') };
    case '.svg':
      return { type: 'image/svg+xml', data: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"><rect width="2" height="2"/></svg>') };
    case '.pdf':
      return { type: 'application/pdf', data: Buffer.from('%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n') };
    default:
      return null;
  }
}

function sendJSON(res: ServerResponse, status: number, body: string): void {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=UTF-8' });
  res.end(body);
}

export async function startWPFixture(opts: { dir?: string; port?: number } = {}): Promise<WPFixture> {
  const dir = opts.dir ?? DEFAULT_FIXTURE_DIR;
  const routes: Record<string, Route> = JSON.parse(readFileSync(join(dir, 'routes.json'), 'utf8'));
  const hits = new Map<string, number>();
  let base = '';

  const handler = (req: IncomingMessage, res: ServerResponse): void => {
    const u = new URL(req.url ?? '/', base);
    hits.set(u.pathname, (hits.get(u.pathname) ?? 0) + 1);
    const head = req.method === 'HEAD';
    if (u.pathname.startsWith('/wp-content/uploads/')) {
      const up = uploadBody(u.pathname);
      if (!up) {
        res.writeHead(404).end();
        return;
      }
      res.writeHead(200, { 'Content-Type': up.type, 'Content-Length': String(up.data.length) });
      res.end(head ? undefined : up.data);
      return;
    }
    const key = canonicalKey(u);
    const rt = routes[key];
    if (!rt) {
      sendJSON(res, 404, '{"code":"rest_no_route","message":"No route","data":{"status":404}}');
      return;
    }
    let body = readFileSync(join(dir, rt.file), 'utf8');
    body = body.split('https://example.org').join(base).split('https:\\/\\/example.org').join(base.replaceAll('/', '\\/'));
    const headers: Record<string, string> = { ...(rt.headers ?? {}) };
    if (!headers['Content-Type']) headers['Content-Type'] = 'application/json; charset=UTF-8';
    res.writeHead(rt.status || 200, headers);
    res.end(head ? undefined : body);
  };

  const server: Server = createServer(handler);
  await new Promise<void>((ok) => server.listen(opts.port ?? 0, '127.0.0.1', ok));
  const addr = server.address();
  if (!addr || typeof addr === 'string') throw new Error('wp fixture: no address');
  base = `http://127.0.0.1:${addr.port}`;
  return {
    url: base,
    hits,
    close: () => new Promise<void>((ok) => server.close(() => ok())),
  };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const fx = await startWPFixture({ port: Number(process.argv[2] ?? 0) });
  console.log(`wp fixture serving ${DEFAULT_FIXTURE_DIR} at ${fx.url}`);
}
