import {test, expect, type Page} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {csrf, setLocale} from '../lib/helpers.ts';

async function searchBoard(page: Page) {
  await page.goto('/admin/canvases');
  const title = 'E2E search ' + randomUUID();
  const created = await page.request.post('/admin/canvases', {form: {_csrf: await csrf(page), title}, maxRedirects: 0});
  expect(created.status()).toBe(303);
  const path = created.headers().location, card = randomUUID();
  const saved = await page.request.post(path + '/document', {
    headers: {'X-CSRF-Token': await csrf(page)},
    data: {version: 1, title, cards: [{id: card, kind: 'note', title: 'Campaign source', body: 'إِطْلَاق حَملة ١٢ — Review this private campaign before publication.', x: 1400, y: 1200}], edges: []},
  });
  expect(saved.status()).toBe(200);
  return {path, card, title};
}

for (const locale of ['en', 'ar'] as const) {
  for (const width of [1280, 390]) {
    test(`canvas search finds Arabic card text, navigates by keyboard and fits ${locale} at ${width}px`, async ({page}, info) => {
      const board = await searchBoard(page);
      try {
        await setLocale(page, locale);
        await page.setViewportSize({width, height: 844});
        const trigger = page.locator('[data-canvas-search-open]');
        await trigger.focus();
        await page.keyboard.press('Control+k');
        const dialog = page.locator('[data-canvas-search-dialog]'), input = dialog.getByRole('combobox');
        await expect(dialog).toBeVisible();
        await expect(input).toBeFocused();
        await expect(input).toHaveAttribute('aria-expanded', 'true');
        await page.keyboard.press('Control+k');
        await expect(dialog).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(dialog).not.toBeVisible();
        await expect(trigger).toBeFocused();
        // Keyboard layouts may send an Arabic character for physical KeyK.
        await page.evaluate(() => document.dispatchEvent(new KeyboardEvent('keydown', {key: 'ن', code: 'KeyK', ctrlKey: true, bubbles: true, cancelable: true})));
        await expect(input).toBeFocused();
        await input.fill('اطلاق حمله 12');
        const hit = dialog.getByRole('option').filter({hasText: board.title});
        await expect(hit).toHaveCount(1);
        await expect(hit).toContainText('إِطْلَاق حَملة ١٢');
        await expect(hit).toHaveAttribute('href', board.path + '#card-' + board.card);
        await expect(input).toHaveAttribute('aria-activedescendant', await hit.getAttribute('id') as string);
        expect(await dialog.evaluate(el => el.scrollWidth > el.clientWidth)).toBe(false);
        expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
        await expect(page.locator('html')).toHaveAttribute('dir', locale === 'ar' ? 'rtl' : 'ltr');
        await page.screenshot({path: info.outputPath(`canvas-search-${locale}-${width}.png`)});
        await page.keyboard.press('ArrowDown');
        await page.keyboard.press('Enter');
        await expect(page).toHaveURL(new RegExp(board.path + '#card-' + board.card + '$'));
        await expect(page.locator('#card-' + board.card)).toBeInViewport();
        // The same palette is available from inside the editor.
        await page.locator('[data-canvas-search-open]').click();
        await expect(page.locator('[data-canvas-search-dialog]')).toBeVisible();
        await page.keyboard.press('Escape');
        await page.locator('[data-action=add]').click();
        const cardDialog = page.locator('dialog[aria-labelledby=canvas-dialog-title]');
        await cardDialog.locator('[name=title]').fill('Unsubmitted card input');
        await page.keyboard.press('Control+k');
        await expect(cardDialog).toBeVisible();
        await expect(cardDialog.locator('[name=title]')).toHaveValue('Unsubmitted card input');
        await expect(page.locator('[data-canvas-search-dialog]')).not.toBeVisible();
        await cardDialog.locator('[data-action=cancel]').first().click();
      } finally {
        const saved=await (await page.request.get(board.path+'/document')).json();
        expect((await page.request.delete(board.path, {headers: {'X-CSRF-Token': await csrf(page)},data:{version:saved.version}})).status()).toBe(200);
      }
    });
  }
}

test('canvas search preserves focus, rejects obsolete replies and recovers from errors without exposing markup', async ({page}) => {
  // Simulate an intermediary that ignores AbortSignal: the serial guard must
  // still reject an older response after the newer result is visible.
  await page.addInitScript(() => {
    const original = window.fetch;
    window.fetch = (input, options) => {
      if (String(input).includes('/admin/canvases/search?')) {
        const {signal: _signal, ...rest} = options || {};
        return original.call(window, input, rest);
      }
      return original.call(window, input, options);
    };
  });
  await page.goto('/admin/canvases');
  await setLocale(page, 'en');
  const board = '01J00000000000000000000001', card = '77777777-7777-4777-8777-777777777777';
  let oldStarted!: () => void, releaseOld!: () => void;
  const oldRequest = new Promise<void>(resolve => {oldStarted = resolve;});
  const oldRelease = new Promise<void>(resolve => {releaseOld = resolve;});
  let failing = true;
  await page.route('**/admin/canvases/search?*', async route => {
    const query = new URL(route.request().url()).searchParams.get('q');
    if (query === 'older') {
      oldStarted();
      await oldRelease;
      await route.fulfill({json: {items: [{board_id: board, board_title: 'Old response', title: 'Obsolete result', href: '/admin/canvases/' + board}]}}).catch(() => {});
    } else if (query === 'latest') {
      await route.fulfill({json: {items: [{board_id: board, board_title: 'Current response', card_id: card, title: '<img src=x onerror=alert(1)>', snippet: '<script>private text</script>', href: 'javascript:alert(1)'}]}});
    } else if (query === 'retry' && failing) {
      await route.fulfill({status: 503, json: {error: 'unavailable'}});
    } else {
      await route.fulfill({json: {items: []}});
    }
  });
  const trigger = page.locator('[data-canvas-search-open]');
  await trigger.click();
  const dialog = page.locator('[data-canvas-search-dialog]'), input = dialog.getByRole('combobox');
  await expect(dialog.locator('[data-canvas-search-status]')).toContainText('Type at least two');
  await input.fill('older');
  await oldRequest;
  await input.fill('latest');
  await expect(dialog.getByRole('option')).toContainText('Current response');
  const olderResponse = page.waitForResponse(response => new URL(response.url()).searchParams.get('q') === 'older');
  releaseOld();
  await (await olderResponse).finished();
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => resolve())));
  await expect(dialog.getByRole('option')).not.toContainText('Obsolete result');
  await expect(dialog.locator('img,script')).toHaveCount(0);
  await expect(dialog.getByRole('option')).toContainText('<img src=x onerror=alert(1)>');
  await expect(dialog.getByRole('option')).toHaveAttribute('href', '/admin/canvases/' + board + '#card-' + card);
  await input.fill('retry');
  await expect(dialog.locator('[data-canvas-search-status]')).toContainText('Could not search');
  failing = false;
  await dialog.locator('[data-canvas-search-retry]').click();
  await expect(dialog.locator('[data-canvas-search-status]')).toContainText('No matches');
  await expect(input).toBeFocused();
  await input.fill('a'.repeat(161));
  await expect(dialog.locator('[data-canvas-search-status]')).toContainText('160');
  await page.keyboard.press('Escape');
  await expect(trigger).toBeFocused();
});
