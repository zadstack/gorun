// Roadmap phase 8, data freedom (docs/specs/data-freedom.md): exporting the
// content list with its filters as a JSON and a CSV download, and translating
// a category into Arabic from the term editor, in English and Arabic, desktop
// and 390 px.
import { test, expect, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { csrf, db, setLocale, submitAndLand } from '../lib/helpers.ts';

const createdContent: string[] = [];
const createdTerms: string[] = [];

async function draft(page: Page, title: string): Promise<string> {
  const token = await csrf(page);
  const r = await page.request.post('/admin/x/webmcp/drafts', {data: {type: 'post', title}, headers: {'X-CSRF-Token': token}});
  expect(r.status()).toBe(201);
  const body = await r.json();
  createdContent.push(body.id);
  return body.id;
}

const noHorizontalScroll = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth);

test.afterAll(() => {
  const database = db();
  const delContent = database.prepare('DELETE FROM content WHERE id=?');
  for (const id of createdContent) delContent.run(id);
  const delTerm = database.prepare('DELETE FROM terms WHERE id=?');
  for (const id of createdTerms) delTerm.run(id);
  database.close();
});

test('the content list exports its current filters as JSON and CSV downloads', async ({page}, testInfo) => {
  await page.goto('/admin');
  await setLocale(page, 'en');
  const tag = 'Export ' + randomUUID().slice(0, 8);
  await draft(page, tag + ' one');
  await draft(page, '=SUM(1) ' + tag); // a formula-looking title must be neutralised in CSV
  await draft(page, 'Unrelated ' + randomUUID().slice(0, 8));

  await page.goto('/admin/content/post?search=' + encodeURIComponent(tag));
  const menu = page.locator('[data-content-export]');
  const summary = menu.locator('summary');
  await expect(summary).toHaveText(/Export/);
  // Keyboard: the summary opens the menu with Enter, and the links are reachable with Tab.
  await summary.focus();
  await page.keyboard.press('Enter');
  const jsonLink = menu.locator('[data-export-format="json"]');
  await expect(jsonLink).toBeVisible();
  await expect(jsonLink).toHaveAttribute('href', new RegExp('search=' + encodeURIComponent(tag).replace(/%20/g, '\\+')));
  await page.keyboard.press('Tab');
  await expect(jsonLink).toBeFocused();
  await page.screenshot({path: testInfo.outputPath('export-menu-en.png'), animations: 'disabled'});

  const [jsonDownload] = await Promise.all([page.waitForEvent('download'), page.keyboard.press('Enter')]);
  expect(jsonDownload.suggestedFilename()).toMatch(/^content-post-\d{8}-\d{6}\.json$/);
  const doc = JSON.parse(readFileSync(await jsonDownload.path(), 'utf8'));
  expect(doc.format).toBe('gorun.content-export');
  expect(doc.count).toBe(2);
  expect(doc.items.map((it: any) => it.title).sort()).toEqual([tag + ' one', '=SUM(1) ' + tag].sort());
  expect(doc.items[0]).toHaveProperty('body');
  expect(doc.items[0]).toHaveProperty('translation_group');

  if (!(await menu.evaluate((d) => (d as HTMLDetailsElement).open))) await summary.click();
  const [csvDownload] = await Promise.all([page.waitForEvent('download'), menu.locator('[data-export-format="csv"]').click()]);
  expect(csvDownload.suggestedFilename()).toMatch(/\.csv$/);
  const csvText = readFileSync(await csvDownload.path(), 'utf8');
  expect(csvText.startsWith('﻿')).toBe(true);
  expect(csvText).toContain(tag + ' one');
  expect(csvText).toContain("'=SUM(1) " + tag);
  expect(csvText).not.toContain('Unrelated');

  // Arabic and 390 px: the menu is labelled in Arabic, stays inside the viewport and the page does not scroll sideways.
  await setLocale(page, 'ar');
  await page.setViewportSize({width: 390, height: 844});
  await page.goto('/admin/content/post?search=' + encodeURIComponent(tag));
  await expect(summary).toHaveText(/تصدير/);
  await summary.click();
  const box = await menu.locator('.content-export-menu').boundingBox();
  expect(box).not.toBeNull();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  expect(await noHorizontalScroll(page)).toBe(true);
  await page.screenshot({path: testInfo.outputPath('export-menu-ar-390.png'), animations: 'disabled'});
});

test('a category is translated into Arabic from the term editor and served per locale', async ({page}, testInfo) => {
  await page.goto('/admin');
  await setLocale(page, 'ar');
  const suffix = randomUUID().slice(0, 6);
  const enName = 'World ' + suffix;
  const enSlug = 'world-' + suffix;
  const arName = 'العالم ' + suffix;
  const arSlug = 'alam-' + suffix;

  // An English category from the term form.
  await page.goto('/admin/taxonomies/category');
  await page.locator('#term-name').fill(enName);
  await page.locator('#term-slug').fill(enSlug);
  await page.locator('#term-locale').selectOption('en');
  await submitAndLand(page, page.locator('form[action="/admin/taxonomies/category/terms"] button[type="submit"]'), '/admin/taxonomies/category/terms');
  const enRow = page.locator('#terms-list tr', {hasText: enName});
  await expect(enRow.locator('[data-term-locale="en"]')).toBeVisible();
  await enRow.getByRole('link', {name: enName}).click();
  await page.waitForURL(/edit=/);
  const enID = new URL(page.url()).searchParams.get('edit');

  // The Translations panel offers Arabic; add the translation inline.
  const panel = page.locator('[data-term-translations]');
  await expect(panel).toBeVisible();
  const arabic = panel.locator('[data-term-translation="ar"]');
  await arabic.locator('summary').click();
  await arabic.locator('input[name="name"]').fill(arName);
  await arabic.locator('input[name="slug"]').fill(arSlug);
  await submitAndLand(page, arabic.locator('button[type="submit"]'), `/admin/taxonomies/category/terms/${enID}/translations`);
  await expect(page.locator('.admin-flash').first()).toContainText('أُضيفت الترجمة');

  // Now on the Arabic term: its panel links back to the English one.
  await expect(page.locator('#term-name')).toHaveValue(arName);
  await expect(page.locator('#term-locale')).toHaveValue('ar');
  await expect(page.locator('[data-term-translation="en"]').getByRole('link', {name: enName})).toBeVisible();
  expect(await noHorizontalScroll(page)).toBe(true);
  await page.screenshot({path: testInfo.outputPath('term-translations-ar.png'), animations: 'disabled', fullPage: true});

  const database = db();
  const rows = database.prepare('SELECT id, locale, translation_group FROM terms WHERE slug IN (?, ?)').all(enSlug, arSlug) as any[];
  database.close();
  for (const r of rows) createdTerms.push(r.id);
  expect(rows).toHaveLength(2);
  expect(new Set(rows.map((r) => r.translation_group)).size).toBe(1);
  expect(rows[0].translation_group).not.toBe('');

  // Public routing: each language has its own archive, and the wrong slug redirects to the right one.
  expect((await page.request.get('/category/' + arSlug)).status()).toBe(200);
  expect((await page.request.get('/en/category/' + enSlug)).status()).toBe(200);
  const wrong = await page.request.get('/category/' + enSlug, {maxRedirects: 0});
  expect(wrong.status()).toBe(301);
  expect(wrong.headers()['location']).toBe('/category/' + encodeURIComponent(arSlug).replace(/%2D/g, '-'));
  const arPage = await page.request.get('/category/' + arSlug);
  expect(await arPage.text()).toContain('hreflang="en" href="' + new URL(page.url()).origin + '/en/category/' + enSlug + '"');

  // 390 px: the term editor and its panel fit.
  await page.setViewportSize({width: 390, height: 844});
  await page.reload();
  await expect(page.locator('[data-term-translations]')).toBeVisible();
  expect(await noHorizontalScroll(page)).toBe(true);
});
