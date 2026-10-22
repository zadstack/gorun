// Stripe test/live mode in Store settings: the current-mode badge, the three
// separate states per mode (configured / authenticated / webhook delivery),
// the confirmation and server-side checks before live mode, and the
// per-order mode chip. Runs after commerce.spec.ts (orders exist; it leaves
// the store in test mode) and before subscription.spec.ts. This store boots
// pinned to live (STRIPE_WEBHOOK_SECRET is in its environment); the spec
// pins live again at its start and end through the database, because a
// genuine switch to live needs a live key that Stripe accepts.
import { test, expect, type Page } from '@playwright/test';
import { db, expectNoServerError, setLocale } from '../lib/helpers.ts';

const SETTINGS = 'form[action="/admin/commerce/settings"]';

async function noOverflow(page: Page, label: string): Promise<void> {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1), `${label} overflows`).toBe(true);
}

async function shoot(page: Page, name: string): Promise<void> {
  for (const [width, height] of [[1280, 900], [375, 812]] as const) {
    await page.setViewportSize({ width, height });
    await page.reload();
    await expectNoServerError(page);
    await noOverflow(page, `${name} @${width}`);
    await page.screenshot({ path: `screenshots/${name}-${width}.png`, fullPage: true });
  }
  await page.setViewportSize({ width: 1280, height: 900 });
}

const pinLive = () => db().prepare("UPDATE settings SET value_json = '\"live\"' WHERE key = 'commerce.stripe_mode'").run();

test.describe.serial('Stripe mode', () => {
  // Fixture (see the header): the store is pinned to live before and after.
  test.beforeAll(pinLive);
  test.afterAll(pinLive);

  test('settings show the current mode badge and three states per mode (en + ar, 1280 + 375)', async ({ page }) => {
    await page.goto('/admin/commerce/settings');
    for (const locale of ['en', 'ar'] as const) {
      await setLocale(page, locale);
      await expect(page.locator('[data-stripe-mode="live"]').first()).toBeVisible();
      await expect(page.locator('#c-mode')).toHaveValue('live');
      await expect(page.locator('input[name="confirm_live"]')).toBeVisible();
      // Both modes are reported with the three separate states.
      for (const mode of ['test', 'live'] as const) {
        const card = page.locator(`[data-mode-status="${mode}"]`);
        await expect(card.locator('[data-stripe-configured]')).toBeVisible();
        await expect(card.locator('[data-stripe-auth="unverified"]')).toBeVisible();
        await expect(card.locator('[data-stripe-delivery]')).toBeVisible();
        await expect(card.locator('code')).toContainText(`/webhooks/stripe/${mode}`);
      }
      // The test slot has no key yet: nothing to verify, no call possible.
      await expect(page.locator('[data-mode-status="test"] form[action="/admin/commerce/settings/verify"] button')).toBeDisabled();
      await shoot(page, `stripe-mode-settings-${locale}`);
    }
  });

  test('a key whose prefix contradicts its slot is refused on any save and nothing is stored', async ({ page }) => {
    await page.goto('/admin/commerce/settings');
    await setLocale(page, 'en');
    // live key into the test slot, together with a switch to test
    await page.selectOption('#c-mode', 'test');
    await page.fill('#c-test-key', 'sk_live_e2e_wrong_slot');
    let posted = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/admin/commerce/settings'));
    await page.locator(`${SETTINGS} button[type="submit"]`).click();
    expect((await posted).status()).toBe(422);
    await expect(page.locator('#c-test-key')).toHaveAttribute('aria-invalid', 'true');
    await expect(page.locator('[data-stripe-mode="live"]').first()).toBeVisible(); // badge: still the stored mode
    await expect(page.locator('#c-mode')).toHaveValue('test'); // select: what was submitted
    expect(db().prepare("SELECT value_json AS v FROM settings WHERE key = 'commerce.stripe_mode'").get()!.v).toBe('"live"');
    expect(db().prepare("SELECT count(*) AS n FROM settings WHERE key = 'commerce.stripe_test_secret_key' AND value_json <> '\"\"'").get()!.n).toBe(0);
    await page.screenshot({ path: 'screenshots/stripe-mode-refused-test-slot-1280.png', fullPage: true });
    // test key into the live slot, no mode change at all
    await page.goto('/admin/commerce/settings');
    await page.fill('#c-stripe', 'sk_test_e2e_wrong_slot');
    posted = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/admin/commerce/settings'));
    await page.locator(`${SETTINGS} button[type="submit"]`).click();
    expect((await posted).status()).toBe(422);
    await expect(page.locator('#c-stripe')).toHaveAttribute('aria-invalid', 'true');
    expect(db().prepare("SELECT count(*) AS n FROM settings WHERE key = 'commerce.stripe_secret_key' AND value_json <> '\"\"'").get()!.n).toBe(0);
  });

  test('switching to test needs no confirmation; back to live needs confirmation and a usable live key', async ({ page }) => {
    await page.goto('/admin/commerce/settings');
    await setLocale(page, 'en');
    await page.selectOption('#c-mode', 'test');
    await page.fill('#c-test-key', 'sk_test_e2e_mode_switch');
    await page.fill('#c-test-webhook', 'whsec_e2e_test_mode_secret');
    let posted = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/admin/commerce/settings'));
    await page.locator(`${SETTINGS} button[type="submit"]`).click();
    expect((await posted).status()).toBe(303);
    await expect(page.locator('[role="status"]').first()).toBeVisible();
    await expect(page.locator('[data-stripe-mode="test"]').first()).toBeVisible();
    await expect(page.locator('#c-mode')).toHaveValue('test');
    expect(db().prepare("SELECT value_json AS v FROM settings WHERE key = 'commerce.stripe_mode'").get()!.v).toBe('"test"');
    await expect(page.locator('[data-mode-status="test"] [data-stripe-configured="yes"]')).toBeVisible();
    await expect(page.locator('[data-mode-status="test"] [data-stripe-delivery="unverified"]')).toBeVisible();

    // The local test event is processed but is not delivery evidence.
    await page.locator('form[action="/admin/commerce/settings/test-event"] button[type="submit"]').click();
    await expect(page.locator('[role="status"]').first()).toBeVisible();
    await expect(page.locator('[data-mode-status="test"] [data-stripe-delivery="unverified"]')).toBeVisible();
    expect(db().prepare("SELECT count(*) AS n FROM stripe_events WHERE mode = 'test' AND source = 'local'").get()!.n).toBeGreaterThan(0);

    for (const locale of ['ar', 'en'] as const) {
      await setLocale(page, locale);
      await shoot(page, `stripe-mode-test-active-${locale}`);
    }

    // Back to live without the confirmation: refused, still test.
    await setLocale(page, 'en');
    await page.selectOption('#c-mode', 'live');
    posted = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/admin/commerce/settings'));
    await page.locator(`${SETTINGS} button[type="submit"]`).click();
    expect((await posted).status()).toBe(422);
    await expect(page.locator('#mode .field-error')).toBeVisible();
    await expect(page.locator('[data-stripe-mode="test"]').first()).toBeVisible(); // badge keeps the stored mode
    await expect(page.locator('[data-stripe-mode="live"]')).toHaveCount(0);
    await expect(page.locator('#c-mode')).toHaveValue('live'); // the select keeps the refused choice
    await page.screenshot({ path: 'screenshots/stripe-mode-refused-no-confirm-1280.png', fullPage: true });
    expect(db().prepare("SELECT value_json AS v FROM settings WHERE key = 'commerce.stripe_mode'").get()!.v).toBe('"test"');

    // Confirmed, but this environment has no live key: refused on the server, no Stripe call possible.
    await page.goto('/admin/commerce/settings');
    await page.selectOption('#c-mode', 'live');
    await page.check('input[name="confirm_live"]');
    posted = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/admin/commerce/settings'));
    await page.locator(`${SETTINGS} button[type="submit"]`).click();
    expect((await posted).status()).toBe(422);
    await expect(page.locator('#mode .field-error')).toBeVisible();
    expect(db().prepare("SELECT value_json AS v FROM settings WHERE key = 'commerce.stripe_mode'").get()!.v).toBe('"test"');
    await setLocale(page, 'ar');
    await page.selectOption('#c-mode', 'live');
    await page.check('input[name="confirm_live"]');
    await page.locator(`${SETTINGS} button[type="submit"]`).click();
    await expect(page.locator('#mode .field-error')).toBeVisible();
    await page.setViewportSize({ width: 375, height: 812 });
    await noOverflow(page, 'refused live switch ar @375');
    await page.screenshot({ path: 'screenshots/stripe-mode-refused-live-ar-375.png', fullPage: true });
    await page.setViewportSize({ width: 1280, height: 900 });
  });

  test('orders carry their own mode; the current mode never relabels them', async ({ page }) => {
    // Orders from commerce.spec.ts were created while the store was live;
    // one row without provenance stands in for a pre-migration order.
    const before = db().prepare("SELECT id, stripe_mode FROM orders ORDER BY created_at DESC").all() as { id: string; stripe_mode: string }[];
    expect(before.length).toBeGreaterThan(0);
    // commerce.spec.ts checks out with a dummy key in the test slot (or the
    // environment's sk_test_ key in the live slot): every Stripe order
    // carries the mode that created it; manual requests keep ''.
    const stamped = before.filter(o => o.stripe_mode === 'test' || o.stripe_mode === 'live');
    expect(stamped.length).toBeGreaterThan(0);
    const last = before[before.length - 1];
    const historic = last.id !== stamped[0].id ? last : null;
    if (historic) db().prepare("UPDATE orders SET stripe_mode = '' WHERE id = ?").run(historic.id);
    // One order is flipped to the other mode in the database to show the
    // chip reads the row, never the store's current (test) mode.
    const other = stamped[0].stripe_mode === 'test' ? 'live' : 'test';
    db().prepare('UPDATE orders SET stripe_mode = ? WHERE id = ?').run(other, stamped[0].id);

    await page.goto('/admin/commerce/orders');
    for (const locale of ['en', 'ar'] as const) {
      await setLocale(page, locale);
      await expect(page.locator(`[data-order-mode="${other}"]`).first()).toBeVisible();
      if (historic) await expect(page.locator('[data-order-mode=""]').first()).toBeVisible();
      await shoot(page, `stripe-mode-orders-${locale}`);
    }
    await page.goto(`/admin/commerce/orders/${stamped[0].id}`);
    await expect(page.locator(`[data-order-mode="${other}"]`)).toBeVisible();
    db().prepare('UPDATE orders SET stripe_mode = ? WHERE id = ?').run(stamped[0].stripe_mode, stamped[0].id);
    if (historic) db().prepare('UPDATE orders SET stripe_mode = ? WHERE id = ?').run(historic.stripe_mode, historic.id);
  });
});
