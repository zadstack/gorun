import { test, expect } from '@playwright/test';
import { db } from '../lib/helpers.ts';

// Chromium does not ship document.modelContext yet. This stand-in follows the
// WebMCP draft (registerTool(tool, {signal}), unregister on abort) and records
// what the admin registers, so the test drives the real tools.
const fakeModelContext = () => {
  const tools = new Map<string, any>();
  (window as any).__webmcp = tools;
  Object.defineProperty(document, 'modelContext', {configurable: true, value: {
    registerTool(tool: any, options: {signal?: AbortSignal} = {}) {
      if (!tool.name || !tool.description || typeof tool.execute !== 'function') return Promise.reject(new TypeError('invalid tool'));
      tools.set(tool.name, tool);
      options.signal?.addEventListener('abort', () => tools.delete(tool.name));
      return Promise.resolve();
    },
  }});
};

async function call(page: any, name: string, input: object = {}) {
  return page.evaluate(async ([name, input]: [string, object]) => {
    const tool = (window as any).__webmcp.get(name);
    if (!tool) throw new Error('not registered: ' + name);
    return JSON.parse(JSON.stringify(await tool.execute(input, {signal: new AbortController().signal})));
  }, [name, input]);
}

test('admin registers WebMCP tools that act as the user and only create drafts', async ({ page }) => {
  await page.addInitScript(fakeModelContext);
  await page.goto('/admin');
  await expect.poll(() => page.evaluate(() => [...(window as any).__webmcp.keys()].sort())).toEqual(
    ['create_content_draft', 'get_current_page', 'list_admin_pages', 'open_admin_page', 'search_admin']);
  const annotations = await page.evaluate(() => Object.fromEntries([...(window as any).__webmcp.values()].map((t: any) => [t.name, t.annotations?.readOnlyHint === true])));
  expect(annotations).toEqual({create_content_draft: false, get_current_page: true, list_admin_pages: true, open_admin_page: false, search_admin: true});

  const manifest = await call(page, 'list_admin_pages');
  expect(manifest.can_draft).toBe(true);
  expect(manifest.content_types.map((t: any) => t.slug)).toContain('post');

  const draft = await call(page, 'create_content_draft', {type: 'post', title: 'WebMCP browser draft', body: '<p>From an in-page agent.</p>'});
  expect(draft.status).toBe('draft');
  const stored = db();
  expect((stored.prepare('SELECT status FROM content WHERE id=?').get(draft.id) as any).status).toBe('draft');
  stored.close();

  const found = await call(page, 'search_admin', {query: 'WebMCP browser'});
  expect(found.content.map((c: any) => c.href)).toContain(draft.href);

  // Only admin paths can be opened; the new page registers its own tools.
  await expect(call(page, 'open_admin_page', {href: 'https://example.com/admin'})).rejects.toThrow(/admin path/);
  await call(page, 'open_admin_page', {href: draft.href});
  await expect(page).toHaveURL(new RegExp(draft.href + '$'));
  await expect.poll(() => page.evaluate(() => (window as any).__webmcp.size)).toBe(5);
  const current = await call(page, 'get_current_page');
  expect(current.path).toBe(draft.href);
  expect(current.text.length).toBeGreaterThan(0);
});

test('admin pages do nothing without WebMCP', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto('/admin');
  expect(await page.evaluate(() => 'modelContext' in document)).toBe(false);
  expect(errors).toEqual([]);
});
