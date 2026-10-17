// A fake mcp-billing/v1 server (port of internal/commerce/fakebilling) for
// the commerce spec. It verifies the HMAC signature exactly as
// docs/COMMERCE.md "Signing" describes, replays Idempotency-Key answers and
// keeps accounts in memory. GET /__calls (unsigned, test-only) returns the
// recorded calls and accounts so a spec can assert provisioning happened.
//
// Run standalone: node fixtures/fake-billing-server.ts [port]
import { createHash, createHmac, timingSafeEqual } from 'node:crypto';
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const PREFIX = '/mcp-billing/v1';
const MAX_SKEW_S = 5 * 60;

export interface Plan {
  code: string;
  name: string;
  name_ar?: string;
}

export interface Account {
  external_id: string;
  plan_code: string;
  status: string;
  period_end: string;
  orders: string[];
}

export interface Call {
  method: string;
  path: string;
  idempotencyKey: string;
  body: Record<string, unknown> | null;
  rawBody: string;
  replayed: boolean;
  failed: number;
  rejected?: string;
}

export interface FakeBilling {
  url: string;
  clientId: string;
  secret: string;
  calls: Call[];
  accounts: Map<string, Account>;
  close(): Promise<void>;
}

// signMCBB returns hex(hmac_sha256(secret, time\nnonce\nMETHOD\ntarget\nhex(sha256(body)))).
export function signMCBB(secret: string, ts: string, nonce: string, method: string, target: string, body: string | Buffer): string {
  const sum = createHash('sha256').update(body).digest('hex');
  return createHmac('sha256', secret).update([ts, nonce, method, target, sum].join('\n')).digest('hex');
}

function json(res: ServerResponse, status: number, v: unknown): string {
  const b = JSON.stringify(v);
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(b);
  return b;
}

const ok = (res: ServerResponse, data: unknown) => ({ status: 200, body: json(res, 200, { ok: true, data }) });
const fail = (res: ServerResponse, status: number, message: string) => ({ status, body: json(res, status, { ok: false, error: { message } }) });

function readBody(req: IncomingMessage): Promise<Buffer> {
  return new Promise((ok, ko) => {
    const parts: Buffer[] = [];
    req.on('data', (c: Buffer) => parts.push(c));
    req.on('end', () => ok(Buffer.concat(parts)));
    req.on('error', ko);
  });
}

export async function startFakeBilling(opts: { clientId?: string; secret?: string; port?: number; plans?: Plan[] } = {}): Promise<FakeBilling> {
  const clientId = opts.clientId ?? 'e2e-client';
  const secret = opts.secret ?? 'e2e-billing-secret';
  const plans: Plan[] = opts.plans ?? [
    { code: 'pro', name: 'Pro', name_ar: 'احترافي' },
    { code: 'team', name: 'Team', name_ar: 'فريق' },
  ];
  const accounts = new Map<string, Account>();
  const idem = new Map<string, { status: number; body: string }>();
  const calls: Call[] = [];

  const verify = (req: IncomingMessage, path: string, body: Buffer): string => {
    if (req.headers['x-mcbb-client'] !== clientId) return 'unknown client';
    const tsRaw = String(req.headers['x-mcbb-timestamp'] ?? '');
    const ts = Number.parseInt(tsRaw, 10);
    if (!Number.isFinite(ts) || Math.abs(Date.now() / 1000 - ts) > MAX_SKEW_S) return 'stale or missing timestamp';
    const nonce = String(req.headers['x-mcbb-nonce'] ?? '');
    if (nonce.length !== 32) return 'bad nonce';
    if (req.method === 'GET' && body.length !== 0) return 'GET must not carry a body';
    const want = Buffer.from(signMCBB(secret, tsRaw, nonce, req.method ?? '', path, body));
    const got = Buffer.from(String(req.headers['x-mcbb-signature'] ?? ''));
    if (want.length !== got.length || !timingSafeEqual(want, got)) return 'bad signature';
    return '';
  };

  const route = (res: ServerResponse, method: string, path: string, body: Record<string, unknown> | null, nCalls: number) => {
    const parts = path.replace(/^\/+|\/+$/g, '').split('/');
    if (method === 'GET' && path === '/catalog') {
      return ok(res, {
        product: { slug: 'fake-mcp', name: 'Fake MCP', name_ar: 'خادم تجريبي' },
        plans,
        tools: [{ name: 'search', description: 'Search' }],
      });
    }
    if (method === 'POST' && path === '/accounts') {
      const b = body ?? {};
      const id = String(b.external_customer_id ?? '');
      const plan = String(b.plan_code ?? '');
      const end = String(b.period_end ?? '');
      if (!id || !plan || !end) return fail(res, 422, 'external_customer_id, plan_code and period_end are required');
      if (!plans.some((p) => p.code === plan)) return fail(res, 422, `unknown plan "${plan}"`);
      const a = accounts.get(id) ?? { external_id: id, plan_code: plan, status: 'active', period_end: '', orders: [] };
      a.plan_code = plan;
      a.status = 'active';
      if (end > a.period_end) a.period_end = end;
      if (b.order_ref) a.orders.push(String(b.order_ref));
      accounts.set(id, a);
      return ok(res, a);
    }
    if (parts[0] === 'accounts' && parts.length >= 2) {
      const a = accounts.get(parts[1]);
      if (!a) return fail(res, 404, 'account not found');
      if (parts.length === 2 && method === 'GET') return ok(res, a);
      if (parts.length === 3 && method === 'POST') {
        switch (parts[2]) {
          case 'suspend':
            a.status = 'suspended';
            return ok(res, a);
          case 'resume':
            a.status = 'active';
            return ok(res, a);
          case 'login-link':
            if (a.status !== 'active') return fail(res, 409, 'account is not active');
            return ok(res, {
              url: `https://billing.example.test/login/${a.external_id}?t=${nCalls}`,
              expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
            });
        }
      }
    }
    return fail(res, 404, 'not found');
  };

  const handler = async (req: IncomingMessage, res: ServerResponse): Promise<void> => {
    const u = new URL(req.url ?? '/', 'http://x');
    const raw = await readBody(req);
    if (u.pathname === '/__calls' && req.method === 'GET') {
      json(res, 200, { calls, accounts: Object.fromEntries(accounts) });
      return;
    }
    if (!u.pathname.startsWith(PREFIX + '/')) {
      fail(res, 404, 'not found');
      return;
    }
    const path = u.pathname.slice(PREFIX.length);
    const method = req.method ?? 'GET';
    const key = String(req.headers['idempotency-key'] ?? '');
    const call: Call = { method, path, idempotencyKey: key, body: null, rawBody: raw.toString('utf8'), replayed: false, failed: 0 };
    const why = verify(req, u.pathname, raw);
    if (why) {
      call.rejected = why;
      call.failed = 401;
      calls.push(call);
      fail(res, 401, why);
      return;
    }
    if (raw.length) {
      try {
        call.body = JSON.parse(call.rawBody);
      } catch {
        call.failed = 400;
        calls.push(call);
        fail(res, 400, 'body is not a JSON object');
        return;
      }
    }
    if (method !== 'GET') {
      if (!key) {
        call.failed = 400;
        calls.push(call);
        fail(res, 400, 'Idempotency-Key required');
        return;
      }
      const rec = idem.get(`${method} ${path} ${key}`);
      if (rec) {
        call.replayed = true;
        calls.push(call);
        res.writeHead(rec.status, { 'Content-Type': 'application/json', 'Idempotent-Replayed': 'true' });
        res.end(rec.body);
        return;
      }
    }
    calls.push(call);
    const out = route(res, method, path, call.body, calls.length);
    if (method !== 'GET' && out.status < 500) idem.set(`${method} ${path} ${key}`, out);
  };

  const server: Server = createServer((req, res) => {
    handler(req, res).catch((err) => {
      res.writeHead(500).end(String(err));
    });
  });
  await new Promise<void>((ok) => server.listen(opts.port ?? 0, '127.0.0.1', ok));
  const addr = server.address();
  if (!addr || typeof addr === 'string') throw new Error('fake billing: no address');
  return {
    url: `http://127.0.0.1:${addr.port}`,
    clientId,
    secret,
    calls,
    accounts,
    close: () => new Promise<void>((ok) => server.close(() => ok())),
  };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const fb = await startFakeBilling({ port: Number(process.argv[2] ?? 0) });
  console.log(`fake billing at ${fb.url} (client ${fb.clientId}, secret ${fb.secret})`);
}
