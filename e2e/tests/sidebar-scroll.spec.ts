import { test, expect } from '@playwright/test';

for (const direction of ['rtl', 'ltr']) {
  test(`sidebar keeps its scroll on navigation, reload and back (${direction})`, async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 640 });
    await page.goto('/admin');
    await page.evaluate(dir => { document.documentElement.dir = dir; }, direction);
    const nav = page.locator('[data-admin-navigation]');
    const link = nav.locator('a[href="/admin/system/jobs"]');
    await link.scrollIntoViewIfNeeded();
    const before = await nav.evaluate(el => el.scrollTop);
    expect(before).toBeGreaterThan(100);
    await link.click();
    await expect(page).toHaveURL(/\/admin\/system\/jobs/);
    await expect.poll(() => nav.evaluate(el => el.scrollTop)).toBeCloseTo(before, 0);
    await page.reload();
    await expect.poll(() => nav.evaluate(el => el.scrollTop)).toBeCloseTo(before, 0);
    await page.goBack();
    await expect.poll(() => nav.evaluate(el => el.scrollTop)).toBeCloseTo(before, 0);
    // Content scroll remains independent, and deliberately returning the nav to
    // its top replaces the previous saved position.
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
    await nav.evaluate(el => { el.scrollTop = 0; });
    await expect.poll(() => page.evaluate(() => sessionStorage.getItem('gorun.admin.navigation.scroll'))).toBe('0');
    await page.reload();
    await expect.poll(() => nav.evaluate(el => el.scrollTop)).toBe(0);
  });
}

test('sidebar navigation works when browser storage is unavailable', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(window, 'sessionStorage', { get() { throw new DOMException('Disabled', 'SecurityError'); } });
  });
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/admin');
  const link = page.locator('[data-admin-navigation] a[href="/admin/system/jobs"]');
  await link.click();
  await expect(page).toHaveURL(/\/admin\/system\/jobs/);
  expect(errors).toEqual([]);
});

test('mobile drawer restores its navigation position when reopened after navigation', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/admin');
  await page.locator('[data-sidebar-toggle]').click();
  const nav = page.locator('[data-admin-navigation]');
  const link = nav.locator('a[href="/admin/system/jobs"]');
  await link.scrollIntoViewIfNeeded();
  const before = await nav.evaluate(el => el.scrollTop);
  expect(before).toBeGreaterThan(100);
  await link.click();
  await expect(page).toHaveURL(/\/admin\/system\/jobs/);
  await page.locator('[data-sidebar-toggle]').click();
  await expect.poll(() => nav.evaluate(el => el.scrollTop)).toBeCloseTo(before, 0);
});
