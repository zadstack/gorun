// Commerce rehearsal with the fake mcp-billing/v1 server
// (fixtures/fake-billing-server.ts): billing server + product in the admin →
// /shop → checkout → Stripe-signed checkout.session.completed webhook → order
// paid → provisioning job calls the fake server → magic link (from the server
// log) → /account shows the entitlement as active → "Open" gets a login link.
//
// Stripe: with STRIPE_SECRET_KEY=sk_test_… in the environment the checkout
// must answer 303 to checkout.stripe.com and the webhook carries the real
// session id. Without it, the spec asserts the "Stripe not configured" path,
// then saves a dummy sk_test key so checkout creates the order (Stripe then
// rejects the key and the order is cancelled), flips that one order back to
// pending in SQLite (the only fixture tweak in the suite) and delivers the
// signed webhook. See docs/E2E.md.
import { test, expect, type Page } from '@playwright/test';
import { db, env, serverLog, stripeSignature, waitForLog } from '../lib/helpers.ts';

const SLUG = 'e2e-pro-plan';
const PRODUCT = 'E2E Pro plan';
const CUSTOMER = 'buyer@e2e.test';
const PRICE_MINOR = 4900;
const realStripe = /^sk_test_/.test(process.env.STRIPE_SECRET_KEY ?? '');

async function csrfOf(page: Page, url: string): Promise<string> {
  await page.goto(url);
  return (await page.locator('input[name="_csrf"]').first().getAttribute('value')) ?? '';
}

test.describe.serial('commerce with the fake billing server', () => {
  let orderID = '';
  let sessionID = '';

  test('billing server: create and test the connection', async ({ page }) => {
    await page.goto('/admin/commerce/servers/new');
    await page.fill('#s-name', 'E2E fake billing');
    await page.fill('#s-client', env('E2E_BILLING_CLIENT'));
    await page.fill('#s-url', env('E2E_BILLING_URL'));
    await page.fill('#s-secret', env('E2E_BILLING_SECRET'));
    await page.locator('form[action="/admin/commerce/servers"] button[type="submit"]').click();
    await expect(page).toHaveURL(/\/admin\/commerce\/servers\/[0-9A-Z]{26}$/);
    await page.locator('form[action$="/test"] button[type="submit"]').click();
    // "Test connection" lists the plans from GET /catalog (signed)
    await expect(page.locator('main')).toContainText('pro');
    await expect(page.locator('main')).toContainText('team');
    const calls = await (await page.request.get(`${env('E2E_BILLING_URL')}/__calls`)).json();
    expect(calls.calls.some((c: any) => c.method === 'GET' && c.path === '/catalog' && !c.rejected)).toBe(true);
  });

  test('product: create it and see it on /shop', async ({ page }) => {
    await page.goto('/admin/commerce/products/new');
    await page.fill('#p-name', PRODUCT);
    await page.fill('#p-slug', SLUG);
    await page.fill('#p-short', 'Fake MCP access for the e2e suite');
    await page.fill('#p-price', String(PRICE_MINOR / 100));
    await page.fill('#p-currency', 'AED');
    await page.selectOption('#p-server', { label: 'E2E fake billing' });
    await page.fill('#p-plan', 'pro');
    await page.fill('#p-months', '1');
    await page.fill('#p-group', 'E2E plans');
    await page.check('input[name="active"]');
    await page.locator('form[action="/admin/commerce/products"] button[type="submit"]').last().click();
    await expect(page).toHaveURL(/\/admin\/commerce\/products(\/[0-9A-Z]{26})?$/);

    const shop = await page.goto('/shop');
    expect(shop?.status()).toBe(200);
    await expect(page.locator('main')).toContainText(PRODUCT);
    const link = page.locator(`main a[href$="/product/${SLUG}"]`).first();
    await expect(link).toBeVisible();
    await link.click();
    await expect(page.locator('h1')).toContainText(PRODUCT);
    await expect(page.locator(`form[action$="/checkout/${SLUG}"]`)).toBeVisible();
  });

  test('checkout: Stripe redirect (test key) or the "not configured" path', async ({ page }) => {
    const token = await csrfOf(page, `/product/${SLUG}`);
    const post = () =>
      page.request.post(`/checkout/${SLUG}`, { form: { _csrf: token, email: CUSTOMER, website: '' }, maxRedirects: 0 });

    if (realStripe) {
      const r = await post();
      expect(r.status()).toBe(303);
      expect(new URL(r.headers().location).host).toBe('checkout.stripe.com');
    } else {
      test.info().annotations.push({ type: 'stripe', description: 'STRIPE_SECRET_KEY not set: not-configured path + signed-webhook simulation' });
      const r = await post();
      expect(r.status()).toBe(503);
      await page.goto(`/product/${SLUG}`);
      await page.fill('#checkout-email', CUSTOMER);
      await page.locator(`form[action$="/checkout/${SLUG}"] button[type="submit"]`).click();
      await expect(page.locator('[role="alert"]')).toBeVisible();
      expect(db().prepare('SELECT count(*) AS n FROM orders').get()!.n).toBe(0); // nothing created

      // a dummy test key: the order and customer are created by the app, then
      // Stripe refuses the key and the app cancels the order
      await page.goto('/admin/commerce/settings');
      await page.fill('#c-stripe', 'sk_test_e2e_dummy_key_not_valid');
      await page.locator('form[action="/admin/commerce/settings"] button[type="submit"]').click();
      await expect(page.locator('[role="status"]').first()).toBeVisible();
      const r2 = await page.request.post(`/checkout/${SLUG}`, {
        form: { _csrf: await csrfOf(page, `/product/${SLUG}`), email: CUSTOMER, website: '' },
        maxRedirects: 0,
      });
      expect(r2.status()).toBe(502); // commerce.checkout.failed
    }

    const row = db().prepare('SELECT id, status, total_minor, currency, stripe_session_id FROM orders WHERE email = ? ORDER BY created_at DESC').get(CUSTOMER) as any;
    expect(row).toBeTruthy();
    expect(row.total_minor).toBe(PRICE_MINOR);
    orderID = row.id;
    if (realStripe) {
      expect(row.status).toBe('pending');
      sessionID = row.stripe_session_id;
    } else {
      expect(row.status).toBe('cancelled');
      // fixture tweak: reopen the order Stripe could not create a session for
      db().prepare("UPDATE orders SET status = 'pending' WHERE id = ?").run(orderID);
      sessionID = `cs_test_e2e_${Date.now()}`;
      // and drop the dummy key again
      await page.goto('/admin/commerce/settings');
      await page.check('input[name="clear_stripe_secret_key"]');
      await page.locator('form[action="/admin/commerce/settings"] button[type="submit"]').click();
    }
  });

  test('signed webhook → paid → provisioned on the fake billing server', async ({ request }) => {
    const ts = Math.floor(Date.now() / 1000);
    const event = {
      id: `evt_e2e_${ts}`,
      object: 'event',
      api_version: '2025-03-31.basil',
      created: ts,
      livemode: false,
      type: 'checkout.session.completed',
      data: {
        object: {
          id: sessionID,
          object: 'checkout.session',
          client_reference_id: orderID,
          customer_email: CUSTOMER,
          payment_status: 'paid',
          status: 'complete',
          mode: 'payment',
          amount_total: PRICE_MINOR,
          currency: 'aed',
          payment_intent: `pi_e2e_${ts}`,
          metadata: { order_id: orderID },
        },
      },
    };
    const payload = JSON.stringify(event);
    const bad = await request.post('/webhooks/stripe', {
      headers: { 'Content-Type': 'application/json', 'Stripe-Signature': stripeSignature(payload, 'whsec_wrong') },
      data: payload,
    });
    expect(bad.status()).toBeGreaterThanOrEqual(400);

    const logOffset = serverLog().length;
    const ok = await request.post('/webhooks/stripe', {
      headers: { 'Content-Type': 'application/json', 'Stripe-Signature': stripeSignature(payload, env('E2E_WEBHOOK_SECRET'), ts) },
      data: payload,
    });
    expect(ok.status()).toBe(200);

    // the provisioning job runs on the server's workers
    await expect
      .poll(async () => {
        const r = await request.get('/api/v1/commerce/orders');
        const list = (await r.json()).data as any[];
        return list.find((o) => o.id === orderID)?.status;
      }, { timeout: 30_000 })
      .toBe('active');

    const calls = (await (await request.get(`${env('E2E_BILLING_URL')}/__calls`)).json()).calls as any[];
    const create = calls.find((c) => c.method === 'POST' && c.path === '/accounts' && !c.rejected);
    expect(create, 'POST /accounts reached the fake billing server').toBeTruthy();
    expect(create.body.plan_code).toBe('pro');
    expect(create.body.order_ref).toBe(`gp_${orderID}`);
    expect(create.idempotencyKey).toBe(`create:${orderID}`);
    // field order is part of the PHP contract
    expect(Object.keys(create.body)).toEqual(['external_customer_id', 'order_ref', 'plan_code', 'period_end']);

    const ents = (await (await request.get('/api/v1/commerce/entitlements')).json()).data as any[];
    expect(ents.find((e) => e.order_id === orderID)?.status).toBe('active');

    // a replayed event changes nothing
    const again = await request.post('/webhooks/stripe', {
      headers: { 'Content-Type': 'application/json', 'Stripe-Signature': stripeSignature(payload, env('E2E_WEBHOOK_SECRET')) },
      data: payload,
    });
    expect(again.status()).toBe(200);
    const creates = (await (await request.get(`${env('E2E_BILLING_URL')}/__calls`)).json()).calls.filter(
      (c: any) => c.method === 'POST' && c.path === '/accounts' && !c.replayed,
    );
    expect(creates).toHaveLength(1);

    // the access email (SMTP not configured → logged) carries the magic link
    const m = await waitForLog(/auth\/magic\/([A-Za-z0-9_\-]+)/, logOffset);
    process.env.E2E_MAGIC_TOKEN = m[1];
  });

  test('magic link → /account shows the entitlement as active → Open gets a login link', async ({ browser }) => {
    const ctx = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    const page = await ctx.newPage();
    const token = process.env.E2E_MAGIC_TOKEN;
    expect(token, 'magic token from the log').toBeTruthy();
    await page.goto(`/auth/magic/${token}`);
    await page.locator('form[action^="/auth/magic/"] button[type="submit"]').click();
    await expect(page).toHaveURL(/\/account$/);
    const ent = page.locator('li[data-status]').filter({ hasText: PRODUCT });
    await expect(ent).toHaveAttribute('data-status', 'active');
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');

    const form = ent.locator('form[action^="/account/open/"]');
    const action = (await form.getAttribute('action'))!;
    const csrfToken = (await form.locator('input[name="_csrf"]').getAttribute('value'))!;
    const open = await page.request.post(action, { form: { _csrf: csrfToken }, maxRedirects: 0 });
    expect(open.status()).toBe(303);
    expect(open.headers().location).toMatch(/^https:\/\/billing\.example\.test\/login\//);
    await ctx.close();
  });

  test('/account login form sends a link without revealing whether the email is known', async ({ browser }) => {
    const ctx = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    const page = await ctx.newPage();
    for (const email of [CUSTOMER, 'nobody@e2e.test']) {
      await page.goto('/account');
      await page.fill('#account-email', email);
      await page.locator('form[action="/account/login"] button[type="submit"]').click();
      await expect(page.locator('[role="status"]')).toBeVisible();
    }
    await ctx.close();
  });
});
