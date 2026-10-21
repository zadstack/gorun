import { test, expect } from '@playwright/test';
import { cpSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { csrf, db, env, setLocale } from '../lib/helpers.ts';

const customSlug = 'e2e-installed';

test('pattern plugins show bundled vs custom source and activate/clear', async ({ page }, testInfo) => {
  const dataDir = env('E2E_DATA');
  const customDir = join(dataDir, 'patterns', customSlug);
  const database = db();
  const prior = database.prepare("SELECT value_json FROM settings WHERE key='site.pattern'").get() as { value_json: string } | undefined;
  database.close();
  try {
    mkdirSync(join(dataDir, 'patterns'), { recursive: true });
    cpSync(resolve('..', 'patterns', 'showcase'), customDir, { recursive: true });
    const manifest = JSON.parse(readFileSync(join(customDir, 'pattern.json'), 'utf8')) as Record<string, unknown>;
    manifest.slug = customSlug;
    manifest.name = { en: 'E2E installed pattern', ar: 'نمط مثبت للاختبار' };
    writeFileSync(join(customDir, 'pattern.json'), `${JSON.stringify(manifest, null, 2)}\n`);

    for (const locale of ['en', 'ar'] as const) {
      await page.goto('/admin/appearance/patterns');
      await setLocale(page, locale);
      await page.setViewportSize({ width: 390, height: 844 });
      const bundledLabel = locale === 'en' ? 'First-party' : 'أساسي';
      const customLabel = locale === 'en' ? 'Installed' : 'مثبّت';
      await expect(page.locator('[data-pattern="editorial"] [data-pattern-source]')).toHaveText(bundledLabel);
      await expect(page.locator(`[data-pattern="${customSlug}"] [data-pattern-source]`)).toHaveText(customLabel);
      await expect(page.getByRole('link', { name: locale === 'en' ? 'Theme identity (colors & type)' : 'هوية القالب (ألوان وخطوط)' })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`patterns-${locale}-mobile.png`), fullPage: true });

      const activate = await page.request.post(`/admin/appearance/patterns/${customSlug}/activate`, {
        form: { _csrf: await csrf(page) },
        maxRedirects: 0,
      });
      expect(activate.status()).toBe(303);
      await page.goto('/admin/appearance/patterns');
      await expect(page.locator(`[data-pattern="${customSlug}"]`)).toHaveClass(/border-green/);
      await expect(page.locator('form[action="/admin/appearance/patterns/clear"]')).toBeVisible();

      const clear = await page.request.post('/admin/appearance/patterns/clear', {
        form: { _csrf: await csrf(page) },
        maxRedirects: 0,
      });
      expect(clear.status()).toBe(303);
    }

    const after = db();
    const row = after.prepare("SELECT value_json FROM settings WHERE key='site.pattern'").get() as { value_json: string };
    after.close();
    expect(JSON.parse(row.value_json)).toBe('');
  } finally {
    rmSync(customDir, { recursive: true, force: true });
    const restore = db();
    if (prior) {
      restore.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json').run('site.pattern', prior.value_json, new Date().toISOString());
    } else {
      restore.prepare("DELETE FROM settings WHERE key='site.pattern'").run();
    }
    restore.close();
  }
});
