// Boots the world for one Playwright run:
//   1. builds the GoRun binary (CGO_ENABLED=0) into e2e/.bin/gorun;
//   2. starts the WordPress fixture server and the fake billing server;
//   3. spawns `gorun serve` on E2E_PORT with a temp GORUN_DATA, logging to
//      <data>/server.log, and waits for /healthz.
// Everything the specs need is exported through process.env (inherited by the
// workers). The returned function is the teardown.
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { mkdtempSync, mkdirSync, openSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startWPFixture } from './fixtures/wp-fixture-server.ts';
import { startFakeBilling } from './fixtures/fake-billing-server.ts';

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '..');

async function waitHealthy(url: string, child: ChildProcess, logPath: string): Promise<void> {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`gorun exited with ${child.exitCode}; see ${logPath}`);
    try {
      const r = await fetch(url);
      if (r.ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((ok) => setTimeout(ok, 200));
  }
  throw new Error(`gorun did not become healthy at ${url}; see ${logPath}`);
}

export default async function globalSetup(): Promise<() => Promise<void>> {
  const port = process.env.E2E_PORT;
  if (!port) throw new Error('E2E_PORT not set (playwright.config.ts sets it)');
  const base = `http://127.0.0.1:${port}`;

  const bin = resolve(here, '.bin/gorun');
  if (!process.env.E2E_SKIP_BUILD) {
    mkdirSync(dirname(bin), { recursive: true });
    execFileSync('go', ['build', '-o', bin, '../cmd/gorun'], {
      cwd: here,
      env: { ...process.env, CGO_ENABLED: '0' },
      stdio: 'inherit',
    });
  }

  const data = process.env.E2E_DATA_DIR ?? mkdtempSync(join(tmpdir(), 'gorun-e2e-'));
  mkdirSync(data, { recursive: true });
  const logPath = join(data, 'server.log');

  const wp = await startWPFixture();
  const billing = await startFakeBilling();
  // A local constant, never the shell's STRIPE_WEBHOOK_SECRET (every STRIPE_* is stripped below).
  const webhookSecret = 'whsec_e2e_local_webhook_secret_0001';

  // Explicit server env: the developer's own GORUN_* / STRIPE_* (a prod
  // base URL, secret key, setup token, live Stripe key…) never leak into the
  // test server. A Stripe key passes only when it is a test key.
  const inherited: NodeJS.ProcessEnv = {};
  for (const [k, v] of Object.entries(process.env)) {
    if (!k.startsWith('GORUN_') && !k.startsWith('STRIPE_')) inherited[k] = v;
  }
  const stripeKey = process.env.STRIPE_SECRET_KEY ?? '';
  const env: NodeJS.ProcessEnv = {
    ...inherited,
    ...(stripeKey.startsWith('sk_test_') ? { STRIPE_SECRET_KEY: stripeKey } : {}),
    GORUN_DATA: data,
    // Explicit test-only fixture; never inherit a developer site activation config.
    GORUN_ADDONS: process.env.E2E_ADDONS ?? "{}",
    GORUN_THEMES: resolve(repo, 'themes'),
    GORUN_PATTERNS: resolve(repo, 'patterns'),
    // Extra theme roots (GORUN_THEMES_EXTRA) only when a downstream suite asks.
    ...(process.env.E2E_THEMES_EXTRA ? { GORUN_THEMES_EXTRA: process.env.E2E_THEMES_EXTRA } : {}),
    GORUN_ENV: 'dev',
    GORUN_ADDR: `127.0.0.1:${port}`,
    GORUN_BASE_URL: base,
    // fixture WordPress + fake billing server live on 127.0.0.1 over http
    GORUN_ALLOW_PRIVATE_NET: '1',
    STRIPE_WEBHOOK_SECRET: webhookSecret,
  };
  const out = openSync(logPath, 'a');
  const child = spawn(bin, ['serve'], { cwd: here, env, stdio: ['ignore', out, out] });
  await waitHealthy(`${base}/healthz`, child, logPath);

  Object.assign(process.env, {
    E2E_BASE_URL: base,
    E2E_DATA: data,
    E2E_LOG: logPath,
    E2E_DB: join(data, 'gorun.db'),
    E2E_WP_URL: wp.url,
    E2E_BILLING_URL: billing.url,
    E2E_BILLING_CLIENT: billing.clientId,
    E2E_BILLING_SECRET: billing.secret,
    E2E_WEBHOOK_SECRET: webhookSecret,
  });
  console.log(`[e2e] gorun ${base} data=${data}\n[e2e] wp fixture ${wp.url} · fake billing ${billing.url}`);

  return async () => {
    child.kill('SIGTERM');
    await new Promise((ok) => {
      const t = setTimeout(() => {
        child.kill('SIGKILL');
        ok(null);
      }, 10_000);
      child.once('exit', () => {
        clearTimeout(t);
        ok(null);
      });
    });
    await wp.close();
    await billing.close();
    if (!process.env.E2E_KEEP_DATA && !process.env.E2E_DATA_DIR) rmSync(data, { recursive: true, force: true });
    else console.log(`[e2e] data kept at ${data}`);
  };
}
