// The admin bar: signed-in staff see a strip at the top of every public page
// (dashboard, the editor of the page they are looking at, the website
// overview, hide); visitors never do. Hiding lasts the browser session.
import { test, expect } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { db, setLocale } from '../lib/helpers.ts';

test('staff see the admin bar on public pages, visitors do not, hide lasts the session', async ({ page, browser }) => {
  await setLocale(page, 'en');
  // A published page to look at.
  await page.goto('/admin/content/page/new');
  const title = 'E2E admin bar ' + randomUUID();
  await page.fill('#title', title);
  await page.locator('button[name=intent][value=publish]').click();
  const id = new URL(page.url()).pathname.split('/').pop()!;
  const database = db();
  const { slug, locale } = database.prepare('SELECT slug, locale FROM content WHERE id=?').get(id) as { slug: string; locale: string };
  // The site locale is unprefixed; a page in another locale lives under /<locale>/.
  let path = '/' + slug;
  if ((await page.request.get(path)).status() !== 200) path = '/' + locale + '/' + slug;
  try {
    const shown = await page.goto(path);
    expect(shown?.status()).toBe(200);
    const bar = page.locator('[data-gorun-admin-bar]');
    await expect(bar).toHaveCount(1);
    await expect(bar).toHaveAttribute('dir', 'ltr');
    await expect(bar.locator('a[href="/admin"]')).toBeVisible();
    await expect(bar.locator('[data-admin-bar-edit]')).toHaveAttribute('href', `/admin/content/page/${id}`);
    await expect(bar.locator('a[href="/admin/website"]')).toBeVisible();
    // The bar is the first thing after <body> and sits above the theme's header.
    const [barTop, headerTop] = await page.evaluate(() => [
      document.querySelector('[data-gorun-admin-bar]')!.getBoundingClientRect().top,
      document.querySelector('header')!.getBoundingClientRect().top,
    ]);
    expect(barTop).toBeLessThan(headerTop);
    // Pages with the bar are private.
    const resp = await page.request.get(path);
    expect(resp.headers()['cache-control']).toBe('private, no-store');

    // Hide: back on the same page, no bar, and still none on the next page.
    await bar.locator('[data-admin-bar-hide]').click();
    await expect(page).toHaveURL(new RegExp(path.replace(/\//g, '\\/') + '$'));
    await expect(page.locator('[data-gorun-admin-bar]')).toHaveCount(0);
    await page.goto('/');
    await expect(page.locator('[data-gorun-admin-bar]')).toHaveCount(0);

    // A visitor without a session: no bar, no stylesheet link, no private cache header.
    const visitor = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    const anon = await visitor.newPage();
    const anonResp = await anon.goto(path);
    expect(anonResp?.status()).toBe(200);
    await expect(anon.locator('[data-gorun-admin-bar]')).toHaveCount(0);
    await expect(anon.locator('link[data-gorun-admin-bar-css]')).toHaveCount(0);
    expect(anonResp?.headers()['cache-control'] ?? '').not.toContain('no-store');
    await visitor.close();
  } finally {
    database.prepare('DELETE FROM content WHERE id=?').run(id);
    database.close();
  }
});

test('the bar follows the staff member\'s own language', async ({ page }) => {
  await setLocale(page, 'ar');
  try {
    await page.goto('/');
    const bar = page.locator('[data-gorun-admin-bar]');
    await expect(bar).toHaveAttribute('dir', 'rtl');
    await expect(bar).toHaveAttribute('lang', 'ar');
    await expect(bar.locator('a[href="/admin"]')).toContainText('GoRun');
  } finally {
    await setLocale(page, 'en');
  }
});
