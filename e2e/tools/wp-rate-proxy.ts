// A read-only, rate-limited reverse proxy in front of a live WordPress site,
// for migration rehearsals (docs/MIGRATION.md). The GoRun migrator
// has no outbound throttle and runs 2 job workers; pointing the wizard at this
// proxy guarantees the live site sees at most RATE requests per second.
//
//   node tools/wp-rate-proxy.ts https://wordpress.example 18091 4 /tmp/proxy.log
//
// - only GET and HEAD are forwarded (anything else: 405, never sent upstream);
// - upstream request starts are spaced >= 1000/RATE ms apart (a FIFO queue);
// - text bodies (json, html, xml, rss) have the upstream origin rewritten to
//   the proxy origin (also the JSON-escaped form) so media downloads and
//   internal links also go through the proxy; redirects are rewritten too;
// - every upstream request is logged as
//   "<end epoch ms> <status> <method> <path> start=<start epoch ms>"; the
//   start times are the ones the rate limit spaces (completions can bunch).
import { createServer } from 'node:http';
import { appendFileSync } from 'node:fs';

const [upstreamArg, portArg = '18091', rateArg = '4', logPath = 'wp-rate-proxy.log'] = process.argv.slice(2);
if (!upstreamArg) {
  console.error('usage: node tools/wp-rate-proxy.ts <https://site> [port] [req/s] [log]');
  process.exit(2);
}
const upstream = new URL(upstreamArg).origin;
const port = Number(portArg);
// 4% headroom: timers can fire a few ms late or early, and "at most RATE in
// any 1000 ms window" must hold for every window, not just on average.
const gap = (1000 / Number(rateArg)) * 1.04;
const self = `http://127.0.0.1:${port}`;
let next = 0;

// slot resolves when the next upstream request may start. Requests queue in
// arrival order; each start is >= gap after the previous actual start.
let queue: Promise<void> = Promise.resolve();
function slot(): Promise<void> {
  const mine = queue.then(async () => {
    const wait = next - Date.now();
    if (wait > 0) await new Promise((ok) => setTimeout(ok, wait));
    while (Date.now() < next) await new Promise((ok) => setTimeout(ok, 1));
    next = Date.now() + gap;
  });
  queue = mine;
  return mine;
}

const rewrite = (s: string) =>
  s.split(upstream).join(self).split(upstream.replaceAll('/', '\\/')).join(self.replaceAll('/', '\\/'));

createServer(async (req, res) => {
  if (req.method !== 'GET' && req.method !== 'HEAD') {
    res.writeHead(405, { Allow: 'GET, HEAD' }).end('read-only proxy');
    return;
  }
  await slot();
  const started = Date.now();
  const target = upstream + (req.url ?? '/');
  const headers: Record<string, string> = { 'User-Agent': String(req.headers['user-agent'] ?? 'GoRun migrator') };
  if (req.headers.accept) headers.Accept = String(req.headers.accept);
  if (req.headers.authorization) headers.Authorization = String(req.headers.authorization);
  try {
    const r = await fetch(target, { method: req.method, headers, redirect: 'manual' });
    appendFileSync(logPath, `${Date.now()} ${r.status} ${req.method} ${req.url} start=${started}\n`);
    const out: Record<string, string> = {};
    r.headers.forEach((v, k) => {
      if (!['content-encoding', 'content-length', 'transfer-encoding', 'connection', 'set-cookie'].includes(k)) out[k] = v;
    });
    if (out.location) out.location = rewrite(out.location);
    const type = r.headers.get('content-type') ?? '';
    if (req.method === 'HEAD') {
      res.writeHead(r.status, out).end();
    } else if (/json|html|xml|rss|text/.test(type)) {
      const body = Buffer.from(rewrite(await r.text()));
      res.writeHead(r.status, { ...out, 'content-length': String(body.length) }).end(body);
    } else {
      const body = Buffer.from(await r.arrayBuffer());
      res.writeHead(r.status, { ...out, 'content-length': String(body.length) }).end(body);
    }
  } catch (err) {
    appendFileSync(logPath, `${Date.now()} ERR ${req.method} ${req.url} ${String(err)}\n`);
    res.writeHead(502).end(String(err));
  }
}).listen(port, '127.0.0.1', () => console.log(`proxy ${self} -> ${upstream} at <= ${rateArg} req/s, log ${logPath}`));
