import { test, expect } from '@playwright/test';
import { env, db, setLocale, expectNoServerError } from '../lib/helpers.ts';

test('WXR image404 evidence matches native validation in Arabic and English', async ({ page, request }, info) => {
  const base = env('E2E_WP_URL');
  const xml = `<?xml version="1.0"?><rss version="2.0" xmlns:wp="http://wordpress.org/export/1.2/" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Core audit</title><link>${base}</link><wp:wxr_version>1.2</wp:wxr_version><wp:base_site_url>${base}</wp:base_site_url><wp:base_blog_url>${base}</wp:base_blog_url><item><title>Safe attachment</title><wp:post_id>1</wp:post_id><wp:post_type>attachment</wp:post_type><wp:status>inherit</wp:status><wp:post_date_gmt>2020-01-01 12:00:00</wp:post_date_gmt><wp:attachment_url>${base}/wp-content/uploads/audit.png</wp:attachment_url></item><item><title>Core audit عنوان</title><link>${base}/core-audit</link><wp:post_id>10</wp:post_id><wp:post_type>post</wp:post_type><wp:post_name>core-audit</wp:post_name><wp:status>publish</wp:status><wp:post_date_gmt>2020-01-01 12:00:00</wp:post_date_gmt><content:encoded><![CDATA[<p>Local fixture</p><img src="/media/audit-missing.png?signature=private"><img src="https://cdn.invalid/capability/private-path-token/a.png?signature=query-token"><img src="/protected/root-path-token/a.png"><a href="/protected/href-private-token/a?sig=query-token">unknown link</a>]]></content:encoded><wp:postmeta><wp:meta_key>_thumbnail_id</wp:meta_key><wp:meta_value>1</wp:meta_value></wp:postmeta></item></channel></rss>`;
  await page.goto('/admin/migrations/new');
  await page.locator('#mig-wxr').setInputFiles({ name: 'audit.xml', mimeType: 'application/xml', buffer: Buffer.from(xml) });
  await page.locator('form[hx-post="/admin/migrations/upload"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/admin\/migrations\/[0-9A-Z]{26}\/scan$/);
  const path = new URL(page.url()).pathname.replace(/\/scan$/, '');
  await page.goto(`${path}/import`);
  await page.locator('#mig-progress button[hx-post$="/start"]').click();
  await expect(page.locator('#mig-progress [data-status]').first()).toHaveAttribute('data-status', 'validated', { timeout: 60000 });
  const rows = db().prepare("SELECT id,body,featured_media_id FROM content WHERE slug='core-audit'").all() as any[];
  expect(rows).toHaveLength(1);
  expect(rows[0].featured_media_id).toBeTruthy();
  const featured = db().prepare('SELECT url FROM media WHERE id=?').get(rows[0].featured_media_id) as any;
  expect((await request.get(featured.url)).status()).toBe(200);
  expect((await request.get('/media/audit-missing.png')).status()).toBe(404);
  await page.goto(`${path}/validate`);
  const id = path.split('/').pop();
  const report = (await (await request.get(`/api/v1/migrations/${id}/report`)).json()).data;
  if (process.env.E2E_AUDIT_BASELINE === '1') {
    expect(report.missing_media).toHaveLength(0);
  } else {
    expect(report.readiness).toBe('blocked');
    // The export's root-relative image belongs to the source site; it failed to
    // import, so the article that still shows it is blocked and listed.
    expect(report.missing_media.some((i: any) => i.content_id === rows[0].id)).toBe(true);
    expect(report.errors.some((i: any) => i.code === 'retained_source_media' && i.content_id === rows[0].id)).toBe(true);
    expect(rows[0].body).toContain(`src="${base}/media/audit-missing.png`);
    expect(JSON.stringify(report)).not.toContain('signature=private');
    expect(JSON.stringify(report)).not.toContain('private-path-token');
    expect(JSON.stringify(report)).not.toContain('query-token');
    expect(JSON.stringify(report)).not.toContain('root-path-token');
    expect(JSON.stringify(report)).not.toContain('href-private-token');
    expect(report.errors.some((i: any) => i.code === 'broken_internal_link' && i.content_id === rows[0].id)).toBe(true);
    await expect(page.locator('body')).not.toContainText('private-path-token');
    await expect(page.locator('[data-readiness="blocked"]')).toBeVisible();
  }
  for (const locale of ['ar', 'en'] as const) {
    await setLocale(page, locale);
    await expect(page.locator('html')).toHaveAttribute('dir', locale === 'ar' ? 'rtl' : 'ltr');
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
      await page.screenshot({ path: info.outputPath(`validation-${locale}-${width}.png`), fullPage: true });
      await expectNoServerError(page);
    }
  }
  if (process.env.E2E_AUDIT_BASELINE !== '1') {
    const sql = db();
    const saved = sql.prepare('SELECT stats_json,updated_at,status FROM migrations WHERE id=?').get(id) as any;
    const stats = JSON.parse(saved.stats_json);
    const unsafe = '/protected/cached-href-private-token/a?sig=cached-query-private-token';
    stats.validate = { ...report, scope: undefined, readiness: undefined, generated_at: '2001-02-03T04:05:06Z', markdown: 'cached-markdown-private-token', broken_links: [{ content_id: rows[0].id, url: unsafe }], errors: [{ content_id: rows[0].id, kind: 'link', code: 'broken_internal_link', old_ref: unsafe, new_ref: unsafe, checked_at: '2001-02-03T04:05:06Z', status: 404 }] };
    const encoded = JSON.stringify(stats);
    sql.prepare('UPDATE migrations SET stats_json=? WHERE id=?').run(encoded, id);
    const cached = (await (await request.get(`/api/v1/migrations/${id}/report`)).json()).data;
    expect(cached.readiness).toBe('unverified');
    expect(cached.generated_at).toBe('2001-02-03T04:05:06Z');
    expect(cached.unverified_checks.some((i: any) => i.code === 'legacy_report_checks_not_recorded')).toBe(true);
    for (const secret of ['cached-href-private-token', 'cached-query-private-token', 'cached-markdown-private-token']) {
      expect(JSON.stringify(cached)).not.toContain(secret);
    }
    await page.goto(`${path}/validate`);
    await expect(page.locator('[data-readiness="unverified"]')).toBeVisible();
    for (const locale of ['ar', 'en'] as const) {
      await setLocale(page, locale);
      for (const width of [1440, 390]) {
        await page.setViewportSize({ width, height: 1000 });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
        await expect(page.locator('body')).not.toContainText('cached-href-private-token');
        await expect(page.locator('body')).not.toContainText('cached-markdown-private-token');
        await expect(page.locator('body')).not.toContainText('wpmigrate.validate.scope.stored_legacy_report');
        await expect(page.locator('body')).not.toContainText('wpmigrate.check.reason.legacy_report_checks_not_recorded');
        await page.screenshot({ path: info.outputPath(`legacy-validation-${locale}-${width}.png`), fullPage: true });
      }
    }
    const after = sql.prepare('SELECT stats_json,updated_at,status FROM migrations WHERE id=?').get(id) as any;
    expect(after).toEqual({ ...saved, stats_json: encoded });
  }

});
