// Visual sanity: full-page screenshots of the finished site (after every other
// spec, Business active) into e2e/screenshots/. Reviewed by hand;
// the spec itself only checks that each page answers 200 without a 500 marker.
import { test, expect } from '@playwright/test';
import { MARKER_500 } from '../lib/helpers.ts';

const SHOTS: [string, string][] = [
  ['/', 'public-home'],
  ['/shop', 'public-shop'],
  ['/admin', 'admin-dashboard'],
  ['/admin/content/post', 'admin-content-post'],
  ['/admin/migrations', 'admin-migrations'],
];

for (const [path, name] of SHOTS) {
  test(`screenshot ${path}`, async ({ page }) => {
    const resp = await page.goto(path);
    expect(resp?.status()).toBe(200);
    await page.waitForLoadState('networkidle');
    expect(await page.content()).not.toContain(MARKER_500);
    await page.screenshot({ path: `screenshots/${name}.png`, fullPage: true });
  });
}

test.describe('mobile', () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true });
  for (const [path, name] of SHOTS.filter(([p]) => !p.startsWith('/admin/content'))) {
    test(`screenshot ${path} (375)`, async ({ page }) => {
      await page.goto(path);
      await page.waitForLoadState('networkidle');
      await page.screenshot({ path: `screenshots/${name}-375.png`, fullPage: true });
    });
  }
});
