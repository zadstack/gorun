// Opt-in documentation capture. Uses a fresh local server and synthetic data;
// never capture a production site or expose one-time credentials.
import { test, expect, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import { mkdirSync, readdirSync } from 'node:fs';
import { createServer } from 'node:http';
import { csrf, db, mcp, toolPayload, setLocale } from '../lib/helpers.ts';

test('capture current README product screens with synthetic campaign data', async ({ page, request },testInfo) => {
  test.skip(process.env.README_SCREENSHOTS !== '1', 'Documentation capture is opt-in.');
  test.setTimeout(300_000);
  await page.setViewportSize({ width: 1600, height: 1040 });
  const output = resolve('..','assets','branding','screenshots');
  mkdirSync(output,{recursive:true});
  const captured=new Set<string>(),secrets:string[]=[];
  const expectedImages=[...readdirSync(output).filter(name=>name.endsWith('.png')),'canvas-ai-suggestions-en.png','canvas-ai-suggestions-ar-mobile.png','extensions-methods-en.png','extensions-methods-ar-mobile.png'];
  const database = db();
  database.prepare("UPDATE users SET name='Amira Hassan' WHERE email='admin@e2e.test'").run();
  database.prepare("INSERT INTO settings(key,value_json,updated_at) VALUES('site.title',?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json").run(JSON.stringify('Atlas Studio'),new Date().toISOString());
  const owner=(database.prepare("SELECT id FROM users WHERE email='admin@e2e.test'").get() as any).id;
  for(const [index,id] of ['canvases','media','site-builder'].entries())database.prepare('INSERT INTO admin_dashboard_pins(user_id,nav_id,position,created_at) VALUES(?,?,?,?)').run(owner,id,index,new Date().toISOString());
  database.prepare("INSERT INTO settings(key,value_json,updated_at) VALUES('site.tagline',?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json").run(JSON.stringify('Thoughtful stories. Clear next steps.'),new Date().toISOString());
  database.close();
  await page.goto('/admin');
  await setLocale(page,'en');
  const errors: string[]=[];
  page.on('pageerror',e=>errors.push(e.message));
  async function shot(name:string,fullPage=false) {
    if(process.env.README_CANVAS_ONLY==='1'&&!name.startsWith('canvas-'))return;
    await page.waitForLoadState('networkidle');
    await page.evaluate(()=>document.fonts.ready);
    await expect(page.locator('body')).not.toContainText('Internal Server Error');
    await expect(page.locator('#k-plaintext,[data-reveal-secret]')).toHaveCount(0);
    const html=await page.content();for(const secret of secrets)expect(html).not.toContain(secret);
    await page.mouse.move(1580,10);
    await page.screenshot({path:resolve(output,name),fullPage});captured.add(name);
  }
  async function key(kind:'api'|'agent', name:string, scopes:string[]) {
    await page.goto('/admin/agents/keys');
    await page.locator('#key-create > summary').click();
    await page.check(`input[name="kind"][value="${kind}"]`);await page.locator('[data-key-permissions] > summary').click();
  await page.fill('#k-name',name);
    for(const scope of scopes) await page.check(`input[name="scopes"][value="${scope}"]`);
    await page.locator('form[action="/admin/agents/keys"] button[type="submit"]').click();
    const secret=await page.locator('#k-plaintext').inputValue();secrets.push(secret);
    await page.goto('/admin/agents/keys'); // one-time secret must not appear in a screenshot
    await expect(page.locator('#k-plaintext')).toHaveCount(0);
    return secret;
  }
  const rest=await key('api','Website integration',['content.view','content.create','content.edit','content.publish']);
  const agent=await key('agent','Campaign assistant',['content.view','content.create','content.publish','canvas.read','canvas.append','canvas.run']);
  const headers={Authorization:`Bearer ${rest}`};
  const stories=[
    {title:'A calmer workspace for your next big idea',slug:'a-calmer-workspace',locale:'en',status:'published',excerpt:'One place to plan a launch, shape the story and keep your team moving.',body:'<p>Good work starts with a clear brief. A clear workspace brings people, content and the next decision together.</p><h2>From an idea to a considered launch</h2><p>Keep the product story, the Arabic edition and your review checklist together. Let your assistant prepare a draft, then let your team make the publishing decision.</p>'},
    {title:'مساحة أهدأ لفكرتك القادمة',slug:'your-next-idea',locale:'ar',status:'published',excerpt:'من الفكرة إلى الإطلاق، بمحتوى واضح وفريق يعمل معًا.',body:'<p>كل مشروع جيد يبدأ بفكرة واضحة. نجمع المحتوى والفريق وخطوة العمل التالية في مساحة واحدة.</p><h2>قصة واحدة بلغتين</h2><p>جهز المسودة، وراجع التفاصيل مع فريقك، ثم اتخذ قرار النشر. تبقى النسخة العربية والإنجليزية ضمن خطة واحدة.</p>'},
    {title:'Meet the team behind the launch',slug:'meet-the-team',locale:'en',status:'draft',excerpt:'The people and small decisions that shape our work.',body:'<p>A behind-the-scenes story ready for editorial review.</p>'},
    {title:'دليل الفريق لإطلاق الحملة',slug:'campaign-guide',locale:'ar',status:'draft',excerpt:'قائمة مراجعة عملية قبل النشر.',body:'<p>راجِع الرسالة والروابط والصور قبل إطلاق الحملة.</p>'},
  ];
  stories.push(
    {title:'A shared brief keeps the launch focused',slug:'a-shared-brief',locale:'en',status:'published',excerpt:'An example editorial note: agree on the audience, the message and the next decision.',body:'<p>A fictional Atlas Studio example for this documentation site.</p><h2>Begin with the audience</h2><p>Write down who the story is for, what they need to know and which claims still need evidence. Keep the final publishing decision with your editor.</p>'},
    {title:'Small reviews, clearer stories',slug:'clearer-stories',locale:'en',status:'published',excerpt:'Review language, links and source material before a draft becomes a public page.',body:'<p>A fictional Atlas Studio example about a careful editorial workflow.</p><h2>Make review practical</h2><p>Read the draft on mobile, check every link and compare translated text with the approved source. A short checklist makes the next step clear.</p>'},
    {title:'موجز مشترك يحافظ على وضوح الإطلاق',slug:'a-shared-brief-ar',locale:'ar',status:'published',excerpt:'ملاحظة تحريرية توضيحية: اتفق على الجمهور والرسالة والقرار التالي.',body:'<p>مثال خيالي من أطلس ستوديو لتوثيق القالب.</p><h2>ابدأ بالجمهور</h2><p>حدّد لمن تكتب وما يحتاج إلى معرفته، وما الذي يحتاج إلى دليل. يبقى قرار النشر لدى المحرر.</p>'},
    {title:'مراجعات صغيرة وقصص أوضح',slug:'clearer-stories-ar',locale:'ar',status:'published',excerpt:'راجع اللغة والروابط والمصادر قبل أن تصبح المسودة صفحة عامة.',body:'<p>مثال خيالي من أطلس ستوديو لسير عمل تحريري متأنٍّ.</p><h2>اجعل المراجعة عملية</h2><p>اقرأ المسودة على الهاتف، وافحص الروابط، وقارن الترجمة بالمصدر المعتمد. قائمة قصيرة تجعل الخطوة التالية واضحة.</p>'},
  );
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
  for(const id of ids.slice(4))mediaDB.prepare("UPDATE content SET published_at='2026-10-01T10:00:00Z' WHERE id=?").run(id);
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
    expect(response.status()).toBeLessThan(300);
    return response.json();
  }
  await boardPost('/document',{title:'Launch a bilingual campaign',version:1,cards,edges:[{from:sourceID,to:actorID},{from:promptID,to:actorID}]});
  await boardPost('/operations/grants',{agent_id:agentID,read:true,append:true,run:true,node_ids:[sourceID,promptID]});
  // A real signed receiver, deliberately disabled; its one-time signing secret
  // is never inserted into the page or captured in documentation screenshots.
  await boardPost(`/cards/${actorID}/inbound`,{action:'configure',command:'prepare-campaign-draft'});
  await page.reload();
  await expect(page.locator('.canvas-card')).toHaveCount(4);
  await page.locator('[data-action=arrange]').click();
  await expect(page.locator('.canvas-state [data-status]')).toHaveText('All changes saved');
  await shot('canvas-brief-en.png');
  await page.locator(`[data-card-id="${actorID}"] .canvas-operation-button`).click();
  await expect(page.locator('dialog.canvas-review')).toBeVisible();
  await shot('canvas-review-en.png');
  await page.locator('dialog.canvas-review header button').click();
  const beforeReview=await (await request.get(boardPath+'/document')).json();
  const preview=await boardPost('/operations/preview',{version:beforeReview.version,node_id:actorID});
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
  await expect(page.locator('.canvas-state [data-status]')).toHaveText('All changes saved');
  await page.locator('[data-action=arrange]').click();
  await expect(page.locator('.canvas-state [data-status]')).toHaveText('All changes saved');
  await expect(page.locator('.canvas-edges path')).toHaveCount(2);
  const inputPorts=await page.locator(`.canvas-edges circle[data-port=to][data-card-id="${actorID}"]`).evaluateAll(nodes=>nodes.map(node=>node.getAttribute('cy')));
  expect(new Set(inputPorts).size).toBe(2);
  const rectangles=await page.locator('.canvas-card').evaluateAll(nodes=>nodes.map(node=>{const r=node.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height};}));
  for(let i=0;i<rectangles.length;i++)for(let j=i+1;j<rectangles.length;j++){
    const a=rectangles[i],b=rectangles[j];
    expect(a.x<b.x+b.width&&a.x+a.width>b.x&&a.y<b.y+b.height&&a.y+a.height>b.y).toBe(false);
  }
  await shot('canvas-en.png');
  const beforeViews=await (await request.get(boardPath+'/document')).json();
  for(const view of ['grid','list']){
    await page.locator(`[data-canvas-view="${view}"]`).click();
    await expect(page.locator(`[data-canvas-view="${view}"]`)).toHaveAttribute('aria-pressed','true');
    await expect(page.locator('.canvas-edges path')).toHaveCount(0);
    await shot(`canvas-${view}-en.png`);
  }
  await page.locator('[data-canvas-view="flow"]').click();
  const afterViews=await (await request.get(boardPath+'/document')).json();
  expect(afterViews.version).toBe(beforeViews.version);
  expect(afterViews.cards).toEqual(beforeViews.cards);
  // Save a second locale and a mobile view to inspect the actual card controls.
  await setLocale(page,'ar');
  await shot('canvas-ar.png');
  await page.setViewportSize({width:390,height:844});
  await shot('canvas-mobile.png',true);
  const moves=page.locator('.canvas-card').first().locator('[data-direction="down"]');
  expect((await moves.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await page.setViewportSize({width:1600,height:1040});
  await setLocale(page,'en');
  // Capture the released shell and read-only agent projection using the same
  // real, permission-scoped campaign above, never an invented online state.
  await page.setViewportSize({width:1440,height:800});
  await page.goto('/admin/content/post');
  await expect(page.locator('[data-navigation-context="content"]')).toBeVisible();
  await expect(page.locator('[data-navigation-sections] [data-navigation-section]')).toHaveCount(7);
  const dock=page.locator('[data-agents-dock]'),drawer=dock.locator('dialog');
  await expect(dock.locator('[data-dock-open]')).toBeVisible();
  await shot('admin-navigation-en.png');
  const runsDB=db(),runsBefore=(runsDB.prepare('SELECT count(*) n FROM admin_canvas_runs').get() as any).n;runsDB.close();
  await dock.locator('[data-dock-open]').click();
  await drawer.locator('[data-dock-list] button').filter({hasText:'03 · Prepare the draft'}).click();
  await expect(drawer.locator('[data-dock-detail]')).toContainText('Campaign assistant');
  await expect(drawer.locator('[data-dock-detail]')).toContainText('Configured · review before running');
  await expect(drawer.locator('[data-dock-detail]')).toContainText('An external client must be connected');
  await expect(drawer.locator('[data-dock-detail]')).not.toContainText(agent);
  await shot('agents-drawer-en.png');
  await drawer.locator('[data-dock-close]').click();
  await setLocale(page,'ar');
  await shot('admin-navigation-ar.png');
  await dock.locator('[data-dock-open]').click();
  await drawer.locator('[data-dock-list] button').filter({hasText:'03 · Prepare the draft'}).click();
  await expect(drawer.locator('[data-dock-detail]')).toContainText('Campaign assistant');
  await shot('agents-drawer-ar.png');
  await drawer.locator('[data-dock-close]').click();
  await page.setViewportSize({width:390,height:844});
  await page.locator('[data-sidebar-toggle]').click();
  await expect(page.locator('#admin-sidebar')).toBeVisible();
  await shot('admin-navigation-ar-mobile.png');
  // Opening the opposite drawer must dismiss the primary mobile menu.
  await dock.locator('[data-dock-open]').click();
  await expect(page.locator('[data-sidebar-toggle]')).toHaveAttribute('aria-expanded','false');
  await drawer.locator('[data-dock-list] button').filter({hasText:'03 · Prepare the draft'}).click();
  await drawer.getByRole('tab',{name:'التعليمات',exact:true}).click();
  await expect(drawer.getByRole('tabpanel',{name:'التعليمات',exact:true})).toContainText(cards[1].body!);
  await expect(page.locator('body')).not.toContainText('Internal Server Error');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);
  await shot('agents-drawer-ar-mobile.png');
  await drawer.locator('[data-dock-close]').click();
  const afterDockDB=db();expect((afterDockDB.prepare('SELECT count(*) n FROM admin_canvas_runs').get() as any).n).toBe(runsBefore);afterDockDB.close();
  await page.setViewportSize({width:1600,height:1040});
  await setLocale(page,'en');
  await page.goto('/admin/agents/keys');
  await expect(page.locator('#k-plaintext')).toHaveCount(0);
  await shot('agent-keys-en.png');

  // Capture design and role catalog with the SAME identity and user as the canvas.
  await page.goto('/admin/site-design');await expect(page).toHaveURL(/\/admin\/site-builder$/);
  await page.locator('h1').click();await shot('site-design-build-en.png');
  await page.goto('/admin/appearance/identity');await shot('site-design-identity-en.png');
  await setLocale(page,'ar');await shot('site-design-identity-ar.png');
  await page.setViewportSize({width:390,height:844});await shot('site-design-identity-ar-mobile.png',true);
  await page.setViewportSize({width:1600,height:1040});await setLocale(page,'en');
  await page.goto('/admin/agents/templates');await expect(page.locator('.agent-role-card')).toHaveCount(7);
  await expect(page.locator('.agent-role-card details[open]')).toHaveCount(0);await shot('agent-roles-en.png');
  await setLocale(page,'ar');await shot('agent-roles-ar.png');
  await page.setViewportSize({width:390,height:844});await shot('agent-roles-ar-mobile.png',true);
  await page.setViewportSize({width:1600,height:1040});await setLocale(page,'en');

  // The three installation-method examples are capture-only saved metadata,
  // never assertions of trust, official origin or working external services.
  const extensionDB=db();
  const extensionExamples=[['directory','editorial-handoff','Editorial handoff','تسليم المحتوى'],['upload','media-archive','Media archive','أرشيف الوسائط'],['legacy_unknown','catalog-sync','Catalog sync','مزامنة الدليل']];
  for(const[source,slug,en,ar]of extensionExamples){const manifest={slug,name:{en,ar},version:'1.0.0',endpoint:{url:'https://example.test/demo-extension',auth:'hmac'},hooks:[],permissions:[]};extensionDB.prepare("INSERT INTO extensions(slug,name,version,manifest_json,status,endpoint_url,secret_enc,installed_at,install_source) VALUES(?,?,?,?,'installed',?,'',?,?)").run(slug,en,'1.0.0',JSON.stringify(manifest),manifest.endpoint.url,new Date().toISOString(),source);}extensionDB.close();
  await page.goto('/admin/extensions');for(const[source,slug]of extensionExamples)await expect(page.locator(`[data-install-source="${source}"] [data-ext="${slug}"]`)).toHaveCount(1);
  await shot('extensions-methods-en.png',true);await setLocale(page,'ar');await page.setViewportSize({width:390,height:844});await shot('extensions-methods-ar-mobile.png',true);
  await page.setViewportSize({width:1600,height:1040});await setLocale(page,'en');

  // A real local HTTP provider returns transparent scripted demo suggestions.
  // Preview makes ZERO model calls; generation follows separate explicit consent.
  let demoLocale='en',calls=0;const seenRequests:any[]=[];
  const provider=createServer(async(req,res)=>{let body='';for await(const part of req)body+=part;seenRequests.push(JSON.parse(body));calls++;
    const examples=demoLocale==='en'?[['Bilingual launch brief','Connect a product story and a clear brief; prepare a private draft for editorial review.'],['Content review checklist','Collect draft notes and review claims, links and language before publishing.'],['Weekly editorial plan','Turn approved topics into a short private plan; choose an executor later.']]:[['موجز إطلاق بلغتين','اجمع قصة المنتج والتعليمات، وجهّز مسودة خاصة يراجعها المحرر.'],['قائمة مراجعة المحتوى','راجع الادعاءات والروابط واللغة قبل اتخاذ قرار النشر.'],['خطة تحرير أسبوعية','حوّل الموضوعات المعتمدة إلى خطة خاصة، ثم اختر المنفّذ لاحقًا.']];
    const suggestions=examples.map(([title,summary])=>({title,summary,cards:[{kind:'prompt',title:demoLocale==='en'?'Instructions for review':'تعليمات للمراجعة',body:demoLocale==='en'?'Prepare a private draft from approved context. Never publish or send automatically.':'جهّز مسودة خاصة من السياق المعتمد. لا تنشر أو ترسل تلقائيًا.',x:24,y:24},{kind:'agent',title:demoLocale==='en'?'Choose your assistant':'اختر مساعدك',body:'',x:420,y:24}],edges:[{from:0,to:1}]}));
    res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',content:JSON.stringify({suggestions})}}]}));});
  await new Promise<void>(resolve=>provider.listen(0,'127.0.0.1',resolve));
  try{
    const address=provider.address() as {port:number};await page.goto('/admin/settings/ai');
    const configured=await request.post('/admin/settings/ai',{form:{_csrf:await csrf(page),edit_token:await page.locator('[name=edit_token]').inputValue(),provider:'openai',base_url:`http://127.0.0.1:${address.port}/v1`,model:'local-scripted-demo'},maxRedirects:0});expect(configured.status()).toBe(303);
    for(const locale of ['en','ar']){demoLocale=locale;await page.goto('/admin/canvases');await setLocale(page,locale);await page.setViewportSize({width:locale==='en'?1600:390,height:locale==='en'?1040:844});const ai=page.locator('[data-canvas-ai-suggestions]');await expect(ai).toBeVisible();const before=calls;
      await ai.locator('[data-ai-refresh]').click();await expect(ai.locator('[data-ai-review]')).toBeVisible();expect(calls).toBe(before);await expect(ai.locator('[data-ai-preview]')).toContainText('local-scripted-demo');await expect(ai.locator('[data-ai-confirm]')).toBeDisabled();
      const preview=JSON.parse(await ai.locator('[data-ai-preview]').innerText());await page.screenshot({path:testInfo.outputPath(`ai-exact-review-${locale}.png`),fullPage:true});
      await ai.locator('[data-ai-consent]').check();await ai.locator('[data-ai-confirm]').click();await expect(ai.locator('[data-ai-grid] article')).toHaveCount(3);expect(calls).toBe(before+1);expect(seenRequests.at(-1).messages[0].content).toBe(preview.system);expect(seenRequests.at(-1).messages[1].content).toBe(preview.user);
      await expect(ai.locator('[data-ai-review]')).toBeHidden();await shot(locale==='en'?'canvas-ai-suggestions-en.png':'canvas-ai-suggestions-ar-mobile.png',locale==='ar');
    }
  }finally{await new Promise<void>(resolve=>provider.close(()=>resolve()));const cleanup=db();cleanup.prepare("DELETE FROM settings WHERE key LIKE 'ai.%'").run();cleanup.close();}
  await page.setViewportSize({width:1600,height:1040});await setLocale(page,'en');

  const created=await mcp(request,agent,'tools/call',{name:'create_content',arguments:{type:'post',title:'Campaign launch email · ready for review',body:'<p>A synthetic campaign draft awaiting the editor’s decision.</p>'}});
  const draftID=toolPayload(created.body.result).data.id;
  const pending=await mcp(request,agent,'tools/call',{name:'publish_content',arguments:{id:draftID}});
  expect(JSON.stringify(toolPayload(pending.body.result))).toContain('pending_approval');
  await setLocale(page,'ar');
  await page.goto('/admin/agents/approvals');
  await shot('approvals-ar.png');
  // The public theme keeps the same fictional Atlas Studio identity, with neutral
  // synthetic artwork; it is not a customer site or product availability claim.
  await page.goto('/admin/media');
  await page.locator('#media-file').setInputFiles(resolve('fixtures/theme-preview.svg'));
  await expect(page.locator('#media-results')).toContainText('theme-preview');
  const themeDB=db();
  const themeMedia=themeDB.prepare('SELECT id FROM media WHERE filename=? ORDER BY created_at DESC,rowid DESC LIMIT 1').get('theme-preview.svg') as any;
  for(const id of ids.slice(0,2))themeDB.prepare('UPDATE content SET featured_media_id=? WHERE id=?').run(themeMedia.id,id);
  themeDB.prepare("UPDATE settings SET value_json=? WHERE key='site.title'").run(JSON.stringify('Atlas Studio'));
  themeDB.close();
  await setLocale(page,'en');
  await page.goto('/en/');
  await expect(page.locator('html')).toHaveAttribute('lang','en');
  await shot('theme-home-en.png',true);
  const arabicThemeDB=db();
  arabicThemeDB.prepare("UPDATE settings SET value_json=? WHERE key='site.title'").run(JSON.stringify('Atlas Studio'));
  arabicThemeDB.close();
  await page.setViewportSize({width:390,height:844});
  await page.goto('/ar/post/your-next-idea');
  await expect(page.locator('html')).toHaveAttribute('lang','ar');
  await shot('theme-post-ar-mobile.png',true);
  if(process.env.README_CANVAS_ONLY!=='1')expect([...new Set(expectedImages)].filter(name=>!captured.has(name))).toEqual([]);
  expect(errors).toEqual([]);
});
