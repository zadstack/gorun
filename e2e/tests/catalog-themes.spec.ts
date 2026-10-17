// Design QA for the five catalog themes: each is activated in turn and every
// public page kind (home, post, archive, search, page, shop, product, cart,
// account, 404) is checked at 375px and 1280px for the theme 500 marker,
// horizontal scroll, page errors and body text contrast (WCAG AA, 4.5:1).
// The site identity settings (Appearance → Theme settings) are exercised once:
// a dark scheme and an accent must reach the public pages and keep contrast.
// Full-page screenshots go to the test output (uploaded with the report).
// The starter's About page (built from the section library) is imported as
// /starter-about (other specs may own /about), and its sections are checked
// inside each theme's .prose-gp body.
// Runs after "chromium" (which creates the content these pages need) and
// before "theme" (Business), and restores the default theme at the end.
import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { MARKER_500, env } from '../lib/helpers.ts';

const THEMES = ['default', 'business', 'magazine', 'portfolio', 'docs'];
const WIDTHS = [375, 1280];
// The starter About page, imported under a slug no other spec uses.
const ABOUT = '/starter-about';

async function activate(page: Page, slug: string): Promise<void> {
  await page.goto('/admin/settings/general');
  await page.selectOption('#s-theme', slug);
  await page.locator('form[action="/admin/settings/general"] button[type="submit"]').click();
  await expect(page.locator('#s-theme')).toHaveValue(slug);
}

// firstLink returns the first same-origin link matching sel on path, or null.
async function firstLink(page: Page, path: string, sel: string): Promise<string | null> {
  const r = await page.request.get(path);
  if (r.status() !== 200) return null;
  const html = await r.text();
  const m = html.match(new RegExp(`href="((?:https?://[^/"]+)?(${sel})[^"#?]*)"`));
  return m ? new URL(m[1], 'http://x').pathname : null;
}

// importStarterAbout imports the starter's About page (internal/starter) as
// ABOUT with the WordPress importer, the way `gorun starter` does.
function importStarterAbout(file: string): void {
  const wxr = readFileSync(resolve('../internal/starter/sample-content.xml'), 'utf8');
  const items = wxr.match(/<item>[\s\S]*?<\/item>/g) ?? [];
  const about = items.find((i) => i.includes('gp-s-hero'));
  if (!about) throw new Error('starter About page with gp-s-hero not found');
  const item = about
    .replace(/<wp:post_name>[^<]*<\/wp:post_name>/, `<wp:post_name>${ABOUT.slice(1)}</wp:post_name>`)
    .replace(/<wp:post_id>\d+<\/wp:post_id>/, '<wp:post_id>9013</wp:post_id>');
  const head = wxr.slice(0, wxr.indexOf('<item>'));
  writeFileSync(file, `${head}${item}</channel></rss>`);
  execFileSync(resolve('.bin/gorun'), ['import-wxr', file, '--slug-policy', 'skip', '--source-name', 'starter-about', '--data', env('E2E_DATA')], {
    stdio: 'pipe',
    env: { ...process.env, GORUN_THEMES: resolve('../themes'), ...(process.env.E2E_THEMES_EXTRA ? { GORUN_THEMES_EXTRA: process.env.E2E_THEMES_EXTRA } : {}) },
  });
}

// sectionStyles reads what the section library must win inside .prose-gp:
// button text vs fill contrast and decoration, list markers, and the hero h1
// size against a plain body h2 in the same prose container.
async function sectionStyles(page: Page): Promise<{ buttons: { contrast: number; decoration: string }[]; steps: string[]; checks: string[]; hero: number; bodyH2: number }> {
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
    const prose = document.querySelector('.prose-gp')!;
    const buttons = Array.from(prose.querySelectorAll('.gp-s-btn:not(.gp-s-btn-ghost)')).map((el) => {
      const cs = getComputedStyle(el);
      return { contrast: ratio(cs.color, cs.backgroundColor), decoration: cs.textDecorationLine };
    });
    const marker = (sel: string) => Array.from(prose.querySelectorAll(sel)).map((el) => getComputedStyle(el).listStyleType);
    const probe = document.createElement('h2');
    probe.textContent = 'probe';
    prose.appendChild(probe);
    const bodyH2 = parseFloat(getComputedStyle(probe).fontSize);
    probe.remove();
    const h1 = prose.querySelector('.gp-s-hero h1');
    return { buttons, steps: marker('.gp-s-steps'), checks: marker('.gp-s-checks'), hero: h1 ? parseFloat(getComputedStyle(h1).fontSize) : 0, bodyH2 };
  });
}

// fillContrast is the text vs fill contrast of every element matching sel.
async function fillContrast(page: Page, sel: string): Promise<number[]> {
  return page.locator(sel).evaluateAll((els) => {
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
    return els.map((el) => {
      const cs = getComputedStyle(el);
      const [x, y] = [lum(rgb(cs.color)), lum(rgb(cs.backgroundColor))].sort((m, n) => n - m);
      return (x + 0.05) / (y + 0.05);
    });
  });
}

async function checkSections(page: Page, label: string): Promise<void> {
  const s = await sectionStyles(page);
  expect(s.buttons.length, `${label}: section buttons on ${ABOUT}`).toBeGreaterThan(0);
  for (const b of s.buttons) {
    expect(b.contrast, `${label}: section button text vs fill contrast`).toBeGreaterThanOrEqual(4.5);
    expect(b.decoration, `${label}: section button underline`).toBe('none');
  }
  expect(s.steps.length, `${label}: .gp-s-steps on ${ABOUT}`).toBeGreaterThan(0);
  for (const m of [...s.steps, ...s.checks]) expect(m, `${label}: section list marker`).toBe('none');
  expect(s.hero, `${label}: hero h1 above the body h2`).toBeGreaterThan(s.bodyH2);
}

async function pageKinds(page: Page): Promise<[string, string, number][]> {
  const kinds: [string, string, number][] = [
    ['home', '/', 200],
    ['archive', '/blog', 200],
    ['search', '/search?q=a', 200],
    ['shop', '/shop', 200],
    ['cart', '/cart', 200],
    ['account', '/account', 200],
    ['404', '/no-such-page-e2e', 404],
    ['page', ABOUT, 200],
  ];
  const post = await firstLink(page, '/blog', '/post/');
  if (post) kinds.push(['post', post, 200]);
  const product = await firstLink(page, '/shop', '/product/');
  if (product) kinds.push(['product', product, 200]);
  return kinds;
}

// bodyContrast is the WCAG contrast ratio of the body text color against the
// first opaque background found from <body> up.
async function bodyContrast(page: Page): Promise<number> {
  return page.evaluate(() => {
    // Computed colors may be oklch()/color-mix(); a canvas converts any CSS color to sRGB.
    const ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true })!;
    const rgba = (css: string): number[] => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = '#000';
      ctx.fillStyle = css;
      ctx.fillRect(0, 0, 1, 1);
      return Array.from(ctx.getImageData(0, 0, 1, 1).data);
    };
    const lum = ([r, g, b]: number[]) => {
      const f = (c: number) => { c /= 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    let bg = [255, 255, 255];
    for (let el: Element | null = document.body; el; el = el.parentElement) {
      const c = getComputedStyle(el).backgroundColor;
      if (c === 'transparent' || /rgba\([^)]*,\s*0\)$/.test(c)) continue;
      const v = rgba(c);
      if (v[3] > 230) { bg = v.slice(0, 3); break; }
    }
    const fg = rgba(getComputedStyle(document.body).color).slice(0, 3);
    const [a, b] = [lum(fg), lum(bg)].sort((x, y) => y - x);
    return (a + 0.05) / (b + 0.05);
  });
}

async function check(page: Page, kind: string, path: string, status: number, label: string): Promise<void> {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  const resp = await page.goto(path);
  expect(resp?.status(), `${label} ${kind} ${path} status`).toBe(status);
  await page.waitForLoadState('networkidle');
  expect(await page.content(), `${label} ${kind} 500 marker`).not.toContain(MARKER_500);
  const { scroll, client } = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    client: document.documentElement.clientWidth,
  }));
  expect(scroll, `${label} ${kind}: horizontal scroll`).toBeLessThanOrEqual(client + 1);
  expect(await bodyContrast(page), `${label} ${kind}: body text contrast`).toBeGreaterThanOrEqual(4.5);
  expect(errors, `${label} ${kind}: page errors`).toEqual([]);
  if (kind === 'page') await checkSections(page, `${label} ${kind}`);
  await page.screenshot({ path: test.info().outputPath(`${label}-${kind}.png`), fullPage: true });
}

test.describe.serial('catalog themes', () => {
  test('import the starter About page', async ({ page }) => {
    importStarterAbout(test.info().outputPath('starter-about.xml'));
    const r = await page.request.get(ABOUT);
    expect(r.status(), `${ABOUT} after import`).toBe(200);
    expect(await r.text(), `${ABOUT} carries the section library`).toContain('gp-s-hero');
  });

  for (const slug of THEMES) {
    for (const width of WIDTHS) {
      test(`${slug} @${width}`, async ({ page }) => {
        await page.setViewportSize({ width, height: width < 768 ? 812 : 900 });
        await activate(page, slug);
        for (const [kind, path, status] of await pageKinds(page)) {
          await check(page, kind, path, status, `${slug}-${width}`);
        }
      });
    }
  }

  test('site identity: dark scheme and accent reach the public pages', async ({ page }) => {
    await activate(page, 'business');
    await page.goto('/admin/appearance/theme-settings');
    await page.selectOption('select[name="settings[identity_scheme]"]', 'dark');
    await page.fill('input[name="settings[identity_accent]"]', '#f59e0b');
    await page.selectOption('select[name="settings[identity_heading_font]"]', 'zad');
    await page.locator('form button[type="submit"]').last().click();
    await expect(page.locator('select[name="settings[identity_scheme]"]')).toHaveValue('dark');

    await page.goto('/');
    await expect(page.locator('style[data-gorun-identity]')).toHaveCount(1);
    const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    expect(bg, 'dark body background').toBe('rgb(17, 20, 24)');
    expect(await bodyContrast(page)).toBeGreaterThanOrEqual(4.5);
    // White on amber is about 2.1:1; the identity derives --on-accent, so
    // accent fills (section buttons, the theme's own buttons) stay readable.
    await page.goto(ABOUT);
    await checkSections(page, `identity #f59e0b ${ABOUT}`);
    await page.goto('/search?q=a');
    const fills = await fillContrast(page, 'main button[type="submit"]');
    expect(fills.length, 'business search button').toBeGreaterThan(0);
    for (const r of fills) expect(r, 'identity #f59e0b: theme button text vs fill contrast').toBeGreaterThanOrEqual(4.5);

    // A bad color is rejected with a field error and nothing is stored.
    await page.goto('/admin/appearance/theme-settings');
    await page.fill('input[name="settings[identity_accent]"]', 'red; } body { display:none');
    await page.locator('form button[type="submit"]').last().click();
    await expect(page.locator('input[name="settings[identity_accent]"]')).toHaveValue('red; } body { display:none');
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();

    // Reset so later projects see the theme's own design.
    await page.goto('/admin/appearance/theme-settings');
    await page.selectOption('select[name="settings[identity_scheme]"]', '');
    await page.fill('input[name="settings[identity_accent]"]', '');
    await page.selectOption('select[name="settings[identity_heading_font]"]', '');
    await page.locator('form button[type="submit"]').last().click();
    await page.goto('/');
    await expect(page.locator('style[data-gorun-identity]')).toHaveCount(0);
  });

  test('restore the default theme', async ({ page }) => {
    await activate(page, 'default');
  });
});
