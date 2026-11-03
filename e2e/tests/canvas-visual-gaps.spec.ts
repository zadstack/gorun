import { test, expect, type Page } from '@playwright/test';
import { db, setLocale } from '../lib/helpers.ts';

// Visual follow-ups to the node shapes: state-aware result seals, unclipped decoration, lanes that fit the viewport,
// a turning ring and writing skeleton on running agents, failure lines, chips, next-run times, the edge legend and
// the scheduled-edge clock. All are client-side views of data the board already loads.

async function presetBoard(page: Page, width = 1440): Promise<{path: string, board: string}> {
 await page.setViewportSize({width,height:1000});
 await page.goto('/admin/canvases');await setLocale(page,'ar');
 await page.locator('#new-canvas-title').fill('لوحة التفاصيل');
 await page.locator('form[action="/admin/canvases"] [type=submit]').click();
 await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]+$/);
 const path=new URL(page.url()).pathname;
 await page.goto(path+'?preset=periodic-brief');
 const setup=page.locator('.canvas-preset-setup');await expect(setup).toBeVisible();
 await setup.locator('select').selectOption('agent');
 await setup.locator('button.canvas-primary').click();
 await expect(page.locator('[data-canvas-cards] .canvas-card')).toHaveCount(5);
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 await page.locator('[data-canvas-view=flow]').click();
 return {path,board:path.split('/').pop()!};
}
const owner=()=>{const d=db();const o=d.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};d.close();return o.id;};
const cardID=(page:Page,kind:string)=>page.locator(`.canvas-card[data-kind=${kind}]`).first().getAttribute('data-card-id') as Promise<string>;
async function insertRun(board:string,node:string,status:string,id:string){
 const d=db();d.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at) VALUES(?,?,?,?,?,?,?,?)").run(id,board,owner(),'gaps-'+id,node,status,JSON.stringify({candidate:{input:{kind:'text'}}}),new Date(Date.now()-5000).toISOString());d.close();
}
async function enableSchedule(board:string,schedule:string,agent:string){
 const d=db();d.prepare("INSERT INTO admin_canvas_schedules(board_id,node_id,provider_node_id,interval,consent_hash,enabled,next_run_at,status) VALUES(?,?,?,'daily','e2e-hash',1,?,'enabled')").run(board,schedule,agent,new Date(Date.now()+42*60_000).toISOString());d.close();
}
const reopen=async(page:Page,path:string,view='flow')=>{await page.goto(path);await page.locator(`[data-canvas-view=${view}]`).click();await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');};

test('a result without media gets a red seal and a placeholder, not a green check', async ({page})=>{
 const {path,board}=await presetBoard(page);
 const d=db();const row=d.prepare('SELECT document_json FROM admin_canvases WHERE id=?').get(board) as {document_json:string};
 const cards=JSON.parse(row.document_json);cards.push({id:'55555555-5555-4555-8555-000000000001',kind:'result',title:'غلاف المقال',body:'',run_id:board,x:1056,y:24});
 d.prepare('UPDATE admin_canvases SET document_json=? WHERE id=?').run(JSON.stringify(cards),board);d.close();
 await reopen(page,path);
 const result=page.locator('.canvas-card[data-kind=result]');
 await expect(result.locator('.canvas-card-body')).toHaveAttribute('data-result','missing');
 await expect(result.locator('.canvas-result-missing')).toBeVisible();
 expect(await result.evaluate(el=>getComputedStyle(el,'::before').backgroundColor)).toBe('rgb(163, 41, 45)');
});

test('decoration above a card at the board origin is not clipped', async ({page})=>{
 const {path,board}=await presetBoard(page);
 const agent=await cardID(page,'agent');
 const d=db();const row=d.prepare('SELECT document_json FROM admin_canvases WHERE id=?').get(board) as {document_json:string};
 const cards=JSON.parse(row.document_json).map((c:{id:string,x:number,y:number})=>c.id===agent?{...c,x:0,y:0}:{...c,x:c.x+360,y:c.y+120});
 d.prepare('UPDATE admin_canvases SET document_json=? WHERE id=?').run(JSON.stringify(cards),board);d.close();
 await reopen(page,path);
 const gap=await page.locator(`#card-${agent}`).evaluate(el=>el.getBoundingClientRect().top-el.closest('.canvas-viewport')!.getBoundingClientRect().top);
 // The avatar rises 21px above the card's 3px top border.
 expect(gap).toBeGreaterThanOrEqual(22);
});

test('footer buttons stay inside rounded corners', async ({page})=>{
 await presetBoard(page);
 const pad=await page.locator('.canvas-card[data-kind=agent] .canvas-card-footer').evaluate(el=>parseFloat(getComputedStyle(el).paddingInlineStart));
 expect(pad).toBeGreaterThanOrEqual(8);
});

test('lanes fit four columns without sideways scrolling at 1440px', async ({page})=>{
 await presetBoard(page,1440);
 await page.locator('[data-canvas-view=lanes]').click();
 await expect(page.locator('.canvas-lane')).toHaveCount(4);
 const viewport=page.locator('.canvas-viewport');
 await expect.poll(()=>viewport.evaluate(el=>el.scrollWidth-el.clientWidth)).toBeLessThanOrEqual(0);
 const width=await page.locator('.canvas-card').first().evaluate(el=>(el as HTMLElement).offsetWidth);
 expect(width).toBeGreaterThanOrEqual(240);expect(width).toBeLessThanOrEqual(300);
});

test('on narrower screens lane cards stop at 240px and the board scrolls', async ({page})=>{
 await presetBoard(page,1280);
 await page.locator('[data-canvas-view=lanes]').click();
 await expect.poll(()=>page.locator('.canvas-card').first().evaluate(el=>(el as HTMLElement).offsetWidth)).toBe(240);
});

test('a running agent turns its ring, shows a writing line and counts in the toolbar', async ({page})=>{
 const {path,board}=await presetBoard(page);
 const agent=await cardID(page,'agent');
 await insertRun(board,agent,'running','44444444-4444-4444-8444-000000000001');
 await reopen(page,path);
 const card=page.locator(`#card-${agent}`);
 await expect(card).toHaveAttribute('data-state','running');
 expect(await card.locator('.canvas-card-heading').evaluate(el=>getComputedStyle(el,'::after').animationName)).toBe('canvas-agent-ring');
 await expect(card.locator('.canvas-node-extra[data-kind=writing]')).toContainText('يكتب الناتج الآن');
 await expect(page.locator('[data-run-count]')).toBeVisible();
 await expect(page.locator('[data-run-count]')).toHaveText(/^قيد التشغيل الآن: /);
 await page.emulateMedia({reducedMotion:'reduce'});
 expect(await card.locator('.canvas-card-heading').evaluate(el=>getComputedStyle(el,'::after').animationName)).toBe('none');
});

test('a failed node says why under its heading; no run hides the counter', async ({page})=>{
 const {path,board}=await presetBoard(page);
 const agent=await cardID(page,'agent');
 await expect(page.locator('[data-run-count]')).toBeHidden();
 await insertRun(board,agent,'failed','44444444-4444-4444-8444-000000000002');
 await reopen(page,path);
 const line=page.locator(`#card-${agent} .canvas-node-extra[data-kind=failed]`);
 await expect(line).toBeVisible();
 expect((await line.textContent())!.trim()).not.toBe('');
 await expect(page.locator(`#card-${agent} .canvas-node-extra[data-kind=writing]`)).toHaveCount(0);
 await expect(page.locator('[data-run-count]')).toBeHidden();
});

test('chips, next run, legend and the scheduled-edge clock', async ({page})=>{
 const {path,board}=await presetBoard(page);
 const agent=await cardID(page,'agent'),schedule=await cardID(page,'schedule');
 const chips=page.locator(`#card-${agent} .canvas-node-chips li`);
 await expect(chips.first()).toHaveText(/^المدخلات: /);
 await expect(chips).toHaveCount(1);
 await expect(page.locator('.canvas-legend')).toBeVisible();
 await expect(page.locator('.canvas-legend li')).toHaveCount(4);
 await enableSchedule(board,schedule,agent);
 await reopen(page,path);
 await expect(page.locator(`#card-${agent} .canvas-node-chips li[data-chip=schedule]`)).toHaveText('مجدول · يوميًا');
 const next=page.locator(`#card-${schedule} time.canvas-schedule-next`);
 await expect(next).toHaveText(/^التالي .+ · .+/);
 expect(await next.getAttribute('title')).not.toBe('');
 await expect(page.locator(`.canvas-edges path[data-from="${schedule}"]`).first()).toHaveAttribute('data-state','scheduled');
 await expect(page.locator('.canvas-edges g.canvas-edge-clock')).toHaveCount(1);
 await page.locator('[data-canvas-view=grid]').click();
 await expect(page.locator('.canvas-legend')).toBeHidden();
 await page.screenshot({path:'test-results/visual-gaps-grid.png'});
});

test('screenshots for review', async ({page})=>{
 const {path,board}=await presetBoard(page);
 const agent=await cardID(page,'agent'),schedule=await cardID(page,'schedule');
 await enableSchedule(board,schedule,agent);
 await insertRun(board,agent,'running','44444444-4444-4444-8444-000000000003');
 await reopen(page,path);
 await page.screenshot({path:'test-results/visual-gaps-flow.png'});
 await page.locator('[data-canvas-view=lanes]').click();
 await page.screenshot({path:'test-results/visual-gaps-lanes.png'});
});
