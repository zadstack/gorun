// Plain http://server-ip is not a secure context, so navigator.clipboard is
// undefined there. gorun.test maps to the local test server and is just as
// insecure, unlike 127.0.0.1.
import { test, expect } from '@playwright/test';
import { ADMIN } from '../lib/helpers.ts';

test.use({
  baseURL: `http://gorun.test:${process.env.E2E_PORT}`,
  storageState: { cookies: [], origins: [] },
  launchOptions: { args: ['--host-resolver-rules=MAP gorun.test 127.0.0.1'] },
});

test('copy buttons work without the Clipboard API', async ({ page }) => {
  await page.addInitScript(() => {
    document.addEventListener('copy', () => {
      const el = document.activeElement as HTMLTextAreaElement;
      document.documentElement.dataset.copied = el.value.slice(el.selectionStart, el.selectionEnd);
    });
  });

  await page.goto('/admin/login');
  await page.fill('input[name="email"]', ADMIN.email);
  await page.fill('input[name="password"]', ADMIN.password);
  await page.locator('form[action="/admin/login"] button[type="submit"]').click();
  await expect(page).not.toHaveURL(/\/admin\/login/);
  expect(await page.evaluate(() => [window.isSecureContext, typeof navigator.clipboard])).toEqual([false, 'undefined']);

  await page.goto('/admin/agents/keys');
  await page.locator('[data-key-create-open]').click();
  await page.fill('#k-name', 'Copy over plain HTTP');
  await page.locator('[data-key-preset="read"]').click();
  await page.locator('form[data-key-create] button[type="submit"]').click();

  const key = await page.locator('#k-plaintext').inputValue();
  expect(key).not.toBe('');
  const button = page.locator('[data-key-created] button:has([data-label-copy])');
  const label = button.locator('[data-label-copy]');
  await button.click();
  await expect(page.locator('html')).toHaveAttribute('data-copied', key);
  await expect(label).toHaveText((await label.getAttribute('data-label-copied'))!);
  await expect(button).toBeFocused();
});
