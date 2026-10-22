import { test, expect } from '@playwright/test';

const modes = JSON.parse(process.env.E2E_ADDONS ?? '{}') as Record<string, string>;
const enabled = (name: string) => modes[name] !== 'disabled';

for (const addon of ['saas', 'membership']) {
  test(`${addon} owns its assets and navigation with mixed activation`, async ({ page, browser }, testInfo) => {
    const path = `/${addon}`;
    if (!enabled(addon)) {
      expect((await page.request.get(path)).status()).toBe(404);
      expect((await page.request.get(`${path}/assets.css`)).status()).toBe(404);
      return;
    }
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 900 });
      const errors: string[] = [];
      const onError = (error: Error) => errors.push(error.message);
      page.on('pageerror', onError);
      expect((await page.goto(path))?.status()).toBe(200);
      const css = page.locator('link[rel="stylesheet"]');
      await expect(css).toHaveAttribute('href', `${path}/assets.css`);
      const asset = await page.request.get(`${path}/assets.css`);
      expect(asset.status()).toBe(200);
      expect(asset.headers()['content-type']).toContain('text/css');
      await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(247, 248, 250)');
      await expect(page.locator('a[href="/account"]')).toHaveCount(enabled('commerce') ? 1 : 0);
      await expect(page.locator('nav a[href="/shop"]')).toHaveCount(enabled('commerce') ? 1 : 0);
      if (addon === 'membership') await expect(page.locator('a[href="/saas"]')).toHaveCount(enabled('saas') ? 1 : 0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
      expect(errors).toEqual([]);
      await page.screenshot({ path: testInfo.outputPath(`${addon}-${width}.png`), fullPage: true });
      page.off('pageerror', onError);
    }
    const anonymous = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    try {
      const response = await anonymous.request.get(path, { maxRedirects: 0 });
      expect(response.status()).toBe(enabled('commerce') ? 303 : 503);
      if (enabled('commerce')) expect(response.headers().location).toBe(`/account?next=${path}`);
      else {
        expect(response.headers().location).toBeUndefined();
        expect(await response.text()).toContain('account_sign_in_unavailable');
      }
    } finally { await anonymous.close(); }
  });
}
