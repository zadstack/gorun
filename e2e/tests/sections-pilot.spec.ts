// Section-component pilot: the demo pages (internal/theme/testdata/
// sections-pilot.{en,ar}.html, imported here as /sections-pilot-ar and
// /en/sections-pilot-en) rendered by the default and business themes at
// 1280 and 375. Checks: no 500 marker, no horizontal scroll, no page errors,
// section buttons ≥ 4.5:1, the comparison table highlights its second column,
// FAQ items open and close, the section background layer is a masked color
// layer, reduced motion removes the card lift, and the identity pattern
// settings (Appearance → Theme settings → Design) reach /site/identity.css and
// the page. Full-page screenshots and page-weight numbers go to the test output.
// Runs in the "catalog" project (serial, restores the default theme).
import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { MARKER_500, db, env, submitAndLand } from '../lib/helpers.ts';

const THEMES = ['default', 'business'];
const WIDTHS = [1280, 375];
const PAGES = [
  { locale: 'ar', path: '/sections-pilot-ar', dir: 'rtl' },
  { locale: 'en', path: '/en/sections-pilot-en', dir: 'ltr' },
];

async function activate(page: Page, slug: string): Promise<void> {
  await page.goto('/admin/settings/general');
  await page.selectOption('#s-theme', slug);
  await page.locator('form[action="/admin/settings/general"] button[type="submit"]').click();
  await expect(page.locator('#s-theme')).toHaveValue(slug);
}

// importPilot wraps both fixtures in the starter WXR envelope and imports them
// the way the catalog spec imports the starter About page.
function importPilot(file: string): void {
  const wxr = readFileSync(resolve('../internal/starter/sample-content.xml'), 'utf8');
  const head = wxr.slice(0, wxr.indexOf('<item>'));
  const items = PAGES.map((p, i) => {
    const body = readFileSync(resolve(`../internal/theme/testdata/sections-pilot.${p.locale}.html`), 'utf8');
    const title = p.locale === 'ar' ? 'تجربة مكتبة الأقسام' : 'Section library pilot';
    const slug = p.path.split('/').pop();
    return `<item><title>${title}</title><dc:creator>admin</dc:creator><content:encoded><![CDATA[${body}]]></content:encoded>` +
      `<wp:post_id>${9021 + i}</wp:post_id><wp:post_date>2026-10-22 10:00:00</wp:post_date><wp:post_name>${slug}</wp:post_name>` +
      `<wp:status>publish</wp:status><wp:post_parent>0</wp:post_parent><wp:menu_order>${21 + i}</wp:menu_order><wp:post_type>page</wp:post_type></item>`;
  });
  writeFileSync(file, `${head}${items.join('')}</channel></rss>`);
  execFileSync(resolve('.bin/gorun'), ['import-wxr', file, '--slug-policy', 'skip', '--source-name', 'sections-pilot', '--data', env('E2E_DATA')], {
    stdio: 'pipe',
    env: { ...process.env, GORUN_THEMES: resolve('../themes'), ...(process.env.E2E_THEMES_EXTRA ? { GORUN_THEMES_EXTRA: process.env.E2E_THEMES_EXTRA } : {}) },
  });
  // The importer files every item under the site locale (ar); the English
  // page is served at /en/… only with locale en.
  const database = db();
  database.prepare("UPDATE content SET locale = 'en' WHERE type = 'page' AND slug = 'sections-pilot-en'").run();
  database.close();
}

type Probe = {
  buttons: number[];
  highlighted: string; plain: string;
  heroMask: string; heroLayerBg: string;
  stepsMarker: string;
  cardTransition: string;
  cssBytes: number; docBytes: number; requests: number;
  domContentLoaded: number;
};

async function probe(page: Page): Promise<Probe> {
  return page.evaluate(() => {
    const ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true })!;
    const rgb = (css: string): number[] => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = '#000';
      ctx.fillStyle = css;
      ctx.fillRect(0, 0, 1, 1);
      return Array.from(ctx.getImageData(0, 0, 1, 1).data).slice(0, 3);
    };
    const lum = ([r, g, b]: number[]) => {
      const f = (c: number) => { c /= 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const ratio = (a: string, b: string) => {
      const [x, y] = [lum(rgb(a)), lum(rgb(b))].sort((m, n) => n - m);
      return (x + 0.05) / (y + 0.05);
    };
    const buttons = Array.from(document.querySelectorAll('.gp-s-btn:not(.gp-s-btn-ghost)')).map((el) => {
      const cs = getComputedStyle(el);
      return ratio(cs.color, cs.backgroundColor);
    });
    const th = (n: number) => getComputedStyle(document.querySelector(`.gp-s-compare thead th:nth-child(${n})`)!).backgroundColor;
    const hero = document.querySelector('.gp-s-hero.gp-s-bg')!;
    const layer = getComputedStyle(hero, '::before');
    const res = performance.getEntriesByType('resource') as PerformanceResourceTiming[];
    const nav = performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming;
    return {
      buttons,
      highlighted: th(2), plain: th(3),
      heroMask: layer.maskImage || (layer as any).webkitMaskImage || '',
      // gp-s-bg-fade paints the layer with a gradient instead of a flat color.
      heroLayerBg: layer.backgroundColor !== 'rgba(0, 0, 0, 0)' ? layer.backgroundColor : layer.backgroundImage,
      stepsMarker: getComputedStyle(document.querySelector('.gp-s-steps')!).listStyleType,
      cardTransition: getComputedStyle(document.querySelector('.gp-s-card')!).transitionDuration,
      cssBytes: res.filter((r) => r.name.endsWith('.css') || r.name.includes('.css?')).reduce((n, r) => n + (r.encodedBodySize || 0), 0),
      docBytes: nav ? nav.encodedBodySize : 0,
      requests: res.length,
      domContentLoaded: nav ? Math.round(nav.domContentLoadedEventEnd - nav.startTime) : 0,
    };
  });
}

async function checkPage(page: Page, label: string, path: string, dir: string): Promise<Probe> {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  const resp = await page.goto(path);
  expect(resp?.status(), `${label} ${path} status`).toBe(200);
  await page.waitForLoadState('networkidle');
  expect(await page.content(), `${label}: 500 marker`).not.toContain(MARKER_500);
  await expect(page.locator('html')).toHaveAttribute('dir', dir);
  const { scroll, client } = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, client: document.documentElement.clientWidth }));
  expect(scroll, `${label}: horizontal scroll`).toBeLessThanOrEqual(client + 1);
  const p = await probe(page);
  expect(p.buttons.length, `${label}: section buttons`).toBeGreaterThan(0);
  for (const r of p.buttons) expect(r, `${label}: button text vs fill`).toBeGreaterThanOrEqual(4.5);
  expect(p.highlighted, `${label}: comparison column 2 highlighted`).not.toBe(p.plain);
  expect(p.heroMask, `${label}: hero background is a masked layer`).toContain('gradient');
  expect(p.heroLayerBg, `${label}: hero layer has a color`).not.toBe('none');
  expect(p.stepsMarker, `${label}: steps marker`).toBe('none');
  // FAQ opens and closes without script.
  const first = page.locator('.gp-s-faq details').first();
  await first.locator('summary').click();
  await expect(first).toHaveAttribute('open', '');
  await first.locator('summary').click();
  await expect(first).not.toHaveAttribute('open', '');
  expect(errors, `${label}: page errors`).toEqual([]);
  await page.screenshot({ path: test.info().outputPath(`${label}.png`), fullPage: true });
  return p;
}

test.describe.serial('section-component pilot', () => {
  test('import the pilot pages', async ({ page }) => {
    importPilot(test.info().outputPath('sections-pilot.xml'));
    for (const p of PAGES) {
      const r = await page.request.get(p.path);
      expect(r.status(), `${p.path} after import`).toBe(200);
      expect(await r.text(), `${p.path} carries the pilot sections`).toContain('gp-s-compare');
    }
  });

  const weights: Record<string, Probe> = {};
  for (const slug of THEMES) {
    for (const width of WIDTHS) {
      test(`${slug} @${width}`, async ({ page }) => {
        await page.setViewportSize({ width, height: width < 768 ? 812 : 900 });
        await activate(page, slug);
        for (const p of PAGES) {
          weights[`${slug}-${width}-${p.locale}`] = await checkPage(page, `${slug}-${width}-${p.locale}`, p.path, p.dir);
        }
      });
    }
  }

  test('reduced motion removes the card lift', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto(PAGES[1].path);
    const card = page.locator('.gp-s-card').first();
    await card.hover();
    const cs = await card.evaluate((el) => ({ t: getComputedStyle(el).transitionDuration, tr: getComputedStyle(el).transform }));
    // The default theme's a11y.css also clamps every transition to 0.00001s.
    expect(parseFloat(cs.t), 'card transition under reduced motion').toBeLessThanOrEqual(0.001);
    expect(cs.tr, 'card transform under reduced motion').toBe('none');
    await page.emulateMedia({ reducedMotion: 'no-preference' });
  });

  test('identity pattern settings reach the pages', async ({ page }) => {
    await activate(page, 'business');
    await page.goto('/admin/appearance/theme-settings');
    await page.locator('[data-theme-tab=design]').click();
    await page.selectOption('select[name="settings[identity_pattern]"]', 'waves');
    await page.selectOption('select[name="settings[identity_pattern_density]"]', 'dense');
    await page.fill('input[name="settings[identity_pattern_color]"]', '#c2410c');
    await submitAndLand(page, page.locator('form button[type="submit"]').last(), '/admin/appearance/theme-settings');
    await expect(page.locator('select[name="settings[identity_pattern]"]')).toHaveValue('waves');

    const css = await (await page.request.get('/site/identity.css')).text();
    expect(css).toContain('--gp-pattern-ink:#c2410c;');
    expect(css).toContain('--gp-pattern-scale:1.4;');
    expect(css).toContain('.gp-s-bg{--gp-pattern-image:var(--gp-pat-waves);--gp-pattern-size:var(--gp-pat-waves-size)}');
    expect(css, 'identity.css carries tokens, never drawings').not.toContain('gradient');

    // A section without its own modifier (the FAQ band uses only gp-s-bg in
    // this probe) takes the site default; a section with a modifier keeps it.
    await page.goto(PAGES[1].path);
    const layer = await page.evaluate(() => {
      const probe = document.createElement('section');
      probe.className = 'gp-s-bg';
      document.querySelector('.prose-gp')!.appendChild(probe);
      const site = getComputedStyle(probe, '::before');
      const hero = getComputedStyle(document.querySelector('.gp-s-hero.gp-s-bg')!, '::before');
      const out = { site: site.maskImage || (site as any).webkitMaskImage, siteSize: site.maskSize || (site as any).webkitMaskSize, hero: hero.maskImage || (hero as any).webkitMaskImage };
      probe.remove();
      return out;
    });
    expect(layer.site, 'site default pattern is the waves SVG').toContain('data:image/svg+xml');
    expect(layer.siteSize, 'dense density scales the tile (120px × 1.4)').toContain('168px');
    expect(layer.hero, 'the hero keeps its own dots modifier').toContain('radial-gradient');

    // A bad colour is rejected and nothing is stored.
    await page.goto('/admin/appearance/theme-settings');
    await page.locator('[data-theme-tab=design]').click();
    await page.fill('input[name="settings[identity_pattern_color]"]', 'url(x)');
    await submitAndLand(page, page.locator('form button[type="submit"]').last(), '/admin/appearance/theme-settings', null);
    await expect(page.locator('input[name="settings[identity_pattern_color]"]')).toHaveValue('url(x)');
    expect(await (await page.request.get('/site/identity.css')).text()).toContain('--gp-pattern-ink:#c2410c;');

    // Reset.
    await page.goto('/admin/appearance/theme-settings');
    await page.locator('[data-theme-tab=design]').click();
    await page.selectOption('select[name="settings[identity_pattern]"]', '');
    await page.selectOption('select[name="settings[identity_pattern_density]"]', '');
    await page.fill('input[name="settings[identity_pattern_color]"]', '');
    await submitAndLand(page, page.locator('form button[type="submit"]').last(), '/admin/appearance/theme-settings');
    await page.goto('/');
    await expect(page.locator('link[data-gorun-identity]')).toHaveCount(0);
  });

  test('page weight report and restore the default theme', async ({ page }) => {
    await test.info().attach('sections-pilot-weights.json', { body: JSON.stringify(weights, null, 2), contentType: 'application/json' });
    writeFileSync(test.info().outputPath('sections-pilot-weights.json'), JSON.stringify(weights, null, 2));
    await activate(page, 'default');
  });
});
