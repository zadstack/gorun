import { test, expect } from '@playwright/test';
import { db, setLocale } from '../lib/helpers.ts';

for (const locale of ['en','ar']) test(`literal readable notes and attributed results · ${locale}`, async ({page},testInfo)=>{
 await page.setViewportSize({width:1440,height:1000});
 await page.goto('/admin/canvases'); await setLocale(page,locale);
 await page.locator('#new-canvas-title').fill(locale==='ar'?'خطة نشر ثنائية اللغة':'Bilingual publishing plan');
 await page.locator('form[action="/admin/canvases"] [type=submit]').click();
 await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]+$/);
 const url=new URL(page.url()).pathname, board=url.split('/').pop()!;
 const own='77777777-7777-4777-8777-777777777771', result='77777777-7777-4777-8777-777777777772', agent='77777777-7777-4777-8777-777777777773';
 const body=(locale==='ar'?'ملخص الحملة\nراجِع النص وروابط المصادر قبل النشر.\n':'Campaign brief\nReview the copy and source references before publishing.\n')+Array(20).fill(locale==='ar'?'ملاحظة عملية طويلة لتنظيم الحملة واختيار المصادر المناسبة.':'A practical long note to organize the campaign and select useful references.').join('\n')+'\n<img src=x onerror="window.noteInjected=true">\n<script>window.noteInjected=true</script>\njavascript:alert(1)\nhttps://example.test/'+('longword'.repeat(25))+'\nEND OF EXACT NOTE';
 const cards=[{id:own,kind:'note',title:locale==='ar'?'ملاحظات التحرير':'Editorial notes',body:locale==='ar'?'هدفنا: دليل مفيد بلغتين.\nاختر سياقًا محددًا وراجع كل نتيجة.':'Our goal: a useful guide in two languages.\nChoose focused context and review every result.',x:24,y:24},{id:result,kind:'note',title:locale==='ar'?'مسودة خطة الحملة':'Campaign plan draft',body,x:360,y:24},{id:agent,kind:'agent',title:locale==='ar'?'مساعد التحرير':'Editorial assistant',agent_id:'',x:696,y:24}];
 const database=db();const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
 database.prepare('UPDATE admin_canvases SET document_json=?,edges_json=? WHERE id=?').run(JSON.stringify(cards),JSON.stringify([{from:own,to:agent},{from:result,to:agent}]),board);
 database.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,result_card_id,created_at) VALUES(?,?,?,?,?,'succeeded',?,?,?)").run(board,board,owner.id,'note-reading-fixture',agent,JSON.stringify({candidate:{input:{kind:'text'}}}),result,new Date().toISOString());
 database.prepare("INSERT INTO admin_canvas_provenance(board_id,card_id,actor_kind,actor_name,run_id,created_at) VALUES(?,?,'api_key','Editorial assistant',?,?)").run(board,result,board,new Date().toISOString());database.close();
 await page.goto(url);await page.locator('[data-canvas-view=flow]').click();
 const card=page.locator('#card-'+result);await expect(card).toHaveAttribute('data-note-source','attributed');await expect(page.locator('#card-'+own)).toHaveAttribute('data-note-source','personal');
 const snapshot=await page.request.get(url+'/document');const before=await snapshot.json();
 const boxes=await page.locator('.canvas-card').evaluateAll(els=>els.map(el=>{const r=el.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height};}));
 for(let i=0;i<boxes.length;i++)for(let j=i+1;j<boxes.length;j++){const a=boxes[i],b=boxes[j];expect(a.x+a.w<=b.x||b.x+b.w<=a.x||a.y+a.h<=b.y||b.y+b.h<=a.y).toBeTruthy();}
 await expect(page.locator('.canvas-edges path')).toHaveCount(2);
 // Stress the attributed actions at a compact 300px width, with no persisted edit.
 await card.evaluate(el=>{el.style.width='300px';});
 const footer=card.locator('.canvas-card-footer');
 for(const control of await footer.locator('button').all()) {
  const bounds=await control.boundingBox(), frame=await card.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(frame!.x);expect(bounds!.x+bounds!.width).toBeLessThanOrEqual(frame!.x+frame!.width+1);
 }
 await card.evaluate(el=>{el.style.width='';});
 const read=card.locator('.canvas-note-read');await read.focus();await page.keyboard.press('Enter');
 const reader=page.locator('.canvas-note-reader');await expect(reader).toBeVisible();await expect(reader.locator('.canvas-note-full')).toHaveText(body);
 await expect(reader.locator('img,script,a')).toHaveCount(0);expect(await page.evaluate(()=>(window as any).noteInjected)).toBeUndefined();
 await page.screenshot({path:testInfo.outputPath('note-reader-'+locale+'-desktop.png')});
 await page.keyboard.press('Escape');await expect(reader).toHaveCount(0);await expect(read).toBeFocused();
 // A legitimate repaint while open must not leave focus on a detached trigger.
 await read.click();await expect(reader).toBeVisible();
 await page.locator('[data-canvas-view=grid]').evaluate((el:HTMLButtonElement)=>el.click());
 await page.keyboard.press('Escape');await expect(card.locator('.canvas-note-read')).toBeFocused();
 await page.locator('[data-canvas-view=flow]').click();
 const after=await(await page.request.get(url+'/document')).json();expect(after.version).toBe(before.version);expect(after.cards).toEqual(before.cards);expect(after.edges).toEqual(before.edges);expect(after.provenance).toEqual(before.provenance);
 await page.screenshot({path:testInfo.outputPath('notes-'+locale+'-desktop.png')});
 await page.setViewportSize({width:390,height:844});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await card.scrollIntoViewIfNeeded();await read.click();await expect(reader).toBeVisible();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy();
 expect(await reader.evaluate(el=>el.getBoundingClientRect().width<=innerWidth)).toBeTruthy();
 await page.screenshot({path:testInfo.outputPath('note-reader-'+locale+'-mobile.png')});await page.keyboard.press('Escape');await expect(read).toBeFocused();
 await page.screenshot({path:testInfo.outputPath('notes-'+locale+'-mobile.png')});
 // Existing editing still receives the full original text, never the excerpt.
 await card.getByRole('button',{name:locale==='ar'?'تعديل البطاقة':'Edit card',exact:true}).click();
 await expect(page.locator('#canvas-card-form [name=body]')).toHaveValue(body);
});

for (const locale of ['en','ar']) test(`mixed card types, distributed real ports and saved layout · ${locale}`, async ({page},testInfo)=>{
 await page.setViewportSize({width:1440,height:1000});await page.goto('/admin/canvases');await setLocale(page,locale);
 await page.locator('#new-canvas-title').fill(locale==='ar'?'مساحة عمل تحريرية متنوعة':'Editorial workbench');
 await page.locator('form[action="/admin/canvases"] [type=submit]').click();await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]+$/);
 const url=new URL(page.url()).pathname, board=url.split('/').pop()!;
 const kinds=['note','prompt','content','source','agent','provider','checklist','link','widget','schedule','result'];
 const names=locale==='ar'?['ملاحظات الفريق','تعليمات التحرير','مرجع المقال','مجموعة المصادر','مساعد التحرير','نموذج الكتابة','قائمة المراجعة','مرجع خارجي','مؤشرات العمل','مراجعة يومية','صورة مرجعية تجريبية']:['Team notes','Editorial instructions','Article reference','Source collection','Editorial assistant','Writing model','Review checklist','External reference','Work indicators','Daily review','Synthetic reference artwork'];
 const ids=kinds.map((_,i)=>`88888888-8888-4888-8888-${String(i+1).padStart(12,'0')}`);
 const database=db();const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
 const content=database.prepare("SELECT id FROM content WHERE type='post' AND deleted_at IS NULL LIMIT 1").get() as {id:string}|undefined;
 const cards=kinds.map((kind,i)=>({id:ids[i],kind,title:names[i],body:kind==='note'?'A focused brief, selected references and a separate review before use.':kind==='prompt'?Array(24).fill(locale==='ar'?'لخص المصدر المرتبط وميّز الحقائق عن المقترحات.':'Summarize the connected source. Distinguish facts from suggestions.').join('\n'):'',x:24+(i%3)*336,y:24+Math.floor(i/3)*420,...(kind==='content'?{content_id:content?.id||''}:{}),...(kind==='source'?{source_type:'post',source_limit:3}:{}),...(kind==='provider'?{provider_id:'',generation_kind:'text'}:{}),...(kind==='agent'?{agent_id:''}:{}),...(kind==='checklist'?{items:[{text:locale==='ar'?'تحقق من المصادر':'Check the sources',checked:false},{text:locale==='ar'?'راجع النسختين':'Review both languages',checked:true}]}:{}),...(kind==='link'?{url:'https://example.com/guide'}:{}),...(kind==='widget'?{widget_id:'jobs'}:{}),...(kind==='schedule'?{interval:'daily'}:{}),...(kind==='result'?{run_id:board}:{})}));
 const edges=[0,1,2,3,9].map(i=>({from:ids[i],to:ids[4]}));edges.push({from:ids[0],to:ids[5]});
 // Synthetic private artwork uses an existing repository PNG, not a model call.
 const { mkdirSync,copyFileSync,statSync }=await import('node:fs');const {join,resolve}=await import('node:path');
 const file=ids[10]+'.bin', mediaDir=join(process.env.E2E_DATA!,'canvas-results');mkdirSync(mediaDir,{recursive:true});copyFileSync(resolve('../assets/branding/gorun-avatar.png'),join(mediaDir,file));
 database.prepare('UPDATE admin_canvases SET document_json=?,edges_json=? WHERE id=?').run(JSON.stringify(cards),JSON.stringify(edges),board);
 database.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,result_card_id,created_at) VALUES(?,?,?,?,?,'succeeded',?,?,?)").run(board,board,owner.id,'mixed-card-fixture',ids[5],JSON.stringify({candidate:{input:{kind:'image'}},media_file:file,media_mime:'image/png',media_size:statSync(join(mediaDir,file)).size}),ids[10],new Date().toISOString());database.close();
 await page.goto(url);await page.locator('[data-canvas-view=flow]').click();await expect(page.locator('.canvas-card')).toHaveCount(11);
 await expect(page.locator('[data-kind=result] img')).toBeVisible();await expect(page.locator('[data-kind=widget] .canvas-widget-metric').first()).toBeVisible();
 const promptText=page.locator('[data-kind=prompt] .canvas-card-body');expect(await promptText.evaluate(el=>el.scrollHeight>el.clientHeight)).toBeTruthy();await promptText.focus();await expect(promptText).toBeFocused();await page.keyboard.press('PageDown');await expect.poll(()=>promptText.evaluate(el=>el.scrollTop)).toBeGreaterThan(0);
 const snapshot=await(await page.request.get(url+'/document')).json();
 const contrasts=await page.locator('.canvas-card-heading small').evaluateAll(els=>els.map(el=>{
  const lum=(s:string)=>{const rgb=s.match(/[\d.]+/g)!.slice(0,3).map(Number).map(n=>{const c=n/255;return c<=.04045?c/12.92:((c+.055)/1.055)**2.4;});return .2126*rgb[0]+.7152*rgb[1]+.0722*rgb[2];};
  const style=getComputedStyle(el),a=lum(style.color),b=lum(style.backgroundColor);return (Math.max(a,b)+.05)/(Math.min(a,b)+.05);
 }));expect(Math.min(...contrasts)).toBeGreaterThanOrEqual(4.5);
 for(const kind of kinds){const card=page.locator(`[data-kind=${kind}]`);await expect(card.locator('.canvas-card-heading small')).not.toBeEmpty();
  const frames=await card.evaluate(el=>{const box=el.getBoundingClientRect();return [...el.querySelectorAll('.canvas-card-footer button')].map(button=>{const r=button.getBoundingClientRect();return r.left>=box.left&&r.right<=box.right+1;});});expect(frames.every(Boolean)).toBeTruthy();}
 const save=page.waitForResponse(r=>r.url().endsWith(url+'/document')&&r.request().method()==='POST');await page.locator('[data-action=arrange]').click();expect((await save).status()).toBe(200);
 await expect(page.locator('.canvas-edges path')).toHaveCount(edges.length);
 const ports=await page.locator(`.canvas-edges circle[data-card-id="${ids[4]}"]`).evaluateAll(els=>els.map(el=>el.getAttribute('cx')+','+el.getAttribute('cy')));expect(ports.length).toBe(5);expect(new Set(ports).size).toBe(5);
 const crossings=await page.locator('.canvas-surface').evaluate(surface=>{
  const boxes=[...surface.querySelectorAll<HTMLElement>('.canvas-card')].map(el=>({x:el.offsetLeft,y:el.offsetTop,w:el.offsetWidth,h:el.offsetHeight}));
  const bad:string[]=[];
  for(const path of surface.querySelectorAll('.canvas-edges path')){
   const n=(path.getAttribute('d')!.match(/[-\d.]+/g)||[]).map(Number);
   for(let i=2;i<n.length;i+=2){const ax=n[i-2],ay=n[i-1],bx=n[i],by=n[i+1];
    for(const box of boxes)if((ax===bx&&ax>box.x+.01&&ax<box.x+box.w-.01&&Math.max(ay,by)>box.y+.01&&Math.min(ay,by)<box.y+box.h-.01)||(ay===by&&ay>box.y+.01&&ay<box.y+box.h-.01&&Math.max(ax,bx)>box.x+.01&&Math.min(ax,bx)<box.x+box.w-.01))bad.push(path.getAttribute('data-from')!);
   }
  }return bad;
 });expect(crossings).toEqual([]);
 const boxes=await page.locator('.canvas-card').evaluateAll(els=>els.map(el=>({id:(el as HTMLElement).dataset.cardId,x:(el as HTMLElement).offsetLeft,y:(el as HTMLElement).offsetTop,w:(el as HTMLElement).offsetWidth,h:(el as HTMLElement).offsetHeight})));
 for(let i=0;i<boxes.length;i++)for(let j=i+1;j<boxes.length;j++){const a=boxes[i],b=boxes[j];expect(a.x+a.w<=b.x||b.x+b.w<=a.x||a.y+a.h<=b.y||b.y+b.h<=a.y).toBeTruthy();}
 const arranged=await(await page.request.get(url+'/document')).json();expect(arranged.edges).toEqual(snapshot.edges);expect(arranged.cards.map(({x,y,...c}:any)=>c)).toEqual(snapshot.cards.map(({x,y,...c}:any)=>c));
 await page.reload();await expect(page.locator('.canvas-card')).toHaveCount(11);const reloaded=await(await page.request.get(url+'/document')).json();expect(reloaded.cards).toEqual(arranged.cards);
 await page.screenshot({path:testInfo.outputPath('mixed-cards-'+locale+'-flow.png')});
 for(const view of ['grid','list']){await page.locator(`[data-canvas-view=${view}]`).click();await expect(page.locator('.canvas-card')).toHaveCount(11);}
 await page.locator('[data-canvas-view=grid]').click();await page.screenshot({path:testInfo.outputPath('mixed-cards-'+locale+'-grid.png')});
 await page.setViewportSize({width:390,height:844});await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 await page.locator('[data-canvas-view=flow]').click();
 for(const kind of kinds){const card=page.locator(`[data-kind=${kind}]`);await card.scrollIntoViewIfNeeded();expect(await card.evaluate(el=>el.getBoundingClientRect().width<=innerWidth)).toBeTruthy();for(const btn of await card.locator('.canvas-card-footer button').all()){if(await btn.isVisible()){await btn.focus();await expect(btn).toBeFocused();}}}
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy();
 await page.locator('[data-kind=checklist]').scrollIntoViewIfNeeded();await page.screenshot({path:testInfo.outputPath('mixed-cards-'+locale+'-mobile.png')});
 const final=await(await page.request.get(url+'/document')).json();expect(final.version).toBe(arranged.version);expect(final.cards).toEqual(arranged.cards);expect(final.edges).toEqual(arranged.edges);
});
