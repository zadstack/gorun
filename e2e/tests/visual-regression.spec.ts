// Visual regression: pixel baselines of the admin components page
// (/admin/design/components) and of the shared site pages, so a change to
// the tokens, to Basecoat or to the stylesheets shows up as a diff instead of
// a surprise. Baselines are Linux renders (the CI runner's fonts and
// Chromium): on another OS the spec skips unless E2E_VISUAL=1. Creating or
// updating them: docs/E2E.md, "Visual baselines".
import { test, expect, type Page } from '@playwright/test';
import { expectNoServerError, setLocale } from '../lib/helpers.ts';

const SLUG = 'e2e-visual-review';

test.skip(process.platform !== 'linux' && !process.env.E2E_VISUAL, 'baselines are Linux renders; set E2E_VISUAL=1 to run elsewhere');

async function activate(page: Page, slug: string): Promise<void> {
  await page.goto('/admin/settings/general');
  await page.selectOption('#s-theme', slug);
  await page.locator('form[action="/admin/settings/general"] button[type="submit"]').click();
  await expect(page.locator('#s-theme')).toHaveValue(slug);
}

// A service product (no billing server) the public pages can show; kept
// across runs, the database is shared with the other specs.
async function ensureProduct(page: Page): Promise<void> {
  if ((await page.request.get(`/product/${SLUG}`)).status() === 200) return;
  await page.goto('/admin/commerce/products/new');
  await page.selectOption('#p-kind', 'service');
  await page.fill('#p-name', 'E2E visual: design review session');
  await page.fill('#p-slug', SLUG);
  await page.fill('#p-short', 'One hour with a designer, with written notes afterwards.');
  await page.fill('#p-price', '120');
  await page.fill('#p-currency', 'AED');
  await page.check('input[name="active"]');
  await page.locator('form[action="/admin/commerce/products"] button[type="submit"]').last().click();
  await expect(page).toHaveURL(/\/admin\/commerce\/products(\/[0-9A-Z]{26})?$/);
}

test.describe.serial('visual baselines', () => {
  test('admin components page in both languages and on a phone', async ({ page }) => {
    for (const locale of ['en', 'ar'] as const) {
      await page.goto('/admin/design/components');
      await setLocale(page, locale);
      await page.goto('/admin/design/components');
      await expectNoServerError(page);
      await expect(page.locator('#main')).toHaveScreenshot(`components-${locale}.png`);
    }
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/admin/design/components');
    await expect(page.locator('#main')).toHaveScreenshot('components-ar-390.png');
    await page.setViewportSize({ width: 1280, height: 720 });
    await setLocale(page, 'en');
  });

  test('shared site pages under the default and business themes', async ({ page, browser }) => {
    await ensureProduct(page);
    const visitor = await browser.newContext({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 720 } });
    const site = await visitor.newPage();
    const shot = async (path: string, name: string) => {
      await site.goto(path);
      await expectNoServerError(site);
      await expect(site).toHaveScreenshot(`${name}.png`, { fullPage: true });
    };
    try {
      await shot('/shop', 'shop-default');
      await shot(`/product/${SLUG}`, 'product-default');
      await site.locator('form[action="/cart/items"] button[type="submit"]').click();
      await expect(site).toHaveURL(/\/cart$/);
      await shot('/cart', 'cart-default');
      await shot('/checkout', 'checkout-default');
      await shot('/account', 'account-default');
      await activate(page, 'business');
      await shot('/shop', 'shop-business');
      await shot('/account', 'account-business');
      await site.setViewportSize({ width: 390, height: 844 });
      await shot(`/product/${SLUG}`, 'product-business-390');
    } finally {
      await visitor.close();
      await activate(page, 'default');
    }
  });
});
