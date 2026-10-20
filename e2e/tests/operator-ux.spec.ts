import { test, expect } from '@playwright/test';
import { setLocale } from '../lib/helpers.ts';

test('operator forms follow selected provider and access mode without exposing secrets', async ({ page }) => {
  await setLocale(page,'en');
  await page.goto('/admin/settings/mail');
  await page.locator('#m-provider').selectOption('resend');
  await expect(page.locator('#m-resend-key')).toBeVisible();
  await expect(page.locator('#m-host')).toBeHidden();
  await page.locator('#m-provider').selectOption('smtp');
  await expect(page.locator('#m-host')).toBeVisible();
  await expect(page.locator('#m-resend-key')).toBeHidden();
  await expect(page.locator('#m-resend-key')).toHaveValue('');
  await expect(page.locator('#m-password')).toHaveValue('');
  await page.goto('/admin/settings/availability');
  await page.locator('select[name=mode]').selectOption('public');
  await expect(page.locator('input[name=password]')).toBeHidden();
  await page.locator('select[name=mode]').selectOption('password');
  await expect(page.locator('input[name=password]')).toBeVisible();
  await page.goto('/admin/settings/ai');
  await page.locator('#ai-provider').selectOption('');
  await expect(page.locator('#ai-model')).toBeHidden();
  await expect(page.locator('#ai-base-url')).toBeHidden();
  await page.locator('#ai-provider').selectOption('openai');
  await expect(page.locator('#ai-model')).toBeVisible();
  await expect(page.locator('#ai-base-url')).toBeVisible();
  await setLocale(page,'ar');
  await page.goto('/admin/settings/mail');
  await expect(page.locator('#m-host')).toBeVisible();
  await page.locator('#m-provider').selectOption('resend');
  await expect(page.locator('#m-resend-key')).toBeVisible();
  await expect(page.locator('#m-host')).toBeHidden();
});

test('backup outcomes and inbox settings remain separate at mobile width', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/admin/system/backups');
  await expect(page.locator('[data-backup-outcomes] section')).toHaveCount(3);
  await expect(page.locator('[data-backup-outcomes]')).toBeVisible();
  await page.goto('/admin/system/health');
  await expect(page.locator('[data-check=backups]')).toBeVisible();
  await expect(page.locator('details').first()).not.toHaveAttribute('open', '');
  await page.goto('/admin/system/form-submissions');
  const settings = page.locator('details').filter({ has: page.locator('form[action="/admin/system/form-submissions/settings"]') }).first();
  await expect(settings).not.toHaveAttribute('open', '');
});


test('navigation preview follows visibility without mutating permissions and search keeps preview intact', async ({ page }) => {
 await page.goto('/admin/navigation');
 const panel=page.locator('.navigation-preview-panel');
 await panel.locator('summary').click();
 await expect(panel).toHaveAttribute('open', '');
 const preview=page.locator('[data-navigation-preview]');
 await expect(preview.locator('li').first()).toBeVisible();
 const checkbox=page.locator('[data-navigation-editor] input[name=visible]:checked:not(:disabled)').first();
 const id=await checkbox.inputValue();
 await expect(preview.locator(`[data-preview-id="${id}"]`)).toHaveCount(1);
 await checkbox.uncheck();
 await expect(preview.locator(`[data-preview-id="${id}"]`)).toHaveCount(0);
 await checkbox.check();
 const count=await preview.locator('li').count();
 await page.locator('[data-navigation-search]').fill('__no_match__');
 await expect(page.locator('.navigation-row:visible')).toHaveCount(0);
 await expect(preview.locator('li')).toHaveCount(count);
 await page.locator('[data-navigation-search]').fill('');
 await expect(checkbox).toBeVisible();
 await page.goto('/admin/roles');
 await page.locator('[data-role-comparison] summary').click();
 await expect(page.locator('[data-role-comparison] tbody tr')).toHaveCount(6);
});

test('SEO inventory filters the scanned rows while preserving summary counts', async ({ page }) => {
 await page.goto('/admin/content/page/new');
 await page.locator('#title').fill('Operator inventory synthetic page');
 await page.locator('#slug').fill('operator-inventory-synthetic');
 await page.locator('button[name=intent][value=publish]').click();
 await expect(page).toHaveURL(/\/admin\/content\/page\/[0-9A-Z]{26}$/);
 await page.goto('/admin/settings/seo/audit');
 await expect(page.locator('[data-seo-row]')).not.toHaveCount(0);
 await expect(page.locator('[data-seo-row]').filter({ hasText:'Operator inventory synthetic page' })).toBeVisible();
 await page.locator('[data-seo-filter=type]').selectOption('page');
 await expect(page.locator('[data-seo-row]:visible').first()).toHaveAttribute('data-type','page');
});
