import {test, expect} from '@playwright/test';

test('code editor keeps draft and publication controls native and restores the original template', async ({page}) => {
  await page.goto('/admin/appearance/templates/edit?name=single.html');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  const original = await page.locator('#template-body').inputValue();
  const marker = '<!-- code-editor-acceptance -->';
  try {
    await editor.click();
    await page.keyboard.press('ControlOrMeta+End');
    // Enter, as a person types it: a "\n" inside insertText is dropped by the
    // contenteditable at the empty last line.
    await page.keyboard.press('Enter');
    await page.keyboard.insertText(marker);
    await expect(page.locator('#template-body')).toHaveValue(original + '\n' + marker);
    await page.keyboard.press('Escape');
    await expect(page.locator('[data-code-exit]')).toBeFocused();
    await page.locator('button[value="draft"]').click();
    await expect(page.locator('#template-body')).toHaveValue(original + '\n' + marker);
    await page.locator('button[value="publish"]').click();
    await expect(page.locator('#template-body')).toHaveValue(original + '\n' + marker);
    const preview = await page.request.get('/?gp_preview_override=1');
    expect(preview.status()).toBe(200);
  } finally {
    await page.goto('/admin/appearance/templates/edit?name=single.html');
    await expect(page.locator('.cm-content')).toBeVisible();
    await page.locator('.cm-content').click();
    await page.keyboard.press('ControlOrMeta+a');
    await page.keyboard.insertText(original);
    await page.locator('button[value="publish"]').click();
    await expect(page.locator('#template-body')).toHaveValue(original);
  }
});

test('failed code bundle leaves a usable native textarea', async ({page}) => {
  await page.route('**/vendor/codemirror-editor.js*', route => route.abort());
  await page.goto('/admin/appearance/templates/edit?name=single.html');
  await expect(page.locator('#template-body')).toBeVisible();
  await expect(page.locator('[data-code-status]')).not.toBeEmpty();
  await expect(page.locator('button[value="draft"]')).toBeEnabled();
});

test('Escape closes the code search panel before leaving the editor', async ({page}) => {
  await page.goto('/admin/appearance/templates/edit?name=single.html');
  const editor = page.locator('.cm-content');
  await expect(editor).toBeVisible();
  await editor.click();
  await page.keyboard.press('ControlOrMeta+f');
  const search = page.locator('.cm-search');
  await expect(search).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(search).toHaveCount(0);
  await expect(page.locator('[data-code-exit]')).not.toBeFocused();
  await editor.click();
  await page.keyboard.press('Escape');
  await expect(page.locator('[data-code-exit]')).toBeFocused();
});
