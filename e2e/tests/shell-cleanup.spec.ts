// Shell clean-up (roadmap phase 4): one owner per destination, the rail holds
// agents only, row actions behind one menu and danger zones at the bottom.
import { test, expect, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { ADMIN, db, setLocale } from '../lib/helpers.ts';

async function createKey(page: Page, name: string): Promise<string> {
  await page.goto('/admin/agents/keys');
  await page.locator('#key-create > summary').click();
  await page.check('input[name="kind"][value="api"]');
  await page.fill('#k-name', name);
  await page.locator('[data-key-permissions] > summary').click();
  await page.check('input[name="scopes"][value="content.view"]');
  await page.locator('form[action="/admin/agents/keys"] button[type="submit"]').click();
  await expect(page.locator('#k-plaintext')).toHaveCount(1);
  const database = db();
  const id = (database.prepare("SELECT id FROM api_keys WHERE name=? AND coalesce(revoked_at,'')='' ORDER BY created_at DESC").get(name) as any).id as string;
  database.close();
  await page.goto('/admin/agents/keys');
  return id;
}

test('every destination has one owner and the rail holds agents only', async ({ page }, testInfo) => {
  for (const locale of ['en', 'ar'] as const) {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/admin/agents/keys');
    await setLocale(page, locale);
    await expect(page.locator('.workspace-tools-rail [data-dock-open]')).toBeVisible();
    await expect(page.locator('.workspace-tools-rail a[data-workspace-tool], .workspace-rail-tools, .workspace-rail-footer')).toHaveCount(0);
    await expect(page.locator('.workspace-top-tools a')).toHaveCount(1);
    await expect(page.locator('.workspace-top-tools a')).toHaveAttribute('href', '/');
    await expect(page.locator('.workspace-sidebar-tools, [data-workspace-mobile-tools]')).toHaveCount(0);
    // Analytics and themes are reached through their Website tabs.
    await page.goto('/admin/website');
    await page.locator('[data-navigation-category="site-analytics"]').click();
    await expect(page.locator('.admin-section-pages a[href="/admin/analytics"]')).toBeVisible();
    const shell = '.admin-topbar, #admin-sidebar [data-navigation-sections], .workspace-tools-rail';
    for (const href of ['/admin/analytics', '/admin/appearance/themes', '/admin/system/backups', '/admin/system/health', '/admin/media']) {
      await expect(page.locator(`:is(${shell}) a[href="${href}"]`)).toHaveCount(0);
    }
    await page.goto('/admin/appearance/themes');
    await expect(page.locator('.admin-section-pages a[href="/admin/appearance/themes"]')).toHaveAttribute('aria-current', 'page');
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/admin/agents/keys');
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
    await expect(page.locator('[data-dock-open]')).toBeVisible();
    await expect(page.locator('.workspace-top-tools a')).toHaveAccessibleName(locale === 'en' ? 'View site' : 'عرض الموقع');
    await page.screenshot({ path: testInfo.outputPath(`shell-${locale}-mobile.png`) });
  }
});

test('API key row actions open as a keyboard menu and keep the rotate field', async ({ page, browser }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await setLocale(page, 'en');
  const id = await createKey(page, 'Menu journey key');
  const row = page.locator(`tr[data-key="${id}"]`);
  const button = row.locator('[data-row-menu-button]'), menu = page.locator(`#key-menu-${id}`);
  await expect(button).toHaveAttribute('aria-haspopup', 'menu');
  await expect(button).toHaveAttribute('aria-expanded', 'false');
  await expect(menu).toBeHidden();
  await button.focus();
  await page.keyboard.press('Enter');
  await expect(menu).toBeVisible();
  await expect(menu).toHaveAttribute('role', 'menu');
  await expect(button).toHaveAttribute('aria-expanded', 'true');
  const items = menu.getByRole('menuitem');
  await expect(items).toHaveCount(4);
  await expect(items.first()).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(items.nth(1)).toBeFocused();
  await page.keyboard.press('End');
  await expect(items.last()).toBeFocused();
  await expect(items.last()).toHaveText('Revoke');
  await page.keyboard.press('ArrowDown');
  await expect(items.first()).toBeFocused();
  await page.screenshot({ path: testInfo.outputPath('row-menu-en.png') });
  await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();
  await expect(button).toBeFocused();
  await expect(button).toHaveAttribute('aria-expanded', 'false');
  await page.keyboard.press('ArrowDown');
  await expect(menu).toBeVisible();
  await page.mouse.click(5, 880);
  await expect(menu).toBeHidden();

  // A menu choice submits the unchanged rotate form with its grace value.
  await button.click();
  await menu.getByRole('menuitem', { name: 'Rotate, old key works 1 hour' }).click();
  await page.locator('[role="dialog"][aria-labelledby="gp-confirm-title"] button[\\@click="$store.confirm.yes()"]').click();
  await expect(page.locator('#k-plaintext')).toHaveCount(1);
  const database = db();
  const old = database.prepare('SELECT revoked_at, expires_at FROM api_keys WHERE id=?').get(id) as any;
  database.close();
  expect(old.revoked_at ?? '').toBe('');
  expect(old.expires_at).toBeTruthy();

  // Arabic phone: the menu stays inside the viewport without horizontal scroll.
  await page.goto('/admin/agents/keys');
  await setLocale(page, 'ar');
  await page.setViewportSize({ width: 390, height: 844 });
  const first = page.locator('[data-row-menu-button]').first();
  await first.scrollIntoViewIfNeeded();
  await first.click();
  const open = page.locator('.row-menu-list:not([hidden])');
  await expect(open).toBeVisible();
  const box = (await open.boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(390);
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  await page.screenshot({ path: testInfo.outputPath('row-menu-ar-mobile.png') });

  // Without JavaScript the actions render inline as ordinary forms.
  const context = await browser.newContext({ javaScriptEnabled: false, storageState: '.auth/admin.json' });
  const plain = await context.newPage();
  await plain.goto(new URL('/admin/agents/keys', page.url()).href);
  const plainRow = plain.locator('tr[data-key]').first();
  await expect(plainRow.locator('[data-row-menu-button]')).toBeHidden();
  await expect(plainRow.locator('form[action$="/revoke"] button')).toBeVisible();
  await context.close();
});

test('destructive actions sit in a danger zone at the bottom of the page', async ({ page }, testInfo) => {
  const database = db(), id = randomUUID(), now = new Date().toISOString();
  const owner = database.prepare('SELECT password_hash FROM users WHERE email=?').get(ADMIN.email) as any;
  database.prepare("INSERT INTO users(id,email,name,password_hash,role,locale,created_at,updated_at) VALUES(?,?,?,?,'editor','en',?,?)").run(id, `danger-${id}@e2e.test`, 'Danger zone editor', owner.password_hash, now, now);
  try {
  await setLocale(page, 'en');
  await page.goto(`/admin/users/${id}`);
  const zone = page.locator('.danger-zone');
  await expect(zone).toBeVisible();
  await expect(zone.getByRole('heading', { name: 'Danger zone' })).toBeVisible();
  await expect(zone.locator('form[action$="/delete"] button.btn-danger')).toBeVisible();
  // Nothing in the main column comes after it.
  expect(await zone.evaluate(el => el.nextElementSibling === null)).toBe(true);
  await page.setViewportSize({ width: 390, height: 844 });
  await setLocale(page, 'ar');
  await expect(zone.getByRole('heading', { name: 'منطقة الخطر' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  await zone.scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath('danger-zone-ar-mobile.png') });
  } finally {
    database.prepare('DELETE FROM users WHERE id=?').run(id);
    database.close();
  }
});
