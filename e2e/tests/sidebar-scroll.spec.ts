import { test, expect } from '@playwright/test';

for (const direction of ['rtl', 'ltr']) {
  test(`compact sections and contextual pages keep page scroll independent (${direction})`, async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 640 });
    await page.goto('/admin');
    await page.evaluate(dir => { document.documentElement.dir = dir; }, direction);
    const nav = page.locator('[data-admin-navigation]');
    // A short sidebar should not need scrolling. Navigation position from an
    // older flat sidebar must be clamped without scrolling the page itself.
    await page.evaluate(() => sessionStorage.setItem('gorun.admin.navigation.scroll', '900'));
    await page.reload();
    await expect.poll(() => nav.evaluate(el => el.scrollTop)).toBe(0);
    await nav.locator('[data-navigation-section="settings"] a').click();
    const context = page.locator('[data-navigation-context="settings"]');
    await context.locator('[data-navigation-category="system"]').click();
    await context.locator('a[href="/admin/system/jobs"]').click();
    await expect(page).toHaveURL(/\/admin\/system\/jobs/);
    await expect(context.locator('a[href="/admin/system/jobs"]')).toHaveAttribute('aria-current', 'page');
    await page.reload();
    await expect(context.locator('a[href="/admin/system/jobs"]')).toHaveAttribute('aria-current', 'page');
    await page.goBack();
    await expect.poll(() => nav.evaluate(el => el.scrollTop)).toBe(0);
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  });
}

test('section navigation works without browser storage', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(window, 'sessionStorage', { get() { throw new DOMException('Disabled', 'SecurityError'); } });
  });
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/admin');
  await page.locator('[data-navigation-section="settings"] a').click();
  await page.locator('[data-navigation-category="system"]').click();
  await page.locator('.admin-section-pages a[href="/admin/system/jobs"]').click();
  await expect(page).toHaveURL(/\/admin\/system\/jobs/);
  expect(errors).toEqual([]);
});

test('mobile sections reach contextual pages without overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/admin');
  await page.locator('[data-sidebar-toggle]').click();
  await page.locator('[data-navigation-section="settings"] a').click();
  await page.locator('[data-navigation-category="system"]').click();
  await page.locator('.admin-section-pages a[href="/admin/system/jobs"]').click();
  await expect(page).toHaveURL(/\/admin\/system\/jobs/);
  await expect(page.locator('[data-navigation-context="settings"]')).toBeVisible();
  await page.locator('[data-sidebar-toggle]').click();
  await expect(page.locator('[data-navigation-section="settings"] a')).toHaveAttribute('aria-current', 'true');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
