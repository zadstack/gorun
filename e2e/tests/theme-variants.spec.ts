// Browser check of the single/archive design variants of every catalog theme
// that declares them: Arabic and English, 1280 and 375 px, a 120-character
// title and a 3 000-word body, a post without a featured image and one with
// a wide image, keyboard reachability of the list links and no horizontal
// overflow. Full-page screenshots go to outputs/theme-variants.
// Runs in the "catalog" project (after "chromium"); restores the default
// theme and clears the variant settings at the end.
import { test, expect, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { MARKER_500, db, setContentLocale } from '../lib/helpers.ts';

const WIDTHS = [1280, 375];
// What each theme declares in theme.json (supports); "native" is implicit.
const DECLARED: Record<string, { single: string[]; archive: string[] }> = {
  default: { single: ['native', 'wide', 'centered'], archive: ['native', 'grid', 'compact'] },
  business: { single: ['native', 'wide', 'centered'], archive: ['native', 'compact'] },
  magazine: { single: ['native', 'wide', 'centered'], archive: ['native', 'grid', 'compact'] },
  portfolio: { single: ['native', 'wide', 'centered'], archive: ['native', 'compact'] },
};
const LONG_TITLE: Record<string, string> = {
  en: ('A deliberately long headline that wraps on narrow screens and tests the measure ').repeat(3).slice(0, 120),
  ar: 'عنوان طويل جدًا يلتف على الشاشات الضيقة ويختبر عرض السطر والمحاذاة '.repeat(3).slice(0, 120),
};
const WORD: Record<string, string> = { en: 'Infrastructure', ar: 'البنية' };

function longBody(locale: string): string {
  let out = '';
  for (let i = 0; i < 60; i++) {
    if (i % 10 === 0) out += `<h2>${WORD[locale]} ${i}</h2>`;
    out += '<p>' + (WORD[locale] + ' ').repeat(50).trim() + '</p>';
  }
  return out;
}

const wideSVG = () => Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="400" viewBox="0 0 1600 400"><rect width="1600" height="400" fill="#c2410c"/><circle cx="300" cy="200" r="120" fill="#fffdf8"/></svg>`);

async function activate(page: Page, slug: string): Promise<void> {
  await page.goto('/admin/settings/general');
  await page.selectOption('#s-theme', slug);
  await page.locator('form[action="/admin/settings/general"] button[type="submit"]').click();
  await expect(page.locator('#s-theme')).toHaveValue(slug);
}

function setSetting(key: string, value: string | null): void {
  const database = db();
  if (value === null) database.prepare('DELETE FROM settings WHERE key=?').run(key);
  else database.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at').run(key, JSON.stringify(value), new Date().toISOString());
  database.close();
}

// createPost publishes an empty post through the editor, then gives it the
// long title/body (and the featured image) directly, the way the import
// path stores them.
async function createPost(page: Page, locale: 'ar' | 'en', slug: string, mediaID: string | null): Promise<string> {
  await page.goto('/admin/content/post/new');
  await setContentLocale(page, locale);
  await page.fill('#title', 'Variant ' + slug);
  await page.locator('button[name="intent"][value="publish"]').click();
  await expect(page).toHaveURL(/\/admin\/content\/post\/[0-9A-Z]{26}$/);
  const id = new URL(page.url()).pathname.split('/').pop()!;
  const database = db();
  database.prepare('UPDATE content SET title=?, slug=?, body=?, featured_media_id=?, excerpt=? WHERE id=?').run(LONG_TITLE[locale], slug, longBody(locale), mediaID, 'Short summary for cards.', id);
  database.close();
  return slug;
}

async function uploadWide(page: Page): Promise<string> {
  const filename = 'variants-wide-' + randomUUID().slice(0, 8) + '.svg';
  await page.goto('/admin/media');
  await page.locator('#media-file').setInputFiles({ name: filename, mimeType: 'image/svg+xml', buffer: wideSVG() });
  await expect(page.locator('[data-media-items] a').filter({ hasText: filename.replace('.svg', '') })).toBeVisible();
  const database = db();
  const row = database.prepare('SELECT id FROM media WHERE filename LIKE ? ORDER BY created_at DESC LIMIT 1').get('%' + filename.replace('.svg', '') + '%') as any;
  database.close();
  if (!row) throw new Error('uploaded media not found');
  return row.id as string;
}

async function checkPage(page: Page, path: string, attr: string, want: string, width: number, shot: string): Promise<number> {
  await page.setViewportSize({ width, height: width < 700 ? 844 : 900 });
  const resp = await page.goto(path);
  expect(resp?.status(), path).toBe(200);
  const html = await page.content();
  expect(html, path).not.toContain(MARKER_500);
  await expect(page.locator(`[${attr}]`).first(), path).toHaveAttribute(attr, want);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${path} overflows at ${width}`).toBe(true);
  // Every image on the page has a source and an alt attribute.
  for (const img of await page.locator('img').all()) {
    expect(await img.getAttribute('src'), 'img src').toBeTruthy();
    expect(await img.getAttribute('alt'), 'img alt').not.toBeNull();
  }
  await page.screenshot({ path: test.info().outputPath(shot + '.png'), fullPage: true });
  // The reading measure is what single variants change; the wrapper may be fixed-width (magazine).
  const measured = attr === 'data-variant-single' ? page.locator(`[${attr}] .prose-gp`).first() : page.locator(`[${attr}]`).first();
  const box = await measured.boundingBox();
  return box ? box.width : 0;
}

// keyboardReaches tabs through the page until focus lands inside sel.
async function keyboardReaches(page: Page, sel: string): Promise<void> {
  await page.keyboard.press('Tab');
  for (let i = 0; i < 60; i++) {
    const inside = await page.evaluate((s) => !!document.activeElement && !!document.activeElement.closest(s) && document.activeElement.tagName === 'A', sel);
    if (inside) return;
    await page.keyboard.press('Tab');
  }
  throw new Error('keyboard never reached a link inside ' + sel);
}

test.describe.serial('design variants', () => {
  const slugs: Record<string, { plain: string; image: string }> = {};
  let mediaID = '';

  test('fixtures: long posts with and without a wide image', async ({ page }) => {
    mediaID = await uploadWide(page);
    for (const locale of ['ar', 'en'] as const) {
      const tag = randomUUID().slice(0, 6);
      slugs[locale] = {
        plain: await createPost(page, locale, `variant-${locale}-plain-${tag}`, null),
        image: await createPost(page, locale, `variant-${locale}-image-${tag}`, mediaID),
      };
    }
  });

  for (const [theme, declared] of Object.entries(DECLARED)) {
    test(`${theme}: every declared single and archive variant`, async ({ page }) => {
      test.setTimeout(240_000);
      await activate(page, theme);
      const articleWidth: Record<string, number> = {};
      for (const variant of [...declared.single, 'bogus']) {
        setSetting(`theme.${theme}.single_style`, variant);
        const want = declared.single.includes(variant) ? variant : 'native';
        for (const locale of ['ar', 'en'] as const) {
          const prefix = locale === 'en' ? '/en' : '';
          for (const width of WIDTHS) {
            for (const kind of ['plain', 'image'] as const) {
              const w = await checkPage(page, `${prefix}/post/${slugs[locale][kind]}`, 'data-variant-single', want, width, `${theme}-single-${variant}-${kind}-${locale}-${width}`);
              await expect(page.locator('h1').first()).toContainText(LONG_TITLE[locale].slice(0, 20));
              articleWidth[`${want}-${locale}-${width}-${kind}`] = w;
            }
          }
        }
      }
      // "wide" is a real layout change on desktop: the reading measure is measurably wider than native.
      if (declared.single.includes('wide')) {
        for (const locale of ['ar', 'en'] as const) {
          expect(articleWidth[`wide-${locale}-1280-plain`], `${theme} wide ${locale}`).toBeGreaterThan(articleWidth[`native-${locale}-1280-plain`] + 40);
        }
      }
      setSetting(`theme.${theme}.single_style`, null);
      for (const variant of [...declared.archive, 'bogus']) {
        setSetting(`theme.${theme}.archive_style`, variant);
        const want = declared.archive.includes(variant) ? variant : 'native';
        for (const locale of ['ar', 'en'] as const) {
          const prefix = locale === 'en' ? '/en' : '';
          for (const width of WIDTHS) {
            await checkPage(page, `${prefix}/blog`, 'data-variant-archive', want, width, `${theme}-archive-${variant}-${locale}-${width}`);
            await expect(page.locator('[data-variant-archive] a[href*="/post/"]').first()).toBeVisible();
            await keyboardReaches(page, '[data-variant-archive]');
          }
        }
      }
      setSetting(`theme.${theme}.archive_style`, null);
    });
  }

  test('restore the default theme', async ({ page }) => {
    await activate(page, 'default');
  });
});
