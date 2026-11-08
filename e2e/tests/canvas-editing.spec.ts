import {test,expect,type Page} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {csrf,setLocale} from '../lib/helpers.ts';
async function board(page:Page) {
 await page.goto('/admin/canvases');await setLocale(page,'en');
 const response=await page.request.post('/admin/canvases',{form:{_csrf:await csrf(page),title:'E2E editing '+randomUUID()},maxRedirects:0});expect(response.status()).toBe(303);
 const path=response.headers().location;await page.goto(path);await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');return path;
}
async function addNote(page:Page,title='Local note') {
 await page.locator('[data-action=add]').click();const form=page.locator('#canvas-card-form');await form.locator('[name=title]').fill(title);await form.locator('[name=body]').fill('Recover this exact private writing.');await form.locator('[type=submit]').click();
}
async function stored(page:Page) {return page.evaluate(()=>Object.keys(localStorage).filter(key=>key.startsWith('gorun:canvas-draft:v1:')).map(key=>JSON.parse(localStorage.getItem(key)!)));}
test('undo and redo card create, move and delete through normal saves',async({page})=>{
 const path=await board(page);await addNote(page,'First note');await expect(page.locator('.canvas-card')).toHaveCount(1);
 await page.locator('[data-action=undo]').click();await expect(page.locator('.canvas-card')).toHaveCount(0);
 await page.locator('[data-action=redo]').click();await expect(page.locator('.canvas-card')).toHaveCount(1);
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');const card=page.locator('.canvas-card');const before=(await(await page.request.get(path+'/document')).json()).cards[0].x;
 await card.hover();await card.locator('[data-direction=right]').click();await page.locator('[data-action=undo]').click();
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');expect((await(await page.request.get(path+'/document')).json()).cards[0].x).toBe(before);
 page.once('dialog',d=>d.accept());await card.hover();await card.locator('[data-remove]').click();await expect(card).toHaveCount(0);
 await page.locator('[data-action=undo]').click();await expect(card).toHaveCount(1);
});
test('arranging several cards is one reversible history step',async({page})=>{
 const path=await board(page),cards=[{id:randomUUID(),kind:'note',title:'Far note',body:'First',x:900,y:400},{id:randomUUID(),kind:'note',title:'Lower note',body:'Second',x:24,y:650}];
 expect((await page.request.post(path+'/document',{headers:{'X-CSRF-Token':await csrf(page)},data:{version:1,title:'Arrange history',cards,edges:[]}})).status()).toBe(200);await page.reload();
 const positions=()=>page.request.get(path+'/document').then(response=>response.json()).then(document=>document.cards.map((card:{id:string,x:number,y:number})=>({id:card.id,x:card.x,y:card.y})));
 const before=await positions();await page.locator('[data-action=arrange]').click();await expect(page.locator('[data-action=undo]')).toBeEnabled();await page.locator('[data-action=undo]').click();await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');expect(await positions()).toEqual(before);
 await expect(page.locator('[data-action=undo]')).toBeDisabled();await page.locator('[data-action=redo]').click();await expect.poll(positions).not.toEqual(before);await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
});
test('local writing survives reload before autosave and requires explicit restore',async({page},info)=>{
 const path=await board(page);let posts=0;await page.route('**/admin/canvases/*/document',async route=>{if(route.request().method()==='POST'){posts++;await route.abort();}else await route.continue();});
 await addNote(page);await expect.poll(async()=> (await stored(page)).length).toBe(1);const postsBeforeReload=posts;await page.reload();
 const recovery=page.locator('[data-canvas-recovery]');await expect(recovery).toBeVisible();await expect(page.locator('.canvas-card')).toHaveCount(0);expect(posts).toBe(postsBeforeReload);
 const download=page.waitForEvent('download');await recovery.locator('[data-recovery-download]').click();expect((await download).suggestedFilename()).toMatch(/\.json$/);
 await page.unroute('**/admin/canvases/*/document');await recovery.locator('[data-recovery-restore]').click();await expect(page.locator('.canvas-card')).toHaveCount(1);await expect(recovery).toBeHidden();
 expect((await(await page.request.get(path+'/document')).json()).cards[0].body).toContain('exact private writing');await page.screenshot({path:info.outputPath('canvas-recovered-en.png')});
});
test('connections undo and redo through ordinary versioned saves',async({page})=>{
 const path=await board(page),source=randomUUID(),target=randomUUID();
 const cards=[{id:source,kind:'note',title:'Connection source',body:'Private instructions',x:24,y:24},{id:target,kind:'provider',title:'Unconfigured destination',body:'',x:400,y:24}];
 expect((await page.request.post(path+'/document',{headers:{'X-CSRF-Token':await csrf(page)},data:{version:1,title:'Connection history',cards,edges:[]}})).status()).toBe(200);await page.reload();
 await page.locator('.canvas-connect-controls [name=edge_from]').selectOption(source);await page.locator('.canvas-connect-controls [name=edge_to]').selectOption(target);await page.locator('.canvas-connect-controls').getByRole('button',{name:'Connect',exact:true}).click();
 await expect(page.locator('.canvas-connection-list li')).toHaveCount(1);await page.locator('[data-action=undo]').click();await expect(page.locator('.canvas-connection-list li')).toHaveCount(0);
 await page.locator('[data-action=redo]').click();await expect(page.locator('.canvas-connection-list li')).toHaveCount(1);await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 expect((await(await page.request.get(path+'/document')).json()).edges).toEqual([{from:source,to:target}]);
 await page.locator('.canvas-connection-list').getByRole('button',{name:'Remove connection',exact:true}).click();await expect(page.locator('.canvas-connection-list li')).toHaveCount(0);await page.locator('[data-action=undo]').click();await expect(page.locator('.canvas-connection-list li')).toHaveCount(1);
});
test('remote version blocks undo and conflict recovery creates a safe personal copy',async({page})=>{
 const path=await board(page);await addNote(page);await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');const server=await(await page.request.get(path+'/document')).json();
 expect((await page.request.post(path+'/document',{headers:{'X-CSRF-Token':await csrf(page)},data:{version:server.version,title:'Remote authoritative title',cards:server.cards,edges:server.edges}})).status()).toBe(200);
 await page.locator('[data-action=undo]').click();await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','conflict');await expect(page.locator('.canvas-card')).toHaveCount(1);
 await page.locator('#canvas-title').fill('Unsent local title');await page.reload();const recovery=page.locator('[data-canvas-recovery]');await expect(recovery.locator('[data-recovery-copy]')).toBeVisible();
 await recovery.locator('[data-recovery-copy]').click();await expect(page).not.toHaveURL(new RegExp(path+'$'));await expect(page.locator('.canvas-card')).toHaveCount(1);expect((await(await page.request.get(path+'/document')).json()).title).toBe('Remote authoritative title');
});
for(const locale of ['ar','en'] as const)test('recovery choices fit desktop and phone · '+locale,async({page},info)=>{
 await board(page);await page.route('**/admin/canvases/*/document',route=>route.request().method()==='POST'?route.abort():route.continue());await addNote(page,'Recovery '+locale);await page.reload();await setLocale(page,locale);
 for(const width of [1280,390]){await page.setViewportSize({width,height:844});const region=page.locator('[data-canvas-recovery]');await expect(region).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);for(const button of await region.locator('button').all())expect((await button.boundingBox())!.height).toBeGreaterThanOrEqual(44);await region.scrollIntoViewIfNeeded();await page.screenshot({path:info.outputPath(`recovery-${locale}-${width}.png`)});}
 page.once('dialog',dialog=>dialog.accept());await page.locator('[data-recovery-discard]').click();await expect(page.locator('[data-canvas-recovery]')).toBeHidden();
});
