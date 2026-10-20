// Shared helpers for the GoRun specs. Specs select elements by name=,
// action= and href= attributes (stable across locales) and only assert copy
// where the copy itself is under test.
import { expect, type APIRequestContext, type Locator, type Page } from '@playwright/test';
import { createHmac } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { DatabaseSync } from 'node:sqlite';

// Test-only credentials for the throwaway e2e instance (temp data dir).
export const ADMIN = { name: 'E2E Admin', email: 'admin@e2e.test', password: 'e2e-admin-password-0001' };

export const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set (global-setup.ts exports it)`);
  return v;
};

// MARKER_500 is in every theme-engine 500 body (internal/theme/render.go Marker500).
export const MARKER_500 = '<!-- gorun:500 -->';

// expectNoServerError fails when the page is the theme 500 page or the admin
// error page.
export async function expectNoServerError(page: Page): Promise<void> {
  const html = await page.content();
  expect(html, `500 marker on ${page.url()}`).not.toContain(MARKER_500);
  await expect(page.locator('body')).not.toContainText('Internal Server Error');
}

export async function csrf(page: Page): Promise<string> {
  const v = await page.locator('input[name="_csrf"]').first().getAttribute('value');
  if (!v) throw new Error('no _csrf input on ' + page.url());
  return v;
}

// setLocale switches the admin UI language for the signed-in user (stored on
// the user and in the locale cookie) and reloads the current page.
export async function setLocale(page: Page, locale: 'ar' | 'en'): Promise<void> {
  if (!page.url().includes('/admin')) await page.goto('/admin');
  const token = await csrf(page);
  const r = await page.request.post('/admin/locale', { form: { locale, _csrf: token }, maxRedirects: 0 });
  expect(r.status()).toBe(303);
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('lang', locale);
}

// setContentLocale picks the content language on a new-item editor. New content
// defaults to the admin UI locale; call this when the public site locale must
// differ (or when a later translation step expects a specific source locale).
export async function setContentLocale(page: Page, locale: 'ar' | 'en'): Promise<void> {
  await page.locator('#editor-locale').selectOption(locale);
  await expect(page.locator('#editor-locale')).toHaveValue(locale);
}

// submitAndLand clicks a form's submit button and returns once its POST has
// answered with status (the admin's post/redirect/get 303; null accepts any) and the page it
// redirects to has loaded. A reload, goto or database read straight after a
// bare click() can run before the POST is sent or committed, and a flash
// already on the page (e.g. "created") cannot tell the old page from the new.
export async function submitAndLand(page: Page, button: Locator, path: string, status: number | null = 303): Promise<void> {
  const posted = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === path);
  const navigated = page.waitForEvent('framenavigated', frame => frame === page.mainFrame());
  await button.click();
  const response = await posted;
  if (status !== null) expect(response.status()).toBe(status);
  await navigated;
  await page.waitForLoadState('load');
}

export function serverLog(): string {
  return readFileSync(env('E2E_LOG'), 'utf8');
}

// waitForLog polls the server log until re matches a line written after
// offset (pass serverLog().length taken before the action).
export async function waitForLog(re: RegExp, offset = 0, timeout = 20_000): Promise<RegExpMatchArray> {
  const deadline = Date.now() + timeout;
  for (;;) {
    const m = serverLog().slice(offset).match(re);
    if (m) return m;
    if (Date.now() > deadline) throw new Error(`log never matched ${re}`);
    await new Promise((ok) => setTimeout(ok, 250));
  }
}

// db opens the run's SQLite database (WAL; the server keeps writing). Used
// read-only for assertions, and for the one documented fixture tweak in the
// commerce spec.
export function db(): DatabaseSync {
  const d = new DatabaseSync(env('E2E_DB'));
  d.exec('PRAGMA busy_timeout = 5000');
  return d;
}

// stripeSignature builds a Stripe-Signature header for payload exactly like
// stripe-go's webhook.GenerateTestSignedPayload: t=<unix>,v1=hex(hmac(secret, "<t>.<payload>")).
export function stripeSignature(payload: string, secret: string, ts = Math.floor(Date.now() / 1000)): string {
  const v1 = createHmac('sha256', secret).update(`${ts}.${payload}`).digest('hex');
  return `t=${ts},v1=${v1}`;
}

export async function mcp(request: APIRequestContext, key: string, method: string, params: unknown = {}, id = 1) {
  const r = await request.post('/mcp', {
    headers: {
      Authorization: `Bearer ${key}`,
      'Content-Type': 'application/json',
      Accept: 'application/json, text/event-stream',
    },
    data: { jsonrpc: '2.0', id, method, params },
  });
  const text = await r.text();
  let body: any = null;
  try {
    body = JSON.parse(text);
  } catch {
    // an SSE frame: take the last data: line
    const m = text.match(/data: (.*)\s*$/m);
    body = m ? JSON.parse(m[1]) : text;
  }
  return { status: r.status(), body };
}

// toolPayload returns the JSON envelope a GoRun MCP tool answered.
export function toolPayload(result: any): any {
  if (result?.structuredContent) return result.structuredContent;
  const text = result?.content?.[0]?.text;
  return text ? JSON.parse(text) : result;
}
