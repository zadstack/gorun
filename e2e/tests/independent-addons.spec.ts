import { test, expect } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { ADMIN, db } from '../lib/helpers.ts';

const modes = JSON.parse(process.env.E2E_ADDONS ?? '{}') as Record<string, string>;
const enabled = (name: string) => modes[name] !== 'disabled';

// Synthetic rows belong only to this test; prior billing tests are unnecessary.
function seedLongSaaSPage(): { name: string; cleanup: () => void } {
  const sql = db();
  const owner = sql.prepare('SELECT id FROM users WHERE email = ?').get(ADMIN.email) as { id: string };
  const id = 'addon-layout-' + randomUUID();
  const name = 'تطبيق تجريبي طويل · Synthetic ' + 'Application'.repeat(18);
  const subject = id + '-subject';
  sql.prepare(`INSERT INTO saas_apps(id, public_id, owner_id, name, slug, status, created_at, updated_at)
    VALUES(?,?,?,?,?,'active','','')`).run(id, 'public-' + 'identifier'.repeat(30), owner.id, name, id);
  sql.prepare(`INSERT INTO saas_entitlements(id, public_id, app_id, owner_id, subject_type, external_id,
    plan_id, status, features_json, limits_json, created_at, updated_at)
    VALUES(?,?,?,?,'user',?,?,'active',?,?,'','')`).run(id, id, id, owner.id, subject,
      'synthetic-plan-' + 'LongPlan'.repeat(20), JSON.stringify({ description: 'ميزة'.repeat(90) }), JSON.stringify({ requests: 10000 }));
  sql.prepare(`INSERT INTO saas_usage_counters(id, app_id, subject_id, metric, quantity, created_at, updated_at)
    VALUES(?,?,?,?,42,'','')`).run(id, id, subject, 'synthetic-metric-' + 'Requests'.repeat(24));
  return { name, cleanup: () => {
    try {
      sql.prepare('DELETE FROM saas_usage_counters WHERE id = ?').run(id);
      sql.prepare('DELETE FROM saas_entitlements WHERE id = ?').run(id);
      sql.prepare('DELETE FROM saas_apps WHERE id = ?').run(id);
    } finally { sql.close(); }
  } };
}

for (const addon of ['saas', 'membership']) {
  test(`${addon} owns its assets and navigation with mixed activation`, async ({ page, browser }, testInfo) => {
    const path = `/${addon}`;
    if (!enabled(addon)) {
      expect((await page.request.get(path)).status()).toBe(404);
      expect((await page.request.get(`${path}/assets.css`)).status()).toBe(404);
      return;
    }
    const fixture = addon === 'saas' ? seedLongSaaSPage() : undefined;
    try {
      for (const width of [1440, 390]) {
        await page.setViewportSize({ width, height: 900 });
        const errors: string[] = [];
        const onError = (error: Error) => errors.push(error.message);
        page.on('pageerror', onError);
        expect((await page.goto(path))?.status()).toBe(200);
        if (fixture) {
          await expect(page.locator('article').filter({ hasText: fixture.name })).toBeVisible();
          await expect(page.locator('main')).toContainText('synthetic-metric-');
          await expect(page.locator('.scroll')).toHaveCount(2);
          for (const table of await page.locator('.scroll').all()) {
            await expect(table).toHaveCSS('overflow-x', 'auto');
            // Wide JSON must not squeeze adjacent columns into single letters.
            const narrowestCell = await table.locator('th, td').evaluateAll((cells) =>
              Math.min(...cells.map((cell) => cell.getBoundingClientRect().width)));
            expect(narrowestCell).toBeGreaterThanOrEqual(128);
            if (width === 390) {
              await table.focus();
              await expect(table).toBeFocused();
              await page.keyboard.press('ArrowLeft');
              await expect.poll(() => table.evaluate((el) => Math.abs(el.scrollLeft))).toBeGreaterThan(0);
            }
          }
        }
        const css = page.locator('link[rel="stylesheet"]');
        await expect(css).toHaveAttribute('href', `${path}/assets.css`);
        const asset = await page.request.get(`${path}/assets.css`);
        expect(asset.status()).toBe(200);
        expect(asset.headers()['content-type']).toContain('text/css');
        await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(247, 248, 250)');
        await expect(page.locator('a[href="/account"]')).toHaveCount(enabled('commerce') ? 1 : 0);
        await expect(page.locator('nav a[href="/shop"]')).toHaveCount(enabled('commerce') ? 1 : 0);
        if (addon === 'membership') await expect(page.locator('a[href="/saas"]')).toHaveCount(enabled('saas') ? 1 : 0);
        expect(await page.evaluate(() => {
          const root = document.documentElement;
          return root.scrollWidth <= innerWidth + 1 &&
            !['hidden', 'clip'].includes(getComputedStyle(root).overflowX) &&
            !['hidden', 'clip'].includes(getComputedStyle(document.body).overflowX);
        })).toBe(true);
        expect(errors).toEqual([]);
        await page.screenshot({ path: testInfo.outputPath(`${addon}-${width}.png`), fullPage: true });
        page.off('pageerror', onError);
      }
      const anonymous = await browser.newContext({ storageState: { cookies: [], origins: [] } });
      try {
        const response = await anonymous.request.get(path, { maxRedirects: 0 });
        expect(response.status()).toBe(enabled('commerce') ? 303 : 503);
        if (enabled('commerce')) expect(response.headers().location).toBe(`/account?next=${path}`);
        else {
          expect(response.headers().location).toBeUndefined();
          expect(await response.text()).toContain('account_sign_in_unavailable');
        }
      } finally { await anonymous.close(); }
    } finally { fixture?.cleanup(); }
  });
}
