import { test, expect } from '@playwright/test';
import { setLocale } from '../lib/helpers.ts';

test('system update setup is clear and disabled without server configuration', async ({ page }) => {
  await setLocale(page, 'en');
  await page.goto('/admin/system/updates');
  await expect(page.locator('h1')).toHaveText('System updates');
  await expect(page.getByRole('button', { name: 'Update from configured source' })).toBeDisabled();
  await expect(page.getByRole('status')).toContainText('Deployment is not connected');
  await expect(page.locator('[data-system-updates]')).toContainText('queued, not completed');
  await expect(page.locator('body')).not.toContainText('admin.updates.');
  await setLocale(page, 'ar');
  await page.goto('/admin/system/updates');
  await expect(page.locator('html')).toHaveAttribute('dir','rtl');
  await expect(page.locator('h1')).toHaveText('تحديثات النظام');
  await expect(page.getByRole('button', { name: 'تحديث من المصدر المهيأ' })).toBeDisabled();
});
