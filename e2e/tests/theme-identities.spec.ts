// Fresh-app documentation fixture: descriptive categories, never customer brands.
// THEME_SCREENSHOTS=1 updates bundled gallery/README images; other runs write only test output.
import { test, expect, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { csrf, db, setLocale, MARKER_500 } from '../lib/helpers.ts';

const themes = [
  {slug:'default',en:'Classic',ar:'كلاسيكي',accent:'#2f5a78',enNote:'A calm reading identity with room for your voice.',arNote:'هوية قراءة هادئة تترك مساحة لصوت موقعك.'},
  {slug:'business',en:'Modern',ar:'حديث',accent:'#0f766e',enNote:'Sharp, high-contrast chrome for clear pages.',arNote:'واجهة حادة وتباين واضح لصفحات مفهومة.'},
  {slug:'portfolio',en:'Minimal',ar:'بسيط',accent:'#3f5d4a',enNote:'Quiet earth tones that stay out of the way.',arNote:'ألوان ترابية هادئة لا تنافس محتواك.'},
  {slug:'magazine',en:'Serif',ar:'سيرف',accent:'#1e3a5f',enNote:'Type-led identity for long-form reading.',arNote:'هوية تعتمد على الخط لقراءة مطوّلة.'},
  {slug:'docs',en:'Docs',ar:'توثيق',accent:'#155e75',enNote:'Light steel chrome for guides and reference.',arNote:'واجهة فولاذية خفيفة للأدلة والمراجع.'},
];

function setting(key:string,value:unknown) {
  const database=db();
  database.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json').run(key,JSON.stringify(value),new Date().toISOString());
  database.close();
}
async function activate(page:Page,slug:string) {
  await page.goto('/admin/appearance/themes');
  const response=await page.request.post(`/admin/appearance/themes/${slug}/activate`,{form:{_csrf:await csrf(page)},maxRedirects:0});
  expect(response.status()).toBe(303);
}

test('descriptive theme previews in English/Arabic and preserved customer identity', async ({page},testInfo) => {
  test.setTimeout(180_000);
  const changedKeys=['site.theme','site.title','site.tagline','site.logo','site.locale','site.front_page','builder.identity'];
  const snapshotDB=db();
  const snapshots=snapshotDB.prepare("SELECT key,value_json,updated_at FROM settings WHERE key IN ("+changedKeys.map(()=>'?').join(',')+") OR key LIKE 'theme.%'").all(...changedKeys) as {key:string,value_json:string,updated_at:string}[];
  snapshotDB.prepare("DELETE FROM settings WHERE key='builder.identity' OR key LIKE 'theme.%'").run();
  snapshotDB.close();
  try {
  const errors:string[]=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.goto('/admin');
  await setLocale(page,'en');
  await page.goto('/admin/media');
  await page.locator('#media-file').setInputFiles(resolve('fixtures/theme-preview.svg'));
  await expect(page.locator('#media-results')).toContainText('theme-preview');
  const database=db();
  const media=database.prepare('SELECT id,url FROM media WHERE filename=? ORDER BY created_at DESC,rowid DESC LIMIT 1').get('theme-preview.svg') as {id:string,url:string};
  database.close();
  const postPaths:Record<string,string>={};
  for(const locale of ['en','ar']) {
    for(let index=0;index<3;index++) {
      const title=locale==='en'?['A clearer way to organize your work','Small steps, considered results','A practical guide to getting started'][index]:['طريقة أوضح لتنظيم العمل','خطوات صغيرة ونتائج مدروسة','دليل عملي للبداية'][index];
      const response=await page.request.post('/api/v1/content',{headers:{'X-CSRF-Token':await csrf(page)},data:{type:'post',title,slug:`theme-story-${locale}-${index}`,locale,status:'published',featured_media_id:media.id,excerpt:locale==='en'?'Example content for this theme preview. Replace it with your own writing.':'محتوى توضيحي لمعاينة القالب. استبدله بمحتوى موقعك.',body:locale==='en'?'<p>A useful page starts with a clear idea and a readable structure.</p><h2>Make the next step clear</h2><p>Use this sample to explore the layout, navigation and reading experience.</p>':'<p>تبدأ الصفحة المفيدة بفكرة واضحة وبنية مريحة للقراءة.</p><h2>خطوة تالية واضحة</h2><p>استخدم هذا المثال لاستكشاف التنسيق والتنقل وتجربة القراءة.</p>'}});
      expect(response.status()).toBe(201);
      if(index===0)postPaths[locale]=`/${locale}/post/theme-story-${locale}-${index}`;
    }
    for(const [index,title] of (locale==='en'?['Getting started','Writing and publishing','Settings and reference']:['دليل البدء','الكتابة والنشر','الإعدادات والمراجع']).entries()) {
      const response=await page.request.post('/api/v1/content',{headers:{'X-CSRF-Token':await csrf(page)},data:{type:'page',title,slug:`theme-guide-${locale}-${index}`,locale,status:'published',body:locale==='en'?'<p>A short example guide for this layout.</p>':'<p>دليل توضيحي قصير لتجربة هذا التنسيق.</p>'}});
      expect(response.status()).toBe(201);
    }
  }
  setting('site.front_page','');setting('site.logo','');setting('site.locale','ar');
  const output=process.env.THEME_PREVIEW_DIR?resolve(process.env.THEME_PREVIEW_DIR):testInfo.outputPath('theme-previews');
  mkdirSync(output,{recursive:true});
  for(const theme of themes) {
    await activate(page,theme.slug);
    await page.goto('/admin/appearance/themes');
    await expect(page.locator(`#theme-${theme.slug}`)).toHaveText(theme.en);
    for(const locale of ['en','ar']) {
      setting('site.title',locale==='en'?theme.en:theme.ar);
      setting('site.tagline',locale==='en'?theme.enNote:theme.arNote);
      for(const width of [1280,390]) {
        await page.setViewportSize({width,height:width===1280?800:844});
        const response=await page.goto(`/${locale}/`);
        expect(response?.status()).toBe(200);
        await page.evaluate(()=>document.fonts.ready);
        await expect(page.locator('html')).toHaveAttribute('lang',locale);
        await expect(page.locator('html')).toHaveAttribute('dir',locale==='ar'?'rtl':'ltr');
        expect(await page.content()).not.toContain(MARKER_500);
        await expect(page.locator('header').first()).toContainText(locale==='en'?theme.en:theme.ar);
        expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);
        const accent=await page.locator('html').evaluate(el=>getComputedStyle(el).getPropertyValue('--accent').trim());
        expect(accent).toBe(theme.accent);
        if(theme.slug==='portfolio'&&width===1280)await expect(page.locator('.studio-header-cta-mobile')).toBeHidden();
        await page.screenshot({path:resolve(output,`${theme.slug}-${locale}-${width}.png`)});
        if(process.env.THEME_SCREENSHOTS==='1') {
          if(locale==='ar'&&width===1280)await page.screenshot({path:resolve('..','themes',theme.slug,'screenshot.png')});
          if(theme.slug==='default'&&locale==='en'&&width===1280)await page.screenshot({path:resolve('..','assets/branding/screenshots/theme-home-en.png')});
          if(theme.slug==='default'&&locale==='ar'&&width===390) {
            await page.goto(postPaths.ar);
            await expect(page.locator('html')).toHaveAttribute('dir','rtl');
            await page.screenshot({path:resolve('..','assets/branding/screenshots/theme-post-ar-mobile.png'),fullPage:true});
          }
        }
      }
    }
  }
  // Activation may change the theme slug only: existing customer identity must win.
  setting('site.title','Customer-owned title');setting('site.tagline','Keep my site identity');setting('site.logo',media.id);
  const custom={accent:'#7c2d12',background:'#fff8ee',text:'#231a12',font:'system',width:'standard'};
  setting('builder.identity',custom);
  for(const theme of themes) {
    await activate(page,theme.slug);
    for(const locale of ['en','ar']) {
    await page.goto(`/${locale}/`);
    await expect(page.locator('header').first()).toContainText('Customer-owned title');
    await expect(page.locator('header img').first()).toHaveAttribute('src',media.url);
    const visibleIdentity=await page.evaluate(()=>{
      const root=getComputedStyle(document.documentElement), body=getComputedStyle(document.body);
      return {accent:root.getPropertyValue('--accent').trim(),buttonAccent:root.getPropertyValue('--gp-accent').trim(),background:body.backgroundColor,text:body.color,font:body.fontFamily};
    });
    expect(visibleIdentity.accent).toBe(custom.accent);
    expect(visibleIdentity.buttonAccent).toBe(custom.accent);
    expect(visibleIdentity.background).toBe('rgb(255, 248, 238)');
    expect(visibleIdentity.text).toBe('rgb(35, 26, 18)');
    expect(visibleIdentity.font).toContain('system-ui');
    const database=db();
    for(const [key,value] of Object.entries({'site.title':'Customer-owned title','site.logo':media.id,'builder.identity':custom})) {
      const row=database.prepare('SELECT value_json FROM settings WHERE key=?').get(key) as {value_json:string};
      expect(JSON.parse(row.value_json)).toEqual(value);
    }
    database.close();
    }
  }
  // A separately saved per-theme accent remains more specific than builder defaults.
  setting('theme.docs.identity_accent','#174a6c');
  await activate(page,'docs');await page.goto('/en/');
  expect(await page.locator('html').evaluate(el=>getComputedStyle(el).getPropertyValue('--accent').trim())).toBe('#174a6c');
  expect(errors).toEqual([]);
  } finally {
    const restore=db();
    restore.prepare("DELETE FROM settings WHERE key IN ("+changedKeys.map(()=>'?').join(',')+") OR key LIKE 'theme.%'").run(...changedKeys);
    for(const row of snapshots)restore.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)').run(row.key,row.value_json,row.updated_at);
    restore.close();
  }
});
