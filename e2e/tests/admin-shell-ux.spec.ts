import { test, expect } from '@playwright/test';
import { setLocale } from '../lib/helpers.ts';

test('manager aliases enter the shared shell and workflow views preserve deep links', async ({ page }) => {
  await setLocale(page, 'en');
  await page.goto('/agents#runs');
  await expect(page).toHaveURL(/\/admin\/workagents#runs$/);
  await expect(page.locator('#admin-sidebar')).toBeVisible();
  await expect(page.locator('#runs')).toBeVisible();
  await expect(page.locator('#profiles')).toBeHidden();
  await page.locator('.module-views').getByRole('link', { name: 'Schedules', exact: true }).click();
  await expect(page.locator('#workflows')).toBeVisible();
  await expect(page.locator('#runs')).toBeHidden();
  await page.reload();
  await expect(page.locator('#workflows')).toBeVisible();
  await setLocale(page, 'ar');
  await page.goto('/admin/workagents');
  await expect(page.getByRole('heading', { name: 'الوكلاء ومسارات العمل', exact: true })).toBeVisible();
  await expect(page.locator('#profiles')).toBeVisible();
});

test('application creation selects its actual subscription target and stays in the admin shell', async ({ page }) => {
  await setLocale(page, 'en');
  await page.goto('/admin/saas/new');
  const form = page.locator('form[action="/admin/saas/apps"]');
  if (!(await form.isVisible())) await page.locator('details').filter({ has: form }).locator('summary').click();
  const suffix = String(Date.now());
  const name = 'Guided synthetic app ' + suffix;
  await form.locator('[name=name]').fill(name);
  await form.locator('[name=slug]').fill('guided-' + suffix);
  await form.getByRole('button', { name: 'Create application', exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/saas\/new\?app=[0-9A-Z]{26}#subscription$/);
  const subscription = page.locator('form[action="/admin/saas/subscriptions"]');
  await expect(subscription.locator('[name=app_id] option:checked')).toHaveText(name);
  await subscription.locator('[name=plan_id]').fill('synthetic-basic');
  await subscription.getByRole('button', { name: 'Create subscription', exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/saas$/);
  await expect(page.locator('#admin-sidebar')).toBeVisible();
  await expect(page.locator('#apps')).toContainText(name);
  await page.locator('.module-views').getByRole('link', { name: 'Subscriptions', exact: true }).click();
  await expect(page.locator('#subscriptions')).toContainText(name);
  await expect(page.locator('#apps')).toBeHidden();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/admin/saas/new');
  const bounds = await page.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth }));
  expect(bounds.scroll).toBeLessThanOrEqual(bounds.width + 1);
});
