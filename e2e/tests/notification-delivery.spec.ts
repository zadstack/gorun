// Notification delivery (docs/NOTIFICATIONS.md): the per-user preferences
// page in Arabic and English at phone width, the "configure mail first" state
// (the e2e server has no mail provider), the Telegram bot settings (token
// never rendered) and the link flow, finished by a fake Telegram webhook call
// signed with the secret header the site would give Telegram.
import { test, expect, type Page } from '@playwright/test';
import { createDecipheriv, createHmac } from 'node:crypto';
import { mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { csrf, db, env, expectNoServerError, setLocale } from '../lib/helpers.ts';

const SHOTS = 'screenshots';
const BOT_TOKEN = '123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw';
const BOT_USER = 'gorun_e2e_bot';

// webhook derives the path and secret header exactly like
// internal/admin/notify_telegram.go, from the encrypted webhook secret.
function webhook(): { path: string; header: string } {
  const key = Buffer.from(readFileSync(join(env('E2E_DATA'), 'secret.key'), 'utf8').trim(), 'base64');
  const row = db().prepare(`SELECT value_json FROM settings WHERE key = 'notify.telegram.webhook_secret'`).get() as { value_json: string };
  const raw = Buffer.from(JSON.parse(row.value_json), 'base64');
  const d = createDecipheriv('aes-256-gcm', key, raw.subarray(0, 12));
  d.setAAD(Buffer.from('settings:notify.telegram.webhook_secret'));
  d.setAuthTag(raw.subarray(raw.length - 16));
  const secret = Buffer.concat([d.update(raw.subarray(12, raw.length - 16)), d.final()]);
  const derive = (purpose: string) => createHmac('sha256', secret).update(purpose).digest('hex');
  return { path: derive('gorun/telegram/path').slice(0, 40), header: derive('gorun/telegram/header') };
}

async function noHorizontalScroll(page: Page): Promise<void> {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
}

test.describe.serial('notification delivery', () => {
  test.beforeAll(() => mkdirSync(SHOTS, { recursive: true }));

  test('preferences page in Arabic and English shows the mail-not-configured state', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/admin/account');
    await page.locator('[data-account-notifications]').click();
    await expect(page).toHaveURL(/\/admin\/account\/notifications$/);
    await expectNoServerError(page);
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await expect(page.locator('[data-mail-not-configured]')).toBeVisible();
    await expect(page.locator('input[name="email_mode"][type="radio"]').first()).toBeDisabled();
    await expect(page.locator('[data-notify-test-email]')).toBeDisabled();
    await expect(page.locator('[data-notify-categories] input[type="checkbox"]').first()).toBeVisible();
    await noHorizontalScroll(page);
    await page.screenshot({ path: `${SHOTS}/notify-prefs-ar-390.png`, fullPage: true });

    await setLocale(page, 'en');
    await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
    await expect(page.locator('[data-mail-not-configured]')).toContainText('Configure mail first');
    await noHorizontalScroll(page);
    await page.screenshot({ path: `${SHOTS}/notify-prefs-en-390.png`, fullPage: true });
    await setLocale(page, 'ar');
  });

  test('category mutes save', async ({ page }) => {
    await page.goto('/admin/account/notifications');
    const first = page.locator('[data-notify-categories] input[type="checkbox"]').first();
    const name = await first.getAttribute('name');
    await first.uncheck();
    await page.locator('[data-notify-save]').click();
    await expect(page.locator('.admin-flash')).toBeVisible();
    await expect(page.locator(`input[name="${name}"]`)).not.toBeChecked();
    await page.locator(`input[name="${name}"]`).check();
    await page.locator('[data-notify-save]').click();
    await expect(page.locator(`input[name="${name}"]`)).toBeChecked();
  });

  test('Telegram bot settings never render the token', async ({ page }) => {
    await page.goto('/admin/settings/notifications');
    await expectNoServerError(page);
    await page.fill('#tg-token', BOT_TOKEN);
    await page.fill('#tg-user', BOT_USER);
    await page.locator('form[action="/admin/settings/notifications"] button[type="submit"]').click();
    await expect(page.locator('[data-bot-token-set]')).toBeVisible();
    expect(await page.content()).not.toContain(BOT_TOKEN.split(':')[1]);
    await expect(page.locator('#tg-token')).toHaveValue('');
  });

  test('link Telegram through the bot webhook, then unlink', async ({ page, request }) => {
    await page.goto('/admin/account/notifications');
    await page.locator('[data-telegram-link-start]').click();
    const deeplink = await page.locator('[data-telegram-deeplink]').getAttribute('href');
    expect(deeplink).toMatch(new RegExp(`^https://t\\.me/${BOT_USER}\\?start=[A-Za-z0-9_-]{16}$`));
    const code = deeplink!.split('start=')[1];
    await expect(page.locator('[data-telegram-code]')).toHaveText(`/start ${code}`);

    const { path, header } = webhook();
    const update = { update_id: 1, message: { text: `/start ${code}`, chat: { id: 777, type: 'private' }, from: { language_code: 'ar' } } };
    const bad = await request.post(`/webhooks/telegram/${path}`, { data: update, headers: { 'X-Telegram-Bot-Api-Secret-Token': 'wrong' } });
    expect(bad.status()).toBe(401);
    const missing = await request.post(`/webhooks/telegram/${'0'.repeat(40)}`, { data: update, headers: { 'X-Telegram-Bot-Api-Secret-Token': header } });
    expect(missing.status()).toBe(404);
    const ok = await request.post(`/webhooks/telegram/${path}`, { data: update, headers: { 'X-Telegram-Bot-Api-Secret-Token': header } });
    expect(ok.status()).toBe(200);
    expect((await ok.json()).method).toBe('sendMessage');
    const reused = await request.post(`/webhooks/telegram/${path}`, { data: update, headers: { 'X-Telegram-Bot-Api-Secret-Token': header } });
    expect((await reused.json()).text).not.toBe((await ok.json()).text);

    await page.goto('/admin/account/notifications');
    await expect(page.locator('[data-telegram-linked]')).toBeVisible();
    await expect(page.locator('input[name="telegram_enabled"]')).toBeChecked();
    await expect(page.locator('[data-notify-test-telegram]')).toBeEnabled();
    await page.setViewportSize({ width: 390, height: 844 });
    await noHorizontalScroll(page);
    await page.screenshot({ path: `${SHOTS}/notify-telegram-linked-ar-390.png`, fullPage: true });

    const token = await csrf(page);
    const unlink = await page.request.post('/admin/account/notifications/telegram/unlink', { form: { _csrf: token }, maxRedirects: 0 });
    expect(unlink.status()).toBe(303);
    await page.reload();
    await expect(page.locator('[data-telegram-link-start]')).toBeVisible();
  });

  test('the bell panel links to delivery preferences; removing the bot', async ({ page }) => {
    await page.goto('/admin');
    await page.locator('.notify-bell').click();
    await expect(page.locator('[data-notify-delivery-link]')).toBeVisible();
    await page.goto('/admin/settings/notifications');
    const token = await csrf(page);
    const r = await page.request.post('/admin/settings/notifications/remove', { form: { _csrf: token }, maxRedirects: 0 });
    expect(r.status()).toBe(303);
    await page.reload();
    await expect(page.locator('[data-bot-token-unset]')).toBeVisible();
  });
});
