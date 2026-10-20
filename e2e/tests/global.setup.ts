// Setup project: creates the first administrator through /admin/setup (only
// possible once per data dir), then signs in through the login form and saves
// the session for every other spec.
import { test as setup, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { ADMIN } from '../lib/helpers.ts';

setup('first admin via /admin/setup, then login', async ({ page }) => {
  await page.goto('/admin');
  await expect(page).toHaveURL(/\/admin\/setup/);
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  await page.fill('input[name="name"]', ADMIN.name);
  await page.fill('input[name="email"]', ADMIN.email);
  await page.fill('input[name="password"]', ADMIN.password);
  await page.fill('input[name="title"]', 'E2E Site');
  await page.fill('input[name="tagline"]', 'Playwright harness');
  // Keep site.locale Arabic (default) so public pages stay RTL for the suite.
  await page.selectOption('select[name="locale"]', 'ar');
  await page.locator('form[action="/admin/setup"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/admin\/workspace/);
  await expect(page.locator('input[name="title"]')).toHaveCount(0);
  await page.locator('input[name="mode"][value="full"]').check();
  await page.locator('form[action="/admin/workspace"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/admin\/workspace/);

  // setup is closed now
  await page.goto('/admin/setup');
  await expect(page).not.toHaveURL(/\/admin\/setup/);

  // sign out, then sign in through the form
  await page.goto('/admin');
  await page.locator('form[action="/admin/logout"] button').first().click();
  await expect(page).toHaveURL(/\/admin\/login/);
  await page.fill('input[name="email"]', ADMIN.email);
  await page.fill('input[name="password"]', ADMIN.password);
  await page.locator('form[action^="/admin/login"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/admin\/?$/);

  mkdirSync('.auth', { recursive: true });
  await page.context().storageState({ path: '.auth/admin.json' });
});
