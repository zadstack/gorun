// Roadmap phase 6, speed for power users: the global command palette
// (Ctrl/Cmd+K), the shortcut map ("?", "/", "g c"), Ctrl/Cmd+S in the editor
// and bulk publish with confirmation and a per-item summary, in English and
// Arabic, desktop and 390 px.
import { test, expect, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { csrf, db, setLocale } from '../lib/helpers.ts';

const created: string[] = [];

async function draft(page: Page, title: string): Promise<string> {
  const token = await csrf(page);
  const r = await page.request.post('/admin/x/webmcp/drafts', {data: {type: 'post', title}, headers: {'X-CSRF-Token': token}});
  expect(r.status()).toBe(201);
  const body = await r.json();
  created.push(body.id);
  return body.id;
}

const noHorizontalScroll = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth);

test.afterAll(() => {
  if (!created.length) return;
  const database = db();
  const del = database.prepare('DELETE FROM content WHERE id=?');
  for (const id of created) del.run(id);
  database.close();
});

test('Ctrl+K opens the command palette, finds pages and content, and Enter navigates', async ({page}, testInfo) => {
  await page.goto('/admin');
  await setLocale(page, 'en');
  const title = 'Palette target ' + randomUUID().slice(0, 8);
  const id = await draft(page, title);

  const palette = page.locator('[data-gp-palette]');
  const input = page.locator('#gp-palette-input');
  await page.keyboard.press('Control+k');
  await expect(palette).toBeVisible();
  await expect(input).toBeFocused();
  await expect(input).toHaveAttribute('aria-expanded', 'true');
  await expect(palette.getByRole('option', {name: 'Settings'}).first()).toBeVisible(); // pages before typing
  await input.fill('setings');
  const settings = palette.getByRole('option', {name: /^Settings/}).first();
  await expect(settings).toBeVisible();
  await expect(palette.getByRole('option').first()).toHaveAttribute('aria-selected', 'true');
  await expect(input).toHaveAttribute('aria-activedescendant', /gp-palette-option-/);
  await page.keyboard.press('End');
  await expect(palette.getByRole('option').last()).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Home');
  await expect(palette.getByRole('option').first()).toHaveAttribute('aria-selected', 'true');
  await page.screenshot({path: testInfo.outputPath('palette-en.png'), animations: 'disabled'});
  await page.keyboard.press('Escape');
  await expect(palette).toBeHidden();

  // A content title comes from the live search; Enter opens it.
  await page.keyboard.press('Control+k');
  await input.fill(title);
  const hit = palette.getByRole('option', {name: new RegExp(title)});
  await expect(palette.locator('#gp-palette-group-content')).toBeVisible();
  await expect(hit.first()).toBeVisible();
  await expect(palette.getByRole('option').first()).toContainText(title);
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(new RegExp('/admin/content/post/' + id + '$'));

  // Recent items are offered the next time, and the page search finds the media library.
  await page.keyboard.press('Control+k');
  await expect(palette.locator('#gp-palette-group-recent')).toBeVisible();
  await expect(palette.getByRole('option', {name: new RegExp(title)}).first()).toBeVisible();
  await input.fill('media');
  await expect(palette.getByRole('option', {name: /^Media/}).first()).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/admin\/media$/);
});

test('the shortcut map, "/", "g c" and Ctrl+S follow the guards', async ({page}) => {
  await page.goto('/admin');
  await setLocale(page, 'en');

  await page.keyboard.press('Shift+Slash');
  const help = page.locator('[data-gp-shortcuts]');
  await expect(help).toBeVisible();
  await expect(help.getByRole('heading', {name: 'Keyboard shortcuts'})).toBeVisible();
  await expect(help.locator('[data-gp-shortcut-row="c"]')).toContainText('Go to content');
  await page.keyboard.press('Escape');
  await expect(help).toBeHidden();

  await page.keyboard.press('/');
  await expect(page.locator('#gp-search')).toBeFocused();
  // Typing "g c" in a field stays in the field.
  await page.keyboard.type('gc');
  await expect(page).toHaveURL(/\/admin$/);
  await expect(page.locator('#gp-search')).toHaveValue('gc');
  await page.locator('#gp-search').fill('');
  await page.locator('#gp-search').blur();

  await page.keyboard.press('g');
  await page.keyboard.press('c');
  await expect(page).toHaveURL(/\/admin\/content\/post$/);

  // Ctrl+S saves the editor through its Save button (autosave untouched).
  const title = 'Shortcut save ' + randomUUID().slice(0, 8);
  const id = await draft(page, title);
  await page.goto('/admin/content/post/' + id);
  await page.fill('#title', title + ' saved');
  await page.keyboard.press('Control+s');
  await expect(page.locator('.admin-flash')).toBeVisible();
  await expect(page.locator('#title')).toHaveValue(title + ' saved');
  const database = db();
  expect((database.prepare('SELECT title FROM content WHERE id=?').get(id) as any).title).toBe(title + ' saved');
  database.close();
});

test('bulk publish asks first and reports each item, in English and Arabic', async ({page}, testInfo) => {
  await page.goto('/admin');
  await setLocale(page, 'en');
  const prefix = 'Bulk ' + randomUUID().slice(0, 8);
  const a = await draft(page, prefix + ' one');
  const b = await draft(page, prefix + ' two');

  await page.goto('/admin/content/post?search=' + encodeURIComponent(prefix));
  const form = page.locator('[data-content-selection]');
  await expect(form.locator('input[name=ids]')).toHaveCount(2);
  await form.locator('thead [data-select-all]').check();
  await expect(form.locator('[data-selection-count]')).toHaveText('2');
  await expect(form.locator('[data-bulk-scope]')).toContainText('only the items shown on this page');
  await form.locator('#bulk-action').selectOption('schedule');
  await expect(form.locator('#bulk-publish-at')).toBeVisible();
  await page.screenshot({path: testInfo.outputPath('bulk-bar-en.png'), animations: 'disabled'});
  await form.locator('#bulk-action').selectOption('publish');
  await expect(form.locator('#bulk-publish-at')).toBeHidden();
  await form.getByRole('button', {name: 'Apply'}).click();
  const confirm = page.locator('[aria-labelledby="gp-confirm-title"]');
  await expect(confirm).toBeVisible();
  await expect(confirm).toContainText('Publish 2 selected items now?');
  await confirm.getByRole('button', {name: 'Confirm'}).click();
  const result = page.locator('[data-bulk-result]');
  await expect(result).toBeVisible();
  await expect(result.locator('[data-bulk-done]')).toHaveText('2 published');
  await expect(page).toHaveURL(new RegExp('search=' + prefix.replace(' ', '\\+')));
  const database = db();
  for (const id of [a, b]) expect((database.prepare('SELECT status FROM content WHERE id=?').get(id) as any).status).toBe('published');
  database.close();
  expect(await noHorizontalScroll(page)).toBe(true);
  await page.screenshot({path: testInfo.outputPath('bulk-result-en.png'), animations: 'disabled'});

  // Arabic: one draft and one already published item; the summary names the skipped one.
  await setLocale(page, 'ar');
  const c = await draft(page, prefix + ' ثلاثة');
  await page.goto('/admin/content/post?search=' + encodeURIComponent(prefix));
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  await form.locator(`input[name=ids][value="${c}"]`).check();
  await form.locator(`input[name=ids][value="${a}"]`).check();
  await form.locator('#bulk-action').selectOption('publish');
  await form.locator('button[type=submit].bulk-bar-apply').click();
  await expect(confirm).toContainText('نشر العناصر المحددة (2) الآن؟');
  await confirm.locator('button').last().click();
  await expect(result.locator('[data-bulk-done]')).toHaveText('نُشر: 1');
  await expect(result.locator('[data-bulk-skipped]')).toHaveText('تُخطّي: 1');
  await expect(result).toContainText('في هذه الحالة أصلًا');
  await expect(result).toContainText(prefix + ' one');
  await page.screenshot({path: testInfo.outputPath('bulk-result-ar.png'), animations: 'disabled'});
  await page.setViewportSize({width: 390, height: 844});
  await page.reload();
  await form.locator(`input[name=ids][value="${c}"]`).check();
  await expect(form.locator('[data-selection-bar]')).toBeVisible();
  expect(await noHorizontalScroll(page)).toBe(true);
  await page.screenshot({path: testInfo.outputPath('bulk-bar-ar-390.png'), animations: 'disabled', fullPage: true});
});

test('the palette works in Arabic and at 390 px', async ({page}, testInfo) => {
  await page.goto('/admin');
  await setLocale(page, 'ar');
  const palette = page.locator('[data-gp-palette]');
  await page.keyboard.press('Control+k');
  await page.locator('#gp-palette-input').fill('اعدادات');
  await expect(palette.getByRole('option', {name: /^الإعدادات/}).first()).toBeVisible();
  await page.locator('#gp-palette-input').fill('settings');
  await expect(palette.getByRole('option', {name: /^الإعدادات/}).first()).toBeVisible(); // English label of an Arabic page
  await page.screenshot({path: testInfo.outputPath('palette-ar.png'), animations: 'disabled'});
  await page.keyboard.press('Escape');

  await page.keyboard.press('Shift+Slash');
  await expect(page.locator('[data-gp-shortcuts]').getByRole('heading', {name: 'اختصارات لوحة المفاتيح'})).toBeVisible();
  await page.screenshot({path: testInfo.outputPath('shortcuts-ar.png'), animations: 'disabled'});
  await page.keyboard.press('Escape');

  await page.setViewportSize({width: 390, height: 844});
  await page.reload();
  expect(await noHorizontalScroll(page)).toBe(true);
  await page.locator('[data-gp-palette-open]').click();
  await expect(palette).toBeVisible();
  await page.locator('#gp-palette-input').fill('محتوى');
  await expect(palette.getByRole('option').first()).toBeVisible();
  expect(await noHorizontalScroll(page)).toBe(true);
  await page.screenshot({path: testInfo.outputPath('palette-ar-390.png'), animations: 'disabled'});
  await page.keyboard.press('Escape');
  await setLocale(page, 'en');
});
