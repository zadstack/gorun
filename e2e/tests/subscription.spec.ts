// Subscription lifecycle against the running server: signed Stripe webhooks
// move an order through subscribe → pay → fail → grace → suspend → resume.
// Without a live sk_test_ key the order is reopened in SQLite the same way
// commerce.spec.ts reopens a checkout Stripe refused.
import { test, expect, type Page } from '@playwright/test';
import { db, env, stripeSignature } from '../lib/helpers.ts';

const SLUG = 'e2e-sub-plan';
const CUSTOMER = 'subscriber@e2e.test';

async function csrfOf(page: Page, url: string): Promise<string> {
  await page.goto(url);
  return (await page.locator('input[name="_csrf"]').first().getAttribute('value')) ?? '';
}

function signed(type: string, id: string, object: Record<string, unknown>) {
  const ts = Math.floor(Date.now() / 1000);
  const event = { id, object: 'event', api_version: '2025-03-31.basil', created: ts, livemode: false, type, data: { object } };
  const payload = JSON.stringify(event);
  return { payload, header: stripeSignature(payload, env('E2E_WEBHOOK_SECRET'), ts) };
}

test.describe.serial('subscription billing', () => {
  let orderID = '';

  test('create a monthly plan and a pending order', async ({ page }) => {
    await page.goto('/admin/commerce/servers/new');
    await page.fill('#s-name', 'E2E fake billing');
    await page.fill('#s-client', env('E2E_BILLING_CLIENT'));
    await page.fill('#s-url', env('E2E_BILLING_URL'));
    await page.fill('#s-secret', env('E2E_BILLING_SECRET'));
    await page.locator('form[action="/admin/commerce/servers"] button[type="submit"]').click();
    await expect(page).toHaveURL(/\/admin\/commerce\/servers\/[0-9A-Z]{26}$/);

    await page.goto('/admin/commerce/products/new');
    await page.fill('#p-name', 'E2E subscription');
    await page.fill('#p-slug', SLUG);
    await page.fill('#p-price', '49');
    await page.fill('#p-currency', 'AED');
    await page.selectOption('#p-billing', 'subscription');
    await page.selectOption('#p-interval', 'month');
    await page.fill('#p-trial', '7');
    await page.selectOption('#p-server', { label: 'E2E fake billing' });
    await page.fill('#p-plan', 'pro');
    await page.fill('#p-months', '1');
    await page.check('input[name="active"]');
    await page.locator('form[action="/admin/commerce/products"] button[type="submit"]').last().click();
    await expect(page).toHaveURL(/\/admin\/commerce\/products(\/[0-9A-Z]{26})?$/);

    await page.goto('/admin/commerce/settings');
    await page.selectOption('#c-mode', 'live');
    await page.fill('#c-stripe', 'sk_test_e2e_subscription_dummy');
    await page.fill('#c-webhook', env('E2E_WEBHOOK_SECRET'));
    await page.locator('form[action="/admin/commerce/settings"] button[type="submit"]').click();
    await expect(page.locator('[role="status"]').first()).toBeVisible();

    await page.goto(`/product/${SLUG}`);
    await expect(page.locator(`form[action$="/checkout/${SLUG}"]`)).toBeVisible();
    const token = await csrfOf(page, `/product/${SLUG}`);
    const r = await page.request.post(`/checkout/${SLUG}`, {
      form: { _csrf: token, email: CUSTOMER, website: '' },
      maxRedirects: 0,
    });
    expect([303, 502]).toContain(r.status());
    const row = db().prepare('SELECT id, status FROM orders WHERE email = ? ORDER BY created_at DESC').get(CUSTOMER) as { id: string; status: string };
    expect(row?.id).toBeTruthy();
    orderID = row.id;
    if (row.status === 'cancelled') db().prepare("UPDATE orders SET status = 'pending' WHERE id = ?").run(orderID);
  });

  test('subscribe, pay, fail, suspend, resume', async ({ request }) => {
    const session = `cs_e2e_sub_${Date.now()}`;
    const post = async (type: string, id: string, object: Record<string, unknown>) => {
      const { payload, header } = signed(type, id, object);
      const res = await request.post('/webhooks/stripe', {
        headers: { 'Content-Type': 'application/json', 'Stripe-Signature': header },
        data: payload,
      });
      expect(res.status(), type).toBe(200);
    };
    await post('checkout.session.completed', `evt_sub_pay_${orderID}`, {
      id: session, object: 'checkout.session', client_reference_id: orderID, payment_status: 'paid', mode: 'subscription',
      amount_total: 4900, currency: 'aed', payment_intent: `pi_${orderID}`, customer: 'cus_e2e_sub', metadata: { order_id: orderID },
    });
    await post('customer.subscription.created', `evt_sub_new_${orderID}`, {
      id: 'sub_e2e', object: 'subscription', customer: 'cus_e2e_sub', status: 'active',
      metadata: { order_id: orderID, plan: 'pro', account_id: 'acct' },
      items: { data: [{ price: { id: 'price_e2e', currency: 'aed' } }] },
    });
    await post('invoice.paid', `evt_sub_invoice_${orderID}`, {
      id: 'in_e2e_paid', object: 'invoice', customer: 'cus_e2e_sub', subscription: 'sub_e2e', status: 'paid',
      hosted_invoice_url: 'https://pay.stripe.test/in_e2e_paid', invoice_pdf: 'https://pay.stripe.test/in_e2e_paid.pdf',
      currency: 'aed', amount_paid: 4900,
    });
    await post('invoice.payment_failed', `evt_sub_fail_${orderID}`, {
      id: 'in_e2e_fail', object: 'invoice', customer: 'cus_e2e_sub', subscription: 'sub_e2e', status: 'open',
      hosted_invoice_url: 'https://pay.stripe.test/in_e2e_fail', invoice_pdf: 'https://pay.stripe.test/in_e2e_fail.pdf',
      currency: 'aed', amount_due: 4900,
    });
    const pastDue = db().prepare(`SELECT status FROM subscriptions WHERE stripe_subscription_id = 'sub_e2e'`).get() as { status: string };
    expect(pastDue.status).toBe('past_due');
    await post('customer.subscription.deleted', `evt_sub_del_${orderID}`, {
      id: 'sub_e2e', object: 'subscription', customer: 'cus_e2e_sub', status: 'canceled',
      metadata: { order_id: orderID, plan: 'pro' },
    });
    const canceled = db().prepare(`SELECT status FROM subscriptions WHERE stripe_subscription_id = 'sub_e2e'`).get() as { status: string };
    expect(canceled.status).toBe('canceled');
    await post('invoice.paid', `evt_sub_resume_${orderID}`, {
      id: 'in_e2e_resume', object: 'invoice', customer: 'cus_e2e_sub', subscription: 'sub_e2e', status: 'paid',
      hosted_invoice_url: 'https://pay.stripe.test/in_e2e_resume', invoice_pdf: 'https://pay.stripe.test/in_e2e_resume.pdf',
      currency: 'aed', amount_paid: 4900,
    });
    const resumed = db().prepare(`SELECT status FROM subscriptions WHERE stripe_subscription_id = 'sub_e2e'`).get() as { status: string };
    expect(resumed.status).toBe('active');
    const invoice = db().prepare(`SELECT hosted_invoice_url FROM invoices WHERE stripe_invoice_id = 'in_e2e_paid'`).get() as { hosted_invoice_url: string };
    expect(invoice.hosted_invoice_url).toContain('in_e2e_paid');
  });

  test('clear the dummy test key', async ({ page }) => {
    await page.goto('/admin/commerce/settings');
    await page.check('input[name="clear_stripe_secret_key"]');
    await page.locator('form[action="/admin/commerce/settings"] button[type="submit"]').click();
    await expect(page.locator('[role="status"]').first()).toBeVisible();
  });
});
