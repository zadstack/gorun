// Admin journey: (first admin + login run in global.setup.ts) → content type
// "Case Study" with 5 field kinds → Arabic content with fields → publish →
// public page (RTL, title) → revisions → trash → restore. Both admin locales.
import { test, expect, type Page } from '@playwright/test';
import { ADMIN, expectNoServerError, setLocale } from '../lib/helpers.ts';
import { tinyPNG } from '../fixtures/wp-fixture-server.ts';

const TYPE = 'case_study';
const TITLE_AR = 'دراسة حالة: منصة زاد السحابية';

type FieldSpec = { label: string; key: string; type: string; options?: string };
const FIELDS: FieldSpec[] = [
  { label: 'Client', key: 'client', type: 'text' },
  { label: 'Budget', key: 'budget', type: 'number' },
  { label: 'Featured', key: 'featured', type: 'boolean' },
  { label: 'Sector', key: 'sector', type: 'select', options: 'gov | Government | حكومي\nretail | Retail | تجزئة' },
  { label: 'Cover', key: 'cover', type: 'image' },
];

async function addField(page: Page, i: number, f: FieldSpec): Promise<void> {
  await page.locator('button[\\@click="add(fields)"]').click();
  // key first: blurring the label auto-fills an empty key (keyify), which
  // would race with an immediate fill of the key input
  await page.locator(`#f-${i}-key`).fill(f.key);
  await page.locator(`#f-${i}-label-en`).fill(f.label);
  await page.locator(`#f-${i}-label-ar`).fill(f.label + ' (ع)');
  await page.locator(`#f-${i}-type`).selectOption(f.type);
  if (f.options) await page.locator(`#f-${i}-options`).fill(f.options);
}

test.describe.serial('admin setup and content', () => {
  test('login form works in English too, with LTR chrome', async ({ browser }) => {
    const ctx = await browser.newContext({ locale: 'en', storageState: { cookies: [], origins: [] } });
    const page = await ctx.newPage();
    await page.goto('/admin/login');
    // anonymous visitors get their browser language (Accept-Language: en)
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
    await page.fill('input[name="email"]', ADMIN.email);
    await page.fill('input[name="password"]', 'wrong-password-123');
    await page.locator('form[action^="/admin/login"] button[type="submit"]').click();
    await expect(page.locator('[role="alert"]').first()).toBeVisible();
    await page.fill('input[name="password"]', ADMIN.password);
    await page.locator('form[action^="/admin/login"] button[type="submit"]').click();
    await expect(page).toHaveURL(/\/admin\/?$/);
    await setLocale(page, 'en');
    await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
    await page.goto('/admin/types');
    await expect(page.locator('.admin-section-pages a[href="/admin/types"]').first()).toContainText(/Content types/i);
    await setLocale(page, 'ar'); // the user's locale is stored on the account: restore it
    await ctx.close();
  });

  test('create the "Case Study" type with text, number, boolean, select and image fields', async ({ page }) => {
    await page.goto('/admin/types/new');
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await page.fill('#slug', TYPE);
    await page.fill('#name_ar', 'دراسات الحالة');
    await page.fill('#name_en', 'Case Studies');
    await page.fill('#singular_ar', 'دراسة حالة');
    await page.fill('#singular_en', 'Case Study');
    await page.fill('#plural_ar', 'دراسات الحالة');
    await page.fill('#plural_en', 'Case Studies');
    await page.check('input[name="has_archive"]');
    for (const [i, f] of FIELDS.entries()) await addField(page, i, f);
    const json = JSON.parse((await page.locator('input[name="fields_json"]').inputValue()) || '[]');
    expect(json.map((f: { type: string }) => f.type)).toEqual(['text', 'number', 'boolean', 'select', 'image']);
    expect(json.map((f: { key: string }) => f.key)).toEqual(FIELDS.map((f) => f.key));
    await page.locator('form[action="/admin/types"] button[type="submit"]').last().click();
    await expect(page).toHaveURL(new RegExp(`/admin/types/${TYPE}$`));
    await expect(page.locator('[role="status"]').first()).toBeVisible();
    // the dashboard's content-counts widget lists it under the admin's language
    await page.goto('/admin');
    const counts = page.locator(`#content-counts a[href="/admin/content/${TYPE}"]`);
    await expect(counts).toContainText('دراسات الحالة');
    await setLocale(page, 'en');
    await expect(counts).toContainText('Case Studies');
    await setLocale(page, 'ar');
  });

  // BUG-01 (fixed): the content-type sidebar entries come from a nav
  // provider evaluated per request, so a type created at runtime is listed.
  test('BUG-01 sidebar links a content type created at runtime', async ({ page }) => {
    await page.goto('/admin/content/post');
    // Built-in and new runtime types share the contextual Content tabs
    await expect(page.locator('.admin-section-pages a[href="/admin/content/post"]')).toHaveCount(1);
    await expect(page.locator(`.admin-section-pages a[href="/admin/content/${TYPE}"]`)).toHaveCount(1, { timeout: 2_000 });
  });

  test('create Arabic content with fields, publish, see it public (RTL) and in revisions', async ({ page }) => {
    await page.goto(`/admin/content/${TYPE}/new`);
    await page.fill('#title', TITLE_AR);
    await page.locator('[data-field-key="body"] .ql-editor').click();
    await page.keyboard.type('نص تجريبي عن المنصة.');
    await page.fill('input[name="fields[client]"]', 'وزارة تجريبية');
    await page.fill('input[name="fields[budget]"]', '125000');
    await page.check('input[type="checkbox"][name="fields[featured]"]');
    await page.selectOption('select[name="fields[sector]"]', 'gov');

    // image field: upload a PNG inside the media picker, then pick it
    await page.locator('button[data-media-open][data-target="fields-cover"]').click();
    const picker = page.locator('#gp-media-picker-body');
    await expect(picker.locator('input[type="file"][name="file"]')).toBeAttached();
    // the upload form has no action: wait until htmx has bound it, or the
    // change handler's requestSubmit() would post to the editor URL
    await expect
      .poll(() => page.evaluate(() => !!(document.querySelector('#gp-media-picker-body form[hx-post]') as any)?.['htmx-internal-data']))
      .toBe(true);
    await picker.locator('input[type="file"][name="file"]').setInputFiles({ name: 'cover-e2e.png', mimeType: 'image/png', buffer: tinyPNG(64, 48) });
    const item = picker.locator('button[data-id]').first();
    await expect(item).toBeVisible();
    await item.click();
    await expect(page.locator('#fields-cover')).not.toHaveValue('');

    await page.locator('button[name="intent"][value="publish"]').click();
    await expect(page).toHaveURL(new RegExp(`/admin/content/${TYPE}/[0-9A-Z]{26}$`));
    await expect(page.locator('#status')).toHaveValue('published');
    await expect(page.locator('input[name="fields[budget]"]')).toHaveValue('125000');
    await expect(page.locator('select[name="fields[sector]"]')).toHaveValue('gov');
    const slug = await page.locator('#slug').inputValue();
    expect(slug).toMatch(/[؀-ۿ]/); // Arabic slug kept

    // edit once more so the revision list has history
    await page.fill('input[name="fields[client]"]', 'وزارة تجريبية ٢');
    await page.locator('button[name="intent"][value="save"]').click();
    const revisions = page.locator('button[formaction*="/revisions/"]');
    await expect(revisions.first()).toBeVisible();
    expect(await revisions.count()).toBeGreaterThanOrEqual(1);

    // public page
    const pub = await page.context().newPage();
    const resp = await pub.goto(`/${TYPE}/${encodeURIComponent(slug)}`);
    expect(resp?.status()).toBe(200);
    await expect(pub.locator('html')).toHaveAttribute('dir', 'rtl');
    await expect(pub.locator('h1')).toContainText(TITLE_AR);
    await expect(pub).toHaveTitle(new RegExp(TITLE_AR.slice(0, 8)));
    await expectNoServerError(pub);
    await pub.close();
  });

  test('trash then restore', async ({ page }) => {
    await page.goto(`/admin/content/${TYPE}`);
    await page.locator('#content-list a[href*="/admin/content/"]').filter({ hasText: TITLE_AR }).first().click();
    const editURL = page.url();
    const slug = await page.locator('#slug').inputValue();
    await page.locator('button[formaction$="/trash"]').click();
    await expect(page).toHaveURL(new RegExp(`/admin/content/${TYPE}$`));

    // gone from the public site
    const gone = await page.request.get(`/${TYPE}/${encodeURIComponent(slug)}`);
    expect(gone.status()).toBe(404);

    // listed under the trash filter
    await page.goto(`/admin/content/${TYPE}?status=trash`);
    await expect(page.locator('#content-list')).toContainText(TITLE_AR);

    // restore: back to draft from the editor, then publish again
    await page.goto(editURL);
    await expect(page.locator('#status')).toHaveValue('trash');
    await page.selectOption('#status', 'draft');
    await page.locator('button[name="intent"][value="save"]').click();
    await expect(page.locator('#status')).toHaveValue('draft');
    await page.locator('button[name="intent"][value="publish"]').click();
    await expect(page.locator('#status')).toHaveValue('published');
    const back = await page.request.get(`/${TYPE}/${encodeURIComponent(slug)}`);
    expect(back.status()).toBe(200);
  });
});
