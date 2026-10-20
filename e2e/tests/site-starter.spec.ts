import {test,expect} from '@playwright/test';
import {db,env,setContentLocale,setLocale,expectNoServerError} from '../lib/helpers.ts';

test('three site starters prepare private drafts, activate usable sites and apply editable identity',async({page,browser},testInfo)=>{
 test.setTimeout(180_000);
 const designs={company:{theme:'business',pages:5,background:'rgb(248, 251, 249)',accent:'#1e6650',font:'zad'},portfolio:{theme:'portfolio',pages:5,background:'rgb(251, 249, 253)',accent:'#6b3fa0',font:'zad'},magazine:{theme:'magazine',pages:4,background:'rgb(244, 240, 232)',accent:'#89402e',font:'serif'}};
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
 const keys=['site.title','site.tagline','site.locale','site.front_page','site.theme','site.logo','builder.identity'];
 const database=db();
 const saved=keys.map(key=>({key,value:(database.prepare('SELECT value_json FROM settings WHERE key=?').get(key) as any)?.value_json}));
 const menus=database.prepare("SELECT id,location FROM menus WHERE location='primary'").all() as any[];
 const oldItems=database.prepare('SELECT count(*) AS n FROM content').get() as any;
 const anonymous=await browser.newContext({storageState:{cookies:[],origins:[]}});const publicPage=await anonymous.newPage();
 const external:string[]=[];publicPage.on('pageerror',error=>errors.push(error.message));
 await publicPage.route('**/*',route=>{const url=route.request().url();if(url.startsWith('http')&&new URL(url).origin!==env('E2E_BASE_URL')){external.push(url);return route.abort()}return route.continue()});
 try{
  await page.goto('/admin/site-builder');await setLocale(page,'ar');
  await expect(page.getByRole('heading',{level:1})).toContainText('تصميم الموقع');
  for(const kind of ['company','portfolio','magazine'] as const){
   const design=designs[kind];
   const siteTitle={company:'شركة وخدمات',portfolio:'Project portfolio',magazine:'مجلة تحريرية'}[kind];
   const siteTagline={company:'نبني مواقع واضحة وتجارب رقمية تخدم أهداف مشروعك.',portfolio:'Independent design, thoughtful identities and digital experiences.',magazine:'مجلة مستقلة للأفكار والثقافة والتصميم.'}[kind];
   const publicThemeBefore=(database.prepare("SELECT value_json FROM settings WHERE key='site.theme'").get() as any)?.value_json;
   const publicFrontBefore=(database.prepare("SELECT value_json FROM settings WHERE key='site.front_page'").get() as any)?.value_json;
   await page.goto('/admin/site-builder');
   await page.locator(`input[name="starter"][value="${kind}"]`).check();
   await page.locator("[data-wizard-controls]").last().getByRole("button",{name:/التالي|Next/}).click();
   await expect(page.locator('input[name="accent"]')).toHaveValue(design.accent);
   await expect(page.locator('select[name="font"]')).toHaveValue(design.font);
   await page.locator('input[name="title"]').fill(siteTitle);
   await page.locator('textarea[name="tagline"]').fill(siteTagline);
   await page.locator('select[name="locale"]').selectOption(kind==='portfolio'?'en':'ar');
   await page.locator("[data-wizard-controls]").last().getByRole("button",{name:/التالي|Next/}).click();
   await page.locator('#main form button[type="submit"]').click();
   await expect(page).toHaveURL(/site-builder\?run=/);
   const runURL=page.url();
   const links=page.locator('a[href^="/admin/builder/"]').filter({hasText:/تعديل|Edit/});
   await expect(links).toHaveCount(design.pages);
   const editorURLs=await links.evaluateAll(nodes=>nodes.map(node=>(node as HTMLAnchorElement).getAttribute('href')!));
   const ids=editorURLs.map(url=>url.split('/').pop()!);
   const rows=ids.map(id=>database.prepare('SELECT slug,title,status,body FROM content WHERE id=?').get(id) as any);
   for(const row of rows){expect(row.status).toBe('draft');expect(row.body).toContain('gp-builder');const response=await anonymous.request.get('/'+row.slug);expect(response.status()).toBe(404)}
   // Pending brand is visible in editor preview, while the existing public site stays unchanged.
   await page.goto(editorURLs[0]);
   await expect(page.frameLocator('#builder-preview').locator(kind==='magazine'?'.ed-wordmark':'h1')).toContainText(siteTitle);
   await expect(page.frameLocator('#builder-preview').locator('html')).toHaveAttribute('data-theme',design.theme);
   await expect(page.frameLocator('#builder-preview').locator('header').first()).toBeVisible();
   await expect(page.frameLocator('#builder-preview').locator('footer').last()).toBeVisible();
   const pendingMenuLinks=await page.frameLocator('#builder-preview').locator('header nav a').evaluateAll(nodes=>nodes.map(node=>(node as HTMLAnchorElement).getAttribute('href')!));
   const pendingMenuPaths=pendingMenuLinks.map(href=>new URL(href,env('E2E_BASE_URL')).pathname);
   for(const [index,row] of rows.entries()){const href=index===0?'/':'/'+row.slug;expect(pendingMenuPaths,`pending menu missing ${row.title}`).toContain(href)}
   expect((database.prepare("SELECT value_json FROM settings WHERE key='site.theme'").get() as any)?.value_json).toBe(publicThemeBefore);
   expect((database.prepare("SELECT value_json FROM settings WHERE key='site.front_page'").get() as any)?.value_json).toBe(publicFrontBefore);
   await expect(page.frameLocator('#builder-preview').locator('html')).toHaveAttribute('dir',kind==='portfolio'?'ltr':'rtl');
   await page.goto(editorURLs[editorURLs.length-1]);
   await expect(page.frameLocator('#builder-preview').locator('form[action="/forms/contact"]')).toHaveCount(1);
   await expect(page.locator('#builder-preview')).toHaveAttribute('sandbox','');
   await page.goto(editorURLs[0]);
   await expect(page.frameLocator('#builder-preview').locator('body')).toHaveCSS('background-color',design.background);
   await page.goto(runURL);
   await page.locator('form[action$="/activate"] button[type="submit"]').click();
   await expect(page.locator('#main [role="status"]:visible')).toContainText(/تفعيل|مفعّل|جاهز|activated|active/);
   const activated = ids.map(id => database.prepare('SELECT status FROM content WHERE id=?').get(id) as any);
   expect(activated.every(row => row.status === 'published')).toBe(true);
   const postTitle=`Design notes: ${kind}`;
   const postSlug=`starter-${kind}-design-notes`;
   const contentLocale=kind==='portfolio'?'en':'ar';
   await page.goto('/admin/content/post/new');
   // Match the starter's public site.locale so unprefixed /post/… and search resolve.
   await setContentLocale(page,contentLocale);
   await page.fill('#title',postTitle);await page.fill('#slug',postSlug);
   await page.locator('[data-field-key="body"] .ql-editor').fill('A clear brief connects the audience, content and visual direction. This editable article is a sample for the publication layout.');
   await page.locator('button[name="intent"][value="publish"]').click();
   const archiveRow=database.prepare("SELECT archive_slug FROM content_types WHERE slug='post'").get() as any;
   const archivePath='/'+(archiveRow.archive_slug||'post');
   await publicPage.goto('/');await expectNoServerError(publicPage);
   // The magazine masthead carries the title; its cover heading invites reading instead.
   if(kind==='magazine'){await expect(publicPage.locator('.ed-wordmark')).toContainText(siteTitle);await expect(publicPage.locator('.gp-builder h1')).not.toContainText(siteTitle)}
   else await expect(publicPage.locator('.gp-builder h1')).toContainText(siteTitle);
   await expect(publicPage.locator('body')).toHaveCSS('background-color',design.background);
   await expect(publicPage.locator('html')).toHaveAttribute('dir',kind==='portfolio'?'ltr':'rtl');
   await expect(publicPage.locator('html')).toHaveAttribute('data-theme',design.theme);
   await publicPage.setViewportSize({width:1280,height:800});
   await publicPage.screenshot({path:testInfo.outputPath(`starter-${kind}-desktop.png`)});
   expect(await publicPage.locator('header nav a').count()).toBeGreaterThanOrEqual(3);
   const screenshotResponse=await page.request.get(`/admin/appearance/themes/${design.theme}/screenshot.png`);expect(screenshotResponse.status()).toBe(200);expect(screenshotResponse.headers()['content-type']).toContain('image/png');
   const allAssetURLs=new Set<string>([`${env('E2E_BASE_URL')}/themes/${design.theme}/assets/site.css`]);
   for(const row of rows){
    for(const width of [320,390,768,1440]){
     await publicPage.setViewportSize({width,height:900});
     await publicPage.goto('/'+row.slug);await expectNoServerError(publicPage);
     await expect(publicPage.locator('html')).toHaveAttribute('data-theme',design.theme);
     expect(await publicPage.evaluate(()=>document.documentElement.scrollWidth<=document.documentElement.clientWidth),`${kind}/${row.slug} overflows at ${width}px`).toBe(true);
     const images=await publicPage.locator('img').evaluateAll(nodes=>nodes.map(node=>(node as HTMLImageElement).currentSrc));
     for(const image of images){if(image)allAssetURLs.add(image)}
     for(const image of await publicPage.locator('img').all()){await image.scrollIntoViewIfNeeded();await expect(image).toHaveJSProperty('complete',true);expect(await image.evaluate(node=>(node as HTMLImageElement).naturalWidth)).toBeGreaterThan(0)}
     if(width===390){await publicPage.screenshot({path:testInfo.outputPath(`starter-${kind}-${row.slug}-mobile.png`),fullPage:true})}
    }
   }
   for(const url of allAssetURLs){const response=await anonymous.request.get(url);expect(response.status(),url).toBe(200)}
   for(const route of ['/search?q=starter','/starter-design-missing-page',`/post/${postSlug}`,archivePath]){
    const response=await publicPage.goto(route);await expectNoServerError(publicPage);
    expect(response?.status()).toBe(route.includes('missing-page')?404:200);
    await expect(publicPage.locator('html')).toHaveAttribute('data-theme',design.theme);
    await expect(publicPage.locator('header').first()).toBeVisible();await expect(publicPage.locator('footer').last()).toBeVisible();
    if(route===`/post/${postSlug}`){await expect(publicPage.locator('h1')).toContainText(postTitle);await expect(publicPage.locator('.prose-gp')).toContainText('A clear brief connects')}
    if(route===archivePath){await expect(publicPage.locator('main')).toContainText(postTitle)}
   }
   await publicPage.setViewportSize({width:1440,height:1000});
   if(kind==='magazine'){
    // Live feed follows published articles rather than fixed starter cards.
    await publicPage.goto('/');await expect(publicPage.locator('.gp-feed-posts .gp-grid')).toContainText(postTitle);
    await page.goto(editorURLs[0]);await expect(page.frameLocator('#builder-preview').locator('.gp-feed-posts .gp-grid')).toContainText(postTitle);
    await page.goto(runURL);
   }
   await publicPage.setViewportSize({width:390,height:844});await publicPage.goto('/');
   const summary=publicPage.locator('header details summary');await expect(summary).toBeVisible();await summary.focus();await summary.press('Enter');
   await expect(publicPage.locator('header details')).toHaveAttribute('open','');
   for(const link of await publicPage.locator('header details nav a').all()){await expect(link).toBeVisible()}
   await summary.press('Enter');await expect(publicPage.locator('header details')).not.toHaveAttribute('open','');
   const contact=rows[rows.length-1];
   await publicPage.goto('/'+contact.slug);await expectNoServerError(publicPage);
   await expect(publicPage.locator('form[action="/forms/contact"]')).toBeVisible();
   {
    await publicPage.fill('#cf-name','Starter acceptance');await publicPage.fill('#cf-email',`starter-${kind}@e2e.test`);await publicPage.fill('#cf-message','Contact from the starter site');
    await publicPage.locator('form[action="/forms/contact"] button[type="submit"]').click();
    await expectNoServerError(publicPage);
    const submission=database.prepare("SELECT count(*) AS n FROM form_submissions WHERE email=?").get(`starter-${kind}@e2e.test`) as any;expect(submission.n).toBe(1);
   }
  }
  await page.goto('/admin/appearance/identity');
  await page.fill('input[name="title"]','Updated identity');
  await page.locator('input[name="accent"]').fill('#1d4ed8');
  await page.locator('input[name="background"]').fill('#ffffff');
  await page.locator('input[name="text"]').fill('#111827');
  await page.locator('select[name="font"]').selectOption('serif');await page.locator('select[name="width"]').selectOption('wide');
  await page.locator('#main form button[type="submit"]').click();await expect(page.locator('#main [role="status"]:visible')).toContainText(/حفظ|saved/);
  await publicPage.goto('/');await expect(publicPage.locator('body')).toHaveCSS('background-color','rgb(255, 255, 255)');
  expect(await publicPage.locator('html').evaluate(node=>getComputedStyle(node).getPropertyValue('--gp-width').trim())).toBe('1280px');
  await page.locator('input[name="text"]').fill('#ffffff');await page.locator('#main form button[type="submit"]').click();await expect(page.locator('[role="alert"]')).toContainText(/تباين|contrast/);
  await publicPage.goto('/');await expect(publicPage.locator('body')).toHaveCSS('color','rgb(17, 24, 39)');
  expect((database.prepare('SELECT count(*) AS n FROM content').get() as any).n).toBeGreaterThan(oldItems.n);
  await publicPage.screenshot({path:testInfo.outputPath('starter-magazine-identity-edited.png'),fullPage:true});
  await setLocale(page,'en');await expect(page.locator('h1')).toContainText('Site design');await setLocale(page,'ar');
  expect(errors).toEqual([]);expect(external).toEqual([]);
 }finally{
  // Restore only this throwaway E2E site's mutable settings/menu locations, preserving other specs' fixtures.
  for(const item of saved){if(item.value===undefined)database.prepare('DELETE FROM settings WHERE key=?').run(item.key);else database.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json').run(item.key,item.value,new Date().toISOString())}
  database.exec('BEGIN IMMEDIATE');try{database.prepare("UPDATE menus SET location='' WHERE location='primary'").run();for(const menu of menus)database.prepare('UPDATE menus SET location=? WHERE id=?').run(menu.location,menu.id);database.exec('COMMIT')}catch(error){database.exec('ROLLBACK');throw error}
  database.close();await anonymous.close();
 }
});
