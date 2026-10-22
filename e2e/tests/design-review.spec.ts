import { test, expect } from '@playwright/test';
import { createServer } from 'node:http';
import { randomUUID } from 'node:crypto';
import { csrf, db, setLocale } from '../lib/helpers.ts';

// This fixture is loopback-only. No site data or credentials leave the test host.
test('review previews require approval, display untrusted findings and preserve the published page', async ({ page }, testInfo) => {
  const calls: string[] = [];
  const provider = createServer(async (req, res) => {
    let body = '';
    for await (const chunk of req) body += chunk;
    calls.push(body);
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ choices: [{ message: { content: JSON.stringify({ findings: [{
      severity: 'warning', location: 'Page heading', evidence: 'Review fixture heading',
      suggestion: calls.length === 1 ? '<img src=x onerror="window.reviewInjected=true">' : 'Use a clearer heading.',
    }] }) } }] }));
  });
  await new Promise<void>(resolve => provider.listen(0, '127.0.0.1', resolve));
  const address = provider.address();
  if (!address || typeof address === 'string') throw new Error('fixture did not bind');
  const database = db();
  const id = () => randomUUID().replaceAll('-', '').slice(0, 26).toUpperCase();
  const pageID = id(), agentID = id(), slug = 'review-fixture-' + randomUUID();
  const owner = database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as { id: string };
  const now = new Date().toISOString();
  try {
    await page.goto('/admin/settings/ai');
    await setLocale(page, 'en');
    const saved = await page.request.post('/admin/settings/ai', { form: {
      _csrf: await csrf(page), edit_token: await page.locator('[name=edit_token]').inputValue(), provider: 'openai', model: 'local-review-fixture',
      base_url: `http://127.0.0.1:${address.port}/v1`,
    } });
    expect(saved.ok(), `settings save returned ${saved.status()}`).toBe(true);
    database.prepare(`INSERT INTO content(id,type,slug,title,body,author_id,status,locale,created_at,updated_at)
      VALUES(?,'page',?,'Review fixture heading','<h1>Review fixture heading</h1><p>Limited page copy.</p>',?,'published','en',?,?)`).run(pageID, slug, owner.id, now, now);
    database.prepare(`INSERT INTO workagents_agents(id,owner_id,name,enabled,created_at,updated_at,execution_kind)
      VALUES(?,?,'Local review fixture',1,?,?,'text')`).run(agentID, owner.id, now, now);
    const contentBefore = database.prepare('SELECT * FROM content WHERE id=?').get(pageID);
    const settingsBefore = database.prepare('SELECT * FROM settings ORDER BY key').all();
    const publicBefore = await (await page.request.get('/' + slug)).text();

    await page.goto('/admin/workagents/design-reviews?page_id=' + pageID);
    await page.locator('[name=agent_id]').selectOption(agentID);
    await page.locator('form[action$="/preview"] button').focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('[data-review-context]')).toContainText('Review fixture heading');
    expect(calls).toHaveLength(0);
    await expect(page.locator('[name=approved]')).not.toBeChecked();
    await page.locator('[name=approved]').focus();
    await page.keyboard.press('Space');
    await page.locator('form[action$="/enqueue"] button').click();
    await expect.poll(async () => {
      await page.reload();
      return page.locator('[data-review-id]').first().innerText();
    }, { timeout: 20_000 }).toContain('Provider returned invalid findings.');
    const failedID = await page.locator('[data-review-id]').first().getAttribute('data-review-id');
    expect((await (await page.request.get('/api/v1/workagents/design-reviews/' + failedID)).json()).data.error_code).toBe('provider_output_invalid');
    expect(calls).toHaveLength(1);
    await page.locator('form[action$="/retry"] button').click();
    await expect.poll(async () => {
      await page.reload();
      return page.locator('[data-review-id]').first().textContent();
    }, { timeout: 20_000 }).toContain('Use a clearer heading.');
    expect(calls).toHaveLength(2);
    const reviewID = await page.locator('[data-review-id]').first().getAttribute('data-review-id');
    const status = await page.request.get('/api/v1/workagents/design-reviews/' + reviewID);
    expect(status.status()).toBe(200);
    const envelope = await status.json();
    expect(envelope.data.status).toBe('done');
    expect(envelope.data.findings[0].evidence).toBe('Review fixture heading');
    expect(await page.evaluate(() => (window as any).reviewInjected)).toBeUndefined();
    await expect(page.locator('img[src=x]')).toHaveCount(0);
    await page.locator('[data-review-id] details').first().locator('summary').click();
    await page.screenshot({ path: testInfo.outputPath('review-en.png'), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await setLocale(page, 'ar');
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
    await page.screenshot({ path: testInfo.outputPath('review-ar-mobile.png'), fullPage: true });
    expect(database.prepare('SELECT * FROM content WHERE id=?').get(pageID)).toEqual(contentBefore);
    expect(database.prepare('SELECT * FROM settings ORDER BY key').all()).toEqual(settingsBefore);
    // CSP nonces intentionally differ for every response. Compare all other HTML.
    const stableHTML = (html: string) => html.replace(/nonce="[^"]*"/g, 'nonce="[per-request]"');
    expect(stableHTML(await (await page.request.get('/' + slug)).text())).toBe(stableHTML(publicBefore));
    await page.goto('/admin/content/page/' + pageID);
    const sidebar = page.locator('[data-review-sidebar]');
    await expect(sidebar).toBeVisible();
    await sidebar.locator('summary').focus();
    await page.keyboard.press('Enter');
    await expect(sidebar.locator('select option')).toHaveCount(2);
    await sidebar.locator('select').selectOption(agentID);
    await sidebar.locator('[data-review-preview]').click();
    await expect(sidebar.locator('[data-review-context]')).toContainText('Review fixture heading');
    expect(calls).toHaveLength(2);
    await expect(sidebar.locator('[data-review-consent]')).not.toBeChecked();
    await sidebar.locator('[data-review-consent]').focus();
    await page.keyboard.press('Space');
    const enqueued = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/workagents/design-reviews');
    await sidebar.locator('[data-review-enqueue]').click();
    const queuedResponse = await enqueued;
    expect(queuedResponse.status()).toBe(202);
    const queued = (await queuedResponse.json()).data;
    await expect.poll(async () => (await (await page.request.get('/api/v1/workagents/design-reviews/' + queued.id)).json()).data.status).toBe('done');
    expect(calls).toHaveLength(3);
    await page.screenshot({ path: testInfo.outputPath('review-editor-ar-mobile.png'), fullPage: true });
    await page.keyboard.press('Escape');
    await expect(sidebar).not.toHaveAttribute('open');
    await expect(sidebar.locator('summary')).toBeFocused();
    expect(database.prepare('SELECT * FROM content WHERE id=?').get(pageID)).toEqual(contentBefore);
    expect(database.prepare('SELECT * FROM settings ORDER BY key').all()).toEqual(settingsBefore);
    expect(stableHTML(await (await page.request.get('/' + slug)).text())).toBe(stableHTML(publicBefore));
  } finally {
    // Fixture data lives only in the harness's temporary database.
    database.close();
    await new Promise<void>((resolve, reject) => provider.close(error => error ? reject(error) : resolve()));
  }
});
