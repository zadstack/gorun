// Opt-in documentation capture. Uses a fresh local server and synthetic data;
// never capture a production site or expose one-time credentials.
import { test, expect, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import { mkdirSync } from 'node:fs';
import { csrf, db, mcp, toolPayload, setLocale } from '../lib/helpers.ts';

test('capture current README product screens with synthetic campaign data', async ({ page, request }) => {
  test.skip(process.env.README_SCREENSHOTS !== '1', 'Documentation capture is opt-in.');
  test.setTimeout(180_000);
  await page.setViewportSize({ width: 1600, height: 1040 });
  const output = resolve('..','assets','branding','screenshots');
  mkdirSync(output,{recursive:true});
  const database = db();
  database.prepare("UPDATE users SET name='Amira Hassan' WHERE email='admin@e2e.test'").run();
  database.prepare("INSERT INTO settings(key,value_json,updated_at) VALUES('site.title',?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json").run(JSON.stringify('Atlas Studio'),new Date().toISOString());
  database.close();
  await page.goto('/admin');
  await setLocale(page,'en');
  const errors: string[]=[];
  page.on('pageerror',e=>errors.push(e.message));
  async function shot(name:string,fullPage=false) {
    await page.waitForLoadState('networkidle');
    await page.evaluate(()=>document.fonts.ready);
    await expect(page.locator('body')).not.toContainText('Internal Server Error');
    await page.mouse.move(1580,10);
    await page.screenshot({path:resolve(output,name),fullPage});
  }
  async function key(kind:'api'|'agent', name:string, scopes:string[]) {
    await page.goto('/admin/agents/keys');
    await page.check(`input[name="kind"][value="${kind}"]`);
    await page.fill('#k-name',name);
    for(const scope of scopes) await page.check(`input[name="scopes"][value="${scope}"]`);
    await page.locator('form[action="/admin/agents/keys"] button[type="submit"]').click();
    const secret=await page.locator('#k-plaintext').inputValue();
    await page.goto('/admin/agents/keys'); // one-time secret must not appear in a screenshot
    await expect(page.locator('#k-plaintext')).toHaveCount(0);
    return secret;
  }
  const rest=await key('api','Website integration',['content.view','content.create','content.edit','content.publish']);
  const agent=await key('agent','Campaign assistant',['content.view','content.create','content.publish','canvas.read','canvas.append','canvas.run']);
  const headers={Authorization:`Bearer ${rest}`};
  const stories=[
    {title:'A calmer workspace for your next big idea',slug:'a-calmer-workspace',locale:'en',status:'published',excerpt:'One place to plan a launch, shape the story and keep your team moving.',body:'<p>Good work starts with a clear brief. Atlas Studio brings people, content and the next decision into one shared workspace.</p><h2>From an idea to a considered launch</h2><p>Keep the product story, the Arabic edition and your review checklist together. Let your assistant prepare a draft, then let your team make the publishing decision.</p>'},
    {title:'مساحة أهدأ لفكرتك القادمة',slug:'your-next-idea',locale:'ar',status:'published',excerpt:'من الفكرة إلى الإطلاق، بمحتوى واضح وفريق يعمل معًا.',body:'<p>كل مشروع جيد يبدأ بفكرة واضحة. في أطلس ستوديو نجمع المحتوى والفريق وخطوة العمل التالية في مساحة واحدة.</p><h2>قصة واحدة بلغتين</h2><p>جهز المسودة، وراجع التفاصيل مع فريقك، ثم اتخذ قرار النشر. تبقى النسخة العربية والإنجليزية ضمن خطة واحدة.</p>'},
    {title:'Meet the team behind the launch',slug:'meet-the-team',locale:'en',status:'draft',excerpt:'The people and small decisions that shape our work.',body:'<p>A behind-the-scenes story ready for editorial review.</p>'},
    {title:'دليل الفريق لإطلاق الحملة',slug:'campaign-guide',locale:'ar',status:'draft',excerpt:'قائمة مراجعة عملية قبل النشر.',body:'<p>راجِع الرسالة والروابط والصور قبل إطلاق الحملة.</p>'},
  ];
  const ids:string[]=[];
  for(const story of stories) {
    const response=await request.post('/api/v1/content',{headers,data:{type:'post',...story}});
    expect(response.status()).toBe(201);
    ids.push((await response.json()).data.id);
  }
  await page.goto('/admin/media');
  await page.locator('#media-file').setInputFiles(resolve('fixtures/atlas-demo.svg'));
  await expect(page.locator('#media-results')).toContainText('atlas-demo');
  const mediaDB=db();
  const demoMedia=mediaDB.prepare('SELECT id,url FROM media ORDER BY created_at DESC LIMIT 1').get() as any;
  const mediaID=demoMedia.id;
  for(const id of ids.slice(0,2)) mediaDB.prepare('UPDATE content SET featured_media_id=? WHERE id=?').run(mediaID,id);
  mediaDB.close();
  await page.goto(`/admin/content/post/${ids[0]}`);
  await expect(page.locator('#title')).toHaveValue(stories[0].title);
  await shot('editor-en.png');
  await setLocale(page,'ar');
  await page.goto('/admin/content/post');
  await shot('content-list-ar.png');

  // A real editor + preview, with a clearly fictional business, not a mockup.
  await page.goto('/admin/content/page/new');
  await page.fill('#title','أطلق فكرتك مع أطلس');
  await page.fill('#slug','atlas-studio');
  await page.locator('button[name="intent"][value="save"]').click();
  await expect(page).toHaveURL(/\/admin\/content\/page\/[0-9A-Z]{26}$/);
  const landingID=page.url().split('/').pop()!;
  await page.goto(`/admin/builder/${landingID}`);
  await page.locator('[data-action="add"]').click();
  const hero=page.locator('#builder-sections > section').first();
  await hero.locator('input[data-field="title"]').fill('مساحة واحدة، وأفكار تصل إلى الناس');
  await hero.locator('[data-field="text"]').fill('من التخطيط إلى النشر، اجمع فريقك ومحتواك في مساحة تمنح كل فكرة بداية واضحة.');
  await hero.locator('[data-field="image"]').fill(demoMedia.url);
  await hero.locator('[data-field="image_alt"]').fill('واجهة أطلس ستوديو التجريبية');
  await hero.locator('[data-field="link"]').fill('/post/your-next-idea');
  await hero.locator('[data-field="link_label"]').fill('اكتشف القصة');
  await page.locator('[data-action="save"]').click();
  await expect(page.locator('#builder-status')).toContainText(/حُفظت|Draft saved/);
  await shot('builder-ar.png');

  await setLocale(page,'en');
  await page.goto('/admin/canvases');
  const boardResponse=await request.post('/admin/canvases',{form:{title:'Launch a bilingual campaign',_csrf:await csrf(page)},maxRedirects:0});
  expect(boardResponse.status()).toBe(303);
  const boardPath=boardResponse.headers().location;
  await page.goto(boardPath);
  const options=await (await request.get(boardPath+'/operations/options')).json();
  const agentID=options.agents.find((x:any)=>x.title==='Campaign assistant').id;
  const sourceID=randomUUID(),promptID=randomUUID(),actorID=randomUUID(),checkID=randomUUID();
  const cards=[
    {id:sourceID,kind:'content',title:'01 · Product story',content_id:ids[0],body:'Approved source content for the campaign.',x:24,y:24},
    {id:promptID,kind:'prompt',title:'02 · Campaign brief',body:'Prepare a short launch email in English and Arabic. Use the product story as context. Keep the tone clear and human. Return a draft for our editor to review.',x:24,y:240},
    {id:actorID,kind:'agent',title:'03 · Prepare the draft',agent_id:agentID,x:360,y:120},
    {id:checkID,kind:'checklist',title:'05 · Editorial review',items:[{text:'Check English and Arabic copy',checked:true},{text:'Verify links and product details',checked:false},{text:'Approve before publishing',checked:false}],x:696,y:450},
  ];
  const csrfToken=await csrf(page);
  async function boardPost(suffix:string,data:any){
    const response=await request.post(boardPath+suffix,{headers:{'X-CSRF-Token':csrfToken},data});
    expect(response.status(),await response.text()).toBeLessThan(300);
    return response.json();
  }
  await boardPost('/document',{title:'Launch a bilingual campaign',version:1,cards,edges:[{from:sourceID,to:actorID},{from:promptID,to:actorID}]});
  await boardPost('/operations/grants',{agent_id:agentID,read:true,append:true,run:true,node_ids:[sourceID,promptID]});
  await page.reload();
  await expect(page.locator('.canvas-card')).toHaveCount(4);
  await shot('canvas-brief-en.png');
  await page.locator(`[data-card-id="${actorID}"] .canvas-operation-button`).click();
  await expect(page.locator('dialog.canvas-review')).toBeVisible();
  await shot('canvas-review-en.png');
  await page.locator('dialog.canvas-review header button').click();
  const preview=await boardPost('/operations/preview',{version:2,node_id:actorID});
  const queued=await boardPost('/operations/runs',{request_id:randomUUID(),review_token:preview.review_token});
  const input:any={board_id:boardPath.split('/').pop(),run_id:queued.run.id};
  const contextResult=await mcp(request,agent,'tools/call',{name:'canvases.context',arguments:input});
  const context=toolPayload(contextResult.body.result).data;
  input.context_token=context.context_token;
  input.text='EN · Your next idea deserves room to grow. Meet Atlas Studio: a shared workspace for planning, content and considered launches. Explore the product story.\n\nAR · فكرتك القادمة تستحق مساحة للنمو. تعرف على أطلس ستوديو: مساحة مشتركة للتخطيط والمحتوى وإطلاق المشاريع بعناية.\n\nDemo draft — human review required.';
  const appended=await mcp(request,agent,'tools/call',{name:'canvases.append_result',arguments:input});
  expect(appended.body.result?.isError).not.toBe(true);
  const document=await (await request.get(boardPath+'/document')).json();
  const result=document.cards.find((c:any)=>!cards.some(x=>x.id===c.id));
  expect(result).toBeTruthy();
  result.x=696;result.y=24;result.title='04 · Draft for review';
  await boardPost('/document',{title:document.title,version:document.version,cards:document.cards,edges:document.edges});
  await page.reload();
  await expect(page.locator('.canvas-card')).toHaveCount(5);
  await expect(page.locator('.canvas-state [data-status]')).toContainText('saved');
  await expect(page.locator('.canvas-edges path')).toHaveCount(2);
  await shot('canvas-en.png');
  // Save a second locale and a mobile view to inspect the actual card controls.
  await setLocale(page,'ar');
  await shot('canvas-ar.png');
  await page.setViewportSize({width:390,height:844});
  await shot('canvas-mobile.png',true);
  const moves=page.locator('.canvas-card').first().locator('[data-direction="down"]');
  expect((await moves.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await page.setViewportSize({width:1600,height:1040});
  await setLocale(page,'en');
  await page.goto('/admin/agents/keys');
  await expect(page.locator('#k-plaintext')).toHaveCount(0);
  await shot('agent-keys-en.png');

  const created=await mcp(request,agent,'tools/call',{name:'create_content',arguments:{type:'post',title:'Campaign launch email · ready for review',body:'<p>A synthetic campaign draft awaiting the editor’s decision.</p>'}});
  const draftID=toolPayload(created.body.result).data.id;
  const pending=await mcp(request,agent,'tools/call',{name:'publish_content',arguments:{id:draftID}});
  expect(JSON.stringify(toolPayload(pending.body.result))).toContain('pending_approval');
  await setLocale(page,'ar');
  await page.goto('/admin/agents/approvals');
  await shot('approvals-ar.png');
  await setLocale(page,'en');
  await page.goto('/en/');
  await expect(page.locator('html')).toHaveAttribute('lang','en');
  await shot('theme-home-en.png');
  await page.setViewportSize({width:390,height:844});
  await page.goto('/ar/post/your-next-idea');
  await expect(page.locator('html')).toHaveAttribute('lang','ar');
  await shot('theme-post-ar-mobile.png',true);
  expect(errors).toEqual([]);
});
