import { test, expect } from '@playwright/test';
import { setLocale } from '../lib/helpers.ts';

test('commerce launch checklist shows webhook URL and settings links', async ({ page }) => {
  for (const locale of ['en', 'ar'] as const) {
    await page.goto('/admin/commerce/setup');
    await setLocale(page, locale);
    await expect(page.locator('[data-setup-step="webhook"] code').first()).toContainText('/webhooks/stripe');
    await expect(page.locator('[data-setup-step="stripe"] a[href="/admin/commerce/settings#stripe"]')).toBeVisible();
    await expect(page.locator('[data-setup-step="server"] a[href="/admin/commerce/servers/new"]')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  }
  await page.goto('/admin/commerce/settings');
  await expect(page.locator('main a[href="/admin/commerce/setup"]').first()).toBeVisible();
  await expect(page.locator('#stripe')).toBeVisible();
  await expect(page.locator('#webhook')).toBeVisible();
});
