import { test, expect, type Page } from '@playwright/test';
import { db, setLocale } from '../lib/helpers.ts';

// Lanes is a read-only arrangement of the same board: positions are computed from each card's kind, never saved.
// Columns read from the inline start (right in ar, left in en): inputs, instructions, execution, results.

const NOTE='66666666-6666-4666-8666-000000000001';

async function lanesBoard(page: Page, locale: 'ar'|'en' = 'ar'): Promise<string> {
 await page.setViewportSize({width:1440,height:1000});
 await page.goto('/admin/canvases');await setLocale(page,locale);
 await page.locator('#new-canvas-title').fill(locale==='ar'?'لوحة المسارات':'Lanes board');
 await page.locator('form[action="/admin/canvases"] [type=submit]').click();
 await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]+$/);
 const path=new URL(page.url()).pathname,board=path.split('/').pop()!;
 await page.goto(path+'?preset=periodic-brief');
 const setup=page.locator('.canvas-preset-setup');await expect(setup).toBeVisible();
 await setup.locator('select').selectOption('agent');
 await setup.locator('button.canvas-primary').click();
 await expect(page.locator('[data-canvas-cards] .canvas-card')).toHaveCount(5);
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 // One agent-written note, attributed the way the run writer records it.
 const database=db();const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
 const row=database.prepare('SELECT document_json FROM admin_canvases WHERE id=?').get(board) as {document_json:string};
 const cards=JSON.parse(row.document_json);
 cards.push({id:NOTE,kind:'note',title:locale==='ar'?'موجز اليوم':'Today\'s brief',body:locale==='ar'?'ناتج الوكيل.':'Agent output.',x:24,y:1200});
 database.prepare('UPDATE admin_canvases SET document_json=? WHERE id=?').run(JSON.stringify(cards),board);
 database.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,result_card_id,created_at) VALUES(?,?,?,?,?,'succeeded',?,?,?)").run(board,board,owner.id,'lanes-note',NOTE,JSON.stringify({candidate:{input:{kind:'text'}}}),NOTE,new Date(Date.now()-3600000).toISOString());
 database.prepare("INSERT INTO admin_canvas_provenance(board_id,card_id,actor_kind,actor_name,run_id,created_at) VALUES(?,?,'api_key','Synthetic test agent',?,?)").run(board,NOTE,board,new Date().toISOString());database.close();
 await page.goto(path);await page.locator('[data-canvas-view=flow]').click();
 await expect(page.locator('[data-canvas-cards] .canvas-card')).toHaveCount(6);
 await expect(page.locator('#card-'+NOTE)).toHaveAttribute('data-note-source','attributed');
 return path;
}

const lanes=async(page:Page)=>{await page.locator('[data-canvas-view=lanes]').click();await expect(page.locator('.canvas-workspace')).toHaveAttribute('data-view','lanes');};
const left=(page:Page,selector:string)=>page.locator(selector).evaluate(el=>(el as HTMLElement).offsetLeft);
const bandStart=(page:Page,lane:number)=>page.locator(`.canvas-lane[data-lane="${lane}"]`).evaluate(el=>(el as HTMLElement).offsetLeft+parseFloat(getComputedStyle(el).paddingLeft));

test('columns hold the right kinds', async ({page})=>{
 await lanesBoard(page);
 await lanes(page);
 await expect(page.locator('.canvas-lane')).toHaveCount(4);
 await expect(page.locator('.canvas-lanes')).toHaveAttribute('aria-hidden','true');
 expect(await left(page,'.canvas-card[data-kind=agent]')).toBe(await bandStart(page,2));
 expect(await left(page,'#card-'+NOTE)).toBe(await bandStart(page,3));
 expect(await left(page,'.canvas-card[data-kind=prompt]')).toBe(await bandStart(page,1));
 expect(await left(page,'.canvas-card[data-kind=schedule]')).toBe(await bandStart(page,1));
 expect(await left(page,'.canvas-card[data-kind=source]')).toBe(await bandStart(page,0));
 expect(await left(page,'.canvas-card[data-kind=note]:not([data-note-source=attributed])')).toBe(await bandStart(page,0));
 // Headers carry the column number, name and count.
 await expect(page.locator('.canvas-lane[data-lane="1"] .canvas-lane-header')).toContainText('التعليمات والمشغّلات');
 await expect(page.locator('.canvas-lane[data-lane="3"] .canvas-lane-header')).toContainText('النتائج');
 // Cards keep their shapes in lanes: the prompt fold is still drawn.
 expect(await page.locator('.canvas-card[data-kind=prompt]').evaluate(el=>getComputedStyle(el,'::before').clipPath)).not.toBe('none');
 // Moving is off, as in grid and list.
 await expect(page.locator('.canvas-card .canvas-handle').first()).toBeHidden();
 await expect(page.locator('[data-action=arrange]')).toBeDisabled();
 // The schedules panel floats as in flow.
 expect(await page.locator('[data-canvas-schedules]').evaluate(el=>getComputedStyle(el).position)).toBe('absolute');
});

test('rtl mirrors en', async ({page})=>{
 const place=async()=>{
  const agent=await left(page,'.canvas-card[data-kind=agent]');
  const rtl=await page.evaluate(()=>getComputedStyle(document.querySelector('.canvas-workspace')!).direction==='rtl');
  const starts=await page.locator('.canvas-lane').evaluateAll(els=>els.map(el=>(el as HTMLElement).offsetLeft+parseFloat(getComputedStyle(el).paddingLeft)));
  const ordered=[...starts].sort((a,b)=>rtl?b-a:a-b);
  return {agent,index:ordered.indexOf(agent)};
 };
 await lanesBoard(page,'ar');await lanes(page);
 const ar=await place();
 await lanesBoard(page,'en');await lanes(page);
 await expect(page.locator('.canvas-lane[data-lane="0"] .canvas-lane-header')).toContainText('Inputs');
 const en=await place();
 await setLocale(page,'ar');
 expect(ar.index).toBe(2);expect(en.index).toBe(2);
 expect(en.agent).not.toBe(ar.agent);
});

test('lanes never saves', async ({page})=>{
 const path=await lanesBoard(page);
 const endpoint=path+'/document';
 const version=async()=>(await (await page.request.get(endpoint)).json()).version;
 const before=await version();
 let posts=0;page.on('request',r=>{if(r.method()==='POST'&&new URL(r.url()).pathname===endpoint)posts++;});
 await lanes(page);
 await page.locator('[data-canvas-view=grid]').click();
 await page.locator('[data-canvas-view=flow]').click();
 await page.waitForTimeout(1500);
 expect(posts).toBe(0);
 expect(await version()).toBe(before);
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
});

test('edges draw in lanes', async ({page})=>{
 await lanesBoard(page);
 const paths=page.locator('.canvas-edges path[data-from]');
 const flow=await paths.count();expect(flow).toBeGreaterThan(0);
 await lanes(page);
 await expect(paths).toHaveCount(flow);
 await expect(page.locator('[data-route-warning]')).toBeHidden();
});

test('mobile lanes is a list', async ({page})=>{
 await lanesBoard(page);
 await lanes(page);
 await page.setViewportSize({width:375,height:812});
 await expect(page.locator('.canvas-lane').first()).toBeHidden();
 const lane=(kind:string,source?:string)=>kind==='note'&&source==='attributed'?3:({prompt:1,schedule:1,agent:2,provider:2,result:3} as Record<string,number>)[kind]??0;
 const cards=await page.locator('[data-canvas-cards] .canvas-card').evaluateAll(els=>els.map(el=>({top:(el as HTMLElement).offsetTop,kind:(el as HTMLElement).dataset.kind!,source:(el as HTMLElement).dataset.noteSource})));
 const byTop=[...cards].sort((a,b)=>a.top-b.top);
 for(let i=1;i<byTop.length;i++)expect(byTop[i].top).toBeGreaterThan(byTop[i-1].top);
 const order=byTop.map(c=>lane(c.kind,c.source));
 expect(order).toEqual([...order].sort((a,b)=>a-b));
 expect(await page.locator('.canvas-edges path').count()).toBe(0);
});

test('screenshots', async ({page})=>{
 const shot=async(locale:'ar'|'en',file:string)=>{
  const path=await lanesBoard(page,locale),board=path.split('/').pop()!;
  const agent=page.locator('.canvas-card[data-kind=agent]');const node=(await agent.getAttribute('data-card-id'))!;
  const database=db();const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
  database.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at) VALUES(?,?,?,?,?,'running',?,?)").run(locale==='ar'?'77777777-7777-4777-8777-0000000000a1':'77777777-7777-4777-8777-0000000000a2',board,owner.id,'lanes-run-'+locale,node,JSON.stringify({candidate:{input:{kind:'text'}}}),new Date().toISOString());database.close();
  await page.reload();await lanes(page);
  await expect(agent).toHaveAttribute('data-state','running');
  const toggle=page.locator('[data-canvas-schedules] button[aria-expanded][aria-controls]');
  if(await toggle.getAttribute('aria-expanded')==='true')await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded','false');
  await page.mouse.move(2,2);
  await page.locator('.canvas-viewport').scrollIntoViewIfNeeded();
  await page.screenshot({path:'../.superpowers/sdd/2026-11-03-canvas-node-shapes/'+file,fullPage:true});
 };
 await shot('ar','task-7-lanes-ar.png');
 await shot('en','task-7-lanes-en.png');
 await setLocale(page,'ar');
});
