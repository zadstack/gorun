import {test,expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {mkdirSync} from 'node:fs';
import {resolve} from 'node:path';
import {csrf,db,setLocale} from '../lib/helpers.ts';
const output=resolve('..','..','..','outputs','connections-table');
test('saved MCP AI S3 profiles share a safe detailed filterable table with explicit editing and no probes',async({page})=>{
 const database=db(),stamp=new Date().toISOString(),ids:string[]=[];let probes=0;
 page.on('request',r=>{if(/\/connections\/[^/]+\/test$|\/models\/discover$/.test(new URL(r.url()).pathname))probes++;});
 try{
 await page.goto('/admin/settings/connections');await setLocale(page,'en');
 for(const [kind,name]of [['mcp','Editorial archive'],['ai','Writing model'],['s3','Media archive']]){
  const before=(database.prepare("SELECT count(*) n FROM settings WHERE key LIKE 'connections.profile.%'").get() as any).n;
  const response=await page.request.post('/admin/settings/connections',{form:{_csrf:await csrf(page),kind,name,endpoint:kind==='s3'?'https://storage.example':'https://'+kind+'.example/private-capability',provider:'openai',model:kind==='ai'?'reviewed-model':'',bucket:kind==='s3'?'media-archive':'',region:'auto',token:kind==='s3'?'':'synthetic-test-token',access_key:kind==='s3'?'synthetic-access-key':'',secret_key:kind==='s3'?'synthetic-secret-key':''},maxRedirects:0});expect(response.status()).toBe(303);
  expect((database.prepare("SELECT count(*) n FROM settings WHERE key LIKE 'connections.profile.%'").get() as any).n).toBe(before+1);
  const settings=database.prepare("SELECT key,value_json FROM settings WHERE key LIKE 'connections.profile.%'").all() as any[];const row=settings.map(r=>({key:r.key,data:JSON.parse(r.value_json)})).find(r=>r.data.name===name)!;ids.push(row.data.id);
  // Synthetic stored historical test results: no provider probe is performed.
  if(kind!=='s3'){Object.assign(row.data,{test_status:kind==='mcp'?'pass':'fail',tested_at:stamp,test_count:kind==='mcp'?4:0,test_protocol:kind==='mcp'?'2025-11-25':''});database.prepare('UPDATE settings SET value_json=? WHERE key=?').run(JSON.stringify(row.data),row.key);}
 }
 await page.reload();const table=page.locator('[data-connections-table]');await expect(table.locator('tr[data-connection-id]')).toHaveCount(3);await expect(table).toContainText('mcp.example');await expect(table).toContainText('reviewed-model');await expect(table).toContainText('media-archive');expect(await table.innerHTML()).not.toContain('private-capability');expect(await table.innerHTML()).not.toContain('synthetic-test-token');expect(await table.innerHTML()).not.toContain('synthetic-access-key');
 await expect(page.locator('#connection-editor')).not.toHaveAttribute('open');await table.locator('[data-connection-type]').selectOption('ai');await expect(table.locator('tr[data-connection-id]:visible')).toHaveCount(1);await expect(table.locator('tr[data-connection-id]:visible')).toContainText('Writing model');await table.locator('[data-connection-reset]').click();
 await table.locator('[data-connection-status]').selectOption('pass');await expect(table.locator('tr[data-connection-id]:visible')).toHaveCount(1);await expect(table.locator('tr[data-connection-id]:visible')).toContainText('Editorial archive');await table.locator('[data-connection-search]').fill('no-match');await expect(table.locator('[data-connection-no-results]')).toBeVisible();await table.locator('[data-connection-reset]').click();
 await table.locator('[data-connection-columns]>summary').click();await table.locator('[data-connection-column=storage]').check();await table.locator('[data-connection-column=protocol]').check();await table.locator('[data-connection-column=identity]').check();await expect(table.locator('th[data-connection-cell=identity]')).toBeVisible();await expect(table.locator('tbody [data-connection-cell=protocol]').first()).toContainText('2025-11-25');
 expect(await table.locator('tr[data-connection-id]').evaluateAll(rows=>Math.max(...rows.map(row=>row.getBoundingClientRect().height)))).toBeLessThan(150);
 mkdirSync(output,{recursive:true});await page.screenshot({path:resolve(output,'connections-en.png'),fullPage:true});
 await table.locator('tr[data-connection-id]').filter({hasText:'Writing model'}).getByRole('link',{name:'Edit',exact:true}).click();await expect(page.locator('#connection-editor')).toHaveAttribute('open');await expect(page.locator('[name=endpoint]')).toHaveValue('https://ai.example/private-capability');await expect(page.locator('[name=token]')).toHaveValue('');
 const version=await page.locator('[data-connection-setup] [name=version]').inputValue();await page.locator('[name=name]').fill('Writing model revised');const saved=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname==='/admin/settings/connections');await page.locator('[data-connection-setup] button[type=submit]').click();expect((await saved).status()).toBe(303);await expect(page.locator('[data-connections-table]')).toContainText('Writing model revised');const edited=JSON.parse((database.prepare('SELECT value_json FROM settings WHERE key=?').get('connections.profile.'+ids[1]) as any).value_json);expect(edited.version).not.toBe(version);expect(edited.test_status||'').toBe('');expect(edited.endpoint).toBe('https://ai.example/private-capability');
 await page.setViewportSize({width:390,height:844});await setLocale(page,'ar');expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);expect(await page.locator('.connections-table-scroll').evaluate(el=>el.scrollWidth>el.clientWidth)).toBe(true);await page.screenshot({path:resolve(output,'connections-ar-mobile.png'),fullPage:true});
 expect(probes).toBe(0);
 }finally{for(const id of ids)database.prepare('DELETE FROM settings WHERE key=?').run('connections.profile.'+id);database.close();}
});
