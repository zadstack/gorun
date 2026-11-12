// Accounts and awareness (roadmap phase 7): TOTP two-factor enrolment and
// sign-in (the spec acts as the authenticator app), recovery codes, per-role
// enforcement, password change on My account, and the notifications bell
// showing a pending agent approval. Other users than the suite's admin are
// used for everything that changes sign-in, so later specs keep working.
import { test, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { createHmac } from 'node:crypto';
import { mkdirSync } from 'node:fs';
import { mcp, toolPayload } from '../lib/helpers.ts';

const PASSWORD = 'e2e-accounts-password-01';
const NEW_PASSWORD = 'e2e-accounts-password-02';
const SHOTS = 'screenshots';

// RFC 6238 TOTP (SHA-1, 6 digits, 30 s) from a base32 setup key.
function base32(key: string): Buffer {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = 0, value = 0;
  const out: number[] = [];
  for (const c of key.replace(/[\s=]/g, '').toUpperCase()) {
    value = (value << 5) | alphabet.indexOf(c);
    bits += 5;
    if (bits >= 8) { out.push((value >>> (bits - 8)) & 255); bits -= 8; }
  }
  return Buffer.from(out);
}
function totp(key: string, at = Date.now()): string {
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(at / 1000 / 30)));
  const h = createHmac('sha1', base32(key)).update(counter).digest();
  const off = h[h.length - 1] & 15;
  return String((h.readUInt32BE(off) & 0x7fffffff) % 1_000_000).padStart(6, '0');
}

async function createUser(page: Page, email: string, role: string, locale: 'ar' | 'en' = 'ar') {
  await page.goto('/admin/users/new');
  await page.fill('#u-name', email.split('@')[0]);
  await page.fill('#u-email', email);
  await page.selectOption('#u-role', role);
  await page.selectOption('#u-locale', locale);
  await page.fill('#u-password', PASSWORD);
  await page.locator('form[action="/admin/users"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/admin\/users(\/|$|\?)/);
}

// signedOut opens a fresh browser context (no saved admin session).
async function signedOut(browser: Browser, locale = 'ar', width = 1280): Promise<{ ctx: BrowserContext; page: Page }> {
  const ctx = await browser.newContext({ storageState: { cookies: [], origins: [] }, locale, viewport: { width, height: 900 } });
  return { ctx, page: await ctx.newPage() };
}

async function signIn(page: Page, email: string, password: string) {
  await page.goto('/admin/login');
  await page.fill('input[name="email"]', email);
  await page.fill('input[name="password"]', password);
  await page.locator('form[action="/admin/login"] button[type="submit"]').click();
}

async function signOut(page: Page) {
  await page.locator('form[action="/admin/logout"] button').first().click();
  await expect(page).toHaveURL(/\/admin\/login/);
}

test.describe.serial('accounts: two-factor, My account, notifications', () => {
  const editor = 'tf-editor@e2e.test';
  const author = 'tf-author@e2e.test';
  const changer = 'tf-password@e2e.test';
  let recovery: string[] = [];
  let key = '';

  test.beforeAll(() => mkdirSync(SHOTS, { recursive: true }));

  test('admin creates the test accounts', async ({ page }) => {
    await createUser(page, editor, 'editor', 'en');
    await createUser(page, author, 'author');
    await createUser(page, changer, 'editor');
  });

  test('enrol two-factor from My account by computing codes from the shown key', async ({ browser }) => {
    const { ctx, page } = await signedOut(browser, 'en-US');
    await signIn(page, editor, PASSWORD);
    await expect(page).toHaveURL(/\/admin\/?$/);
    // The topbar name opens My account.
    await page.locator('[data-account-link]').click();
    await expect(page).toHaveURL(/\/admin\/account$/);
    await expect(page.locator('[data-account-two-factor]')).toBeVisible();
    await page.screenshot({ path: `${SHOTS}/accounts-account-en.png`, fullPage: true });
    await page.locator('[data-account-two-factor] a[href="/admin/account/two-factor"]').click();
    await page.locator('form[action="/admin/account/two-factor/start"] button').click();
    const secret = page.locator('[data-totp-secret]');
    await expect(secret).toBeVisible();
    key = (await secret.textContent())!.trim();
    expect(key.replace(/\s/g, '')).toMatch(/^[A-Z2-7]{32}$/);
    await expect(page.locator('[data-totp-uri]')).toHaveAttribute('href', /^otpauth:\/\/totp\//);
    // The same URI drawn as an inline SVG QR code (passkeys.spec decodes it).
    await expect(page.locator('svg[data-totp-qr]')).toBeVisible();
    await page.screenshot({ path: `${SHOTS}/accounts-enrol-en.png`, fullPage: true });
    await page.fill('#confirm-code', totp(key));
    await page.locator('form[action="/admin/account/two-factor/confirm"] button').click();
    const codes = page.locator('[data-recovery-codes] code');
    await expect(codes).toHaveCount(10);
    recovery = (await codes.allTextContents()).map((c) => c.trim());
    await page.screenshot({ path: `${SHOTS}/accounts-codes-en.png`, fullPage: true });
    expect(recovery[0]).toMatch(/^[0-9a-z]{5}-[0-9a-z]{5}$/);
    await page.goto('/admin/account');
    await expect(page.locator('[data-account-two-factor] .badge-success')).toBeVisible();
    await signOut(page);
    await ctx.close();
  });

  test('sign in with a code, then with a recovery code once', async ({ browser }) => {
    for (const [locale, suffix] of [['en-US', 'en'], ['ar', 'ar']] as const) {
      const { ctx, page } = await signedOut(browser, locale);
      await signIn(page, editor, PASSWORD);
      await expect(page).toHaveURL(/\/admin\/login\/2fa$/);
      await expect(page.locator('html')).toHaveAttribute('dir', suffix === 'ar' ? 'rtl' : 'ltr');
      await page.screenshot({ path: `${SHOTS}/accounts-2fa-step-${suffix}.png`, fullPage: true });
      await ctx.close();
    }
    const { ctx, page } = await signedOut(browser, 'en-US');
    await signIn(page, editor, PASSWORD);
    await page.fill('#code', '000000');
    await page.locator('form[action="/admin/login/2fa"] button').click();
    await expect(page.locator('[role="alert"]')).toBeVisible();
    // The enrolment used the current step; the next step is within the drift window.
    await page.fill('#code', totp(key, Date.now() + 30_000));
    await page.locator('form[action="/admin/login/2fa"] button').click();
    await expect(page).toHaveURL(/\/admin\/?$/);
    await signOut(page);

    await signIn(page, editor, PASSWORD);
    await page.fill('#code', recovery[0]);
    await page.locator('form[action="/admin/login/2fa"] button').click();
    await expect(page).toHaveURL(/\/admin\/?$/);
    await page.goto('/admin/account/two-factor');
    await expect(page.locator('main')).toContainText('9');
    await signOut(page);

    await signIn(page, editor, PASSWORD);
    await page.fill('#code', recovery[0]);
    await page.locator('form[action="/admin/login/2fa"] button').click();
    await expect(page).toHaveURL(/\/admin\/login\/2fa$/);
    await expect(page.locator('[role="alert"]')).toBeVisible();
    await ctx.close();
  });

  test('a role that requires two-factor sends its members to enrolment', async ({ page, browser }) => {
    await page.goto('/admin/security/login-attempts');
    await page.check('input[name="roles"][value="author"]');
    await page.locator('form[action="/admin/security/two-factor"] button').click();
    await expect(page.locator('input[name="roles"][value="author"]')).toBeChecked();

    const { ctx, page: p } = await signedOut(browser);
    await signIn(p, author, PASSWORD);
    await expect(p).toHaveURL(/\/admin\/account\/two-factor$/);
    await expect(p.locator('[data-two-factor-required]')).toBeVisible();
    await p.goto('/admin/content/post');
    await expect(p).toHaveURL(/\/admin\/account\/two-factor$/);
    await p.screenshot({ path: `${SHOTS}/accounts-enforced-ar.png`, fullPage: true });
    await ctx.close();

    // Restore: no role requires two-factor for the rest of the suite.
    await page.goto('/admin/security/login-attempts');
    await page.uncheck('input[name="roles"][value="author"]');
    await page.locator('form[action="/admin/security/two-factor"] button').click();
    await expect(page.locator('input[name="roles"][value="author"]')).not.toBeChecked();
  });

  test('change the password on My account', async ({ browser }) => {
    const { ctx, page } = await signedOut(browser, 'ar', 390);
    await signIn(page, changer, PASSWORD);
    await page.goto('/admin/account');
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    await page.screenshot({ path: `${SHOTS}/accounts-account-ar-390.png`, fullPage: true });
    await page.fill('#acc-current', PASSWORD);
    await page.fill('#acc-new', NEW_PASSWORD);
    await page.fill('#acc-confirm', NEW_PASSWORD);
    await page.locator('form[action="/admin/account/password"] button').click();
    await expect(page).toHaveURL(/\/admin\/account$/);
    await expect(page.locator('.admin-flash')).toBeVisible();
    await signOut(page);
    await signIn(page, changer, PASSWORD);
    await expect(page).toHaveURL(/\/admin\/login/);
    await signIn(page, changer, NEW_PASSWORD);
    await expect(page).toHaveURL(/\/admin\/?$/);
    await ctx.close();
  });

  test('the bell shows a pending agent approval', async ({ page, request }) => {
    // An agent key whose publish call waits for a human approval.
    await page.goto('/admin/agents/keys');
    await page.locator('.audit-key-create > summary').click();
    await page.locator('[data-key-permissions] > summary').click();
    await page.check('input[name="kind"][value="agent"]');
    await page.fill('#k-name', 'E2E accounts agent');
    for (const s of ['content.view', 'content.create', 'content.publish']) await page.check(`input[name="scopes"][value="${s}"]`);
    await page.locator('form[action="/admin/agents/keys"] button[type="submit"]').click();
    const agentKey = await page.locator('#k-plaintext').inputValue();
    const created = toolPayload((await mcp(request, agentKey, 'tools/call', { name: 'create_content', arguments: { type: 'post', title: 'E2E bell draft', body: '<p>x</p>' } })).body.result);
    const pub = toolPayload((await mcp(request, agentKey, 'tools/call', { name: 'publish_content', arguments: { id: created.data.id } })).body.result);
    const approvalID = (pub.data ?? pub).approval_id;
    expect(approvalID).toBeTruthy();

    for (const [locale, suffix] of [['en', 'en'], ['ar', 'ar']] as const) {
      await page.goto('/admin');
      const token = await page.locator('input[name="_csrf"]').first().getAttribute('value');
      await page.request.post('/admin/locale', { form: { locale, _csrf: token! }, maxRedirects: 0 });
      await page.goto('/admin');
      await expect(page.locator('#notify-count .notify-count')).toBeVisible();
      await page.locator('[data-notifications] .notify-bell').click();
      const item = page.locator(`#notify-panel [data-notify-item="mcp.approval:${approvalID}"]`);
      await expect(item).toBeVisible();
      await page.screenshot({ path: `${SHOTS}/accounts-bell-${suffix}.png` });
      await page.keyboard.press('Escape');
      await expect(page.locator('#notify-panel')).toBeHidden();
    }
    // Phone width: the panel stays inside the viewport.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/admin');
    await page.locator('[data-notifications] .notify-bell').click();
    await expect(page.locator(`#notify-panel [data-notify-item="mcp.approval:${approvalID}"]`)).toBeVisible();
    const box = await page.locator('#notify-panel').boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(390);
    await page.screenshot({ path: `${SHOTS}/accounts-bell-ar-390.png` });
    // Opening the item marks it read and lands on the approvals page.
    await page.locator(`#notify-panel [data-notify-item="mcp.approval:${approvalID}"] a`).click();
    await expect(page).toHaveURL(/\/admin\/agents\/approvals/);
    await page.setViewportSize({ width: 1280, height: 900 });

    // Clean up: deny the parked call.
    const deny = page.locator(`form[action="/admin/agents/approvals/${approvalID}/deny"]`);
    await deny.locator('button[type="submit"]').click();
    const modal = page.locator('[role="dialog"][aria-labelledby="gp-confirm-title"]');
    await expect(modal).toBeVisible();
    await modal.locator('button[\\@click="$store.confirm.yes()"]').click();
    await expect(deny).toHaveCount(0);
  });

  test('admin removes the test accounts', async ({ page }) => {
    for (const email of [editor, author, changer]) {
      await page.goto(`/admin/users?q=${encodeURIComponent(email)}`);
      await page.locator('#users-results tbody a[href^="/admin/users/"]').first().click();
      await page.locator('form[action$="/delete"] button[type="submit"]').click();
      const modal = page.locator('[role="dialog"][aria-labelledby="gp-confirm-title"]');
      await modal.locator('button[\\@click="$store.confirm.yes()"]').click();
      await expect(page).toHaveURL(/\/admin\/users(\?|$)/);
    }
    await page.goto('/admin/users');
    for (const email of [editor, author, changer]) await expect(page.locator('main')).not.toContainText(email);
  });
});
