import { test, expect } from '@playwright/test';
import { setLocale } from '../lib/helpers.ts';

test('system update setup is clear and disabled without server configuration', async ({ page }) => {
  await setLocale(page, 'en');
  await page.goto('/admin/system/updates');
  await expect(page.locator('h1')).toHaveText('System updates');
  await expect(page.getByRole('button', { name: 'Start staged update' })).toBeDisabled();
  await expect(page.locator('[data-system-updates]')).toContainText('Staged updates are not enabled');
  await expect(page.locator('[data-system-updates]')).toHaveAttribute('data-configured', 'false');
  await expect(page.locator('[data-system-updates]')).toContainText('never a completed update');
  await expect(page.locator('[data-stat="latest-version"]')).toContainText('unknown');
  await expect(page.locator('body')).not.toContainText('admin.updates.');
  await setLocale(page, 'ar');
  await page.goto('/admin/system/updates');
  await expect(page.locator('html')).toHaveAttribute('dir','rtl');
  await expect(page.locator('h1')).toHaveText('تحديثات النظام');
  await expect(page.getByRole('button', { name: 'بدء التحديث المرحلي' })).toBeDisabled();
  await expect(page.locator('body')).not.toContainText('admin.updates.');
});
