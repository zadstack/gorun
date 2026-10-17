// Arabic search normalisation (roadmap 1.4): a post titled with أحمد (alef with hamza),
// مُحَمَّد (harakat) and المدرسة (taa marbuta) is found by the bare spellings احمد / محمد /
// مدرسه on the public site in the Arabic UI (/search), its English translation (title
// still naming أحمد) in the English UI (/en/search), and both in the admin content search
// in both admin locales. Results show the stored title, not the folded text.
import { test, expect } from '@playwright/test';
import { expectNoServerError, setLocale } from '../lib/helpers.ts';

const TITLE = 'قصة أحمد ومُحَمَّد في المدرسة';
const TITLE_EN = 'The story of أحمد and مُحَمَّد at المدرسة';
const titles: Record<string, string> = { '': TITLE, '/en': TITLE_EN };

test.describe.serial('Arabic search normalisation', () => {
  test('publish a post with hamza, harakat and taa marbuta', async ({ page }) => {
    await page.goto('/admin/content/post/new');
    await page.fill('#title', TITLE);
    await page.locator('button[name="intent"][value="publish"]').click();
    await expect(page).toHaveURL(/\/admin\/content\/post\/[0-9A-Z]{26}$/);
    await expect(page.locator('#status')).toHaveValue('published');
    // English translation (the public /en site lists English and locale-less items only)
    await page.locator('button[name="locale"][value="en"][formaction$="/translations"]').click();
    await expect(page).toHaveURL(/\/admin\/content\/post\/[0-9A-Z]{26}$/);
    await page.fill('#title', TITLE_EN);
    await page.locator('button[name="intent"][value="publish"]').click();
    await expect(page.locator('#status')).toHaveValue('published');
    await expect(page.locator('#title')).toHaveValue(TITLE_EN);
  });

  for (const prefix of ['', '/en']) {
    test(`site search finds it without hamza or harakat (${prefix || '/ar'})`, async ({ browser }) => {
      const ctx = await browser.newContext({ storageState: { cookies: [], origins: [] } });
      const page = await ctx.newPage();
      try {
        for (const q of ['احمد', 'محمد', 'مدرسه']) {
          const resp = await page.goto(`${prefix}/search?q=${encodeURIComponent(q)}`);
          expect(resp?.status()).toBe(200);
          await expectNoServerError(page);
          await expect(page.locator('html')).toHaveAttribute('lang', prefix ? 'en' : 'ar');
          await expect(page.locator('body'), `query ${q}`).toContainText(titles[prefix]);
        }
        // the search form on the page submits the same way
        await page.locator('form[role="search"] input[name="q"]').first().fill('احمد');
        await page.locator('form[role="search"] input[name="q"]').first().press('Enter');
        await expect(page).toHaveURL(/[?&]q=/);
        await expect(page.locator('body')).toContainText(titles[prefix]);
        // an unrelated query does not
        await page.goto(`${prefix}/search?q=${encodeURIComponent('زرافة')}`);
        await expect(page.locator('body')).not.toContainText(titles[prefix]);
      } finally {
        await ctx.close();
      }
    });
  }

  test('admin content search finds it in both admin locales', async ({ page }) => {
    for (const locale of ['ar', 'en'] as const) {
      await setLocale(page, locale);
      await page.goto(`/admin/content/post?search=${encodeURIComponent('احمد')}`);
      await expect(page.locator('#content-list')).toContainText(TITLE);
      await expect(page.locator('#content-list')).toContainText(TITLE_EN);
    }
    await setLocale(page, 'ar');
  });
});
