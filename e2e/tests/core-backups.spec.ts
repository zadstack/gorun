import { test, expect } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { ADMIN, env, setLocale, expectNoServerError, submitAndLand } from '../lib/helpers.ts';

test('native backup presence and upload receipts remain distinct from verified restore', async ({ page }, info) => {
  await page.goto('/admin/system/backups');
  await page.locator('form[action="/admin/system/backups"] button[type="submit"]').click();
  await expect(page.locator('[data-local-state="paired_not_inspected"]')).toBeVisible();
  const inspectLink = page.locator('[data-inspect-link]').first();
  await expect(inspectLink).toBeVisible();
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

  await page.locator('[data-inspect-link]').first().click();
  await expect(page).toHaveURL(/\/admin\/system\/backups\/\d{8}T\d{6}Z\/inspect$/);
  await expect(page.locator('[data-inspect-readiness="local_ok"]')).toBeVisible();
  await expect(page.locator('[data-inspect-cutover]')).toBeVisible();
  await page.locator('[data-restore-submit]').click();
  const modal = page.locator('[role="dialog"][aria-labelledby="gp-confirm-title"]');
  await expect(modal).toBeVisible();
  await modal.locator('button[\\@click="$store.confirm.yes()"]').click();
  await expect(page.locator('[data-restore-done]')).toBeVisible({ timeout: 15000 });
  await expect(page.locator('[data-restore-dest]')).toContainText(`restores/${manifest.stamp}`);

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
  await page.goto('/admin/system/backups');
  await expect(page.locator('[data-remote-state="partial"]')).toBeVisible();
  await expect(page.locator('[data-upload-state="uploaded"]')).toBeVisible();
  await expect(page.locator('[data-upload-state="failed"]')).toBeVisible();
  await expect(page.locator('[data-local-state="legacy_unverified"]')).toBeVisible();
  await expect(page.locator('[data-local-state="partial"]')).toBeVisible();
  await expect(page.locator('[data-restored-path]')).toBeVisible();
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


test('backup schedule choices and failure alerts work in both languages', async ({ page }, info) => {
  await page.goto('/admin/system/backups');
  const form = page.locator('form[action="/admin/system/backups/schedule"]');
  await expect(page.locator('[data-backup-storage]')).toBeVisible();
  await expect(page.locator('[data-backup-last-success]')).toBeVisible();
  for (const hours of ['24', '168', '0']) {
    await form.locator('[name="schedule_hours"]').selectOption(hours);
    await form.locator('[name="retain"]').fill('3');
    await submitAndLand(page, form.locator('button[type="submit"]'), '/admin/system/backups/schedule');
    await expect(form.locator('[name="schedule_hours"]')).toHaveValue(hours);
  }
  const db = new DatabaseSync(join(env('E2E_DATA'), 'gorun.db'));
  db.exec('PRAGMA busy_timeout=5000');
  const prior = db.prepare('SELECT value_json, updated_at FROM settings WHERE key = ?').get('backup.schedule.state') as {value_json: string; updated_at: string} | undefined;
  db.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at')
    .run('backup.schedule.state', JSON.stringify({status:'failed', stage:'verify', attempt_at:new Date().toISOString()}), new Date().toISOString());
  try {
    for (const locale of ['ar', 'en'] as const) {
      await setLocale(page, locale);
      await expect(page.locator('[role="alert"][data-schedule-status="failed"]')).toBeVisible();
      for (const width of [1440, 390]) {
        await page.setViewportSize({width, height:1000});
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
        await page.screenshot({path:info.outputPath(`backup-alert-${locale}-${width}.png`), fullPage:true});
        await expectNoServerError(page);
      }
    }
    await page.goto('/admin/system/health');
    await expect(page.locator('[data-check="backups"][data-status="fail"]')).toBeVisible();
  } finally {
    if (prior) db.prepare('UPDATE settings SET value_json=?,updated_at=? WHERE key=?').run(prior.value_json, prior.updated_at, 'backup.schedule.state');
    else db.prepare('DELETE FROM settings WHERE key=?').run('backup.schedule.state');
    db.close();
  }
});
