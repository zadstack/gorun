import { test, expect } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { ADMIN, db, setLocale } from '../lib/helpers.ts';

test('activity canvas shows safe metadata, refreshes, and fits RTL mobile', async ({ page }, testInfo) => {
  const database = db();
  const board = "01ARZ3NDEKTSV4RRFFQ69G5FAV", card = randomUUID(), hook = "01ARZ3NDEKTSV4RRFFQ69G5FAW";
  const now = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');
  const owner = database.prepare('SELECT id FROM users WHERE email=?').get(ADMIN.email) as { id: string };
  const cards = [{ id: card, kind: 'note', title: 'Campaign draft ready', body: '<script>window.activityUnsafe=true</script> Review bilingual launch copy.', x: 24, y: 24 }];
  try {
    database.prepare('INSERT INTO admin_canvases(id,owner_id,title,document_json,version,created_at,updated_at) VALUES(?,?,?,?,1,?,?)').run(board, owner.id, 'Campaign workspace', JSON.stringify(cards), now, now);
    database.prepare('INSERT INTO admin_canvas_provenance(board_id,card_id,actor_kind,actor_name,run_id,created_at) VALUES(?,?,?,?,?,?)').run(board, card, 'agent', 'Campaign assistant', randomUUID(), now);
    database.prepare('INSERT INTO webhooks(id,name,url,secret_enc,created_at,updated_at) VALUES(?,?,?,?,?,?)').run(hook, 'Editorial notification', 'https://example.invalid/PRIVATE_URL', 'PRIVATE_SECRET', now, now);
    database.prepare('INSERT INTO webhook_deliveries(id,webhook_id,event,payload,status,error,created_at) VALUES(?,?,?,?,?,?,?)').run(randomUUID(), hook, 'content.created', 'PRIVATE_PAYLOAD', 'failed', 'PRIVATE_ERROR', now);
    await page.goto('/admin');
    await setLocale(page, 'en');
    const activity = page.locator('#canvas-activity');
    await expect(activity.getByText('Campaign draft ready')).toBeVisible();
    await expect(activity.getByText('Editorial notification')).toBeVisible();
    await expect(activity.locator('[data-activity-kind=webhook]')).toHaveAttribute('href', `/admin/webhooks/${hook}`);
    expect(await page.evaluate(() => (window as any).activityUnsafe)).toBeUndefined();
    const html = await activity.innerHTML();
    for (const secret of ['PRIVATE_URL', 'PRIVATE_SECRET', 'PRIVATE_PAYLOAD', 'PRIVATE_ERROR']) expect(html).not.toContain(secret);
    database.prepare("UPDATE webhook_deliveries SET status='delivered' WHERE webhook_id=?").run(hook);
    cards[0].body = 'Review the bilingual launch copy before publication.';
    database.prepare('UPDATE admin_canvases SET document_json=? WHERE id=?').run(JSON.stringify(cards), board);
    const refreshed = page.waitForResponse(r => r.url().endsWith('/admin/x/canvas-activity'));
    await activity.getByRole('button', { name: 'Refresh', exact: true }).click();
    const response = await refreshed;
    expect(response.status()).toBe(200);
    expect(response.headers()['cache-control']).toBe('private, no-store');
    await expect(page.locator('#canvas-activity [data-activity-kind=webhook]')).toHaveAttribute('data-activity-status', 'delivered');
    await page.locator('[data-widget=canvas_gallery]').screenshot({ path: testInfo.outputPath('activity-desktop.png') });
    const detailResponse = await page.request.get(`/admin/webhooks/${hook}`);
    expect(detailResponse.status()).toBe(200);
    await activity.locator('[data-activity-kind=note]').click();
    await expect(page).toHaveURL(new RegExp(`/admin/canvases/${board}#card-${card}$`));
    await expect(page.locator(`#card-${card}`)).toBeVisible();
    await page.goto('/admin');
    await setLocale(page, 'ar');
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await expect(page.locator('#canvas-activity')).toContainText('لوحة النشاط');
    await page.locator('[data-widget=canvas_gallery]').screenshot({ path: testInfo.outputPath('activity-mobile-rtl.png') });
    const overflow = await page.locator('#canvas-activity').evaluate(el => el.scrollWidth > el.clientWidth);
    expect(overflow).toBe(false);
    const rectangles = await page.locator('.canvas-activity-card').evaluateAll(cards => cards.map(c => {const r=c.getBoundingClientRect();return {top:r.top,bottom:r.bottom,left:r.left,right:r.right};}));
    for(let i=1;i<rectangles.length;i++) expect(rectangles[i].top).toBeGreaterThanOrEqual(rectangles[i-1].bottom);
  } finally {
    database.prepare('DELETE FROM webhooks WHERE id=?').run(hook);
    database.prepare('DELETE FROM admin_canvases WHERE id=?').run(board);
    database.close();
  }
});
