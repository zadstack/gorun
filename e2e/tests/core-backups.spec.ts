import { test, expect } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { ADMIN, env, setLocale, expectNoServerError } from '../lib/helpers.ts';

test('native backup presence and upload receipts remain distinct from verified restore', async ({ page }, info) => {
  await page.goto('/admin/system/backups');
  await page.locator('form[action="/admin/system/backups"] button[type="submit"]').click();
  await expect(page.locator('[data-local-state="paired_not_inspected"]')).toBeVisible();
  const manifestLink = page.locator('a[href$=".manifest.json"]').first();
  const href = await manifestLink.getAttribute('href');
  expect(href).toBeTruthy();
  // Downloads need a password confirmation within the last 10 minutes; the saved
  // session may be older than that, so confirm explicitly before downloading.
  await page.goto(`/admin/reauth?next=${encodeURIComponent('/admin/system/backups')}`);
  await page.fill('input[name="password"]', ADMIN.password);
  await page.locator('form[action="/admin/reauth"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/admin\/system\/backups$/);
  const response = await page.request.get(href!);
  expect(response.status()).toBe(200);
  const manifest = await response.json();
  expect(manifest.format_version).toBe(1);
  expect(manifest.database.sha256).toMatch(/^[a-f0-9]{64}$/);
  expect(manifest.archive.sha256).toMatch(/^[a-f0-9]{64}$/);
  expect(manifest.consistency).toBe('database_snapshot_files_not_atomic');
  const backups = join(env('E2E_DATA'), 'backups');
  mkdirSync(backups, { recursive: true });
  // Synthetic receipts are confined to the disposable instance; no provider is called.
  writeFileSync(join(backups, `${manifest.stamp}.remote.json`), JSON.stringify({
    state: 'partial', read_back_verified: false,
    database_sha256: manifest.database.sha256, archive_sha256: manifest.archive.sha256,
    files: [
      { name: `${manifest.stamp}.db`, state: 'uploaded', checked_at: '2020-01-01T00:00:00Z' },
      { name: `${manifest.stamp}.tar.gz`, state: 'failed', code: 'provider_upload_failed', checked_at: '2020-01-01T00:00:00Z' },
    ],
  }));
  writeFileSync(join(backups, '20000101T000000Z.db'), 'legacy fixture');
  writeFileSync(join(backups, '20000101T000000Z.tar.gz'), 'legacy fixture');
  writeFileSync(join(backups, '20000102T000000Z.db'), 'partial fixture');
  await page.reload();
  await expect(page.locator('[data-remote-state="partial"]')).toBeVisible();
  await expect(page.locator('[data-upload-state="uploaded"]')).toBeVisible();
  await expect(page.locator('[data-upload-state="failed"]')).toBeVisible();
  await expect(page.locator('[data-local-state="legacy_unverified"]')).toBeVisible();
  await expect(page.locator('[data-local-state="partial"]')).toBeVisible();
  for (const locale of ['ar', 'en'] as const) {
    await setLocale(page, locale);
    await expect(page.locator('html')).toHaveAttribute('dir', locale === 'ar' ? 'rtl' : 'ltr');
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
      await page.screenshot({ path: info.outputPath(`backups-${locale}-${width}.png`), fullPage: true });
      await expectNoServerError(page);
    }
  }
});
