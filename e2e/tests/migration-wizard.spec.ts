// Migration wizard end to end against the fictional example.org REST API
// served by fixtures/wp-fixture-server.ts: connect (anonymous) → scan (three
// buckets) → map types → map fields → preview (counts) → import (progress
// reaches validated) → validate page → Markdown report download.
import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createServer } from 'node:net';
import { resolve } from 'node:path';
import { db, env, expectNoServerError } from '../lib/helpers.ts';

test.describe.serial('WordPress migration wizard (fixture example.org)', () => {
  let base = ''; // /admin/migrations/{id}

  test('connect anonymously and scan: the report has three buckets', async ({ page }) => {
    await page.goto('/admin/migrations');
    await page.locator('a[href="/admin/migrations/new"]').first().click();
    await page.fill('#mig-url', env('E2E_WP_URL'));
    await page.fill('#mig-name', 'E2E Example Press fixture');
    await page.selectOption('#mig-policy', 'suffix');
    await page.locator('form[action="/admin/migrations/connect"] button[type="submit"]').click();
    await expect(page).toHaveURL(/\/admin\/migrations\/[0-9A-Z]{26}\/scan$/);
    base = page.url().replace(/\/scan$/, '');

    const scanForm = page.locator(`form[action$="/scan"]`);
    if (await page.locator('#b-available').count() === 0) await scanForm.locator('button[type="submit"]').click();
    await expect(page.locator('#b-available')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('#b-partial')).toBeVisible();
    await expect(page.locator('#b-unavailable')).toBeVisible();
    const available = page.locator('section[aria-labelledby="b-available"] li');
    expect(await available.count()).toBeGreaterThan(2);
    // anonymous: the menus and raw blocks are only partly available; custom templates are not migrated
    await expect(page.locator('section[aria-labelledby="b-partial"]')).not.toBeEmpty();
    await expect(page.locator('section[aria-labelledby="b-unavailable"]')).toContainText('pricing');
    await expectNoServerError(page);
  });

  test('map types and fields', async ({ page }) => {
    await page.goto(`${base}/map`);
    await expect(page.locator('select[name="type.post"]')).toBeVisible();
    await expect(page.locator('select[name="type.page"]')).toBeVisible();
    await page.locator('form[action$="/map"] button[type="submit"]').click();
    await expect(page).toHaveURL(new RegExp(`${base}/(fields|preview)$`));
    if (page.url().endsWith('/fields')) {
      await page.locator('form[action$="/fields"] button[type="submit"]').click();
      await expect(page).toHaveURL(new RegExp(`${base}/preview$`));
    }
  });

  test('preview (dry run) shows counts and writes nothing', async ({ page, request }) => {
    const before = (await (await request.get('/api/v1/content?type=page&per_page=100')).json()).meta;
    expect(typeof before?.total).toBe('number');
    const rows = () => db().prepare('SELECT (SELECT count(*) FROM media) AS m, (SELECT count(*) FROM redirects) AS r, (SELECT count(*) FROM terms) AS t').get();
    const rowsBefore = rows();
    await page.goto(`${base}/preview`);
    await page.locator('form[action$="/preview"] button[type="submit"]').click();
    const cards = page.locator('main .grid h2');
    await expect(cards.first()).toBeVisible({ timeout: 60_000 });
    expect(await cards.count()).toBeGreaterThanOrEqual(4);
    await expect(page.locator('#pv-conflicts')).toBeVisible();
    const text = await page.locator('main').innerText();
    test.info().annotations.push({ type: 'preview', description: text.replace(/\s+/g, ' ').slice(0, 600) });
    const after = (await (await request.get('/api/v1/content?type=page&per_page=100')).json()).meta;
    expect(after?.total).toBe(before?.total);
    expect(rows()).toEqual(rowsBefore); // no media, redirects or terms written
  });

  test('import runs to "validated" with live progress', async ({ page }) => {
    test.setTimeout(240_000);
    await page.goto(`${base}/import`);
    const progress = page.locator('#mig-progress');
    await expect(progress).toBeVisible();
    const start = progress.locator(`button[hx-post$="/start"]`);
    await expect(start).toBeVisible();
    await start.click();
    const status = page.locator('#mig-progress [data-status]').first();
    await expect(status).toHaveAttribute('data-status', /running|completed|validated/, { timeout: 30_000 });
    await expect(status).toHaveAttribute('data-status', 'validated', { timeout: 200_000 });
    await expect(page.locator('#mig-kinds')).toBeVisible();
    await expect(page.locator('#mig-progress [role="progressbar"]').first()).toHaveAttribute('aria-valuenow', '100');
  });

  test('validate page and Markdown report download', async ({ page, request }) => {
    await page.goto(`${base}/validate`);
    await expect(page.locator('#val-counts')).toBeVisible();
    await expectNoServerError(page);
    const counts = await page.locator('main').innerText();
    test.info().annotations.push({ type: 'validate', description: counts.replace(/\s+/g, ' ').slice(0, 800) });

    const dl = page.waitForEvent('download');
    await page.locator('a[href$="/report.md"]').click();
    const file = await dl;
    expect(file.suggestedFilename()).toMatch(/\.md$/);
    const md = await request.get(`${base}/report.md`);
    expect(md.status()).toBe(200);
    const body = await md.text();
    expect(body).toContain('Migration report');
    expect(body).toMatch(/zs_plans|shortcode/i);

    // the imported site answers: posts moved to /post/<slug> with a 301 from the WordPress permalink
    const posts = (await (await request.get('/api/v1/content?type=post&per_page=100')).json()).data as any[];
    expect(posts.length).toBeGreaterThanOrEqual(11);
    const pages = (await (await request.get('/api/v1/content?type=page&per_page=100')).json()).data as any[];
    expect(pages.some((p) => p.slug === 'pricing')).toBe(true);
  });

  test('validation shows unavailable checks when the public server cannot be reached', async ({ page }) => {
    const socket = createServer();
    await new Promise<void>(done => socket.listen(0, '127.0.0.1', done));
    const port = (socket.address() as { port: number }).port;
    await new Promise<void>((done, reject) => socket.close(error => error ? reject(error) : done()));
    const setBase = (value: string) => execFileSync(resolve('.bin/gorun'), [
      'settings', 'set', 'site.base_url', value, '--data', env('E2E_DATA'),
    ], { env: { PATH: process.env.PATH, GORUN_ENV: 'dev' } });
    setBase(`http://127.0.0.1:${port}`);
    try {
      await page.goto(`${base}/validate`);
      await page.locator('form[action$="/validate"] button[type="submit"]').click();
      await expect(page.locator('#val-unverified')).toBeVisible();
      await expect(page.locator('[data-outcome="unavailable"]').first()).toBeVisible();
      await expectNoServerError(page);
    } finally {
      setBase(env('E2E_BASE_URL'));
    }
    await page.goto(`${base}/validate`);
    await page.locator('form[action$="/validate"] button[type="submit"]').click();
    await expect(page.locator('[data-outcome="unavailable"]')).toHaveCount(0);
  });

  // BUG-03 (fixed): with commerce booted the wpmigrate.product_importer
  // adapter imports the WooCommerce products.
  test('BUG-03 WooCommerce products are imported when commerce is booted', async ({ request }) => {
    const id = base.split('/').pop();
    const st = (await (await request.get(`/api/v1/migrations/${id}`)).json()).data;
    expect(st.counts.product?.done ?? 0).toBeGreaterThan(0);
  });

  // BUG-04 (fixed): every source-host URL in the imported SEO (canonical,
  // Yoast JSON-LD at any depth) is mapped to this site.
  test('BUG-04 imported Yoast schema no longer points at the WordPress site', async () => {
    const rows = db().prepare("SELECT slug FROM content WHERE seo_json LIKE ?").all(`%${env('E2E_WP_URL')}%`) as any[];
    expect(rows.map((r) => r.slug)).toEqual([]);
    // positive control: imported schema exists and its search action targets this site
    const withSchema = db().prepare("SELECT count(*) AS n FROM content WHERE seo_json LIKE '%search?q={search_term_string}%'").get() as any;
    expect(withSchema.n).toBeGreaterThan(0);
  });
});
