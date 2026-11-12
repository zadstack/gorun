// Passkeys (WebAuthn) as a second factor and the TOTP enrolment QR code
// (docs/specs/passkeys.md). WebAuthn needs a secure context and a domain RP
// ID, and the suite's shared server runs on http://127.0.0.1 (an IP, which
// can never be an RP ID); a passkey on the shared admin would also change
// every later sign-in. So this spec boots its own GoRun on
// http://localhost:<port> with a fresh data directory, and uses Chromium's
// virtual authenticator (CDP WebAuthn domain) as the passkey.
import { test, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { mkdtempSync, mkdirSync, openSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '../..');
const SHOTS = 'screenshots';
const ADMIN = { name: 'Passkey Admin', email: 'passkeys@e2e.test', password: 'e2e-passkeys-password-01' };

let child: ChildProcess | undefined;
let data = '';
let base = '';

function freePort(): string {
  return execFileSync(process.execPath, [
    '-e',
    "const s=require('net').createServer();s.listen(0,'127.0.0.1',()=>{process.stdout.write(String(s.address().port));s.close()})",
  ]).toString();
}

test.beforeAll(async () => {
  mkdirSync(SHOTS, { recursive: true });
  const port = freePort();
  base = `http://localhost:${port}`;
  data = mkdtempSync(join(tmpdir(), 'gorun-e2e-passkeys-'));
  const env: NodeJS.ProcessEnv = {};
  for (const [k, v] of Object.entries(process.env)) if (!k.startsWith('GORUN_') && !k.startsWith('STRIPE_')) env[k] = v;
  Object.assign(env, {
    GORUN_DATA: data,
    GORUN_ADDONS: '{}',
    GORUN_THEMES: resolve(repo, 'themes'),
    GORUN_PATTERNS: resolve(repo, 'patterns'),
    GORUN_UPDATE_SOURCE_URL: '',
    GORUN_ENV: 'dev',
    GORUN_ADDR: `127.0.0.1:${port}`,
    GORUN_BASE_URL: base,
  });
  const out = openSync(join(data, 'server.log'), 'a');
  child = spawn(resolve(here, '../.bin/gorun'), ['serve'], { cwd: resolve(here, '..'), env, stdio: ['ignore', out, out] });
  const deadline = Date.now() + 30_000;
  for (;;) {
    if (child.exitCode !== null) throw new Error(`gorun exited; see ${data}/server.log`);
    try {
      if ((await fetch(`http://127.0.0.1:${port}/healthz`)).ok) break;
    } catch { /* not up yet */ }
    if (Date.now() > deadline) throw new Error('passkeys server did not start');
    await new Promise((ok) => setTimeout(ok, 200));
  }
});

test.afterAll(async () => {
  child?.kill('SIGTERM');
  if (child && child.exitCode === null) await new Promise((ok) => child!.once('exit', ok));
  if (!process.env.E2E_KEEP_DATA) rmSync(data, { recursive: true, force: true });
});

async function fresh(browser: Browser, locale = 'en-US'): Promise<{ ctx: BrowserContext; page: Page }> {
  const ctx = await browser.newContext({ storageState: { cookies: [], origins: [] }, locale, viewport: { width: 1280, height: 900 } });
  return { ctx, page: await ctx.newPage() };
}

async function signIn(page: Page) {
  await page.goto(`${base}/admin/login`);
  await page.fill('input[name="email"]', ADMIN.email);
  await page.fill('input[name="password"]', ADMIN.password);
  await page.locator('form[action="/admin/login"] button[type="submit"]').click();
}

async function setLanguage(page: Page, locale: 'ar' | 'en') {
  await page.goto(`${base}/admin/account`);
  await page.selectOption('#acc-locale', locale);
  await page.locator('form[action="/admin/account/profile"] button[type="submit"]').click();
  await expect(page.locator('html')).toHaveAttribute('lang', locale);
}

async function noHorizontalScroll(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
}

test.describe.serial('passkeys and the enrolment QR code', () => {
  test('first admin on the localhost instance', async ({ browser }) => {
    const { ctx, page } = await fresh(browser);
    await page.goto(`${base}/admin`);
    await expect(page).toHaveURL(/\/admin\/setup/);
    await page.fill('input[name="name"]', ADMIN.name);
    await page.fill('input[name="email"]', ADMIN.email);
    await page.fill('input[name="password"]', ADMIN.password);
    await page.fill('input[name="title"]', 'Passkeys E2E');
    await page.selectOption('select[name="locale"]', 'en');
    await page.locator('form[action="/admin/setup"] button[type="submit"]').click();
    await expect(page).toHaveURL(/\/admin\/workspace/);
    await ctx.close();
  });

  test('the TOTP enrolment page draws the setup link as a QR code', async ({ browser }) => {
    const { ctx, page } = await fresh(browser);
    await signIn(page);
    await expect(page).not.toHaveURL(/\/admin\/login/);
    await page.goto(`${base}/admin/account/two-factor`);
    await page.locator('form[action="/admin/account/two-factor/start"] button').click();
    const qr = page.locator('svg[data-totp-qr]');
    await expect(qr).toBeVisible();
    await expect(page.locator('[data-totp-secret]')).toBeVisible();
    const uri = await page.locator('[data-totp-uri]').getAttribute('href');
    expect(uri).toMatch(/^otpauth:\/\/totp\//);
    const box = await qr.boundingBox();
    expect(box!.width).toBeGreaterThan(150);
    // Decode the drawn code where the browser has a barcode reader.
    const decoded = await page.evaluate(async () => {
      const B = (window as any).BarcodeDetector;
      if (!B) return null;
      const svg = document.querySelector('svg[data-totp-qr]')!;
      const img = new Image();
      img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(new XMLSerializer().serializeToString(svg));
      await img.decode();
      const c = document.createElement('canvas');
      c.width = c.height = 400;
      c.getContext('2d')!.drawImage(img, 0, 0, 400, 400);
      try {
        const found = await new B({ formats: ['qr_code'] }).detect(c);
        return found[0]?.rawValue ?? '';
      } catch {
        return null;
      }
    });
    if (decoded !== null) expect(decoded).toBe(uri);
    else test.info().annotations.push({ type: 'note', description: 'BarcodeDetector unavailable: QR decoding is covered by the Go test' });
    await page.screenshot({ path: `${SHOTS}/passkeys-qr-en.png`, fullPage: true });
    await setLanguage(page, 'ar');
    await page.goto(`${base}/admin/account/two-factor`);
    await expect(page.locator('svg[data-totp-qr]')).toBeVisible();
    await page.screenshot({ path: `${SHOTS}/passkeys-qr-ar.png`, fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await expect(page.locator('svg[data-totp-qr]')).toBeVisible();
    await noHorizontalScroll(page);
    await page.screenshot({ path: `${SHOTS}/passkeys-qr-ar-390.png`, fullPage: true });
    await setLanguage(page, 'en');
    await ctx.close();
  });

  test('register a passkey on My account, then sign in with password + passkey', async ({ browser }) => {
    const { ctx, page } = await fresh(browser);
    const cdp = await ctx.newCDPSession(page);
    await cdp.send('WebAuthn.enable');
    const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
      options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
    });

    await signIn(page);
    await expect(page).not.toHaveURL(/\/admin\/login/);
    await page.goto(`${base}/admin/account`);
    await page.locator('[data-account-add-passkey]').click();
    await expect(page).toHaveURL(/\/admin\/account\/two-factor\/passkeys/);
    await expect(page.locator('[data-passkeys-empty]')).toBeVisible();
    await page.fill('#passkey-name', 'Virtual key');
    await page.locator('form[data-passkey-register] button[type="submit"]').click();
    // The first second factor comes with ten recovery codes, shown once.
    const codes = page.locator('[data-passkey-codes] code');
    await expect(codes).toHaveCount(10);
    await page.locator('[data-passkey-codes] a.btn-primary').click();
    await expect(page.locator('[data-passkey]')).toHaveCount(1);
    await expect(page.locator('[data-passkey]')).toContainText('Virtual key');
    const { credentials } = await cdp.send('WebAuthn.getCredentials', { authenticatorId });
    expect(credentials).toHaveLength(1);
    await page.locator('#passkeys').screenshot({ path: `${SHOTS}/passkeys-section-en.png` });
    await page.screenshot({ path: `${SHOTS}/passkeys-page-en.png`, fullPage: true });

    await setLanguage(page, 'ar');
    await page.goto(`${base}/admin/account/two-factor/passkeys`);
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await page.screenshot({ path: `${SHOTS}/passkeys-page-ar.png`, fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await noHorizontalScroll(page);
    await page.screenshot({ path: `${SHOTS}/passkeys-page-ar-390.png`, fullPage: true });
    await page.goto(`${base}/admin/account`);
    await noHorizontalScroll(page);
    await page.screenshot({ path: `${SHOTS}/passkeys-account-ar-390.png`, fullPage: true });
    await page.setViewportSize({ width: 1280, height: 900 });
    await setLanguage(page, 'en');

    // Sign out, then password first and the passkey as the second step.
    await page.locator('form[action="/admin/logout"] button').first().click();
    await expect(page).toHaveURL(/\/admin\/login/);
    await signIn(page);
    await expect(page).toHaveURL(/\/admin\/login\/2fa$/);
    const button = page.locator('[data-passkey-login-button]');
    await expect(button).toBeVisible();
    await page.screenshot({ path: `${SHOTS}/passkeys-2fa-step-en.png`, fullPage: true });
    await button.click();
    await expect(page).toHaveURL(/\/admin\/?$/);
    await page.goto(`${base}/admin/account/two-factor/passkeys`);
    await expect(page.locator('[data-passkey]')).not.toContainText('not used yet');
    await ctx.close();
  });
});
