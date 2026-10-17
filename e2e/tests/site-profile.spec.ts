import {test,expect,type Page} from '@playwright/test';
import {execFileSync} from 'node:child_process';
import {resolve} from 'node:path';
import {db,env,setLocale,expectNoServerError} from '../lib/helpers.ts';
import {tinyPNG} from '../fixtures/wp-fixture-server.ts';

async function chooseImage(page:Page,name:string,filename:string){
 const target=await page.locator(`input[name="${name}"]`).getAttribute('id');expect(target).toBeTruthy();
 await page.locator(`button[data-media-open][data-target="${target}"]`).click();
 const item=page.locator('#gp-media-picker-body button[data-id]').filter({hasText:filename}).first();await expect(item).toBeVisible();const id=await item.getAttribute('data-id');await item.click();await expect(page.locator(`input[name="${name}"]`)).toHaveValue(id!);return id!;
}

test('guided profiles produce personalized draft and published sites in both languages',async({page,browser},testInfo)=>{
 test.setTimeout(180_000);
 const database=db(), cli=resolve('.bin/gorun');
 const keys=['site.title','site.tagline','site.locale','site.front_page','site.theme','site.logo','builder.identity','contact.email','contact.phone','contact.address'];
 const saved=keys.map(key=>({key,value:(database.prepare('SELECT value_json FROM settings WHERE key=?').get(key) as any)?.value_json}));
 const menus=database.prepare("SELECT id,location FROM menus WHERE location='primary'").all() as any[];
 const liveSettings=()=>JSON.stringify(database.prepare("SELECT key,value_json FROM settings WHERE key NOT LIKE 'builder.starter.%' ORDER BY key").all());
 const anon=await browser.newContext({storageState:{cookies:[],origins:[]}}), publicPage=await anon.newPage();
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));publicPage.on('pageerror',e=>errors.push(e.message));
 try{
  for(const image of [{name:'profile-logo.png',width:120,height:80,color:[15,118,110,255]},{name:'profile-hero.png',width:600,height:400,color:[159,43,25,255]}]){
   await page.goto('/admin/media');await page.locator('#media-file').setInputFiles({name:image.name,mimeType:'image/png',buffer:tinyPNG(image.width,image.height,image.color as [number,number,number,number])});await expect(page.locator('#media-results')).toContainText(image.name.replace('.png',''));
  }
  for(const kind of ['company','portfolio','magazine'] as const){
   const locale=kind==='portfolio'?'en':'ar';
   const profile=locale==='ar'?{title:`استوديو الاختبار ${kind}`,description:'نص تعريفي حقيقي يشرح عملنا في تصميم المواقع العربية.',about:'نحن فريق مستقل نساعد المؤسسات على تحويل أفكارها إلى مواقع واضحة.',services:'تصميم مواقع عربية\nتحسين تجربة الاستخدام\nإدارة المحتوى',phone:'+201001234567',address:'القاهرة، مصر'}:{title:'Actual Portfolio Studio',description:'We design clear digital experiences for independent organizations.',about:'An independent designer working with organizations on useful websites.',services:'Website design',phone:'+441234567890',address:'Bristol, United Kingdom'};
   await page.goto('/admin/site-builder');await setLocale(page,locale);await page.locator(`input[name="starter"][value="${kind}"]`).check();
   await page.fill('input[name="title"]',profile.title);await page.fill('textarea[name="tagline"]',locale==='ar'?'تصميم واضح يخدم الناس.':'Clear design for people.');await page.selectOption('select[name="locale"]',locale);
   await page.fill('[name="description"]',profile.description);await page.fill('[name="about"]',profile.about);await page.fill('[name="services"]',profile.services);await page.fill('[name="contact_email"]',`profile-${kind}@e2e.test`);await page.fill('[name="contact_phone"]',profile.phone);await page.fill('[name="contact_address"]',profile.address);
   const logoID=await chooseImage(page,'logo','profile-logo');const heroID=await chooseImage(page,'hero_image','profile-hero');
   for(const width of [320,390]){await page.setViewportSize({width,height:844});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=document.documentElement.clientWidth),`profilewizard ${locale}/${width}`).toBe(true);await page.getByRole('heading',{level:1}).scrollIntoViewIfNeeded();if(width===390)await page.screenshot({path:testInfo.outputPath(`profile-${kind}-wizard-mobile.png`),fullPage:true})}
   await page.setViewportSize({width:1280,height:900});await page.getByRole('heading',{level:1}).scrollIntoViewIfNeeded();await page.screenshot({path:testInfo.outputPath(`profile-${kind}-wizard-desktop.png`),fullPage:true});
   if(kind==='company'){const savedBeforeInvalid=liveSettings();await page.locator('form[action="/admin/site-builder"]').evaluate(form=>(form as HTMLFormElement).noValidate=true);await page.fill('[name="contact_email"]','invalid-email');await page.locator('form[action="/admin/site-builder"] button[type="submit"]').click();await expect(page.locator('[role="alert"]')).toBeVisible();await expect(page.locator('[name="description"]')).toHaveValue(profile.description);await expect(page.locator('[name="logo"]')).toHaveValue(logoID);expect(liveSettings()).toBe(savedBeforeInvalid);await page.fill('[name="contact_email"]',`profile-${kind}@e2e.test`)}
   const before=liveSettings();await publicPage.goto('/');const oldTheme=await publicPage.locator('html').getAttribute('data-theme');const oldTitle=await publicPage.title();
   await page.locator('form[action="/admin/site-builder"] button[type="submit"]').click();await expect(page).toHaveURL(/site-builder\?run=/);const runURL=page.url();
   expect(liveSettings()).toBe(before);await publicPage.reload();expect(await publicPage.title()).toBe(oldTitle);await expect(publicPage.locator('html')).toHaveAttribute('data-theme',oldTheme!);
   const editorURLs=await page.locator('a[href^="/admin/builder/"]').filter({hasText:/تعديل|Edit/}).evaluateAll(nodes=>nodes.map(node=>(node as HTMLAnchorElement).getAttribute('href')!));expect(editorURLs).toHaveLength(kind==='magazine'?4:5);
   const rows=editorURLs.map(url=>database.prepare('SELECT id,slug,title,status,body FROM content WHERE id=?').get(url.split('/').pop()!) as any);
   const home=rows[0],about=rows.find(row=>row.body.includes(profile.about)),contact=rows.find(row=>row.body.includes(profile.phone));expect(about).toBeTruthy();expect(contact).toBeTruthy();
   expect(home.body).toContain(profile.description);expect(contact.body).toContain(profile.address);expect(contact.body).toContain(`profile-${kind}@e2e.test`);
   for(const service of profile.services.split('\n'))expect(rows.map(row=>row.body).join('\n')).toContain(service);
   for(const row of rows){expect(row.status).toBe('draft');expect((await anon.request.get('/'+row.slug)).status()).toBe(404)}
   await page.goto(editorURLs[0]);const preview=page.frameLocator('#builder-preview');await expect(preview.locator('html')).toHaveAttribute('data-theme',kind==='company'?'business':kind==='portfolio'?'portfolio':'magazine');await expect(preview.locator('main')).toContainText(profile.description);await expect(preview.locator('header img')).toHaveCount(1);await expect(preview.locator('main img').first()).toHaveAttribute('src',/\/media\//);await expect(preview.locator('footer').last()).toBeVisible();
   await page.screenshot({path:testInfo.outputPath(`profile-${kind}-draft-preview.png`)});expect(liveSettings()).toBe(before);
   await page.goto(runURL);await page.locator('form[action$="/activate"] button[type="submit"]').click();await expect(page.locator('main p[role="status"]:visible')).toBeVisible();
   expect(JSON.parse((database.prepare("SELECT value_json FROM settings WHERE key='site.logo'").get() as any).value_json)).toBe(logoID);expect(JSON.parse((database.prepare("SELECT value_json FROM settings WHERE key='contact.email'").get() as any).value_json)).toBe(`profile-${kind}@e2e.test`);
   for(const row of rows) expect((database.prepare('SELECT status FROM content WHERE id=?').get(row.id) as any).status).toBe('published');
   const mediaURL=(database.prepare('SELECT url FROM media WHERE id=?').get(heroID) as any)?.url;
   for(const row of [home,about,contact]){
    await publicPage.goto('/'+row.slug);await expectNoServerError(publicPage);await expect(publicPage.locator('html')).toHaveAttribute('dir',locale==='ar'?'rtl':'ltr');await expect(publicPage.locator('header img')).toHaveCount(1);
    if(row.id===home.id){await expect(publicPage.locator('main')).toContainText(profile.description);if(mediaURL)await expect(publicPage.locator('main img').first()).toHaveAttribute('src',mediaURL)}
    if(row.id===about.id)await expect(publicPage.locator('main')).toContainText(profile.about);
    if(row.id===contact.id){await expect(publicPage.locator('main')).toContainText(profile.phone);await expect(publicPage.locator('main')).toContainText(profile.address);await expect(publicPage.locator('form[action="/forms/contact"]')).toHaveCount(1)}
    for(const width of [320,390]){await publicPage.setViewportSize({width,height:844});expect(await publicPage.evaluate(()=>document.documentElement.scrollWidth<=document.documentElement.clientWidth),`profile ${kind}/${row.slug}/${width}`).toBe(true)}
   }
   await publicPage.goto('/');await publicPage.screenshot({path:testInfo.outputPath(`profile-${kind}-public-mobile.png`),fullPage:true});
  }
  expect(errors).toEqual([]);
 }finally{
  for(const item of saved){execFileSync(cli,item.value===undefined?['settings','unset',item.key,'--data',env('E2E_DATA')]:['settings','set',item.key,item.value,'--data',env('E2E_DATA')],{stdio:'pipe'})}
  database.exec('BEGIN IMMEDIATE');try{database.prepare("UPDATE menus SET location='' WHERE location='primary'").run();for(const menu of menus)database.prepare('UPDATE menus SET location=? WHERE id=?').run(menu.location,menu.id);database.exec('COMMIT')}catch(error){database.exec('ROLLBACK');throw error}
  await page.setViewportSize({width:1280,height:900});await page.goto('/admin');await setLocale(page,'ar');database.close();await anon.close();
 }
});
