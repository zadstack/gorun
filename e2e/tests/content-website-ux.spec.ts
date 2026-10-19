import {test,expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {csrf,db,setLocale} from '../lib/helpers.ts';

test('content selection and editor actions are clear, language filters work and mobile keeps content reachable',async({page},testInfo)=>{
 await page.goto('/admin/content/post/new');await setLocale(page,'en');const title='UX draft '+randomUUID();
 await page.fill('#title',title);await expect(page.locator('[data-save-state]')).toHaveText('Unsaved changes');await expect(page.locator('[data-save-label]')).toHaveText('Save draft');
 await page.locator('button[name=intent][value=save]').click();await expect(page).toHaveURL(/\/admin\/content\/post\/[^/]+$/);const edit=new URL(page.url()).pathname;
 try {
  await page.goto('/admin/content/post');const form=page.locator('[data-content-selection]');await expect(form.locator('[data-selection-bar]')).not.toBeVisible();const row=form.locator('tr').filter({has:page.getByRole('link',{name:title,exact:true})});await row.locator('input[name=ids]').check();await expect(form.locator('[data-selection-bar]')).toBeVisible();await expect(form.locator('[data-selection-count]')).toHaveText('1');await row.locator('input[name=ids]').uncheck();await expect(form.locator('[data-selection-bar]')).not.toBeVisible();
  await page.locator('#content-locale').selectOption('en');await page.locator('#content-filters button[type=submit]').first().click();await expect(page).toHaveURL(/locale=en/);
  await page.goto(edit);await page.locator('button[name=intent][value=publish]').click();await expect(page.locator('[data-save-label]')).toHaveText('Update published version');await expect(page.locator('button[name=intent][value=publish]')).toHaveCount(0);await page.locator('#status').selectOption('draft');await expect(page.locator('[data-save-label]')).toHaveText('Save draft and unpublish');await page.locator('#status').selectOption('published');await page.fill('#title',title+' updated');await page.locator('button[name=intent][value=save]').click();await expect(page.locator('#title')).toHaveValue(title+' updated');
  await page.setViewportSize({width:390,height:844});await page.goto('/admin/content/post');await expect(page.locator('.content-filter-drawer')).not.toHaveAttribute('open');await expect(page.getByRole('link',{name:title+' updated',exact:true})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);await page.screenshot({path:testInfo.outputPath('content-mobile-selection.png'),animations:'disabled'});
 }finally {const database=db();database.prepare('DELETE FROM content WHERE id=?').run(edit.split('/').pop()!);database.close();}
});

test('website setup is three deliberate steps, bundled previews load and a missing preview has a descriptive fallback',async({page},testInfo)=>{
 await page.goto('/admin/website');await setLocale(page,'en');await expect(page.locator('[data-website-group=pages]')).toBeVisible();await expect(page.locator('[data-website-group=design]')).toBeVisible();
 await page.goto('/admin/site-builder');await expect(page.locator('[data-wizard-step="0"]')).toBeVisible();await expect(page.locator('[data-wizard-step="1"]')).not.toBeVisible();
 for(const image of await page.locator('[data-starter-preview]').all()){await image.scrollIntoViewIfNeeded();await expect(image).toHaveJSProperty('complete',true);expect(await image.evaluate(n=>(n as HTMLImageElement).naturalWidth)).toBeGreaterThan(0);}
 const image=page.locator('[data-starter-preview]').first();await image.evaluate(n=>{(n as HTMLImageElement).src='/static/admin/missing-preview.png';});await expect(page.locator('[data-preview-fallback]').first()).toBeVisible();
 await page.locator('input[name=starter][value=company]').check();await page.locator('[data-wizard-controls]').getByRole('button',{name:'Next',exact:true}).click();await page.fill('[name=title]','Draft-only company');await page.locator('[data-wizard-controls]').getByRole('button',{name:'Next',exact:true}).click();await expect(page.locator('[data-wizard-review]')).toContainText('Draft-only company');await expect(page.locator('form[action="/admin/site-builder"] button[type=submit]')).toBeVisible();
 await page.goto('/admin/appearance/identity');await page.fill('[name=title]','Local identity preview');await expect(page.locator('[data-preview-title]')).toHaveText('Local identity preview');await page.locator('[name=background]').fill('#ffffff');await expect(page.locator('[data-identity-preview]')).toHaveCSS('background-color','rgb(255, 255, 255)');
 await page.setViewportSize({width:390,height:844});await setLocale(page,'ar');await expect(page.locator('[data-identity-preview]')).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);await page.screenshot({path:testInfo.outputPath('identity-live-preview-ar.png'),animations:'disabled'});
});

test('onboarding language updates immediately without resetting entered account fields',async({page})=>{
 const database=db();database.prepare("UPDATE users SET onboarding_required=1 WHERE email='admin@e2e.test'").run();
 try {
  await page.goto('/admin/onboarding');await page.fill('#name','Account owner');await page.fill('#password','Synthetic password not submitted');await page.selectOption('#locale','ar');await expect(page.locator('html')).toHaveAttribute('dir','rtl');await expect(page.locator('h1')).toContainText('أهلًا بك');await expect(page.locator('legend').first()).toHaveText('تأمين حسابك');await expect(page.locator('#name')).toHaveValue('Account owner');await expect(page.locator('#password')).toHaveValue('Synthetic password not submitted');await page.selectOption('#locale','en');await expect(page.locator('html')).toHaveAttribute('dir','ltr');await expect(page.locator('legend').first()).toHaveText('Secure your account');
 }finally{database.prepare("UPDATE users SET onboarding_required=0 WHERE email='admin@e2e.test'").run();database.close();await page.goto('/admin');}
});

test('media library can switch to a compact list without losing the selected file',async({page})=>{
 const {tinyPNG}=await import('../fixtures/wp-fixture-server.ts');await page.goto('/admin/media');await setLocale(page,'en');const filename='ux-library-'+randomUUID()+'.png';await page.locator('#media-file').setInputFiles({name:filename,mimeType:'image/png',buffer:tinyPNG()});
 const item=page.locator('[data-media-items] a').filter({hasText:filename.replace('.png','')});await expect(item).toBeVisible();await page.locator('[data-media-view-button=list]').click();await expect(page.locator('#media-results')).toHaveAttribute('data-media-view','list');await expect(item).toBeVisible();await page.locator('[data-media-view-button=grid]').click();await expect(page.locator('#media-results')).toHaveAttribute('data-media-view','grid');
});

test('section image picker uses real media and keeps the resulting layout private',async({page})=>{
 const {tinyPNG}=await import('../fixtures/wp-fixture-server.ts');await page.goto('/admin/media');await setLocale(page,'en');const filename='ux-section-'+randomUUID()+'.png';await page.locator('#media-file').setInputFiles({name:filename,mimeType:'image/png',buffer:tinyPNG()});await expect(page.locator('[data-media-items]')).toContainText(filename.replace('.png',''));
 await page.goto('/admin/content/page/new');await page.fill('#title','Private section picker '+randomUUID());await page.locator('button[name=intent][value=save]').click();const id=new URL(page.url()).pathname.split('/').pop()!;
 try {
  await page.goto('/admin/builder/'+id);await page.locator('#builder-kind').selectOption('hero');await page.locator('[data-action=add]').click();await page.getByRole('button',{name:'Choose from media library',exact:true}).first().click();const choice=page.locator('#gp-media-picker-body button[data-id]').filter({hasText:filename.replace('.png','')});await expect(choice).toBeVisible();await choice.first().click();await expect(page.locator('[data-field=image]').first()).toHaveValue(/^\/media\//);await page.locator('[data-action=save]').click();const database=db();await expect.poll(()=>JSON.parse((database.prepare('SELECT document_json FROM builder_documents WHERE content_id=?').get(id) as any )?.document_json||'{}').sections?.[0]?.image||'').toMatch(/^\/media\//);expect((database.prepare('SELECT status FROM content WHERE id=?').get(id) as any).status).toBe('draft');database.close();
 }finally{const database=db();database.prepare('DELETE FROM content WHERE id=?').run(id);database.close();}
});
