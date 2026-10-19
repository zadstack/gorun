import {test, expect} from '@playwright/test';
import {db, setLocale} from '../lib/helpers.ts';

test.beforeAll(() => {
  const database=db(), now=new Date().toISOString();
  try {
    const insert=database.prepare(`INSERT OR IGNORE INTO content(id,type,slug,title,body,status,locale,published_at,created_at,updated_at) VALUES(?,'page',?,?,?,'published','en',?,?,?)`);
    for(let i=0;i<22;i++) insert.run(`overview-latency-${i}`,`overview-latency-${i}`,`Overview page ${i}`,`<p>Local preview fixture ${i}</p>`,now,now,now);
  } finally {database.close();}
});

test.afterAll(() => {
  const database=db();
  try{const remove=database.prepare('DELETE FROM content WHERE id=?');for(let i=0;i<22;i++)remove.run(`overview-latency-${i}`);}
  finally{database.close();}
});

for(const locale of ['en','ar'] as const) for(const width of [1440,390,320]) {
  test(`overview defers offscreen previews and loads on scroll/expand ${locale}/${width}`, async({page}, info) => {
    await page.setViewportSize({width,height:1000});
    await page.goto('/admin'); await setLocale(page,locale);
    const documents:string[]=[], errors:string[]=[];
    page.on('request',r=>{if(r.resourceType()==='document')documents.push(new URL(r.url()).pathname);});
    page.on('pageerror',e=>errors.push(e.message));
    await page.goto('/admin/appearance/overview');
    const frames=page.locator('.ov-page iframe');
    expect(await frames.count()).toBeGreaterThan(22);
    const initial={documents:documents.length,frames:await frames.count(),withSource:await frames.evaluateAll(fs=>fs.filter(f=>f.hasAttribute('src')).length),timing:await page.evaluate(()=>{const n=performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming;return {dcl:n.domContentLoadedEventEnd,load:n.loadEventEnd,resources:performance.getEntriesByType('resource').length};})};
    await info.attach('initial-preview-count', {body:JSON.stringify(initial),contentType:'application/json'});
    console.log(JSON.stringify({overview:locale,width,initial}));
    await expect(frames.last()).not.toHaveAttribute('src',/.+/);
    // Contextual page tabs can place the first preview below a small screen.
    // Enter its actual viewport before requiring lazy loading to start.
    await page.locator('.ov-page').first().scrollIntoViewIfNeeded();
    await expect.poll(()=>frames.evaluateAll(fs=>fs.filter(f=>f.hasAttribute('src')).length)).toBeGreaterThan(0);
    expect(await frames.evaluateAll(fs=>fs.filter(f=>f.hasAttribute('src')).length)).toBeLessThan(await frames.count());
    await expect(frames.first()).toHaveAttribute('sandbox','');
    await expect(frames.first()).toHaveAttribute('referrerpolicy','no-referrer');
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    const lastCard=page.locator('.ov-page').last();
    await lastCard.locator('button[aria-expanded]').dispatchEvent('click');
    await expect(frames.last()).toHaveAttribute('src',/.+/);
    const middle=page.locator('.ov-page').nth(10);
    await middle.scrollIntoViewIfNeeded();
    await expect(middle.locator('iframe')).toHaveAttribute('src',/.+/);
    await page.locator('[data-overview-load-all]').click();
    await expect.poll(()=>frames.evaluateAll(fs=>fs.every(f=>f.hasAttribute('src')))).toBe(true);
    await page.screenshot({path:info.outputPath(`overview-${locale}-${width}.png`)});
    expect(errors).toEqual([]);
  });
}

test('overview fallback without IntersectionObserver loads previews and print activates all',async({page})=>{
  await page.addInitScript(()=>{Object.defineProperty(window,'IntersectionObserver',{value:undefined});});
  await page.goto('/admin/appearance/overview');
  const frames=page.locator('.ov-page iframe');
  await expect.poll(()=>frames.evaluateAll(fs=>fs.every(f=>f.hasAttribute('src')))).toBe(true);
  await page.evaluate(()=>{window.print=()=>{document.documentElement.dataset.testPrinted='yes';};});
  await page.locator('[data-overview-print]').click();
  await expect(page.locator('html')).toHaveAttribute('data-test-printed','yes');
});

test('overview filters/preview sizes keep deferred URLs and print activates remaining previews',async({page})=>{
  await page.goto('/admin'); await setLocale(page,'en');
  await page.goto('/admin/appearance/overview');
  const frames=page.locator('.ov-page iframe');
  await expect(frames.last()).not.toHaveAttribute('src',/.+/);
  const filter=page.locator('.site-overview > nav button').last();
  await filter.click();
  await expect(filter).toHaveAttribute('aria-pressed','true');
  await expect(frames.last()).toHaveAttribute('src',/.+/);
  await page.locator('.site-overview > nav button').first().click();
  await page.getByRole('button',{name:'Mobile',exact:true}).click();
  await expect(frames.first()).toHaveCSS('width','390px');
  await page.getByRole('button',{name:'Tablet',exact:true}).click();
  await expect(frames.first()).toHaveCSS('width','820px');
  await page.getByRole('button',{name:'Desktop',exact:true}).click();
  await expect(frames.first()).toHaveCSS('width','1440px');
  expect(await frames.evaluateAll(fs=>fs.some(f=>!f.hasAttribute('src')))).toBe(true);
  await page.evaluate(()=>{window.print=()=>{document.documentElement.dataset.testPrinted='yes';};});
  await page.locator('[data-overview-print]').click();
  await expect.poll(()=>frames.evaluateAll(fs=>fs.every(f=>f.hasAttribute('src')))).toBe(true);
  await expect(page.locator('html')).toHaveAttribute('data-test-printed','yes');
});

test('overview print waits for actual slow preview documents before opening print',async({page})=>{
  const pending=new Set<unknown>();
  page.on('request',r=>{if(r.resourceType()==='document'&&/\/overview-latency-/.test(new URL(r.url()).pathname))pending.add(r);});
  page.on('requestfinished',r=>pending.delete(r));
  page.on('requestfailed',r=>pending.delete(r));
  await page.route('**/overview-latency-*',async route=>{
    const response=await route.fetch();
    await new Promise(resolve=>setTimeout(resolve,1250));
    await route.fulfill({response});
  });
  await page.goto('/admin/appearance/overview');
  await page.evaluate(()=>{window.print=()=>{document.documentElement.dataset.testPrinted='yes';};});
  const started=Date.now();
  await page.locator('[data-overview-print]').click();
  await expect(page.locator('html')).toHaveAttribute('data-test-printed','yes');
  expect(pending.size,'print opened while preview document responses were pending').toBe(0);
  expect(Date.now()-started).toBeGreaterThan(1250);
  await expect(page.locator('.ov-page[data-preview-ready]')).toHaveCount(await page.locator('.ov-page').count());
  await expect(page.locator('[data-overview-print-error]')).toBeHidden();
});

test('native print has linked fallbacks for incomplete sandboxed previews',async({page},info)=>{
  await page.goto('/admin');await setLocale(page,'en');
  await page.route('**/overview-latency-*',async route=>{
    const response=await route.fetch();await new Promise(resolve=>setTimeout(resolve,4000));
    try {await route.fulfill({response});}
    catch(error){if(!(error instanceof Error)||!error.message.includes('Route is already handled'))throw error;}
  });
  await page.goto('/admin/appearance/overview');
  await page.pdf({path:info.outputPath('overview-native-print.pdf'),printBackground:true});
  await page.emulateMedia({media:'print'});
  await page.evaluate(()=>window.dispatchEvent(new Event('beforeprint')));
  const pending=page.locator('.ov-page:not([data-preview-ready])');
  if(await pending.count()){
    await expect(page.locator('.ov-print-help')).toBeVisible();
    await expect(pending.first().locator('.ov-print-fallback')).toBeVisible();
    await expect(pending.first().locator('iframe')).toBeHidden();
    await expect(pending.first().locator('.ov-print-fallback a')).toHaveAttribute('href',/overview-latency-/);
    await pending.first().screenshot({path:info.outputPath('native-print-fallback.png')});
  }
  await page.unrouteAll({behavior:'ignoreErrors'});
});

test('overview print timeout stays usable and does not print incomplete previews',async({page})=>{
  let release!:()=>void;
  const blocked=new Promise<void>(resolve=>{release=resolve;});
  await page.route('**/overview-latency-*',async route=>{const response=await route.fetch();await blocked;await route.fulfill({response});});
  try{
    await page.goto('/admin/appearance/overview',{waitUntil:'domcontentloaded'});
    await page.evaluate(()=>{window.print=()=>{document.documentElement.dataset.testPrinted='yes';};});
    const button=page.locator('[data-overview-print]');
    await button.click();await expect(button).toBeDisabled();
    await expect(page.locator('[data-overview-print-error]')).toBeVisible({timeout:20000});
    await expect(button).toBeEnabled();await expect(button).not.toHaveAttribute('aria-busy','true');
    expect(await page.locator('html').getAttribute('data-test-printed')).toBeNull();
  }finally{release();await page.unrouteAll({behavior:'wait'});}
});


test('overview search and single-preview focus preserve real edit links and source states',async({page})=>{
  await page.goto('/admin');await setLocale(page,'en');await page.goto('/admin/appearance/overview');
  const all=page.locator('.ov-page'),count=await all.count(),search=page.locator('[data-overview-search]');
  await search.fill('Overview page 12');const visible=page.locator('.ov-page:visible');await expect(visible).toHaveCount(1);await expect(visible).toContainText(/published/i);await expect(visible.locator('a[href="/admin/content/page/overview-latency-12"]')).toBeVisible();
  await visible.locator('[data-overview-focus]').click();await expect(visible.locator('button[aria-expanded]')).toHaveAttribute('aria-expanded','true');await expect(visible.locator('iframe')).toHaveAttribute('src',/.+/);await expect(page.locator('[data-overview-clear-focus]')).toBeVisible();await page.locator('.site-overview > nav button').first().click();await expect(page.locator('[data-overview-clear-focus]')).toBeHidden();await expect(visible).toHaveCount(1);
  await search.fill('');await expect(page.locator('.ov-page:visible')).toHaveCount(count);await search.fill('no such preview fixture');await expect(page.locator('.ov-page:visible')).toHaveCount(0);await expect(page.locator('[data-overview-search-status]')).not.toHaveText('');await search.fill('');await expect(page.locator('.ov-page:visible')).toHaveCount(count);
});
