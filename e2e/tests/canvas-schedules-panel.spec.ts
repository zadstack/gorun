import { test, expect, type Page } from '@playwright/test';
import { db, setLocale } from '../lib/helpers.ts';

// The schedules panel lists this board's schedule nodes, grouped by their target, from data the editor
// already loaded. It reuses the node's own enable flow and the #card-<id> reveal; there is no new write path.

async function newBoard(page: Page, title: string): Promise<string> {
 await page.setViewportSize({width:1440,height:1000});
 await page.goto('/admin/canvases');await setLocale(page,'ar');
 await page.locator('#new-canvas-title').fill(title);
 await page.locator('form[action="/admin/canvases"] [type=submit]').click();
 await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]+$/);
 return new URL(page.url()).pathname;
}

async function presetBoard(page: Page): Promise<string> {
 const path=await newBoard(page,'لوحة الجداول');
 await page.goto(path+'?preset=periodic-brief');
 const setup=page.locator('.canvas-preset-setup');await expect(setup).toBeVisible();
 await setup.locator('select').selectOption('agent');
 await setup.locator('button.canvas-primary').click();
 await expect(page.locator('[data-canvas-cards] .canvas-card')).toHaveCount(5);
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 await page.locator('[data-canvas-view=flow]').click();
 return path;
}

const panel=(page:Page)=>page.locator('[data-canvas-schedules]');
const toggle=(page:Page)=>panel(page).locator('button[aria-expanded][aria-controls]');
const SCHEDULE='ملخص يومي اختياري', AGENT='اختيار الوكيل';

test('lists the schedule under its agent', async ({page})=>{
 await presetBoard(page);
 await expect(panel(page)).toBeVisible();
 await expect(toggle(page)).toHaveAttribute('aria-expanded','true');
 const groups=panel(page).locator('.canvas-schedules-group');
 await expect(groups).toHaveCount(1);
 await expect(groups.locator('h3')).toContainText(AGENT);
 await expect(groups.locator('h3')).toContainText('وكيل مسمّى'); // the kind is written as text
 const row=groups.locator('li');
 await expect(row).toHaveCount(1);
 await expect(row).toContainText(SCHEDULE);
 await expect(row).toContainText('يوميًا');
 await expect(row.locator('.canvas-schedules-chip')).toHaveText('متوقف');
 await expect(row.locator('bdi').first()).toHaveText(SCHEDULE);
 await expect(toggle(page)).toContainText('المهام المجدولة');
});

test('no panel without schedules', async ({page})=>{
 await newBoard(page,'لوحة فارغة');
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 await expect(panel(page)).toHaveCount(1);
 await expect(panel(page)).toBeHidden();
});

test('collapse persists and Escape returns focus', async ({page})=>{
 await presetBoard(page);
 const region=page.locator('#'+await toggle(page).getAttribute('aria-controls'));
 await expect(region).toBeVisible();
 await toggle(page).click();
 await expect(toggle(page)).toHaveAttribute('aria-expanded','false');
 await expect(region).toBeHidden();
 await page.reload();
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 await expect(toggle(page)).toHaveAttribute('aria-expanded','false');
 await expect(region).toBeHidden();
 await toggle(page).click();
 await expect(toggle(page)).toHaveAttribute('aria-expanded','true');
 await region.getByRole('button',{name:'اعرض على اللوح'}).focus();
 await page.keyboard.press('Escape');
 await expect(toggle(page)).toBeFocused();
 await expect(toggle(page)).toHaveAttribute('aria-expanded','false');
 await expect(region).toBeHidden();
});

async function unobscured(page:Page, target:ReturnType<Page['locator']>){
 await expect.poll(()=>target.evaluate(el=>{const r=el.getBoundingClientRect(),hit=document.elementFromPoint(r.left+r.width/2,r.top+r.height/2);return !!hit&&(hit===el||el.contains(hit));})).toBe(true);
}

for(const locale of ['ar','en'] as const){
 test(`show on board focuses the node · ${locale}`, async ({page})=>{
  await presetBoard(page);
  if(locale==='en'){await setLocale(page,'en');await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');}
  const show=panel(page).getByRole('button',{name:locale==='ar'?'اعرض على اللوح':'Show on board'});
  const handle=page.locator('.canvas-card[data-kind=schedule] .canvas-handle');
  // Move the node out of sight first, so the reveal has work to do.
  await page.locator('.canvas-viewport').evaluate(el=>{el.scrollTop=el.scrollHeight;});
  await page.evaluate(()=>window.scrollTo(0,document.documentElement.scrollHeight));
  await expect(handle).not.toBeInViewport();
  await show.click();
  await expect(handle).toBeFocused();
  await expect(handle).toBeInViewport();
  await unobscured(page,handle);
  // The floating panel folds so it cannot cover the node it revealed.
  await expect(toggle(page)).toHaveAttribute('aria-expanded','false');
  await expect(page).toHaveURL(/#card-[0-9a-f-]{36}$/);
  // A second reveal of the same node still works.
  await toggle(page).click();
  await show.click();
  await expect(handle).toBeFocused();
  await unobscured(page,handle);
 });
}

test('show on board focuses the node in lanes', async ({page})=>{
 await presetBoard(page);
 await page.locator('[data-canvas-view=lanes]').click();
 await expect(page.locator('.canvas-workspace')).toHaveAttribute('data-view','lanes');
 if(await toggle(page).getAttribute('aria-expanded')==='false')await toggle(page).click();
 const card=page.locator('.canvas-card[data-kind=schedule]');
 await page.locator('.canvas-viewport').evaluate(el=>{el.scrollTop=el.scrollHeight;});
 await page.evaluate(()=>window.scrollTo(0,document.documentElement.scrollHeight));
 await panel(page).getByRole('button',{name:'اعرض على اللوح'}).click();
 // Focus lands on a rendered control inside the node (the move handle is hidden in lanes), never on body.
 await expect.poll(()=>card.evaluate(el=>{const a=document.activeElement as HTMLElement|null;return !!a&&a!==document.body&&el.contains(a)&&a.getClientRects().length>0;})).toBe(true);
 const focused=page.locator(':focus');
 await expect(focused).toBeInViewport();
 await unobscured(page,focused);
});

test('show on board keeps the docked panel open', async ({page})=>{
 await presetBoard(page);
 await page.setViewportSize({width:375,height:812});
 await expect.poll(()=>panel(page).evaluate(el=>getComputedStyle(el).position)).toBe('static');
 await panel(page).getByRole('button',{name:'اعرض على اللوح'}).click();
 const handle=page.locator('.canvas-card[data-kind=schedule] .canvas-handle');
 await expect(handle).toBeFocused();
 await unobscured(page,handle);
 await expect(toggle(page)).toHaveAttribute('aria-expanded','true');
});

async function outcome(page:Page){
 const dialog=page.locator('.canvas-review[open]'),message=page.locator('.canvas-ops-message');
 await expect.poll(async()=>(await dialog.count())>0||((await message.textContent())||'').trim()!=='').toBe(true);
 await expect(page.locator('[data-canvas-operations]')).not.toHaveAttribute('aria-busy','true');
 const open=(await dialog.count())>0;
 const text=open?'':((await message.textContent())||'').trim();
 if(open){await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0);}
 await message.evaluate(el=>{el.textContent='';});
 return {open,text};
}

test('needs_review row offers review', async ({page})=>{
 const path=await presetBoard(page),board=path.split('/').pop()!;
 const schedule=(await page.locator('.canvas-card[data-kind=schedule]').getAttribute('data-card-id'))!;
 const agent=(await page.locator('.canvas-card[data-kind=agent]').getAttribute('data-card-id'))!;
 const database=db();
 database.prepare("INSERT INTO admin_canvas_schedules(board_id,node_id,provider_node_id,interval,consent_hash,enabled,next_run_at,status) VALUES(?,?,?,'daily','e2e-hash',0,?,'needs_review')").run(board,schedule,agent,new Date(Date.now()+3600_000).toISOString());
 database.close();
 await page.reload();
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 const row=panel(page).locator('li').filter({hasText:SCHEDULE});
 await expect(row.locator('.canvas-schedules-chip')).toHaveText('يلزم المراجعة');
 const review=row.getByRole('button',{name:'راجع وفعّل'});
 await expect(review).toBeVisible();
 // Wait for the operations options so both clicks see the same state.
 await expect(page.locator('.canvas-card[data-kind=agent] .canvas-operation-button')).toBeVisible();
 await review.click();
 const fromPanel=await outcome(page);
 await page.locator('.canvas-card[data-kind=schedule]').hover();
 await page.locator('.canvas-card[data-kind=schedule] .canvas-card-body').getByRole('button',{name:'تفعيل الجدول'}).click();
 const fromNode=await outcome(page);
 expect(fromPanel).toEqual(fromNode);
});

test('unconnected schedule', async ({page})=>{
 await presetBoard(page);
 const edge=page.locator('.canvas-connection-list li').filter({hasText:SCHEDULE});
 await expect(edge).toHaveCount(1);
 await edge.getByRole('button',{name:'إزالة الرابط'}).click();
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 const groups=panel(page).locator('.canvas-schedules-group');
 await expect(groups).toHaveCount(1);
 await expect(groups.locator('h3')).toHaveText('غير موصول');
 await expect(groups.locator('li')).toContainText(SCHEDULE);
});

test('panel has no horizontal overflow', async ({page})=>{
 await presetBoard(page);
 const long='وكيلLongName'.repeat(7)+'وكيل';
 expect(new TextEncoder().encode(long).length).toBe(120);
 const card=page.locator('.canvas-card[data-kind=agent]');
 await card.locator('.canvas-card-heading h2').hover();
 await card.locator('.canvas-card-actions button').first().click();
 await page.locator('#canvas-card-form [name=title]').fill(long);
 await page.locator('#canvas-card-form [type=submit]').click();
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 await expect(panel(page).locator('h3')).toContainText(long);
 for(const width of [1280,375]){
  await page.setViewportSize({width,height:900});
  await expect(panel(page)).toBeVisible();
  const m=await panel(page).evaluate(el=>{const region=el.querySelector('[role=region]')!;return {sw:el.scrollWidth,cw:el.clientWidth,rsw:region.scrollWidth,rcw:region.clientWidth,right:el.getBoundingClientRect().right,left:el.getBoundingClientRect().left,vw:document.documentElement.clientWidth};});
  expect(m.sw,`${width}px panel`).toBeLessThanOrEqual(m.cw);
  expect(m.rsw,`${width}px region`).toBeLessThanOrEqual(m.rcw);
  expect(m.right,`${width}px right edge`).toBeLessThanOrEqual(m.vw);
  expect(m.left,`${width}px left edge`).toBeGreaterThanOrEqual(0);
 }
});

test('floats in desktop flow and docks elsewhere', async ({page})=>{
 await presetBoard(page);
 const stage=page.locator('.canvas-stage');
 await expect(stage.locator('> [data-canvas-schedules]')).toHaveCount(1);
 await expect(stage.locator('> .canvas-viewport')).toHaveCount(1);
 expect(await panel(page).evaluate(el=>getComputedStyle(el).position)).toBe('absolute');
 await page.locator('[data-canvas-view=grid]').click();
 expect(await panel(page).evaluate(el=>getComputedStyle(el).position)).toBe('static');
 await page.locator('[data-canvas-view=flow]').click();
 await page.setViewportSize({width:375,height:812});
 await expect.poll(()=>panel(page).evaluate(el=>getComputedStyle(el).position)).toBe('static');
});

test('en locale', async ({page})=>{
 await presetBoard(page);
 await setLocale(page,'en');
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 await expect(toggle(page)).toHaveText('Scheduled tasks · 1');
 await expect(panel(page).getByRole('button',{name:'Show on board'})).toBeVisible();
});
