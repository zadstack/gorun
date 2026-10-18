// Business theme (a catalog theme): activate it in Settings → General, then
// the public pages render (no 500 marker, RTL, the theme's stylesheet), the
// mobile layout has no horizontal scroll, and the contact form answers with
// its result page. Runs in the "theme" project, after "catalog": it changes every
// public page. Missing pages are created here so the spec also runs alone.
import { test, expect, type Page } from '@playwright/test';
import { MARKER_500 } from '../lib/helpers.ts';

const THEME = 'business';
const PAGES = ['/', '/shop', '/account', '/contact'];

async function ensurePage(page: Page, slug: string, title: string): Promise<void> {
  const r = await page.request.get(`/${slug}`);
  if (r.status() === 200) return;
  await page.goto('/admin/content/page/new');
  await page.fill('#title', title);
  await page.fill('#slug', slug);
  await page.locator('button[name="intent"][value="publish"]').click();
  await expect(page.locator('#status')).toHaveValue('published');
}

test.describe.serial('Business theme', () => {
  test('activate Business in Settings → General', async ({ page }) => {
    await page.goto('/admin/settings/general');
    await page.selectOption('#s-theme', THEME);
    await page.locator('form[action="/admin/settings/general"] button[type="submit"]').click();
    await expect(page.locator('[role="status"]').first()).toBeVisible();
    await expect(page.locator('#s-theme')).toHaveValue(THEME);
    await ensurePage(page, 'contact', 'تواصل معنا');
  });

  for (const path of PAGES) {
    test(`${path} renders with the theme (RTL, stylesheet, no 500)`, async ({ page }) => {
      const errors: string[] = [];
      page.on('pageerror', (e) => errors.push(e.message));
      const resp = await page.goto(path);
      expect(resp?.status(), `${path} status`).toBe(200);
      const html = await page.content();
      expect(html).not.toContain(MARKER_500);
      await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
      await expect(page.locator('html')).toHaveAttribute('data-theme', THEME);
      expect(html).toContain(`/themes/${THEME}/assets/site.css`);
      await expect(page.locator('main, #main').first()).toBeVisible();
      expect(errors, `${path} page errors`).toEqual([]);
    });
  }

  test.describe('mobile 375', () => {
    test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

    for (const path of PAGES) {
      test(`${path}: no horizontal scroll`, async ({ page }) => {
        await page.goto(path);
        await page.waitForLoadState('networkidle');
        const { scroll, client } = await page.evaluate(() => ({
          scroll: document.documentElement.scrollWidth,
          client: document.documentElement.clientWidth,
        }));
        expect(scroll, `${path} scrollWidth ${scroll} > clientWidth ${client}`).toBeLessThanOrEqual(client);
      });
    }
  });

  test('contact form submit shows the result page', async ({ page }) => {
    await page.goto('/contact');
    await page.fill('#cf-name', 'زائر تجريبي');
    await page.fill('#cf-email', 'visitor@e2e.test');
    await page.fill('#cf-message', 'رسالة تجريبية من مجموعة الاختبارات.');
    await page.locator('form[action="/forms/contact"] button[type="submit"]').click();
    await expect(page).toHaveURL(/\/contact/);
    // Business answers with the form-result template: a heading and a way back.
    await expect(page.locator('main h1')).toBeVisible();
    await expect(page.locator('main a[href="/contact"]')).toBeVisible();
    expect(await page.content()).not.toContain(MARKER_500);
  });
});

// Cheap smoke for the other catalog themes (catalog project already runs the
// full design QA). Activate each, hit home, assert shell + no 500, restore default.
test.describe.serial('Catalog theme home smoke', () => {
  for (const slug of ['magazine', 'portfolio', 'docs'] as const) {
    test(`${slug} home renders`, async ({ page }) => {
      await page.goto('/admin/settings/general');
      await page.selectOption('#s-theme', slug);
      await page.locator('form[action="/admin/settings/general"] button[type="submit"]').click();
      await expect(page.locator('#s-theme')).toHaveValue(slug);
      const resp = await page.goto('/');
      expect(resp?.status()).toBe(200);
      const html = await page.content();
      expect(html).not.toContain(MARKER_500);
      await expect(page.locator('html')).toHaveAttribute('data-theme', slug);
      await expect(page.locator('main, #main').first()).toBeVisible();
    });
  }

  test('restore default theme', async ({ page }) => {
    await page.goto('/admin/settings/general');
    await page.selectOption('#s-theme', 'default');
    await page.locator('form[action="/admin/settings/general"] button[type="submit"]').click();
    await expect(page.locator('#s-theme')).toHaveValue('default');
  });
});
