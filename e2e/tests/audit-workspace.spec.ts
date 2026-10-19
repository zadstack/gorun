import {test,expect} from '@playwright/test';
import {mkdirSync} from 'node:fs';
import {resolve} from 'node:path';
import {randomUUID} from 'node:crypto';
import {setLocale,db} from '../lib/helpers.ts';
const output=resolve('..','..','..','outputs','admin-audit-fixes');
test('daily dashboard, explicit customization and reviewed permission presets work without auto granting',async({page})=>{
 await page.goto('/admin');await setLocale(page,'en');
 const customize=page.locator('[data-dashboard-customize]');await expect(customize).not.toHaveAttribute('open');await expect(page.locator('[data-layout-drag]').first()).not.toBeVisible();await customize.locator('summary').click();await expect(page.locator('[data-layout-drag]').first()).toBeVisible();await customize.locator('summary').click();
 mkdirSync(output,{recursive:true});await page.screenshot({path:resolve(output,'dashboard-en.png')});
 await page.goto('/admin/agents/keys');await expect(page.locator('#k-name')).not.toBeVisible();await page.locator('.audit-key-create>summary').click();await expect(page.locator('[data-key-summary]')).toHaveText('No permissions selected.');
 await page.locator('[data-key-preset=draft]').click();expect(await page.locator('input[name=scopes]:checked').evaluateAll(nodes=>nodes.map(n=>(n as HTMLInputElement).value).sort())).toEqual(['canvas.append','canvas.read','canvas.run','content.view']);await expect(page.locator('input[name=all]')).not.toBeChecked();
 await page.locator('[data-key-preset=read]').click();await expect(page.locator('input[name=scopes]:checked')).toHaveCount(1);await expect(page.locator('input[name=scopes][value="content.view"]')).toBeChecked();
 await page.locator('[data-key-preset=custom]').click();await expect(page.locator('input[name=scopes]:checked')).toHaveCount(0);await expect(page.locator('#k-plaintext')).toHaveCount(0);
 await page.setViewportSize({width:390,height:844});await setLocale(page,'ar');await page.locator('.audit-key-create>summary').click();await page.locator('[data-key-preset=read]').click();expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);await page.screenshot({path:resolve(output,'key-presets-ar-mobile.png'),fullPage:true});
});

test('template cards, search and unselected content reference distinguish setup from access loss',async({page})=>{
 await page.goto('/admin/canvases');await setLocale(page,'en');await expect(page.locator('[data-preset-choice]')).toHaveCount(7);await page.locator('[data-preset-choice="ask-content"]').click();await expect(page.locator('#canvas-preset')).toHaveValue('ask-content');await page.locator('[data-preset-choice=""]').click();
 const title='Audit content '+randomUUID();await page.locator('#new-canvas-title').fill(title);await page.locator('form[action="/admin/canvases"] [type=submit]').click();const path=new URL(page.url()).pathname;
 try{
 const database=db(),id=randomUUID();database.prepare('UPDATE admin_canvases SET document_json=? WHERE id=?').run(JSON.stringify([{id,kind:'content',title:'Choose a source',x:24,y:24}]),path.split('/').pop()!);database.close();
 await page.reload();await expect(page.locator('[data-reference]')).toContainText('Choose an article or page using Edit.');await expect(page.locator('[data-reference]')).not.toContainText('unavailable');
 await page.goto('/admin/canvases');await page.locator('[data-board-search]').fill(title);await expect(page.locator('.canvas-board-list>li:visible')).toHaveCount(1);await page.locator('[data-board-search]').fill('no-match-'+randomUUID());await expect(page.locator('[data-board-search-empty]')).toBeVisible();
 }finally{const database=db();database.prepare('DELETE FROM admin_canvases WHERE id=?').run(path.split('/').pop()!);database.close();}
});

test('structured Studio saves exact reviewed config, preserves untouched settings and never discovers or executes on edit',async({page})=>{
 const database=db(),saved=database.prepare("SELECT value_json,updated_at FROM settings WHERE key='ai.studio'").get() as any;
 try{
 database.prepare("INSERT INTO settings(key,value_json,updated_at) VALUES('ai.studio',?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json").run(JSON.stringify({image_model:'saved-image-model',bindings:[],presets:[{id:'original-policy',name:'Original policy',kind:'text',prompt:'Preserve the original instructions.'}]}),new Date().toISOString());
 await page.goto('/admin/settings/studio');await setLocale(page,'en');let discovery=0;page.on('request',r=>{if(r.url().includes('/settings/studio/discover'))discovery++;});
 await expect(page.locator('[data-studio-advanced]')).not.toHaveAttribute('open');await page.getByRole('button',{name:'Add instructions',exact:true}).click();const card=page.locator('[data-studio-structured] article').filter({has:page.getByRole('heading',{name:'Instructions',exact:true})}).last();
 await card.getByLabel('Name',{exact:true}).fill('Evidence summary');await card.getByLabel('Identifier',{exact:true}).fill('audit-evidence');await card.getByLabel('Purpose and instructions',{exact:true}).fill('Summarize only the supplied evidence; preserve uncertainty.');
 const posted=page.waitForRequest(r=>new URL(r.url()).pathname==='/admin/settings/studio'&&r.method()==='POST');await page.locator('[data-studio-config-save]').click();const config=(await posted).postDataJSON();expect(config.presets.some((p:any)=>p.id==='audit-evidence'&&p.prompt.includes('preserve uncertainty'))).toBe(true);expect(config.image_model).toBe('saved-image-model');expect(config.presets.find((p:any)=>p.id==='original-policy').prompt).toBe('Preserve the original instructions.');await expect(page.locator('[data-studio-config-status]')).toContainText('Saved. No generation was executed.');expect(discovery).toBe(0);
 await page.reload();await expect(page.locator('[data-studio-structured]')).toContainText('Evidence summary');await page.screenshot({path:resolve(output,'studio-en.png'),fullPage:true});
 await page.setViewportSize({width:390,height:844});await setLocale(page,'ar');expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);await page.screenshot({path:resolve(output,'studio-ar-mobile.png'),fullPage:true});
 }finally{if(saved)database.prepare("UPDATE settings SET value_json=?,updated_at=? WHERE key='ai.studio'").run(saved.value_json,saved.updated_at);else database.prepare("DELETE FROM settings WHERE key='ai.studio'").run();database.close();}
});

test('connection type guidance preserves saved input and provides explicit provider examples',async({page})=>{
 await page.goto('/admin/settings/connections');await setLocale(page,'en');const form=page.locator('[data-connection-setup]');await form.locator('[name=kind]').selectOption('ai');await expect(form.locator('[data-connection-purpose]')).toContainText('Writing model');await form.locator('[name=endpoint]').fill('https://custom.example/v1');await form.locator('[name=provider]').selectOption('anthropic');await expect(form.locator('[name=endpoint]')).toHaveValue('https://custom.example/v1');await form.locator('[name=kind]').selectOption('s3');await expect(form.locator('[data-connection-purpose]')).toContainText('does not move files');await expect(form.locator('[name=bucket]')).toBeVisible();await expect(form.locator('[name=token]')).not.toBeVisible();
});

test('structured and Advanced JSON modes roundtrip actual saved settings without dropping provider mappings',async({page})=>{
 const database=db(),saved=database.prepare("SELECT value_json,updated_at FROM settings WHERE key='ai.studio'").get() as any;
 const id='01'+randomUUID().replaceAll('-','').slice(0,24).toUpperCase(),version='01'+randomUUID().replaceAll('-','').slice(0,24).toUpperCase();
 const profileKey='connections.profile.'+id,discoveryKey='ai.studio.discovery.'+id;
 // A synthetic saved profile/catalog permits the real settings-save contract;
 // the fixture performs no network discovery or third-party tool execution.
 const binding={id:'audit-roundtrip',name:'Archive draft',kind:'text',connection_id:id,tool:'draft',arguments:{prompt:'{{prompt}}',vendor_options:{mode:'source-only',limit:3}},text_path:'structuredContent.text',job_path:'structuredContent.job',status_tool:'status',status_arguments:{job_id:'{{job_id}}',vendor_poll:{source:'exact'}},status_path:'structuredContent.state',ready_value:'ready',failed_value:'failed'};
 const put=database.prepare('INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json');
 const candidates=()=>Number((database.prepare('SELECT count(*) n FROM admin_studio_candidates').get() as any).n);
 try{
 put.run(profileKey,JSON.stringify({id,version,kind:'mcp',name:'Synthetic archive',endpoint:'https://archive.example/mcp'}),new Date().toISOString());
 put.run(discoveryKey,JSON.stringify({version,tools:[{name:'draft',inputSchema:{type:'object'}},{name:'status',inputSchema:{type:'object'}}]}),new Date().toISOString());
 put.run('ai.studio',JSON.stringify({image_model:'',bindings:[binding],presets:[]}),new Date().toISOString());
 await page.goto('/admin/settings/studio');await setLocale(page,'en');
 let discovery=0;page.on('request',r=>{if(r.url().includes('/settings/studio/discover'))discovery++;});const before=candidates();
 const form=page.locator('[data-studio-structured]'),card=form.locator('article'),advanced=page.locator('[data-studio-advanced]'),raw=page.locator('[data-studio-config]'),mode=page.locator('[data-studio-editor-mode]');
 await card.getByLabel('Name',{exact:true}).fill('Edited in fields');await advanced.locator('summary').click();await expect.poll(async()=>JSON.parse(await raw.inputValue()).bindings[0].name).toBe('Edited in fields');
 const changed=JSON.parse(await raw.inputValue());changed.bindings[0].name='Edited in JSON';changed.bindings[0].arguments.vendor_options.mode='strict';await raw.fill(JSON.stringify(changed,null,2));
 await expect(mode).toContainText('Save uses Advanced JSON');expect(await form.locator('input,textarea,select,button').evaluateAll(nodes=>nodes.every(n=>(n as HTMLInputElement).disabled))).toBe(true);
 const rawSave=page.waitForRequest(r=>new URL(r.url()).pathname==='/admin/settings/studio'&&r.method()==='POST');await page.locator('[data-studio-config-save]').click();expect((await rawSave).postDataJSON().bindings[0].name).toBe('Edited in JSON');await expect(page.locator('[data-studio-config-status]')).toContainText('Saved. No generation was executed.');
 await page.locator('[data-studio-json-load]').click();await expect(mode).not.toBeVisible();await expect(card.getByLabel('Name',{exact:true})).toBeEnabled();await expect(card.getByLabel('Name',{exact:true})).toHaveValue('Edited in JSON');
 await card.getByLabel('Name',{exact:true}).fill('Final structured name');await advanced.locator('summary').click();await advanced.locator('summary').click();await expect.poll(async()=>JSON.parse(await raw.inputValue()).bindings[0].name).toBe('Final structured name');
 const finalSave=page.waitForRequest(r=>new URL(r.url()).pathname==='/admin/settings/studio'&&r.method()==='POST');await page.locator('[data-studio-config-save]').click();const sent=(await finalSave).postDataJSON().bindings[0];
 expect(sent.status_arguments).toEqual(binding.status_arguments);for(const key of ['job_path','status_tool','status_path','ready_value','failed_value'])expect(sent[key]).toBe((binding as any)[key]);expect(sent.arguments.vendor_options).toEqual({mode:'strict',limit:3});
 await expect(page.locator('[data-studio-config-status]')).toContainText('Saved. No generation was executed.');await page.reload();await expect(page.locator('[data-studio-structured]').getByLabel('Name',{exact:true})).toHaveValue('Final structured name');
 // A broken mapping must not expose stale JSON as if it reflected the fields.
 await page.locator('[data-studio-structured]').getByLabel('Input mapping (JSON object)',{exact:true}).fill('{broken');await page.locator('[data-studio-advanced]>summary').click();await expect(page.locator('[data-studio-advanced]')).not.toHaveAttribute('open');await expect(page.locator('[data-studio-config-status]')).toContainText('valid JSON object');await expect(page.locator('[data-studio-structured] [aria-invalid=true]')).toBeFocused();
 expect(discovery).toBe(0);expect(candidates()).toBe(before);
 }finally{
 database.prepare('DELETE FROM settings WHERE key IN(?,?)').run(profileKey,discoveryKey);
 if(saved)database.prepare("UPDATE settings SET value_json=?,updated_at=? WHERE key='ai.studio'").run(saved.value_json,saved.updated_at);else database.prepare("DELETE FROM settings WHERE key='ai.studio'").run();database.close();
 }
});
