import { test, expect } from '@playwright/test';
import { csrf, setLocale } from '../lib/helpers.ts';

test('navigation editor reorders items by keyboard and drag, then persists on save', async ({ page }) => {
  await setLocale(page, 'en');
  await page.goto('/admin/navigation');
  const editor = page.locator('[data-navigation-editor]');
  await expect(editor).toBeVisible();
  await expect(editor.locator('[data-navigation-drag]').first()).toBeVisible();

  const firstGroup = editor.locator('[data-navigation-groups] > [data-navigation-row]').first();
  const items = firstGroup.locator('[data-navigation-items] > [data-navigation-row]');
  await expect(items).not.toHaveCount(0);
  const count = await items.count();
  test.skip(count < 2, 'need at least two items in the first group');

  const before = await items.evaluateAll(rows =>
    rows.map(row => (row.querySelector('input[name="item_order"]') as HTMLInputElement).value),
  );
  try {
    await items.nth(1).locator('[data-move="up"]').click();
    await expect(items.nth(0).locator('input[name="item_order"]')).toHaveValue(before[1]);
    await expect(items.nth(1).locator('input[name="item_order"]')).toHaveValue(before[0]);
    await expect(editor.locator('[data-order-status]')).toContainText('Unsaved changes');

    const handle = items.nth(0).locator('[data-navigation-drag]');
    await handle.focus();
    await handle.press('Alt+ArrowDown');
    await expect(items.nth(0).locator('input[name="item_order"]')).toHaveValue(before[0]);
    await expect(items.nth(1).locator('input[name="item_order"]')).toHaveValue(before[1]);

    // Native HTML5 drag: move second item onto the first.
    await items.nth(1).evaluate(el => el.scrollIntoView({ block: 'center', behavior: 'instant' }));
    const from = await items.nth(1).locator('[data-navigation-drag]').boundingBox();
    const to = await items.nth(0).boundingBox();
    expect(from && to).toBeTruthy();
    await page.mouse.move(from!.x + from!.width / 2, from!.y + from!.height / 2);
    await page.mouse.down();
    await page.mouse.move(to!.x + to!.width / 2, to!.y + to!.height / 2, { steps: 8 });
    await page.mouse.up();
    await expect(items.nth(0).locator('input[name="item_order"]')).toHaveValue(before[1]);
    await expect(items.nth(1).locator('input[name="item_order"]')).toHaveValue(before[0]);

    await editor.locator('button[name="intent"][value="save"]').click();
    await expect(page).toHaveURL(/\/admin\/navigation\?saved=1/);
    await expect(page.locator('[role="status"]').filter({ hasText: 'Sidebar preferences saved' })).toBeVisible();

    const saved = page.locator('[data-navigation-groups] > [data-navigation-row]').first()
      .locator('[data-navigation-items] > [data-navigation-row]');
    await expect(saved.nth(0).locator('input[name="item_order"]')).toHaveValue(before[1]);
    await expect(saved.nth(1).locator('input[name="item_order"]')).toHaveValue(before[0]);

    await page.reload();
    const reloaded = page.locator('[data-navigation-groups] > [data-navigation-row]').first()
      .locator('[data-navigation-items] > [data-navigation-row]');
    await expect(reloaded.nth(0).locator('input[name="item_order"]')).toHaveValue(before[1]);
    await expect(reloaded.nth(1).locator('input[name="item_order"]')).toHaveValue(before[0]);

    await setLocale(page, 'ar');
    await page.goto('/admin/navigation');
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await expect(page.locator('[data-navigation-drag]').first()).toBeVisible();
    await expect(page.locator('.navigation-order-hint')).toContainText('اسحب');
  } finally {
    // Shared e2e DB: restore defaults so later specs see the stock section order.
    const token = await csrf(page);
    expect((await page.request.post('/admin/navigation', {
      form: { intent: 'reset', _csrf: token },
      maxRedirects: 0,
    })).status()).toBe(303);
  }
});
