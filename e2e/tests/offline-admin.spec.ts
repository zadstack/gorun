import { test, expect } from '@playwright/test';
import { env, db, expectNoServerError, setLocale } from '../lib/helpers.ts';

test('admin editing, menus and language toggle work without external network access', async ({ page }) => {
  const external: string[] = [];
  const errors: string[] = [];
  const origin = new URL(env('E2E_BASE_URL')).origin;
  page.on('pageerror', error => errors.push(error.message));
  await page.route('**/*', async route => {
    if (new URL(route.request().url()).origin === origin) await route.continue();
    else {
      external.push(route.request().url());
      await route.abort('blockedbyclient');
    }
  });
  const response = await page.goto('/admin');
  const policy = response?.headers()['content-security-policy'] ?? '';
  expect(policy).toContain("script-src 'self'");
  expect(policy.split('script-src ')[1].split(';')[0]).not.toMatch(/https?:\/\//);
  await setLocale(page, 'ar');
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  await page.goto('/admin/content/post/new');
  await page.fill('#title', 'E2E offline editor');
  await page.locator('[data-field-key="body"] .ql-editor').click();
  await page.keyboard.type('Editor works with every external request blocked.');
  await page.locator('button[name="intent"][value="save"]').click();
  await expect(page).toHaveURL(/\/admin\/content\/post\/[0-9A-Z]{26}$/);
  await expect(page.locator('.ql-editor')).toContainText('every external request blocked');
  await expectNoServerError(page);
  await page.goto('/admin/menus');
  await page.fill('form[action="/admin/menus"] #name', 'E2E offline menu');
  await page.fill('form[action="/admin/menus"] #slug', 'e2e-offline-menu');
  await page.locator('form[action="/admin/menus"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/admin\/menus\/[0-9A-Z]{26}$/);
  const menuID = new URL(page.url()).pathname.split('/').pop()!;
  for (const label of ['First offline link', 'Second offline link']) {
    await page.fill('#add-label', label);
    await page.fill('#add-url', '/offline-' + label.split(' ')[0].toLowerCase());
    await page.locator("#menu-add [x-show=\"tab === 'custom'\"] button").click();
  }
  await expect(page.locator('.menu-tree-item')).toHaveCount(2);
  await page.locator('.menu-tree-item').nth(1).locator('[data-handle]').press('Alt+ArrowUp');
  await expect(page.locator('.menu-tree-item').first()).toContainText('Second offline link');
  await page.locator('form[action$="/items"] button[type="submit"]').click();
  await page.reload();
  await expect(page.locator('.menu-tree-item').first()).toContainText('Second offline link');
  const database = db();
  const stored = database.prepare('SELECT label,url FROM menu_items WHERE menu_id=? ORDER BY position').all(menuID) as any[];
  expect(stored.map((item: any) => item.label)).toEqual(['Second offline link', 'First offline link']);
  database.close();
  await setLocale(page, 'en');
  await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
  expect(errors).toEqual([]);
  expect(external).toEqual([]);
});
