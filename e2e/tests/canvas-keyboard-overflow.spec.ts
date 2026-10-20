import {test,expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {csrf,setLocale} from '../lib/helpers.ts';

for(const locale of ['en','ar'] as const)test('short multiline non-note body supports real keyboard scrolling · '+locale,async({page})=>{
 await page.goto('/admin/canvases');await setLocale(page,locale);
 const created=await page.request.post('/admin/canvases',{form:{_csrf:await csrf(page),title:'Keyboard overflow fixture'},maxRedirects:0});expect(created.status()).toBe(303);const path=created.headers().location;
 try{
  await page.goto(path);const token=await csrf(page),body=Array.from({length:24},(_,i)=>(locale==='ar'?'سطر ':'Line ')+(i+1)).join('\n');expect(body.length).toBeLessThan(360);expect(body.split('\n').length).toBeGreaterThan(6);
  expect((await page.request.post(path+'/document',{headers:{'X-CSRF-Token':token},data:{version:1,title:'Keyboard overflow fixture',cards:[{id:randomUUID(),kind:'prompt',title:'Multiline instructions',body,x:24,y:24}],edges:[]}})).status()).toBe(200);
  await page.reload();const card=page.locator('[data-kind=prompt]'),region=card.locator('.canvas-card-body');await expect(card).toBeVisible();expect(await region.evaluate(el=>el.scrollHeight>el.clientHeight)).toBe(true);await expect(region).toHaveAttribute('role','region');await expect(region).toHaveAccessibleName('Multiline instructions');
  await card.locator('.canvas-handle').focus();await page.keyboard.press('Tab');await expect(region).toBeFocused();await page.keyboard.press('PageDown');await expect.poll(()=>region.evaluate(el=>el.scrollTop)).toBeGreaterThan(0);
  const saved=await(await page.request.get(path+'/document')).json();expect(saved.version).toBe(2);expect(saved.cards[0].body).toBe(body);
 }finally{await page.request.delete(path,{headers:{'X-CSRF-Token':await csrf(page)}});}
});
