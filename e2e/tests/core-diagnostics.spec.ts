import { test, expect } from '@playwright/test';
import { setLocale, expectNoServerError } from '../lib/helpers.ts';

test('native diagnostic collector metadata stays readable in both locales', async ({ page }, info) => {
  const response = await page.goto('/admin/system/errors');
  expect(response?.status()).toBe(200);
  expect(response?.headers()['cache-control']).toBe('private, no-store');
  const metadata = page.locator('[data-diagnostic-collector="available"]');
  const disclosure = page.locator('[data-error-diagnostics]');
  await expect(metadata).toBeHidden();
  await disclosure.locator('summary').click();
  await expect(metadata).toBeVisible();
  await expect(metadata.locator('[data-collector-dropped]')).toHaveText('0');
  await expect(metadata.locator('[data-collector-storage-failures]')).toHaveText('0');
  const readAt = await metadata.locator('[data-collector-read-at]').getAttribute('datetime');
  const lastAt = await metadata.locator('[data-collector-last-success]').getAttribute('datetime');
  expect(readAt).toMatch(/Z$/);
  expect(lastAt).toMatch(/Z$/);
  expect(Math.abs(Date.now() - Date.parse(readAt!))).toBeLessThan(15000);
  expect(Date.parse(lastAt!)).toBeLessThanOrEqual(Date.parse(readAt!));
  for (const locale of ['ar', 'en'] as const) {
    await setLocale(page, locale);
    await expect(page.locator('html')).toHaveAttribute('dir', locale === 'ar' ? 'rtl' : 'ltr');
    if (!(await disclosure.evaluate((element: HTMLDetailsElement) => element.open))) await disclosure.locator('summary').click();
    await expect(metadata).toBeVisible();
    await expect(metadata).not.toContainText('errorinventory.');
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
      await page.screenshot({ path: info.outputPath(`diagnostics-${locale}-${width}.png`), fullPage: true });
      await expectNoServerError(page);
    }
  }
});
