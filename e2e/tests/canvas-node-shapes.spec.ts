import { test, expect, type Page } from '@playwright/test';
import { db, setLocale } from '../lib/helpers.ts';

// Flow-view node shapes are decoration on top of the unchanged card DOM: the kind label stays text,
// badges are pseudo-elements, and the footer becomes a hover/focus toolbar on desktop only.

async function newBoard(page: Page, title: string): Promise<string> {
 await page.setViewportSize({width:1440,height:1000});
 await page.goto('/admin/canvases');await setLocale(page,'ar');
 await page.locator('#new-canvas-title').fill(title);
 await page.locator('form[action="/admin/canvases"] [type=submit]').click();
 await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]+$/);
 return new URL(page.url()).pathname;
}

async function presetBoard(page: Page): Promise<string> {
 const path=await newBoard(page,'لوحة الأشكال');
 await page.goto(path+'?preset=periodic-brief');
 const setup=page.locator('.canvas-preset-setup');await expect(setup).toBeVisible();
 await setup.locator('select').selectOption('agent');
 await setup.locator('button.canvas-primary').click();
 await expect(page.locator('[data-canvas-cards] .canvas-card')).toHaveCount(5);
 await expect(page.locator('.canvas-state')).toHaveAttribute('data-state','saved');
 await page.locator('[data-canvas-view=flow]').click();
 return path;
}

// Every kind on one board, written straight to the fixture database (read-only in this test).
async function allKindsBoard(page: Page): Promise<string> {
 const path=await newBoard(page,'كل الأنواع'), board=path.split('/').pop()!;
 const id=(n:number)=>'88888888-8888-4888-8888-'+String(n).padStart(12,'0');
 const at=(n:number)=>({x:24+(n%4)*336,y:24+Math.floor(n/4)*480});
 const cards=[
  {kind:'source',title:'مصدر',source_type:'',source_limit:3},
  {kind:'content',title:'محتوى',content_id:''},
  {kind:'prompt',title:'تعليمات',body:'لخّص المحتوى.'},
  {kind:'note',title:'ملاحظة',body:'سياق حر.'},
  {kind:'checklist',title:'قائمة',items:[{text:'أولًا',checked:true},{text:'ثانيًا',checked:false}]},
  {kind:'link',title:'رابط',url:'https://example.test/'},
  {kind:'widget',title:'ودجت',widget_id:'content-summary'},
  {kind:'agent',title:'وكيل',agent_id:''},
  {kind:'provider',title:'مزوّد',provider_id:'',generation_kind:'image'},
  {kind:'schedule',title:'جدولة',interval:'daily'},
  {kind:'result',title:'نتيجة',run_id:board,body:'ناتج.'},
 ].map((c,n)=>({id:id(n+1),body:'',...c,...at(n)}));
 const database=db();const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
 database.prepare('UPDATE admin_canvases SET document_json=?,edges_json=? WHERE id=?').run(JSON.stringify(cards),'[]',board);
 database.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,result_card_id,created_at) VALUES(?,?,?,?,?,'succeeded',?,?,?)").run(board,board,owner.id,'node-shapes-result',id(8),JSON.stringify({candidate:{input:{kind:'text'}}}),id(11),new Date().toISOString());
 database.close();
 await page.goto(path);await page.locator('[data-canvas-view=flow]').click();
 await expect(page.locator('[data-canvas-cards] .canvas-card')).toHaveCount(11);
 return path;
}

const shape=(el:Element)=>{
 const s=getComputedStyle(el),b=getComputedStyle(el,'::before'),a=getComputedStyle(el,'::after'),h=el.querySelector('.canvas-card-heading')!,hb=getComputedStyle(h,'::before'),hs=getComputedStyle(h);
 return [s.borderRadius,s.borderInlineStartWidth,b.content,b.clipPath,b.boxShadow,a.content,hs.borderRadius,hb.content,hb.borderRadius,hb.backgroundImage].join('|');
};

test('every kind keeps a text label', async ({page})=>{
 await presetBoard(page);
 for(const card of await page.locator('[data-canvas-cards] .canvas-card').all()){
  const label=card.locator('.canvas-card-heading small');await expect(label).toBeVisible();expect((await label.textContent())!.trim()).not.toBe('');
 }
 await allKindsBoard(page);
 const cards=page.locator('[data-canvas-cards] .canvas-card');
 for(const card of await cards.all()){
  const label=card.locator('.canvas-card-heading small');await expect(label).toBeVisible();expect((await label.textContent())!.trim()).not.toBe('');
 }
 // Shape, not just hue: each of the 11 kinds has its own geometry.
 const signatures=await cards.evaluateAll((els,fn)=>els.map(el=>(new Function('return '+fn)())(el)),shape.toString());
 expect(new Set(signatures).size).toBe(11);
});

test('kinds differ by shape', async ({page})=>{
 await presetBoard(page);
 const radius=(kind:string)=>page.locator(`.canvas-card[data-kind=${kind}]`).evaluate(el=>getComputedStyle(el).borderRadius);
 expect(await radius('agent')).not.toBe(await radius('source'));
 const prompt=page.locator('.canvas-card[data-kind=prompt]');
 expect(await prompt.evaluate(el=>getComputedStyle(el,'::before').clipPath)).not.toBe('none');
 expect(await prompt.evaluate(el=>getComputedStyle(el).clipPath)).toBe('none');
 const clock=page.locator('.canvas-card[data-kind=schedule] .canvas-card-heading');
 expect(await clock.evaluate(el=>getComputedStyle(el,'::before').borderRadius)).toBe('50%');
 // Grid view keeps today's look.
 await page.locator('[data-canvas-view=grid]').click();
 expect(await prompt.evaluate(el=>getComputedStyle(el,'::before').content)).toBe('none');
 expect(await radius('agent')).toBe(await radius('source'));
});

test('toolbar visible on focus', async ({page})=>{
 await presetBoard(page);
 const top=page.locator('.canvas-card[data-kind=source]');
 await expect(top).toHaveAttribute('style',/top:\s*24px/);
 const footer=top.locator('.canvas-card-footer');
 await page.mouse.move(2,2);
 await expect.poll(()=>footer.evaluate(el=>getComputedStyle(el).opacity)).toBe('0');
 const edit=footer.locator('.canvas-card-actions button').first();
 await top.locator('.canvas-handle').focus();
 for(let i=0;i<10&&!(await edit.evaluate(el=>el===document.activeElement));i++)await page.keyboard.press('Tab');
 await expect(edit).toBeFocused();
 await expect.poll(()=>footer.evaluate(el=>getComputedStyle(el).opacity)).toBe('1');
 const box=(await footer.boundingBox())!,surface=(await page.locator('.canvas-surface').boundingBox())!;
 expect(box.y).toBeGreaterThanOrEqual(surface.y);expect(box.x).toBeGreaterThanOrEqual(surface.x);
 expect(box.y+box.height).toBeLessThanOrEqual(surface.y+surface.height);expect(box.x+box.width).toBeLessThanOrEqual(surface.x+surface.width);
 // The toolbar never covers content: it stays in flow below the body.
 const body=(await top.locator('.canvas-card-body').boundingBox())!;
 expect(box.y).toBeGreaterThanOrEqual(body.y+body.height-0.5);
});

test('grid view keeps the footer', async ({page})=>{
 await presetBoard(page);
 await page.locator('[data-canvas-view=grid]').click();
 await expect(page.locator('.canvas-workspace')).toHaveAttribute('data-view','grid');
 await page.mouse.move(2,2);
 for(const footer of await page.locator('.canvas-card-footer').all())expect(await footer.evaluate(el=>getComputedStyle(el).opacity)).toBe('1');
});

test('a wrapped note toolbar never covers the body or takes clicks at rest', async ({page})=>{
 const path=await newBoard(page,'ملاحظة منسوبة'),board=path.split('/').pop()!;
 const note='99999999-9999-4999-8999-999999999991';
 const database=db();const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
 database.prepare('UPDATE admin_canvases SET document_json=?,edges_json=? WHERE id=?').run(JSON.stringify([{id:note,kind:'note',title:'مسودة خطة الحملة',body:'خطة إطلاق عملية.\nراجِعها قبل النشر.\n'+Array(12).fill('سطر إضافي طويل لتنظيم الحملة واختيار المصادر.').join('\n'),x:24,y:24}]),'[]',board);
 database.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,result_card_id,created_at) VALUES(?,?,?,?,?,'succeeded',?,?,?)").run(board,board,owner.id,'node-shapes-note',note,JSON.stringify({candidate:{input:{kind:'text'}}}),note,new Date().toISOString());
 database.prepare("INSERT INTO admin_canvas_provenance(board_id,card_id,actor_kind,actor_name,run_id,created_at) VALUES(?,?,'api_key','وكيل تجريبي',?,?)").run(board,note,board,new Date().toISOString());database.close();
 await page.goto(path);await page.locator('[data-canvas-view=flow]').click();
 const card=page.locator('#card-'+note);await expect(card).toHaveAttribute('data-note-source','attributed');
 const footer=card.locator('.canvas-card-footer');
 await expect(footer.locator('button')).toHaveCount(7); // edit, create draft, remove, four moves
 await page.mouse.move(2,2);
 await expect.poll(()=>footer.evaluate(el=>getComputedStyle(el).opacity)).toBe('0');
 // Wrapped or not, the footer sits below the body's last child.
 const f=(await footer.boundingBox())!;
 const last=(await card.locator('.canvas-card-body > :last-child').boundingBox())!,body=(await card.locator('.canvas-card-body').boundingBox())!;
 expect(f.y).toBeGreaterThanOrEqual(last.y+last.height-0.5);expect(f.y).toBeGreaterThanOrEqual(body.y+body.height-0.5);
 // At rest the hidden toolbar takes no clicks anywhere in its zone.
 const hits=await footer.evaluate(el=>{const r=el.getBoundingClientRect(),out:string[]=[];
  for(const fy of [.25,.5,.75])for(let x=r.left+4;x<r.right-4;x+=12){const hit=document.elementFromPoint(x,r.top+r.height*fy);if(hit&&el.contains(hit))out.push(hit.outerHTML.slice(0,60));}
  return out;});
 expect(hits).toEqual([]);
 // Hovering the card shows it and makes it clickable again.
 await card.locator('.canvas-card-heading h2').hover();
 await expect.poll(()=>footer.evaluate(el=>getComputedStyle(el).opacity)).toBe('1');
 const center=await footer.locator('button[data-remove]').evaluate(el=>{const r=el.getBoundingClientRect();return document.elementFromPoint(r.left+r.width/2,r.top+r.height/2)===el;});
 expect(center).toBe(true);
});

test('running agent shows its state', async ({page})=>{
 const path=await presetBoard(page),board=path.split('/').pop()!;
 const agent=page.locator('.canvas-card[data-kind=agent]');const node=(await agent.getAttribute('data-card-id'))!;
 const database=db();const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
 const run='77777777-7777-4777-8777-000000000001';
 database.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at) VALUES(?,?,?,?,?,'running',?,?)").run(run,board,owner.id,'node-state-run',node,JSON.stringify({candidate:{input:{kind:'text'}}}),new Date(Date.now()-65000).toISOString());database.close();
 await page.reload();await page.locator('[data-canvas-view=flow]').click();
 await expect(agent).toHaveAttribute('data-state','running');
 await expect(agent.locator('.canvas-node-state')).toHaveText(/^قيد التشغيل · \d+:\d{2}/);
 await expect(page.locator('.canvas-state-live')).toHaveAttribute('aria-live','polite');
 await page.screenshot({path:'../.superpowers/sdd/2026-11-03-canvas-node-shapes/task-3-flow.png'});
 const d2=db();d2.prepare("UPDATE admin_canvas_runs SET status='failed' WHERE id=?").run(run);d2.close();
 await page.reload();await page.locator('[data-canvas-view=flow]').click();
 await expect(agent).toHaveAttribute('data-state','failed');
 await expect(agent.locator('.canvas-node-state')).toHaveText('فشل');
});

test('waiting state offers the existing check action', async ({page})=>{
 const path=await presetBoard(page),board=path.split('/').pop()!;
 const agent=page.locator('.canvas-card[data-kind=agent]');const node=(await agent.getAttribute('data-card-id'))!;
 const database=db();const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
 database.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at) VALUES(?,?,?,?,?,'waiting',?,?)").run('77777777-7777-4777-8777-000000000002',board,owner.id,'node-state-wait',node,JSON.stringify({candidate:{input:{kind:'text'}}}),new Date().toISOString());database.close();
 await page.reload();await page.locator('[data-canvas-view=flow]').click();
 await expect(agent).toHaveAttribute('data-state','waiting');
 await expect(agent.locator('.canvas-node-state')).toContainText('بانتظارك');
 await expect(agent.locator('.canvas-node-state button')).toHaveCount(1);
 const pill=agent.locator('.canvas-node-state button');const visible=(await pill.textContent())!.trim();const title=(await agent.locator('h2').textContent())!.trim();
 await expect(pill).toHaveAccessibleName(visible+': '+title);
 await page.locator('[data-canvas-operations] details').nth(1).locator('summary').click();
 await expect(page.getByRole('button',{name:visible,exact:true})).toHaveCount(1);
});

async function insertRun(board:string,node:string,id:string,status:string,created=new Date().toISOString()){
 const database=db();const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
 database.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at) VALUES(?,?,?,?,?,?,?,?)").run(id,board,owner.id,'req-'+id,node,status,JSON.stringify({candidate:{input:{kind:'text'}}}),created);database.close();
}
const refresh=async(page:Page)=>{await page.locator('[data-canvas-operations] details').nth(1).locator('summary').click();await page.locator('[data-canvas-operations] details').nth(1).locator('button').first().click();};

test('initial load does not announce existing states; a later change announces once', async ({page})=>{
 const path=await presetBoard(page),board=path.split('/').pop()!;
 const agent=page.locator('.canvas-card[data-kind=agent]');const node=(await agent.getAttribute('data-card-id'))!;
 const id='77777777-7777-4777-8777-000000000003';
 await insertRun(board,node,id,'running');
 await page.reload();await page.locator('[data-canvas-view=flow]').click();
 await expect(agent).toHaveAttribute('data-state','running');
 await expect(page.locator('.canvas-state-live')).toHaveText('');
 const d=db();d.prepare("UPDATE admin_canvas_runs SET status='failed' WHERE id=?").run(id);d.close();
 await refresh(page);
 await expect(agent).toHaveAttribute('data-state','failed');
 const title=(await agent.locator('h2').textContent())!;
 await expect(page.locator('.canvas-state-live')).toHaveText(title+': فشل');
});

test('refresh keeps focus on the waiting action', async ({page})=>{
 const path=await presetBoard(page),board=path.split('/').pop()!;
 const agent=page.locator('.canvas-card[data-kind=agent]');const node=(await agent.getAttribute('data-card-id'))!;
 await insertRun(board,node,'77777777-7777-4777-8777-000000000004','waiting');
 await page.reload();await page.locator('[data-canvas-view=flow]').click();
 const act=agent.locator('.canvas-node-state button');await expect(act).toHaveCount(1);
 await act.focus();
 await page.evaluate(()=>{(document.activeElement as any).__mark=1;});
 await page.locator('[data-canvas-operations] details').nth(1).evaluate(d=>{(d as HTMLDetailsElement).open=true;(d.querySelector('button') as HTMLButtonElement).click();}); // click() keeps focus where it is
 await page.waitForTimeout(500);
 expect(await page.evaluate(()=>{const a=document.activeElement as any;return a&&a.__mark===1&&!!a.closest('.canvas-node-state');})).toBe(true);
});

async function runningAgentBoard(page:Page,id:string){
 const path=await presetBoard(page),board=path.split('/').pop()!;
 const agent=page.locator('.canvas-card[data-kind=agent]');const node=(await agent.getAttribute('data-card-id'))!;
 await insertRun(board,node,id,'running');
 await page.reload();await page.locator('[data-canvas-view=flow]').click();
 await expect(agent).toHaveAttribute('data-state','running');
 return node;
}

test('edge into a running agent flows', async ({page})=>{
 const node=await runningAgentBoard(page,'77777777-7777-4777-8777-000000000005');
 const into=page.locator(`.canvas-edges path[data-to="${node}"]`);
 expect(await into.count()).toBeGreaterThan(0);
 for(const p of await into.all())await expect(p).toHaveAttribute('data-state','flowing');
 await expect(page.locator('.canvas-edge-packet').first()).toBeAttached();
 const sched=await page.locator('.canvas-card[data-kind=schedule]').first().getAttribute('data-card-id');
 await expect(page.locator(`.canvas-edges path[data-from="${sched}"]`).first()).toHaveAttribute('data-state','flowing');
});

test('reduced motion stops edges', async ({page})=>{
 const node=await runningAgentBoard(page,'77777777-7777-4777-8777-000000000006');
 await page.emulateMedia({reducedMotion:'reduce'});
 const p=page.locator(`.canvas-edges path[data-to="${node}"]`).first();
 await expect(p).toHaveAttribute('data-state','flowing');
 expect(await p.evaluate(e=>getComputedStyle(e).animationName)).toBe('none');
 expect(await page.locator('.canvas-edge-packet').first().evaluate(e=>getComputedStyle(e).display)).toBe('none');
});

test('every edge has an arrowhead', async ({page})=>{
 await presetBoard(page);
 const paths=page.locator('.canvas-edges path[data-from]');
 expect(await paths.count()).toBeGreaterThan(0);
 for(const p of await paths.all())await expect(p).toHaveAttribute('marker-end','url(#canvas-edge-arrow)');
 await expect(page.locator('.canvas-edges marker#canvas-edge-arrow')).toHaveAttribute('orient','auto-start-reverse');
});

const runsResponse=(page:Page)=>page.waitForResponse(r=>/\/operations\/runs(\?|$)/.test(r.url())&&r.request().method()==='GET',{timeout:10000});

test('state updates without reload', async ({page})=>{
 const id='77777777-7777-4777-8777-000000000007';
 await runningAgentBoard(page,id);
 const agent=page.locator('.canvas-card[data-kind=agent]');
 const d=db();d.prepare("UPDATE admin_canvas_runs SET status='succeeded',finished_at=? WHERE id=?").run(new Date().toISOString(),id);d.close();
 await expect(agent).toHaveAttribute('data-state','done',{timeout:10000});
});

test('card element survives polling', async ({page})=>{
 await runningAgentBoard(page,'77777777-7777-4777-8777-000000000008');
 const agent=page.locator('.canvas-card[data-kind=agent]');
 await agent.evaluate(e=>{(e as any).__probe=1;});
 const handle=agent.locator('.canvas-handle').first();
 await handle.focus();
 await page.evaluate(()=>{(document.activeElement as any).__mark=1;});
 await runsResponse(page);await page.waitForTimeout(300);
 expect(await agent.evaluate(e=>(e as any).__probe)).toBe(1);
 expect(await page.evaluate(()=>(document.activeElement as any)?.__mark)).toBe(1);
});

test('polling stops when idle', async ({page})=>{
 const id='77777777-7777-4777-8777-000000000009';
 await runningAgentBoard(page,id);
 const agent=page.locator('.canvas-card[data-kind=agent]');
 const d=db();d.prepare("UPDATE admin_canvas_runs SET status='failed',finished_at=? WHERE id=?").run(new Date().toISOString(),id);d.close();
 await expect(agent).toHaveAttribute('data-state','failed',{timeout:10000});
 let n=0;page.on('request',r=>{if(/\/operations\/runs(\?|$)/.test(r.url()))n++;});
 await page.waitForTimeout(9000);
 expect(n).toBeLessThanOrEqual(1);
});

test('a poll with no change keeps the edge elements', async ({page})=>{
 const node=await runningAgentBoard(page,'77777777-7777-4777-8777-000000000010');
 const flow=page.locator(`.canvas-edges path[data-to="${node}"][data-state=flowing]`).first();
 await expect(flow).toBeAttached({timeout:15000});
 const pillText=page.locator('.canvas-card[data-kind=agent] .canvas-node-state');
 const before=await pillText.textContent();
 const polled=runsResponse(page);
 await flow.evaluate(e=>{(e as any).__probe=1;});
 await polled;
 // The running pill shows an elapsed timer, so its text only changes once applyRunStates has processed the poll.
 await expect.poll(()=>pillText.textContent(),{timeout:10000}).not.toBe(before);
 expect(await page.locator(`.canvas-edges path[data-to="${node}"][data-state=flowing]`).first().evaluate(e=>(e as any).__probe)).toBe(1);
});

test('polling keeps focus on Cancel run in the run history', async ({page})=>{
 const path=await presetBoard(page),board=path.split('/').pop()!;
 const agent=page.locator('.canvas-card[data-kind=agent]');const node=(await agent.getAttribute('data-card-id'))!;
 const id='77777777-7777-4777-8777-000000000011';
 await insertRun(board,node,id,'waiting_agent'); // active (polled) and cancellable
 await page.reload();await page.locator('[data-canvas-view=flow]').click();
 await expect(agent).toHaveAttribute('data-state','running');
 const history=page.locator('[data-canvas-operations] details').nth(1);
 await history.locator('summary').click();
 const cancel=history.getByRole('button',{name:'إلغاء التشغيل'});
 await expect(cancel).toHaveCount(1);
 await cancel.focus();
 await page.evaluate(()=>{(document.activeElement as any).__mark=1;});
 // An unchanged poll leaves the control in place.
 await runsResponse(page);await page.waitForTimeout(300);
 expect(await page.evaluate(()=>(document.activeElement as any)?.__mark)).toBe(1);
 await expect(cancel).toBeFocused();
 // A poll that changes the list rebuilds it and puts focus back on the same run's control.
 await insertRun(board,'',  '77777777-7777-4777-8777-000000000012','failed',new Date(Date.now()-60_000).toISOString());
 await runsResponse(page);await page.waitForTimeout(300);
 await expect(history.locator('li')).toHaveCount(2);
 await expect(page.locator(`[data-run-id="${id}"][data-action=cancel]`)).toBeFocused();
});

test.describe('coarse pointer', ()=>{
 test.use({hasTouch:true,isMobile:true});
 for(const locale of ['ar','en'] as const) test(`the waiting pill and its 44px action stay inside a 300px card · ${locale}`, async ({page})=>{
  const path=await presetBoard(page),board=path.split('/').pop()!;
  expect(await page.evaluate(()=>matchMedia('(pointer:coarse)').matches)).toBe(true);
  const agent=page.locator('.canvas-card[data-kind=agent]');const node=(await agent.getAttribute('data-card-id'))!;
  await insertRun(board,node,'77777777-7777-4777-8777-00000000001'+(locale==='ar'?'3':'4'),'waiting');
  // A long title leaves the pill no room beside it.
  const database=db();const row=database.prepare('SELECT document_json FROM admin_canvases WHERE id=?').get(board) as {document_json:string};
  const cards=JSON.parse(row.document_json);for(const c of cards)if(c.id===node)c.title='وكيل التلخيص اليومي للمحتوى المنشور حديثًا';
  database.prepare('UPDATE admin_canvases SET document_json=? WHERE id=?').run(JSON.stringify(cards),board);database.close();
  await page.reload();
  if(locale==='en')await setLocale(page,'en');
  await page.locator('[data-canvas-view=flow]').click();
  await expect(agent.locator('.canvas-node-state')).toContainText(locale==='ar'?'بانتظارك':'Needs you');
  const m=await agent.evaluate(card=>{
   const c=card.getBoundingClientRect(),h=card.querySelector('.canvas-card-heading') as HTMLElement,pill=h.querySelector('.canvas-node-state') as HTMLElement,b=pill.querySelector('button')!.getBoundingClientRect(),p=pill.getBoundingClientRect();
   const title=h.querySelector('h2')!.parentElement!.getBoundingClientRect();
   return {tw:title.width,cw:c.width,inside:p.left>=c.left-0.5&&p.right<=c.right+0.5&&b.left>=c.left-0.5&&b.right<=c.right+0.5,hsw:h.scrollWidth,hcw:h.clientWidth,psw:pill.scrollWidth,pcw:pill.clientWidth,bw:b.width,bh:b.height};
  });
  if(locale==='en')await setLocale(page,'ar');
  expect(m.cw).toBe(300);
  expect(m.inside).toBe(true);
  // The pill wraps to its own line rather than crushing the title beside it.
  expect(m.tw).toBeGreaterThanOrEqual(144);
  expect(m.hsw).toBeLessThanOrEqual(m.hcw);
  expect(m.psw).toBeLessThanOrEqual(m.pcw);
  expect(m.bw).toBeGreaterThanOrEqual(44);expect(m.bh).toBeGreaterThanOrEqual(44);
 });
});

test('packets are gated behind offset-path support', async ({page})=>{
 await runningAgentBoard(page,'77777777-7777-4777-8777-000000000015');
 const rules=await page.evaluate(()=>{
  const out:{supports:string|null,display:string}[]=[];
  const walk=(list:CSSRuleList,supports:string|null)=>{for(const r of [...list]){
   if(r instanceof CSSSupportsRule)walk(r.cssRules,r.conditionText);
   else if(r instanceof CSSMediaRule)continue;
   else if(r instanceof CSSStyleRule&&r.selectorText.includes('canvas-edge-packet'))out.push({supports,display:r.style.display});
  }};
  for(const s of [...document.styleSheets])try{walk(s.cssRules,null);}catch(_){}
  return out;
 });
 expect(rules.find(r=>r.supports===null)?.display).toBe('none');
 const gated=rules.find(r=>r.supports!==null)!;
 expect(gated.supports).toContain('offset-path');
 expect(gated.display).not.toBe('none');
 // Chromium supports offset-path, so the packet shows and moves along its edge.
 const packet=page.locator('.canvas-edge-packet').first();
 expect(await packet.evaluate(e=>getComputedStyle(e).display)).not.toBe('none');
 expect(await packet.evaluate(e=>getComputedStyle(e).animationName)).toBe('canvas-edge-packet');
});

test('the prompt fold mask matches the surface in flow and lanes', async ({page})=>{
 await presetBoard(page);
 const prompt=page.locator('.canvas-card[data-kind=prompt]');
 const viewport=await page.locator('.canvas-viewport').evaluate(e=>getComputedStyle(e).backgroundColor);
 const mask=()=>prompt.evaluate(e=>{const a=getComputedStyle(e,'::after');return {color:a.backgroundColor,image:a.backgroundImage};});
 expect(await mask()).toEqual({color:viewport,image:'none'});
 await page.locator('[data-canvas-view=lanes]').click();
 const band=await page.locator('.canvas-lane[data-lane="1"]').evaluate(e=>getComputedStyle(e).backgroundColor);
 const lanes=await mask();
 expect(lanes.color).toBe(viewport);
 expect(lanes.image).toBe(`linear-gradient(${band}, ${band})`);
 await page.locator('[data-canvas-view=flow]').click();
});
