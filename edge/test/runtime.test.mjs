import {after,before,test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {Miniflare,convertV4MiniflareOptions} from 'miniflare';
const runtime=options=>new Miniflare(convertV4MiniflareOptions(options));
let mf,db,bucket;
const fetch=(path,init)=>mf.dispatchFetch('https://edge.example'+path,init);
async function row(id,options={}) {
  const p={site:'demo',type:'post',slug:id,locale:'en',title:id,excerpt:'Summary',body:'Body',status:'published',date:'2026-01-01T00:00:00Z',...options};
  await db.prepare('INSERT INTO edge_content VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10)').bind(p.site,id,p.type,p.slug,p.locale,p.title,p.excerpt,p.body,p.status,p.date).run();
}
async function media(id,key,visibility='public',type='text/plain',size=6) {
  await db.prepare('INSERT INTO edge_media VALUES (?1,?2,?3,?4,?5,?6)').bind('demo',id,key,type,size,visibility).run();
}
before(async()=>{
  mf=runtime({modules:true,scriptPath:new URL('../dist/index.js',import.meta.url).pathname,compatibilityDate:'2026-10-01',bindings:{SITE_ID:'demo'},d1Databases:{DB:'edge-test'},r2Buckets:{MEDIA:'edge-test'}});
  db=await mf.getD1Database('DB');bucket=await mf.getR2Bucket('MEDIA');
  const sql=await readFile(new URL('../migrations/0001_readonly.sql',import.meta.url),'utf8');
  for(const statement of sql.split(';').map(s=>s.trim()).filter(Boolean)) await db.prepare(statement).run();
  await db.prepare("INSERT INTO edge_sites VALUES ('demo','Demo <script>alert(1)</script>','en'),('other','Other site','en')").run();
  await row('welcome');await row('draft',{status:'draft',date:null});await row('private',{status:'private'});await row('future',{date:'2999-01-01T00:00:00Z'});await row('secret-other',{site:'other'});await row('arabic',{locale:'ar',title:'العربية'});
});
after(async()=>{await mf?.dispose();});

test('real workerd serves site-scoped published summaries without body/private fields',async()=>{
  const r=await fetch('/api/v1/content');assert.equal(r.status,200);const p=await r.json();assert.deepEqual(p.data.map(a=>a.id),['welcome']);assert.equal(p.data[0].body_text,undefined);
  assert.equal(r.headers.get('cache-control'),'no-store');assert.equal(r.headers.get('access-control-allow-origin'),null);
});
test('draft/private/future/other site content is never public in HTML or JSON',async()=>{
  for(const id of ['draft','private','future','secret-other','missing']) for(const prefix of ['/post/','/api/v1/content/post/']) assert.equal((await fetch(prefix+id)).status,404,id);
  const home=await(await fetch('/')).text();for(const id of ['draft','private','future','secret-other'])assert.ok(!home.includes(id));
});
test('content and site names are escaped; Arabic routes retain language and direction',async()=>{
  await row('xss',{title:'<img src=x onerror=alert(1)>',body:'<script>fetch("/private")</script>'});
  const r=await fetch('/post/xss'),body=await r.text();assert.ok(!body.includes('<script>'));assert.ok(!body.includes('<img src=x'));assert.match(body,/&lt;script&gt;/);assert.match(r.headers.get('content-security-policy'),/default-src 'none'/);assert.equal(r.headers.get('x-content-type-options'),'nosniff');
  const ar=await(await fetch('/ar/post/arabic')).text();assert.match(ar,/<html lang="ar" dir="rtl">/);assert.match(ar,/العربية/);
  const json=await(await fetch('/api/v1/content?locale=ar')).json();assert.equal(json.data[0].id,'arabic');
});
test('bounded strict queries and paths cannot inject SQL or select another site/status',async()=>{
  for(const q of ['?site_id=other','?status=private','?per_page=51','?per_page=-1','?page=101','?page=1&page=2','?type=anything','?locale=zz',"?type=post%27%20OR%201=1--"])
    assert.equal((await fetch('/api/v1/content'+q)).status,400,q);
  for(const p of ['/post/welcome%2Fprivate','/post/%ZZ','/api/v1/content/post/welcome?status=draft','/media/key?key=private']) assert.ok([400,404].includes((await fetch(p)).status),p);
});
test('every write method is rejected and admin/manager/MCP routes are absent',async()=>{
  for(const method of ['POST','PUT','PATCH','DELETE','OPTIONS']){
    const r=await fetch('/api/v1/content',{method,body:method==='OPTIONS'?undefined:'{}'});assert.equal(r.status,405);assert.equal(r.headers.get('allow'),'GET, HEAD');
  }
  for(const p of ['/admin','/admin/setup','/mcp','/webhooks/canvas/test','/api/v1/managers'])assert.equal((await fetch(p)).status,404);
});
test('public media requires metadata, matching namespace, allowed MIME and real object size',async()=>{
  await bucket.put('sites/demo/public/readme.txt','public',{httpMetadata:{contentType:'text/html',cacheControl:'public, max-age=99999'}});
  await media('readme','sites/demo/public/readme.txt');
  const r=await fetch('/media/readme');assert.equal(r.status,200);assert.equal(await r.text(),'public');assert.equal(r.headers.get('content-type'),'text/plain');assert.equal(r.headers.get('content-disposition'),'attachment');assert.equal(r.headers.get('cache-control'),'no-store');
  const head=await fetch('/media/readme',{method:'HEAD'});assert.equal(head.status,200);assert.equal(await head.text(),'');assert.equal(head.headers.get('content-length'),'6');
  await media('private-file','sites/demo/public/readme.txt','private');await media('other-site','sites/other/public/private.txt');await bucket.put('sites/other/public/private.txt','secret');
  await media('wrong-area','sites/demo/private/file.txt');await bucket.put('sites/demo/private/file.txt','secret');
  await media('unsafe','sites/demo/public/readme.txt','public','text/html');await media('wrong-size','sites/demo/public/readme.txt','public','text/plain',7);
  await media('traversal','sites/demo/public/../private/file.txt');await media('missing-object','sites/demo/public/missing.txt');
  for(const id of ['missing','private-file','other-site','wrong-area','unsafe','wrong-size','traversal','missing-object'])assert.equal((await fetch('/media/'+id)).status,404,id);
  assert.equal((await fetch('/media/sites/demo/public/readme.txt')).status,404);
});
test('publication/media revocation immediately affects repeat URLs (no cache leak)',async()=>{
  await row('revocable');assert.equal((await fetch('/post/revocable')).status,200);await db.prepare("UPDATE edge_content SET status='private' WHERE site_id='demo' AND id='revocable'").run();assert.equal((await fetch('/post/revocable')).status,404);
  await media('revocable','sites/demo/public/readme.txt');assert.equal((await fetch('/media/revocable')).status,200);await db.prepare("UPDATE edge_media SET visibility='private' WHERE id='revocable'").run();assert.equal((await fetch('/media/revocable')).status,404);
});
test('HEAD has GET metadata and no response body; readiness uses actual D1 schema',async()=>{
  const r=await fetch('/post/welcome',{method:'HEAD'});assert.equal(r.status,200);assert.equal(await r.text(),'');assert.equal((await fetch('/readyz')).status,200);
  await db.prepare('DELETE FROM edge_schema').run();assert.equal((await fetch('/readyz')).status,503);await db.prepare('INSERT INTO edge_schema VALUES (1)').run();
});
test('missing site/schema/bindings fail closed without exposing storage errors',async()=>{
  const broken=runtime({modules:true,scriptPath:new URL('../dist/index.js',import.meta.url).pathname,compatibilityDate:'2026-10-01',bindings:{SITE_ID:'unknown'},d1Databases:{DB:'empty'},r2Buckets:{MEDIA:'empty'}});
  try{const r=await broken.dispatchFetch('https://edge.example/');assert.equal(r.status,503);assert.deepEqual(await r.json(),{ok:false,error:{code:'unavailable'}});assert.equal((await broken.dispatchFetch('https://edge.example/healthz')).status,200);}finally{await broken.dispose();}
});

test('missing bindings return generic503 while liveness remains independent',async()=>{
  const broken=runtime({modules:true,scriptPath:new URL('../dist/index.js',import.meta.url).pathname,compatibilityDate:'2026-10-01',bindings:{SITE_ID:'demo'}});
  try {assert.equal((await broken.dispatchFetch('https://edge.example/readyz')).status,503);assert.equal((await broken.dispatchFetch('https://edge.example/healthz')).status,200);} finally {await broken.dispose();}
});
