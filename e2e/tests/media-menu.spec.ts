// Media + menus: upload a PNG in the media library → it is in the picker →
// featured image of a published post → a primary menu with 2 items built in
// the native menu editor (explicit drop zone and keyboard reorder) → the public header shows it.
import { test, expect } from '@playwright/test';
import { db, expectNoServerError, setContentLocale, submitAndLand } from '../lib/helpers.ts';
import { tinyPNG } from '../fixtures/wp-fixture-server.ts';

const FILE = 'e2e-featured.png';
const POST_TITLE = 'E2E featured image post';
const POST_SLUG = 'e2e-featured-image-post';

test.describe.serial('media and menus', () => {
  test('upload a PNG in the media library', async ({ page }) => {
    await page.goto('/admin/media');
    await page.locator('#media-file').setInputFiles({ name: FILE, mimeType: 'image/png', buffer: tinyPNG(120, 80, [22, 114, 82, 255]) });
    const results = page.locator('#media-results');
    await expect(results).toContainText('e2e-featured');
    // the thumbnail variant is served
    const thumb = results.locator('img').first();
    await expect(thumb).toBeVisible();
    const src = await thumb.getAttribute('src');
    expect(src).toBeTruthy();
    const r = await page.request.get(src!);
    expect(r.status()).toBe(200);
    expect(r.headers()['content-type']).toMatch(/image\//);
  });

  test('the upload is in the picker and becomes the featured image of a published post', async ({ page }) => {
    await page.goto('/admin/content/post/new');
    // New content takes the admin UI locale, which an earlier spec may have
    // left in English; /post/<slug> serves the Arabic (default) site.
    await setContentLocale(page, 'ar');
    await page.fill('#title', POST_TITLE);
    await page.fill('#slug', POST_SLUG);
    await page.locator('button[data-media-open][data-target="featured-media"]').click();
    const picker = page.locator('#gp-media-picker-body');
    const item = picker.locator('button[data-id]').filter({ hasText: 'e2e-featured' }).first();
    await expect(item).toBeVisible();
    const mediaID = await item.getAttribute('data-id');
    await item.click();
    await expect(page.locator('#featured-media')).toHaveValue(mediaID!);
    await page.locator('button[name="intent"][value="publish"]').click();
    await expect(page.locator('#status')).toHaveValue('published');
    await expect(page.locator('#featured-media')).toHaveValue(mediaID!);

    const resp = await page.goto(`/post/${POST_SLUG}`);
    expect(resp?.status()).toBe(200);
    await expectNoServerError(page);
    const img = page.locator('main figure img').first();
    await expect(img).toBeVisible();
    expect(await img.getAttribute('src')).toMatch(/^\/media\//);
  });

  test('native menu drop and keyboard reorder persist in SQLite and the public header', async ({ page }) => {
    await page.goto('/admin/menus');
    await page.fill('form[action="/admin/menus"] #name', 'E2E main menu');
    await page.fill('form[action="/admin/menus"] #slug', 'e2e-main');
    await page.selectOption('form[action="/admin/menus"] #location', 'primary');
    await page.locator('form[action="/admin/menus"] button[type="submit"]').click();
    await expect(page).toHaveURL(/\/admin\/menus\/[0-9A-Z]{26}$/);

    const menuID = new URL(page.url()).pathname.split('/').pop()!;

    // item 1: a custom link; item 2: the post, from the content search tab
    await page.fill('#add-label', 'E2E About');
    await page.fill('#add-url', '/about-e2e');
    await page.locator('button[\\@click^="add({ kind: \'custom\'"]').click();
    await page.locator('aside button[role="tab"]').nth(1).click();
    await page.fill('#add-q-content', 'E2E featured');
    const hit = page.locator('#add-results-content li').filter({ hasText: POST_TITLE }).locator('button').first();
    await expect(hit).toBeVisible();
    await hit.click();

    const rows = page.locator('.menu-tree-item');
    const title = (index: number) => rows.nth(index).locator('.menu-item-toggle [x-text="title(it)"]');
    await expect(rows).toHaveCount(2);
    await expect(title(0)).toHaveText('E2E About');
    await expect(title(1)).toHaveText(new RegExp('^' + POST_TITLE + '(?: ·.*)?$'));
    // add() focuses the new item's expanded editor, which can leave the first
    // row against the viewport edge. Native HTML drag autoscroll then moves the
    // highlighted drop zone between dragover and mouseup. Collapse the editor
    // and center the destination before taking any pointer coordinates.
    await rows.nth(1).locator('.menu-item-toggle').click();
    await expect(rows.nth(1).locator('.menu-item-toggle')).toHaveAttribute('aria-expanded', 'false');
    await rows.nth(0).evaluate(element => element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }));
    const handle = await rows.nth(1).locator('[data-handle]').boundingBox();
    expect(handle).toBeTruthy();
    const x = handle!.x + handle!.width / 2;
    const y = handle!.y + handle!.height / 2;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x, y - 5, { steps: 3 });
    await page.mouse.move(x, y - 20, { steps: 5 });
    const before = rows.nth(0).locator('.menu-drop-zones > span').first();
    await expect(before).toBeVisible();
    // The drop zones shift the rows as they appear: move onto the zone where it
    // is now (dragover needs real moves) and drop only once it is the target.
    await expect(async () => {
      const zone = (await before.boundingBox())!;
      await page.mouse.move(zone.x + zone.width / 2, zone.y + zone.height / 2, { steps: 5 });
      await expect(before).toHaveClass(/is-drop-target/, { timeout: 500 });
    }).toPass({ timeout: 10_000 });
    await page.mouse.up();
    await expect(title(0)).toHaveText(new RegExp('^' + POST_TITLE + '(?: ·.*)?$'));
    await rows.nth(0).locator('[data-handle]').press('Alt+ArrowDown');
    await expect(title(0)).toHaveText('E2E About');
    await rows.nth(1).locator('[data-handle]').press('Alt+ArrowUp');
    await expect(title(0)).toHaveText(new RegExp('^' + POST_TITLE + '(?: ·.*)?$'));
    await expect(title(1)).toHaveText('E2E About');
    // The "created" flash is already on the page, so wait for the save itself.
    await submitAndLand(page, page.locator('form[action$="/items"] button[type="submit"]'), `/admin/menus/${menuID}/items`);
    await expect(page.locator('#main div[role="status"]')).toBeVisible();
    await page.reload();
    await expect(title(0)).toHaveText(new RegExp('^' + POST_TITLE + '(?: ·.*)?$'));
    await expect(title(1)).toHaveText('E2E About');
    const database = db();
    const stored = database.prepare('SELECT kind,label,ref_id,parent_id,position FROM menu_items WHERE menu_id=? ORDER BY position').all(menuID) as any[];
    const post = database.prepare('SELECT id FROM content WHERE slug=?').get(POST_SLUG) as any;
    expect(stored).toHaveLength(2);
    expect(stored.map(item => item.kind)).toEqual(['content', 'custom']);
    expect(stored.map(item => item.position)).toEqual([0, 1]);
    expect(stored[0].ref_id).toBe(post.id);
    expect(stored[1].label).toBe('E2E About');
    expect(stored.every(item => !item.parent_id)).toBe(true);
    database.close();

    await page.goto('/');
    await expectNoServerError(page);
    // The header renders the menu twice (desktop nav and the collapsed mobile
    // one); count what this desktop viewport shows.
    const nav = page.locator('header nav a').filter({ visible: true });
    await expect(nav).toHaveCount(2);
    await expect(nav.nth(0)).toHaveText(POST_TITLE);
    await expect(nav.nth(1)).toHaveText('E2E About');
    expect(await nav.nth(1).getAttribute('href')).toMatch(/\/about-e2e$/);
  });

  // BUG-02 (fixed): content menu items carry the theme permalink
  // (/post/<slug>), not the menus fallback "/"+slug.
  test('BUG-02 a menu item for a post links to its permalink', async ({ page }) => {
    await page.goto('/');
    // positive control: the custom link from the same menu is rendered
    const custom = await page.locator('header nav a').filter({ visible: true, hasText: 'E2E About' }).getAttribute('href');
    expect(custom, 'custom link href').toMatch(/\/about-e2e$/);
    const href = await page.locator('header nav a').filter({ visible: true, hasText: POST_TITLE }).getAttribute('href');
    expect(href, 'menu href').toMatch(new RegExp(`/post/${POST_SLUG}$`));
    const r = await page.request.get(href!);
    expect(r.status()).toBe(200);
  });
});
