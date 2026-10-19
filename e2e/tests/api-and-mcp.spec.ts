// REST API + MCP: an API key (kind api) reads the OpenAPI document and the
// content list; an agent key is refused on /api, lists tools on /mcp, and a
// sensitive tool call waits for a human approval in the admin UI before
// check_approval returns its result.
import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import { csrf, env, mcp, toolPayload } from '../lib/helpers.ts';

async function createKey(page: Page, kind: 'api' | 'agent', name: string, scopes: string[] | '*'): Promise<string> {
  await page.goto('/admin/agents/keys');
  await page.locator('.audit-key-create > summary').click();await page.locator('[data-key-permissions] > summary').click();
  await page.check(`input[name="kind"][value="${kind}"]`);
  await page.fill('#k-name', name);
  if (scopes === '*') await page.check('input[name="all"]');
  else for (const s of scopes) await page.check(`input[name="scopes"][value="${s}"]`);
  await page.locator('form[action="/admin/agents/keys"] button[type="submit"]').click();
  const plaintext = page.locator('#k-plaintext');
  await expect(plaintext).toBeVisible();
  const key = await plaintext.inputValue();
  expect(key).toMatch(/^gp_/);
  return key;
}

test.describe.serial('API keys, OpenAPI and MCP', () => {
  let apiKey = '';
  let agentKey = '';
  let draftID = '';
  let approvalID = '';

  test('API key (kind api): OpenAPI document and bearer content list', async ({ page, playwright }) => {
    apiKey = await createKey(page, 'api', 'E2E REST key', '*');
    // shown once: a reload no longer reveals it
    await page.goto('/admin/agents/keys');
    await expect(page.locator('#k-plaintext')).toHaveCount(0);

    const anon = await playwright.request.newContext({ baseURL: test.info().project.use.baseURL, storageState: { cookies: [], origins: [] } });
    const spec = await anon.get('/api/v1/openapi.json');
    expect(spec.status()).toBe(200);
    const doc = await spec.json();
    expect(String(doc.openapi)).toMatch(/^3\./);
    expect(doc.info?.title).toBeTruthy();
    const paths = Object.keys(doc.paths ?? {});
    expect(paths.length).toBeGreaterThan(20);
    expect(paths.some((p) => /\/content$/.test(p))).toBe(true);
    for (const [p, ops] of Object.entries<Record<string, any>>(doc.paths)) {
      for (const [method, op] of Object.entries(ops)) {
        if (!['get', 'post', 'put', 'patch', 'delete'].includes(method)) continue;
        expect(op.responses, `${method.toUpperCase()} ${p} has responses`).toBeTruthy();
      }
    }

    const unauth = await anon.get('/api/v1/content?type=post');
    expect(unauth.status()).toBe(401);
    const list = await anon.get('/api/v1/content?type=post', { headers: { Authorization: `Bearer ${apiKey}` } });
    expect(list.status()).toBe(200);
    const body = await list.json();
    expect(body.ok).toBe(true);
    expect(Array.isArray(body.data)).toBe(true);
    const me = await anon.get('/api/v1/me', { headers: { Authorization: `Bearer ${apiKey}` } });
    expect(me.status()).toBe(200);
    await anon.dispose();
  });

  test('agent key: refused on /api, tools/list over HTTP MCP', async ({ page, request }) => {
    agentKey = await createKey(page, 'agent', 'E2E agent key', ['content.view', 'content.create', 'content.publish']);
    const me = await request.get('/api/v1/me', { headers: { Authorization: `Bearer ${agentKey}` } });
    expect(me.status()).toBe(403);

    const noKey = await request.post('/mcp', { data: { jsonrpc: '2.0', id: 1, method: 'tools/list' } });
    expect(noKey.status()).toBe(401);

    const { status, body } = await mcp(request, agentKey, 'tools/list');
    expect(status).toBe(200);
    const names: string[] = body.result.tools.map((t: { name: string }) => t.name);
    expect(names).toEqual(expect.arrayContaining(['get_site_structure', 'list_content', 'create_content', 'publish_content', 'check_approval']));
    expect(names).not.toContain('delete_content'); // not in the key's scopes
    expect(names).not.toContain('update_settings');
  });

  test('sensitive tool → pending_approval → approve in admin → check_approval has the result', async ({ page, request }) => {
    const created = await mcp(request, agentKey, 'tools/call', {
      name: 'create_content',
      arguments: { type: 'post', title: 'E2E agent draft', body: '<p>written by an agent</p>' },
    });
    expect(created.status).toBe(200);
    const c = toolPayload(created.body.result);
    expect(c.ok).toBe(true);
    draftID = c.data.id;
    expect(c.data.status).toBe('draft');

    const pub = await mcp(request, agentKey, 'tools/call', { name: 'publish_content', arguments: { id: draftID } });
    const p = toolPayload(pub.body.result);
    const pending = p.data ?? p;
    expect(pending.status).toBe('pending_approval');
    approvalID = pending.approval_id;
    expect(approvalID).toBeTruthy();

    // still a draft
    const before = await mcp(request, agentKey, 'tools/call', { name: 'check_approval', arguments: { approval_id: approvalID } });
    expect(JSON.stringify(toolPayload(before.body.result))).toContain('pending_approval');

    // a human approves it in AI Agents → Approvals
    await page.goto('/admin/agents/approvals');
    const form = page.locator(`form[action="/admin/agents/approvals/${approvalID}/approve"]`);
    await expect(form).toBeVisible();
    await expect(page.locator('main')).toContainText('publish_content');
    await form.locator('button[type="submit"]').click();
    const modal = page.locator('[role="dialog"][aria-labelledby="gp-confirm-title"]');
    await expect(modal).toBeVisible();
    await modal.locator('button[\\@click="$store.confirm.yes()"]').click();
    await expect(form).toHaveCount(0);

    const after = await mcp(request, agentKey, 'tools/call', { name: 'check_approval', arguments: { approval_id: approvalID } });
    const a = toolPayload(after.body.result);
    const st = a.data ?? a;
    expect(st.status).toBe('approved');
    expect(JSON.stringify(st.result)).toContain(draftID);

    const item = await mcp(request, agentKey, 'tools/call', { name: 'get_content', arguments: { id: draftID } });
    expect(toolPayload(item.body.result).data.status).toBe('published');
  });

  test('template draft requires approval and its preview cannot execute JavaScript', async ({ page, request }) => {
    const key = await createKey(page, 'agent', 'E2E template agent', ['themes.manage']);
    const draft = await mcp(request, key, 'tools/call', {
      name: 'create_template_draft', arguments: {
        name: 'front-page.html',
        body: '<!doctype html><html><body><p id="preview-safe">E2E static preview</p><script nonce="{{nonce}}">document.documentElement.dataset.previewExecuted="yes"</script></body></html>',
      },
    });
    const payload = toolPayload(draft.body.result);
    const pending = payload.data ?? payload;
    expect(pending.status).toBe('pending_approval');
    await page.goto('/admin/appearance/templates/edit?name=front-page.html');
    await expect(page.locator('#template-body')).not.toHaveValue(/E2E static preview/);
    await page.goto('/admin/agents/approvals');
    const approval = page.locator(`form[action="/admin/agents/approvals/${pending.approval_id}/approve"]`);
    await approval.locator('button[type="submit"]').click();
    const modal = page.locator('[role="dialog"][aria-labelledby="gp-confirm-title"]');
    await expect(modal).toBeVisible();
    await modal.locator('button[\\@click="$store.confirm.yes()"]').click();
    await expect(approval).toHaveCount(0);
    try {
      const live = await page.goto('/');
      expect(live?.status()).toBe(200);
      await expect(page.locator('#preview-safe')).toHaveCount(0);
      const preview = await page.goto('/?gp_preview_override=1');
      expect(preview?.headers()['content-security-policy']).toContain("script-src 'none'");
      expect(preview?.headers()['content-security-policy']).toContain('sandbox');
      await expect(page.locator('#preview-safe')).toBeVisible();
      await expect(page.locator('html')).not.toHaveAttribute('data-preview-executed', 'yes');
    } finally {
      await page.goto('/admin/appearance/templates/edit?name=front-page.html');
      const deleted = await page.request.post('/admin/appearance/templates/edit?name=front-page.html', {
        form: { _csrf: await csrf(page), action: 'delete' }, maxRedirects: 0,
      });
      expect(deleted.status()).toBe(303);
    }
  });

  test('OpenAI settings require a destination before saving', async ({ page }) => {
    await page.goto('/admin/settings/ai');
    await page.selectOption('select[name="provider"]', 'openai');
    await page.fill('input[name="base_url"]', '');
    await page.fill('input[name="model"]', 'model');
    const saved = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/admin/settings/ai'));
    await page.locator('form[action="/admin/settings/ai"] button[type="submit"]').click();
    expect((await saved).status()).toBe(422);
    await expect(page.locator('#ai-base-url').locator('..').locator('.field-error')).toBeVisible();
  });

  test('maintenance closes the shop while admin assets and authenticated MCP remain usable', async ({ page, request }) => {
    const setMaintenance = (enabled: boolean) => execFileSync(resolve('.bin/gorun'), [
      'settings', 'set', 'site.maintenance', String(enabled), '--data', env('E2E_DATA'),
    ], { env: { PATH: process.env.PATH, GORUN_ENV: 'dev' } });
    setMaintenance(true);
    try {
      expect((await request.get('/shop')).status()).toBe(503);
      expect((await request.get('/account')).status()).toBe(503);
      expect((await request.post('/checkout/unknown')).status()).toBe(503);
      const response = await page.goto('/admin');
      expect(response?.status()).toBe(200);
      expect((await request.get('/static/admin/admin.css')).status()).toBe(200);
      expect((await request.get('/static/admin/admin.js')).status()).toBe(200);
      await expect(page.locator('main')).toBeVisible();
      const tools = await mcp(request, agentKey, 'tools/list');
      expect(tools.status).toBe(200);
      expect(tools.body.result.tools.length).toBeGreaterThan(0);
      expect((await request.get('/healthz')).status()).toBe(200);
    } finally {
      setMaintenance(false);
    }
    expect((await request.get('/shop')).status()).toBe(200);
  });
});
