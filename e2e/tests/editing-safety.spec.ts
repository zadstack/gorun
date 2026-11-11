// Editing safety (docs/EDITING_SAFETY.md): autosave with restore and
// conflicts, revision compare, shareable preview links.
import { test, expect, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { db, setLocale } from '../lib/helpers.ts';

async function createPost(page: Page, title: string, body = ''): Promise<string> {
  await page.goto('/admin/content/post/new');
  await page.fill('#title', title);
  if (body) {
    await page.locator('[data-field-type=richtext] .ql-editor').click();
    await page.keyboard.type(body);
  }
  await page.locator('button[name=intent][value=save]').click();
  await expect(page).toHaveURL(/\/admin\/content\/post\/[^/]+$/);
  return new URL(page.url()).pathname.split('/').pop()!;
}

function removeItem(id: string): void {
  const database = db();
  database.prepare('DELETE FROM content WHERE id=?').run(id);
  database.close();
}

test('autosave keeps the item, restores on reopen and stops on a conflict', async ({ page }, testInfo) => {
  await page.goto('/admin');
  await setLocale(page, 'en');
  const title = 'Autosave ' + randomUUID();
  const id = await createPost(page, title);
  const edit = '/admin/content/post/' + id;
  try {
    const status = page.locator('[data-autosave-status]');
    await expect(status).toHaveAttribute('data-state', 'idle');
    await page.fill('#title', title + ' (autosaved)');
    await expect(status).toHaveAttribute('data-state', 'saved', { timeout: 15_000 });
    await expect(status.locator('time')).toHaveAttribute('dir', 'ltr');
    const database = db();
    expect((database.prepare('SELECT title FROM content WHERE id=?').get(id) as any).title).toBe(title);
    expect((database.prepare('SELECT COUNT(*) AS n FROM content_autosaves WHERE content_id=?').get(id) as any).n).toBe(1);
    database.close();

    // Reopen: the banner offers the autosaved text; nothing is lost when leaving.
    await page.goto(edit);
    const banner = page.locator('[data-autosave-banner]');
    await expect(banner).toBeVisible();
    await expect(page.locator('#title')).toHaveValue(title);
    await expect(banner.locator('time')).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('autosave-banner-en.png'), animations: 'disabled' });
    // Arabic at phone width: right to left, no horizontal scroll.
    await setLocale(page, 'ar');
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator('[data-autosave-banner]')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
    await page.screenshot({ path: testInfo.outputPath('autosave-banner-ar-390.png'), animations: 'disabled', fullPage: true });
    await setLocale(page, 'en');
    await page.setViewportSize({ width: 1280, height: 900 });
    await banner.getByRole('button', { name: 'Restore autosaved changes' }).click();
    await expect(page.locator('#title')).toHaveValue(title + ' (autosaved)');
    await expect(banner.locator('[data-autosave-restored]')).toBeVisible();
    await page.locator('button[name=intent][value=save]').click();
    await expect(page.locator('[data-autosave-banner]')).toHaveCount(0);
    await expect(page.locator('#title')).toHaveValue(title + ' (autosaved)');
    const after = db();
    expect((after.prepare('SELECT COUNT(*) AS n FROM content_autosaves WHERE content_id=?').get(id) as any).n).toBe(0);

    // Someone else saves: the next autosave is refused and the text stays.
    await page.goto(edit);
    after.prepare("UPDATE content SET title=?, updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now','+1 second') WHERE id=?").run(title + ' (other editor)', id);
    after.close();
    await page.fill('#title', title + ' (mine)');
    await expect(status).toHaveAttribute('data-state', 'conflict', { timeout: 15_000 });
    await expect(status).toContainText('someone saved a newer version');
    await expect(page.locator('#title')).toHaveValue(title + ' (mine)');
    const check = db();
    expect((check.prepare('SELECT title FROM content WHERE id=?').get(id) as any).title).toBe(title + ' (other editor)');
    expect((check.prepare('SELECT COUNT(*) AS n FROM content_autosaves WHERE content_id=?').get(id) as any).n).toBe(0);
    check.close();
  } finally {
    removeItem(id);
  }
});

test('revision compare shows field and body changes with text labels', async ({ page }, testInfo) => {
  await page.goto('/admin');
  await setLocale(page, 'en');
  const title = 'Compare ' + randomUUID();
  const id = await createPost(page, title, 'First paragraph text');
  try {
    await page.fill('#title', title + ' v2');
    await page.locator('[data-field-type=richtext] .ql-editor').click();
    await page.keyboard.press('End');
    await page.keyboard.type(' changed');
    await page.locator('button[name=intent][value=save]').click();
    await expect(page.locator('#title')).toHaveValue(title + ' v2');
    // The compare link sits before the restore button of each revision.
    const oldest = page.locator('[data-revision-compare-link]').last();
    await expect(oldest.locator('xpath=following-sibling::button')).toHaveCount(1);
    await oldest.click();
    await expect(page).toHaveURL(/\/revisions\/[^/]+\/compare$/);
    const titleRow = page.locator('[data-diff-field=title]');
    await expect(titleRow.locator('ins')).toContainText('v2');
    await expect(titleRow.locator('ins .sr-only')).toHaveText('Added:');
    await expect(page.locator('[data-diff-body] [data-op=change] ins')).toContainText('changed');
    await page.screenshot({ path: testInfo.outputPath('revision-compare-en.png'), animations: 'disabled', fullPage: true });
    await setLocale(page, 'ar');
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator('[data-diff-body]')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
    await page.screenshot({ path: testInfo.outputPath('revision-compare-ar-390.png'), animations: 'disabled', fullPage: true });
    await setLocale(page, 'en');
    await page.setViewportSize({ width: 1280, height: 900 });
  } finally {
    removeItem(id);
  }
});

test('a preview link shows a draft to a visitor without an account until it is revoked', async ({ page, browser }, testInfo) => {
  await page.goto('/admin');
  await setLocale(page, 'en');
  const title = 'Preview ' + randomUUID();
  const id = await createPost(page, title, 'Embargoed paragraph');
  try {
    const panel = page.locator('[data-preview-links]');
    await expect(panel.locator('[data-preview-empty]')).toBeVisible();
    await expect(panel.locator('[name=preview_ttl]')).toHaveValue('24h');
    await panel.locator('[name=preview_ttl]').selectOption('1h');
    // Choosing a link lifetime is not an edit of the item.
    await expect(page.locator('[data-save-state]')).toHaveText('Changes save only when you choose a save or publish action.');
    await panel.locator('[data-preview-create]').click();
    const urlField = panel.locator('[data-preview-url]');
    await expect(urlField).toHaveValue(/\/preview\/[A-Za-z0-9_-]{43}$/);
    await expect(panel.locator('[data-preview-list] li')).toHaveCount(1);
    await expect(panel.locator('[data-preview-list] li time')).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('preview-links-editor.png'), animations: 'disabled', fullPage: true });
    const link = new URL(await urlField.inputValue());

    // A fresh context: no session cookie, like someone the link was sent to.
    const visitor = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    try {
      const guest = await visitor.newPage();
      const resp = await guest.goto(link.pathname);
      expect(resp!.status()).toBe(200);
      expect(resp!.headers()['x-robots-tag']).toBe('noindex, nofollow');
      expect(resp!.headers()['cache-control']).toBe('private, no-store');
      expect(resp!.headers()['referrer-policy']).toBe('no-referrer');
      await expect(guest.locator('[data-gorun-preview]')).toBeVisible();
      await expect(guest.getByText('Embargoed paragraph')).toBeVisible();
      await guest.setViewportSize({ width: 390, height: 844 });
      expect(await guest.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
      await guest.screenshot({ path: testInfo.outputPath('preview-visitor-390.png'), animations: 'disabled' });

      // Revoke from the editor: the same URL is now a plain 404.
      await panel.locator('[data-preview-revoke]').click();
      const modal = page.locator('[role="dialog"][aria-labelledby="gp-confirm-title"]');
      await expect(modal).toBeVisible();
      await modal.locator('button[\\@click="$store.confirm.yes()"]').click();
      await expect(panel.locator('[data-preview-list] li')).toHaveCount(0);
      const gone = await guest.goto(link.pathname);
      expect(gone!.status()).toBe(404);
      await expect(guest.getByText('Embargoed paragraph')).toHaveCount(0);
      const unknown = await guest.goto('/preview/' + 'A'.repeat(43));
      expect(unknown!.status()).toBe(404);
    } finally {
      await visitor.close();
    }
  } finally {
    removeItem(id);
  }
});
