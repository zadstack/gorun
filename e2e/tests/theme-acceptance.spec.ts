// Brief §6 acceptance: each catalog theme home + 3 inner pages at 1440 and 390.
// Criteria: contrast fails 0 · tap < 44px 0 (inline prose links excluded) ·
// no horizontal scroll · text < 12px 0 · Arabic negative tracking 0 · mono on
// Arabic 0 · console errors 0 · CSP violations 0 · broken nav/footer links 0 ·
// heading order valid · Tailwind CDN/Play runtime absent.
import { test, expect, type Page } from '@playwright/test';
import { MARKER_500 } from '../lib/helpers.ts';

const THEMES = ['default', 'business', 'magazine', 'portfolio', 'docs'];
const WIDTHS = [1440, 390];

async function activate(page: Page, slug: string): Promise<void> {
  await page.goto('/admin/settings/general');
  await page.selectOption('#s-theme', slug);
  await page.locator('form[action="/admin/settings/general"] button[type="submit"]').click();
  await expect(page.locator('#s-theme')).toHaveValue(slug);
}

async function firstLink(page: Page, path: string, sel: string): Promise<string | null> {
  const r = await page.request.get(path);
  if (r.status() !== 200) return null;
  const html = await r.text();
  const m = html.match(new RegExp(`href="((?:https?://[^/"]+)?(${sel})[^"#?]*)"`));
  return m ? new URL(m[1], 'http://x').pathname : null;
}

async function samplePages(page: Page): Promise<{ kind: string; path: string; status: number }[]> {
  const pages = [
    { kind: 'home', path: '/', status: 200 },
    { kind: 'archive', path: '/blog', status: 200 },
    { kind: 'search', path: '/search?q=a', status: 200 },
  ];
  const post = await firstLink(page, '/blog', '/post/');
  if (post) {
    pages.push({ kind: 'post', path: post, status: 200 });
  } else {
    pages.push({ kind: '404', path: '/no-such-page-acceptance', status: 404 });
  }
  return pages.slice(0, 4);
}

type Audit = {
  contrastFails: number;
  tapFails: number;
  horizontalOverflow: boolean;
  textUnder12: number;
  arabicNegativeTracking: number;
  monoOnArabic: number;
  headingOrderOk: boolean;
  h1Count: number;
  skipPresent: boolean;
  tailwindRuntime: boolean;
  brokenChromeLinks: string[];
};

async function auditPage(page: Page): Promise<Audit> {
  return page.evaluate(() => {
    const ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true })!;
    const rgba = (css: string): number[] => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = '#000';
      ctx.fillStyle = css;
      ctx.fillRect(0, 0, 1, 1);
      return Array.from(ctx.getImageData(0, 0, 1, 1).data);
    };
    const lum = ([r, g, b]: number[]) => {
      const f = (c: number) => {
        c /= 255;
        return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const ratio = (fg: string, bg: string) => {
      const [a, b] = [lum(rgba(fg).slice(0, 3)), lum(rgba(bg).slice(0, 3))].sort((x, y) => y - x);
      return (a + 0.05) / (b + 0.05);
    };
    const opaqueBg = (el: Element): string => {
      for (let n: Element | null = el; n; n = n.parentElement) {
        const c = getComputedStyle(n).backgroundColor;
        if (c === 'transparent' || /rgba\([^)]*,\s*0\)$/.test(c)) continue;
        const v = rgba(c);
        if (v[3] > 230) return c;
      }
      return 'rgb(255,255,255)';
    };

    let contrastFails = 0;
    for (const el of Array.from(document.querySelectorAll('body, p, li, h1, h2, h3, a, button, label, td, th'))) {
      const cs = getComputedStyle(el);
      if (cs.visibility === 'hidden' || cs.display === 'none') continue;
      const size = parseFloat(cs.fontSize);
      if (!Number.isFinite(size) || size < 1) continue;
      const need = size >= 24 || (size >= 18.66 && (parseInt(cs.fontWeight, 10) >= 700)) ? 3 : 4.5;
      if (ratio(cs.color, opaqueBg(el)) < need - 0.05) contrastFails++;
    }

    const proseInline = (el: Element) =>
      !!el.closest('.prose-gp, .reading-article, article.prose, .ed-reading') &&
      el.tagName === 'A' &&
      !el.classList.contains('gr-btn') &&
      !el.classList.contains('gp-button') &&
      !el.classList.contains('gp-s-btn');

    let tapFails = 0;
    for (const el of Array.from(
      document.querySelectorAll(
        'header a, header button, header summary, footer a, footer button, nav a, nav button, main button, main summary, main a.gr-btn, main .gp-button, main input:not([type="hidden"]), main select',
      ),
    )) {
      if (proseInline(el)) continue;
      const r = (el as HTMLElement).getBoundingClientRect();
      if (r.width < 1 || r.height < 1) continue;
      if (r.height < 44 - 0.5 || (el.tagName !== 'A' && r.width < 44 - 0.5 && r.height < 44 - 0.5)) {
        // Links may be wide+short text rows in nav; require min dimension 44 on chrome controls.
        if (el.tagName === 'A' && r.height >= 44 - 0.5) continue;
        if (r.width >= 44 - 0.5 && r.height >= 44 - 0.5) continue;
        if (r.height < 44 - 0.5) tapFails++;
      }
    }

    const horizontalOverflow =
      document.documentElement.scrollWidth > document.documentElement.clientWidth + 1;

    let textUnder12 = 0;
    for (const el of Array.from(document.querySelectorAll('body *'))) {
      if (!(el instanceof HTMLElement)) continue;
      if (!el.childNodes.length) continue;
      const hasText = Array.from(el.childNodes).some((n) => n.nodeType === 3 && (n.textContent || '').trim());
      if (!hasText) continue;
      const cs = getComputedStyle(el);
      if (cs.display === 'none' || cs.visibility === 'hidden') continue;
      const size = parseFloat(cs.fontSize);
      if (size > 0 && size < 12 - 0.05) textUnder12++;
    }

    let arabicNegativeTracking = 0;
    let monoOnArabic = 0;
    const rtl = document.documentElement.getAttribute('dir') === 'rtl';
    if (rtl) {
      for (const el of Array.from(document.querySelectorAll('h1, h2, h3, h4, p, li, time, .prose-gp, main'))) {
        const cs = getComputedStyle(el);
        const ls = parseFloat(cs.letterSpacing);
        if (Number.isFinite(ls) && ls < -0.01) arabicNegativeTracking++;
        if (/mono|menlo|consolas|courier/i.test(cs.fontFamily) && !el.closest('pre, code, kbd, samp')) {
          monoOnArabic++;
        }
      }
    }

    const headings = Array.from(document.querySelectorAll('h1, h2, h3, h4, h5, h6')).map((h) =>
      Number(h.tagName.slice(1)),
    );
    const h1Count = headings.filter((l) => l === 1).length;
    let headingOrderOk = h1Count === 1;
    let prev = 0;
    for (const level of headings) {
      if (prev && level > prev + 1) headingOrderOk = false;
      prev = level;
    }

    const skipPresent = !!document.querySelector('a.gr-skip, a.sb-skip, a.ed-skip, a.studio-skip, a[href="#main"]');
    const html = document.documentElement.outerHTML;
    const tailwindRuntime =
      html.includes('cdn.tailwindcss.com') || html.includes('tailwindcss-3.4.17.js') || html.includes('tailwind.config');

    const brokenChromeLinks: string[] = [];
    for (const a of Array.from(document.querySelectorAll('header a[href], footer a[href], nav a[href]'))) {
      const href = (a as HTMLAnchorElement).getAttribute('href') || '';
      if (!href || href.startsWith('#') || href.startsWith('mailto:') || href.startsWith('tel:') || href.startsWith('javascript:')) {
        continue;
      }
      try {
        const u = new URL(href, location.origin);
        if (u.origin !== location.origin) continue;
        // Checked via request outside evaluate; collect candidates.
        brokenChromeLinks.push(u.pathname + u.search);
      } catch {
        brokenChromeLinks.push(href);
      }
    }

    return {
      contrastFails,
      tapFails,
      horizontalOverflow,
      textUnder12,
      arabicNegativeTracking,
      monoOnArabic,
      headingOrderOk,
      h1Count,
      skipPresent,
      tailwindRuntime,
      brokenChromeLinks: [...new Set(brokenChromeLinks)].slice(0, 40),
    };
  });
}

test.describe.serial('theme acceptance (§6)', () => {
  for (const slug of THEMES) {
    for (const width of WIDTHS) {
      test(`${slug} @${width}`, async ({ page }) => {
        const consoleErrors: string[] = [];
        const cspViolations: string[] = [];
        page.on('pageerror', (e) => consoleErrors.push(e.message));
        page.on('console', (msg) => {
          if (msg.type() === 'error') consoleErrors.push(msg.text());
        });
        page.on('response', async () => {
          /* CSP reports arrive as console/securitypolicyviolation in Chromium via page events below */
        });
        await page.addInitScript(() => {
          document.addEventListener('securitypolicyviolation', (e) => {
            (window as unknown as { __csp?: string[] }).__csp = (window as unknown as { __csp?: string[] }).__csp || [];
            (window as unknown as { __csp?: string[] }).__csp!.push(`${e.violatedDirective}:${e.blockedURI}`);
          });
        });

        await page.setViewportSize({ width, height: width < 768 ? 844 : 900 });
        await activate(page, slug);

        for (const { kind, path, status } of await samplePages(page)) {
          const resp = await page.goto(path);
          expect(resp?.status(), `${slug} ${kind} status`).toBe(status);
          await page.waitForLoadState('domcontentloaded');
          expect(await page.content(), `${slug} ${kind} 500`).not.toContain(MARKER_500);

          const result = await auditPage(page);
          const csp = await page.evaluate(() => (window as unknown as { __csp?: string[] }).__csp || []);
          cspViolations.push(...csp);

          expect(result.skipPresent, `${slug} ${kind}: skip link`).toBe(true);
          expect(result.h1Count, `${slug} ${kind}: exactly one h1`).toBe(1);
          expect(result.headingOrderOk, `${slug} ${kind}: heading order`).toBe(true);
          expect(result.horizontalOverflow, `${slug} ${kind}: horizontal scroll`).toBe(false);
          expect(result.textUnder12, `${slug} ${kind}: text < 12px`).toBe(0);
          expect(result.arabicNegativeTracking, `${slug} ${kind}: Arabic negative tracking`).toBe(0);
          expect(result.monoOnArabic, `${slug} ${kind}: mono on Arabic`).toBe(0);
          expect(result.tailwindRuntime, `${slug} ${kind}: Tailwind runtime`).toBe(false);
          expect(result.tapFails, `${slug} ${kind}: tap < 44px`).toBe(0);
          // Body/text contrast: allow a small number of decorative/icon-only misses; fail hard if many.
          expect(result.contrastFails, `${slug} ${kind}: contrast fails`).toBe(0);

          for (const href of result.brokenChromeLinks) {
            const r = await page.request.get(href);
            expect(r.status(), `${slug} ${kind}: chrome link ${href}`).toBeLessThan(400);
          }
        }

        expect(consoleErrors, `${slug} @${width}: console errors`).toEqual([]);
        expect(cspViolations, `${slug} @${width}: CSP violations`).toEqual([]);
      });
    }
  }

  test('restore the default theme', async ({ page }) => {
    await activate(page, 'default');
  });
});
