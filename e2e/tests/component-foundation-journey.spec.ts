import { test, expect, type BrowserContext, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { csrf, env, expectNoServerError } from '../lib/helpers.ts';

// One isolated synthetic collection proves the composition, not every variant.
// Requests seed fixtures; the anonymous browser performs the whole public flow.
test.describe.configure({ mode: 'serial' });
const suffix = randomUUID().slice(0, 8);
const field = `audience_${suffix}`;
const query = `مسار${suffix}`;
const slug = `foundation-${suffix}`;
let admin: BrowserContext;
let editor: Page;
let token = '';
let originalType: any;
let originalTheme = '';
let directoryID = '';
const created: string[] = [];

async function create(input: Record<string, unknown>) {
  const response = await editor.request.post('/api/v1/content', {
    headers: { 'X-CSRF-Token': token }, data: input,
  });
  expect(response.status()).toBe(201);
  const item = (await response.json()).data;
  created.push(item.id);
  return item;
}

async function activate(theme: string) {
  const response = await editor.request.post(`/admin/appearance/themes/${theme}/activate`, {
    headers: { 'X-CSRF-Token': token }, maxRedirects: 0,
  });
  expect(response.status()).toBe(303);
}

async function noOverflow(page: Page) {
  const bounds = await page.evaluate(() => ({ width: innerWidth, actual: document.documentElement.scrollWidth }));
  expect(bounds.actual).toBeLessThanOrEqual(bounds.width + 1);
}

test.beforeAll(async ({ browser }) => {
  admin = await browser.newContext({ baseURL: env('E2E_BASE_URL'), storageState: '.auth/admin.json' });
  editor = await admin.newPage();
  await editor.goto('/admin/settings/general');
  token = await csrf(editor);
  originalTheme = await editor.locator('#s-theme').inputValue();
  await activate('default');
  const typeResponse = await editor.request.get('/api/v1/types/page');
  expect(typeResponse.status()).toBe(200);
  originalType = (await typeResponse.json()).data;
  // Restore this complete schema in afterAll; do not leave a global field behind.
  const configured = await editor.request.post('/api/v1/types', {
    headers: { 'X-CSRF-Token': token },
    data: {
      ...originalType,
      templates: [...new Set([...(originalType.templates ?? []), 'public-detail'])],
      fields: [...(originalType.fields ?? []), {
        key: field, type: 'select', indexed: true,
        label: { ar: 'الجمهور التجريبي', en: 'Fixture audience' },
        options: [{ value: 'team', label: { ar: 'للفريق', en: 'Team' } }, { value: 'solo', label: { ar: 'للأفراد', en: 'Solo' } }],
      }],
    },
  });
  expect(configured.status()).toBe(201);
  for (let i = 0; i < 25; i++) {
    await create({ type: 'page', slug: `foundation-team-${suffix}-${i}`, locale: 'ar',
      title: `${query} دليل للفريق ${i}`, status: 'published', template: 'public-detail',
      body: '<p>تفاصيل السجل المنشور والخطوة التالية.</p>', fields: { [field]: 'team' } });
  }
  await create({ type: 'page', slug: `foundation-solo-${suffix}`, locale: 'ar', title: `${query} فردي`, status: 'published', template: 'public-detail', fields: { [field]: 'solo' } });
  await create({ type: 'page', slug: `foundation-draft-${suffix}`, locale: 'ar', title: `${query} مسودة مخفية`, status: 'draft', fields: { [field]: 'team' } });
  await create({ type: 'page', slug: `foundation-english-${suffix}`, locale: 'en', title: `${query} English hidden`, status: 'published', fields: { [field]: 'team' } });
  const directory = await create({ type: 'page', slug, locale: 'ar', title: 'دليل رحلة المكوّنات', status: 'published' });
  directoryID = directory.id;
  const draft = (await (await editor.request.get(`/api/v1/builder/${directoryID}`)).json()).data;
  const saved = await editor.request.put(`/api/v1/builder/${directoryID}/draft`, {
    headers: { 'X-CSRF-Token': token },
    data: { base_updated_at: draft.updated_at, document: { version: 1, sections: [
      { id: 'entry', library: 'sections', kind: 'task_entry', variant: 'search_first', title: 'اختر ما تحتاجه',
        native: { search: { action: `/${slug}`, label: 'ابحث في الدليل', param: 'q', placeholder: 'اكتب كلمة البحث' } } },
      { id: 'collection', library: 'sections', kind: 'directory', variant: 'compact', title: 'النتائج', native: { list_type: 'page' } },
    ] } },
  });
  expect(saved.status()).toBe(200);
  const published = await editor.request.post(`/api/v1/builder/${directoryID}/publish`, {
    headers: { 'X-CSRF-Token': token }, data: { updated_at: (await saved.json()).data.updated_at },
  });
  expect(published.status()).toBe(200);
});

test.afterAll(async () => {
  try {
    if (!editor) return;
    // Delete only this spec's records, even after a failed browser assertion.
    for (const id of created.reverse()) {
      const response = await editor.request.delete(`/api/v1/content/${id}?hard=1`, { headers: { 'X-CSRF-Token': token } });
      expect(response.ok()).toBeTruthy();
    }
  } finally {
    try {
      if (originalType) {
        const restored = await editor.request.post('/api/v1/types', { headers: { 'X-CSRF-Token': token }, data: originalType });
        expect(restored.status()).toBe(201);
      }
      if (originalTheme) await activate(originalTheme);
    } finally { await admin?.close(); }
  }
});

test('RTL mobile discovery retains filters and paging through published details and back', async ({ browser }, info) => {
  const context = await browser.newContext({ baseURL: env('E2E_BASE_URL'), storageState: { cookies: [], origins: [] }, viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  try {
    await page.goto(`/${slug}`);
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    const task = page.locator('.gp-task-search-form');
    await expect(task).toHaveAttribute('method', 'get');
    await expect(task.getByLabel('ابحث في الدليل')).toBeVisible();
    const css = page.locator('link[rel=stylesheet][href^="/static/site/native-ui.css"]');
    await expect(css).toHaveCount(1);
    expect((await page.request.get((await css.getAttribute('href'))!)).status()).toBe(200);
    await task.getByLabel('ابحث في الدليل').fill(query);
    const searched = page.waitForRequest(request => new URL(request.url()).pathname === `/${slug}` && new URL(request.url()).searchParams.get('q') === query);
    await task.getByRole('button', { name: 'بحث', exact: true }).click();
    expect((await searched).method()).toBe('GET');
    const collection = page.locator('.gp-s-directory');
    await expect(collection.locator('.gp-s-dir-count')).toContainText('26');
    await expect(collection).not.toContainText('مسودة مخفية');
    await expect(collection).not.toContainText('English hidden');
    await collection.locator('.gp-filter-link').filter({ hasText: 'للفريق' }).click();
    await expect.poll(() => new URL(page.url()).searchParams.get(field)).toBe('team');
    await expect(collection.locator('.gp-s-dir-count')).toContainText('25');
    await collection.locator('.gp-s-dir-pagination a[rel=next]').click();
    await expect.poll(() => new URL(page.url()).searchParams.get('page')).toBe('2');
    expect(new URL(page.url()).searchParams.get('q')).toBe(query);
    expect(new URL(page.url()).searchParams.get(field)).toBe('team');
    await expect(collection.locator('article')).toHaveCount(1);
    const result = collection.locator('article h2 a').first();
    const recordTitle = await result.textContent();
    const destination = new URL((await result.getAttribute('href'))!, page.url());
    const returnURL = new URL(destination.searchParams.get('back')!, page.url());
    expect(returnURL.pathname).toBe(`/${slug}`);
    expect(returnURL.searchParams.get('q')).toBe(query);
    expect(returnURL.searchParams.get(field)).toBe('team');
    expect(returnURL.searchParams.get('page')).toBe('2');
    await result.click();
    await expect(page.locator('.gp-public-detail h1')).toHaveText(recordTitle!);
    await expect(page.locator('link[href*="public-ui.css"]')).toHaveCount(1);
    const full = page.locator('.gp-public-detail footer a[href="#public-content"]');
    await full.focus(); await full.press('Enter');
    await expect(page.locator('#public-content')).toBeFocused();
    await page.locator('.gp-public-detail > a.gp-public-action').click();
    expect(new URL(page.url()).searchParams.get('page')).toBe('2');
    expect(new URL(page.url()).searchParams.get('q')).toBe(query);
    expect(new URL(page.url()).searchParams.get(field)).toBe('team');
    await expect(collection.locator('article')).toHaveCount(1);
    await noOverflow(page);
    await page.screenshot({ path: info.outputPath('foundation-discovery-mobile.png'), fullPage: true });
    await expectNoServerError(page);
    expect(errors).toEqual([]);
  } finally { await context.close(); }

  // The sandbox shares layout CSS but remains unable to submit the GET form.
  await editor.goto(`/admin/builder/${directoryID}`);
  const preview = editor.frameLocator('#builder-preview');
  await expect(preview.locator('.gp-task-search-form')).toBeVisible();
  await expect(preview.locator('meta[http-equiv="Content-Security-Policy"]')).toHaveAttribute('content', /form-action 'none'/);
  const inlineStyles = await preview.locator('style').allTextContents();
  expect(inlineStyles.join('\n')).toContain('.gp-task-search form');
  await expect(preview.locator('link[href^="/static/site/native-ui.css"]')).toHaveCount(0);
});
