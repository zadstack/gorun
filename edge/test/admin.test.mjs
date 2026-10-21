import {before,after,test} from 'node:test';
import assert from 'node:assert/strict';
import {randomBytes} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import {Miniflare,convertV4MiniflareOptions} from 'miniflare';
const modules=['index','admin','admin-auth'].map(name=>({type:'ESModule',path:new URL('../dist/'+name+'.js',import.meta.url).pathname}));
import {ownerHash,context,sign,hash} from '../dist/admin-auth.js';
import {save,publish,discard} from '../dist/admin.js';
let mf,db,key=randomBytes(32).toString('base64url');
const secret=randomBytes(32).toString('base64url');
const env={SITE_ID:'demo',EDGE_ADMIN_ENABLED:'true',EDGE_ADMIN_ORIGIN:'https://edge.example',EDGE_ADMIN_SECRET:secret};
const runtime=bindings=>new Miniflare(convertV4MiniflareOptions({modules,compatibilityDate:'2026-10-01',bindings,d1Databases:{DB:'admin-test'},r2Buckets:{MEDIA:'admin-test'}}));
const request=(path,init={})=>mf.dispatchFetch('https://edge.example'+path,{redirect:'manual',...init});
function absorb(jar,response){const all=response.headers.getSetCookie?.()??[response.headers.get('set-cookie')??''];for(const line of all){const [a]=line.split(';');const [k,v]=a.split('=');if(v)jar[k]=v;else delete jar[k];}}
const cookies=jar=>Object.entries(jar).map(([k,v])=>k+'='+v).join('; ');
async function post(path,fields,jar,extra={}) {const r=await request(path,{method:'POST',headers:{origin:'https://edge.example','content-type':'application/x-www-form-urlencoded',cookie:cookies(jar),...extra},body:new URLSearchParams(fields).toString()});absorb(jar,r);return r;}
async function login(){const jar={};const r=await request('/admin/login');absorb(jar,r);const token=(await r.text()).match(/name="challenge" value="([^"]+)"/)[1];const result=await post('/admin/login',{challenge:token,key},jar);assert.equal(result.status,303);const home=await request('/admin',{headers:{cookie:cookies(jar)}});const csrf=(await home.text()).match(/name="csrf" value="([^"]+)"/)[1];return{jar,csrf};}
const input=(id,extra={})=>({id,draft_version:'0',source_version:'0',type:'post',slug:id,locale:'en',title:'Title '+id,excerpt:'Summary',body_text:'Body '+id,...extra});
async function counts(){return Promise.all(['edge_drafts','edge_content','edge_revisions','edge_audit'].map(async t=>(await db.prepare('SELECT count(*) n FROM '+t).first()).n));}
before(async()=>{
 mf=runtime(env);db=await mf.getD1Database('DB');
 const one=await readFile(new URL('../migrations/0001_readonly.sql',import.meta.url),'utf8');for(const s of one.split(';').map(s=>s.trim()).filter(Boolean))await db.prepare(s).run();
 await db.exec((await readFile(new URL('../migrations/0002_owner_admin.sql',import.meta.url),'utf8')).split('\n').filter(s=>s.trim()&&!s.trim().startsWith('--')).join('\n'));
 const managers=(await readFile(new URL('../migrations/0003_managers.sql',import.meta.url),'utf8')).split('\n').filter(s=>s.trim()&&!s.trim().startsWith('--')).join('\n');
 for(const statement of managers.split(';').map(s=>s.trim()).filter(Boolean)) await db.prepare(statement).run();
 await db.prepare("INSERT INTO edge_sites VALUES ('demo','Demo','en'),('other','Other','en')").run();
 await db.prepare('INSERT INTO edge_owners(site_id,key_hash) VALUES(?1,?2)').bind('demo',await ownerHash('demo',key)).run();
});
after(async()=>mf?.dispose());
test('default-off/invalid origin has no admin/bootstrap; HTTPS cookies and challenge are strict',async()=>{
 const off=runtime({SITE_ID:'demo'});try{assert.equal((await off.dispatchFetch('https://edge.example/admin')).status,404);}finally{await off.dispose();}
 const r=await request('/admin/login');assert.equal(r.status,200);assert.equal(r.headers.get('referrer-policy'),'same-origin');const cookie=r.headers.get('set-cookie');assert.match(cookie,/__Host-edge_login=/);assert.match(cookie,/Secure/);assert.match(cookie,/HttpOnly/);assert.match(cookie,/SameSite=Strict/);
 assert.equal((await mf.dispatchFetch('https://wrong.example/admin/login')).status,404);assert.equal((await request('/admin/bootstrap')).status,404);
 const jar={};absorb(jar,r);const challenge=(await r.text()).match(/name="challenge" value="([^"]+)"/)[1];assert.equal((await post('/admin/login',{challenge,key},jar,{origin:'https://evil.example'})).status,403);assert.equal((await post('/admin/login',{challenge:'bad',key},jar)).status,403);
});
test('invalid-key budget is bounded and cannot lock the legitimate owner out',async()=>{
 for(let i=0;i<12;i++){const r=await request('/admin/login'),jar={};absorb(jar,r);const challenge=(await r.text()).match(/name="challenge" value="([^"]+)"/)[1];const out=await post('/admin/login',{challenge,key:'invalid'},jar);assert.equal(out.status,i>=10?429:401);}
 assert.equal((await db.prepare('SELECT count(*) n FROM edge_login_limits').first()).n,1);await login();
});
test('CSRF/origin/body limits reject writes; drafts remain private until exact review approval',async()=>{
 const {jar,csrf}=await login(),fields=input('reviewed');
 assert.equal((await post('/admin/save',{...fields,csrf:'wrong'},jar)).status,403);
 assert.equal((await post('/admin/save',{...fields,csrf},jar,{origin:'https://evil.example'})).status,403);
 assert.equal((await post('/admin/save',{...fields,csrf,body_text:'x'.repeat(140000)},jar)).status,400);
 assert.equal((await post('/admin/save',{...fields,csrf},jar)).status,303);assert.equal((await request('/post/reviewed')).status,404);
 const review=await post('/admin/review',{csrf,id:'reviewed',draft_version:'1',source_version:'0'},jar);assert.equal(review.status,200);const token=(await review.text()).match(/name="review" value="([^"]+)"/)[1];
 assert.equal((await post('/admin/publish',{csrf,id:'reviewed',draft_version:'1',source_version:'0',review:token},jar)).status,403);
 assert.equal((await post('/admin/publish',{csrf,id:'reviewed',draft_version:'1',source_version:'0',review:token,approve:'yes'},jar)).status,303);assert.equal((await request('/post/reviewed')).status,200);
 assert.equal((await post('/admin/publish',{csrf,id:'reviewed',draft_version:'1',source_version:'0',review:token,approve:'yes'},jar)).status,409);
});
test('draft edits preserve published version, reject stale saves/reviews without side effects',async()=>{
 const {jar,csrf}=await login();const fields=input('reviewed',{source_version:'1',title:'New <script>title</script>',body_text:'<script>alert(1)</script>'});
 assert.equal((await post('/admin/save',{...fields,csrf},jar)).status,303);assert.ok(!(await(await request('/post/reviewed')).text()).includes('New'));
 const before=await counts();assert.equal((await post('/admin/save',{...fields,csrf},jar)).status,409);assert.deepEqual(await counts(),before);
 const review=await post('/admin/review',{csrf,id:'reviewed',draft_version:'1',source_version:'1'},jar);const token=(await review.text()).match(/name="review" value="([^"]+)"/)[1];
 assert.equal((await post('/admin/save',{...fields,csrf,draft_version:'1',title:'Changed again'},jar)).status,303);
 const count=await counts();assert.equal((await post('/admin/publish',{csrf,id:'reviewed',draft_version:'1',source_version:'1',review:token,approve:'yes'},jar)).status,409);assert.deepEqual(await counts(),count);
});
test('owner recovery between authenticated read and atomic save cannot append draft/audit/revision',async()=>{
 const {jar}=await login();const bindings=await mf.getBindings();const c=await context(new Request('https://edge.example/admin',{headers:{cookie:cookies(jar)}}),{...env,...bindings});assert.ok(c.session);
 key=randomBytes(32).toString('base64url');await db.prepare('UPDATE edge_owners SET key_hash=?1,auth_epoch=auth_epoch+1 WHERE site_id=?2').bind(await ownerHash('demo',key),'demo').run();
 const before=await counts();assert.equal(await save(c,'must-not-save',{type:'post',slug:'must-not-save',locale:'en',title:'no',excerpt:'',body_text:'no'},0,0),false);assert.deepEqual(await counts(),before);
 assert.equal((await request('/admin',{headers:{cookie:cookies(jar)}})).status,303);assert.equal((await db.prepare('SELECT count(*) n FROM edge_sessions').first()).n,0);
});
test('owner/session expiry is checked by D1 at write, content export excludes auth and other sites',async()=>{
 const {jar,csrf}=await login(),bindings=await mf.getBindings();const c=await context(new Request('https://edge.example/admin',{headers:{cookie:cookies(jar)}}),{...env,...bindings});
 await db.prepare('UPDATE edge_sessions SET expires_at=? WHERE site_id=? AND token_hash=?').bind(Date.now()-1000,'demo',c.session.token_hash).run();const before=await counts();assert.equal(await save(c,'expired',{type:'post',slug:'expired',locale:'en',title:'no',excerpt:'',body_text:'no'},0,0),false);assert.deepEqual(await counts(),before);
 const fresh=await login();await db.prepare("INSERT INTO edge_content(site_id,id,type,slug,locale,title,body_text,status) VALUES('other','secret','post','secret','en','OTHER SECRET','secret','private')").run();
 const exported=await request('/admin/export',{headers:{cookie:cookies(fresh.jar)}}),text=await exported.text();assert.equal(exported.status,200);for(const forbidden of ['key_hash','token_hash','csrf','EDGE_ADMIN_SECRET','OTHER SECRET'])assert.ok(!text.includes(forbidden));
 assert.equal((await post('/admin/logout',{csrf:fresh.csrf},fresh.jar)).status,303);assert.equal((await request('/admin',{headers:{cookie:cookies(fresh.jar)}})).status,303);
});
test('cross-site restore/IDs are isolated; unpublish revokes public content and audit is scoped',async()=>{
 const {jar,csrf}=await login();assert.equal((await post('/admin/restore',{csrf,id:'reviewed',draft_version:'2',source_version:'1',revision:'other-site-revision'},jar)).status,404);
 assert.equal((await post('/admin/unpublish',{csrf,id:'reviewed',draft_version:'2',source_version:'1',confirm:'yes'},jar)).status,303);assert.equal((await request('/post/reviewed')).status,404);
 const html=await(await request('/admin?lang=ar',{headers:{cookie:cookies(jar)}})).text();assert.match(html,/dir="rtl"/);assert.match(html,/admin-workspace/);assert.match(html,/admin-sidebar/);assert.match(html,/محتوى جديد/);assert.equal(html.includes('<script'),false);assert.ok(!html.includes('OTHER SECRET'));
});

test('login challenge is consumed and expired signed challenges fail',async()=>{
 const r=await request('/admin/login'),jar={};absorb(jar,r);const challenge=(await r.text()).match(/name="challenge" value="([^"]+)"/)[1],replay={...jar};
 assert.equal((await post('/admin/login',{challenge,key},jar)).status,303);const count=(await db.prepare('SELECT count(*) n FROM edge_sessions').first()).n;
 assert.equal((await post('/admin/login',{challenge,key},replay)).status,409);assert.equal((await db.prepare('SELECT count(*) n FROM edge_sessions').first()).n,count);
 const bindings=await mf.getBindings(),c=await context(new Request('https://edge.example/admin'),{...env,...bindings});
 const expired=await sign(c,{nonce:randomBytes(32).toString('base64url'),issued:Date.now()-400000,epoch:c.owner.auth_epoch});
 assert.equal((await post('/admin/login',{challenge:expired,key},{'__Host-edge_login':expired})).status,403);
});
test('discard serializes once: expiry before write is no-op, expiry after marker cannot partially audit',async()=>{
 const {jar}=await login(),bindings=await mf.getBindings(),c=await context(new Request('https://edge.example/admin',{headers:{cookie:cookies(jar)}}),{...env,...bindings});
 const b={type:'post',slug:'clock-cross',locale:'en',title:'Clock',excerpt:'',body_text:'Clock'};assert.equal(await save(c,'clock-cross',b,0,0),true);
 await db.prepare('UPDATE edge_sessions SET expires_at=0 WHERE site_id=? AND token_hash=?').bind('demo',c.session.token_hash).run();const before=await counts();assert.equal(await discard(c,'clock-cross',1),false);assert.deepEqual(await counts(),before);
 const fresh=await login(),freshContext=await context(new Request('https://edge.example/admin',{headers:{cookie:cookies(fresh.jar)}}),{...env,...bindings});
 await db.exec("CREATE TRIGGER test_expiry AFTER UPDATE OF op_token ON edge_drafts WHEN NEW.id='clock-cross' BEGIN UPDATE edge_sessions SET expires_at=0 WHERE site_id=NEW.site_id; END;");
 const auditBefore=(await db.prepare('SELECT count(*) n FROM edge_audit').first()).n;assert.equal(await discard(freshContext,'clock-cross',1),true);assert.equal(await db.prepare("SELECT id FROM edge_drafts WHERE id='clock-cross'").first(),null);assert.equal((await db.prepare('SELECT count(*) n FROM edge_audit').first()).n,auditBefore+1);await db.exec('DROP TRIGGER test_expiry;');
});
test('content filters and the editor show status without changing publication',async()=>{
 const {jar,csrf}=await login();
 assert.equal((await post('/admin/save',{...input('filter-ar',{locale:'ar',title:'مسودة عربية'}),csrf},jar)).status,303);
 for(let i=0;i<100;i++){const id='pad-'+String(i).padStart(3,'0');await db.prepare("INSERT INTO edge_content(site_id,id,type,slug,locale,title,status) VALUES('demo',?1,'post',?1,'en',?2,'private')").bind(id,'AAA '+id).run();}
 const html=await(await request('/admin?status=draft&locale=ar&q='+encodeURIComponent('مسودة'),{headers:{cookie:cookies(jar)}})).text();
 assert.match(html,/مسودة عربية/);assert.match(html,/aria-current="page"/);assert.equal(html.includes('reviewed'),false);assert.equal(html.includes('The list stops at 100 matches.'),false);
 const all=await(await request('/admin',{headers:{cookie:cookies(jar)}})).text();assert.match(all,/The list stops at 100 matches\./);assert.equal(all.includes('مسودة عربية'),false);
 const folded=await(await request('/admin?q='+encodeURIComponent('aaa pad-000'),{headers:{cookie:cookies(jar)}})).text();assert.match(folded,/AAA pad-000/);
 const wild=await(await request('/admin?q='+encodeURIComponent('%_'),{headers:{cookie:cookies(jar)}})).text();assert.equal(wild.includes('AAA pad-000'),false);
 assert.equal((await request('/admin?status=nope',{headers:{cookie:cookies(jar)}})).status,400);
 const editor=await(await request('/admin/edit?id=reviewed&lang=en',{headers:{cookie:cookies(jar)}})).text();
 assert.match(editor,/class="badge private"/);assert.equal(editor.includes('href="/post/reviewed"'),false);
});
test('Unicode search scans past 100 rows, keeps literal punctuation and applies the match cap after filters',async()=>{
 const {jar,csrf}=await login();
 const insert=async(site,id,title,status='private',locale='en')=>db.prepare("INSERT INTO edge_content(site_id,id,type,slug,locale,title,status,published_at) VALUES(?1,?2,'post',?2,?3,?4,?5,'2026-10-01T00:00:00Z')").bind(site,id,locale,title,status).run();
 for(let i=0;i<100;i++)await insert('demo','unicode-padding-'+i,'AAA Unicode padding '+i);
 await db.prepare(`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<100) INSERT INTO edge_drafts(site_id,id,version,source_version,body_json,op_token,updated_at) SELECT 'demo','unicode-null-'||i,1,0,'{"type":"page","locale":"en"}','','2026-10-01T00:00:00Z' FROM n`).run();
 for(let i=0;i<101;i++)await insert('demo','unicode-match-'+String(i).padStart(3,'0'),'ÉCOLE repeated');
 await insert('other','unicode-other-site','ÉCOLE repeated');
 await insert('demo','unicode-other-locale','ÉCOLE repeated','private','ar');
 await insert('demo','unicode-other-status','ÉCOLE repeated','published');
 await insert('demo','unicode-literal','ÉCOLE literal %_');
 await insert('demo','unicode-not-literal','ÉCOLE literal %X');
 for(const [id,title] of [['unicode-cyrillic','МОСКВА'],['unicode-greek','ΑΘΉΝΑ']]) {
  assert.equal((await post('/admin/save',{...input(id,{title,locale:'ar'}),csrf},jar)).status,303);
 }
 const search=async(q,status='private',locale='en')=>{
  const result=await request('/admin?'+new URLSearchParams({q,status,locale}),{headers:{cookie:cookies(jar)}});
  assert.equal(result.status,200);return result.text();
 };
 const ids=html=>[...html.matchAll(/href="\/admin\/edit\?id=([^"&]+)/g)].map(m=>m[1]);
 const matches=await search('école repeated');
 assert.deepEqual(ids(matches),Array.from({length:100},(_,i)=>'unicode-match-'+String(i).padStart(3,'0')));
 assert.match(matches,/The list stops at 100 matches\./);
 assert.deepEqual(ids(await search('école repeated','','en')),ids(matches));
 assert.deepEqual(ids(await search('école literal %_')),['unicode-literal']);
 assert.deepEqual(ids(await search('%_')),['unicode-literal']);
 assert.deepEqual(ids(await search('москва','draft','ar')),['unicode-cyrillic']);
 assert.deepEqual(ids(await search('αθήνα','draft','ar')),['unicode-greek']);
 assert.deepEqual(ids(await search('москва','private','ar')),[]);
});
test('an incomplete search reports its scan limit instead of a successful empty list',async()=>{
 const {jar}=await login();
 const {n:existing}=await db.prepare("SELECT count(*) n FROM edge_content WHERE site_id='demo' AND status='private' AND locale='ar'").first();
 await db.prepare(`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<?1) INSERT INTO edge_content(site_id,id,type,slug,locale,title,status) SELECT 'demo','scan-budget-'||i,'post','scan-budget-'||i,'ar','Scan budget padding '||i,'private' FROM n`).bind(4000-existing).run();
 try {
  const response=await request('/admin?status=private&locale=ar&q=not-a-match',{headers:{cookie:cookies(jar)}});
  assert.equal(response.status,503);
  const html=await response.text();assert.match(html,/Search scan limit reached/);
  assert.equal(html.includes('No content matches these filters.'),false);
  assert.equal(html.includes('The list stops at 100 matches.'),false);
  await db.prepare("DELETE FROM edge_content WHERE site_id='demo' AND id='scan-budget-0'").run();
  const complete=await request('/admin?status=private&locale=ar&q=not-a-match',{headers:{cookie:cookies(jar)}});
  assert.equal(complete.status,200);assert.match(await complete.text(),/No content matches these filters\./);
 } finally {await db.prepare("DELETE FROM edge_content WHERE site_id='demo' AND id LIKE 'scan-budget-%'").run();}
});
test('primary manager key is shown once and whoami accepts only that key',async()=>{
 const {jar,csrf}=await login();
 const hashOf=async()=>(await db.prepare("SELECT key_hash FROM edge_managers WHERE site_id='demo' AND primary_manager=1").first())?.key_hash??'';
 const created=await post('/admin/managers/create',{csrf,label:'GoRun Full'},jar);assert.equal(created.status,303);
 assert.equal(created.headers.get('location'),'/admin/managers?lang=en');
 const managerKey=jar['__Host-edge_manager'];assert.match(managerKey,/^[A-Za-z0-9_-]{43}$/);
 assert.equal((await created.text()).includes(managerKey),false);assert.equal(created.headers.get('location').includes(managerKey),false);
 assert.match(created.headers.get('set-cookie'),/Max-Age=120/);assert.match(created.headers.get('set-cookie'),/HttpOnly/);
 assert.match(created.headers.get('set-cookie'),/; Secure/);assert.match(created.headers.get('set-cookie'),/SameSite=Strict/);assert.match(created.headers.get('set-cookie'),/Path=\//);
 assert.equal(created.headers.get('cache-control'),'private, no-store');
 const stored=await hashOf();
 const page=await request('/admin/managers',{headers:{cookie:cookies(jar)}});const html=await page.text();absorb(jar,page);
 assert.equal(page.headers.get('cache-control'),'private, no-store');assert.equal(html.includes(stored),false);
 assert.equal((html.match(new RegExp(managerKey,'g'))??[]).length,2);assert.equal(jar['__Host-edge_manager'],undefined);
 const again=await(await request('/admin/managers',{headers:{cookie:cookies(jar)}})).text();
 assert.equal(again.includes(managerKey),false);assert.equal(await hashOf(),stored);assert.match(again,/GoRun Full/);assert.match(again,new RegExp(managerKey.slice(-4)));
 const ok=await request('/api/v1/manager',{headers:{authorization:'Bearer '+managerKey}});assert.equal(ok.status,200);
 const body=await ok.json();assert.equal(body.ok,true);assert.equal(body.data.primary,true);assert.equal(body.data.site_id,'demo');assert.equal(body.data.label,'GoRun Full');
 assert.equal(JSON.stringify(body).includes(managerKey),false);
 assert.equal((await request('/api/v1/manager')).status,401);
 assert.equal((await request('/api/v1/manager',{headers:{authorization:'Bearer '+key}})).status,401);
 const exported=await(await request('/admin/export',{headers:{cookie:cookies(jar)}})).text();
 assert.equal(exported.includes(managerKey),false);assert.equal(exported.includes('key_hash'),false);
 const rotated=await post('/admin/managers/rotate',{csrf,id:body.data.manager_id},jar);assert.equal(rotated.status,303);
 const next=jar['__Host-edge_manager'];assert.notEqual(next,managerKey);assert.equal((await rotated.text()).includes(next),false);
 assert.equal((await request('/api/v1/manager',{headers:{authorization:'Bearer '+managerKey}})).status,401);
 const rotatedPage=await request('/admin/managers',{headers:{cookie:cookies(jar)}});const rotatedHtml=await rotatedPage.text();absorb(jar,rotatedPage);
 assert.equal((rotatedHtml.match(new RegExp(next,'g'))??[]).length,2);
 const rotatedHash=await hashOf();
 const rotatedAgain=await(await request('/admin/managers',{headers:{cookie:cookies(jar)}})).text();
 assert.equal(rotatedAgain.includes(next),false);assert.equal(await hashOf(),rotatedHash);
 assert.equal((await request('/api/v1/manager',{headers:{authorization:'Bearer '+next}})).status,200);
 assert.equal((await post('/admin/managers/create',{csrf,label:'Second'},jar)).status,409);
 assert.equal((await post('/admin/managers/revoke',{csrf,id:body.data.manager_id,confirm:'yes'},jar)).status,303);
 assert.equal((await request('/api/v1/manager',{headers:{authorization:'Bearer '+next}})).status,401);
 const cleared=await(await request('/admin/managers',{headers:{cookie:cookies(jar)}})).text();
 assert.equal(cleared.includes(next),false);assert.match(cleared,/Create GoRun key|إنشاء مفتاح GoRun/);
});
test('pending manager keys are not shown after rotation or revocation and logout clears the handoff',async()=>{
 const {jar,csrf}=await login();
 assert.equal((await post('/admin/managers/create',{csrf,label:'Pending key'},jar)).status,303);
 const firstKey=jar['__Host-edge_manager'];
 const {id}=await db.prepare("SELECT id FROM edge_managers WHERE site_id='demo' AND primary_manager=1").first();
 assert.equal((await post('/admin/managers/rotate',{csrf,id},jar)).status,303);
 const nextKey=jar['__Host-edge_manager'];assert.notEqual(firstKey,nextKey);
 const stale=await request('/admin/managers',{headers:{cookie:cookies({...jar,'__Host-edge_manager':firstKey})}});
 const staleHTML=await stale.text();assert.equal(staleHTML.includes(firstKey),false);assert.equal(staleHTML.includes('data-manager-key='),false);
 assert.match(stale.headers.get('set-cookie'),/Max-Age=0/);
 assert.equal((await post('/admin/managers/revoke',{csrf,id,confirm:'yes'},jar)).status,303);
 assert.equal(jar['__Host-edge_manager'],undefined);
 const revoked=await request('/admin/managers',{headers:{cookie:cookies({...jar,'__Host-edge_manager':nextKey})}});
 assert.equal((await revoked.text()).includes(nextKey),false);
 assert.equal((await post('/admin/managers/create',{csrf,label:'Logout key'},jar)).status,303);
 assert.ok(jar['__Host-edge_manager']);
 assert.equal((await post('/admin/logout',{csrf},jar)).status,303);assert.equal(jar['__Host-edge_manager'],undefined);
 const fresh=await login();
 const current=await db.prepare("SELECT id FROM edge_managers WHERE site_id='demo' AND primary_manager=1").first();
 assert.equal((await post('/admin/managers/revoke',{csrf:fresh.csrf,id:current.id,confirm:'yes'},fresh.jar)).status,303);
});
test('publication rejects recovery between review and atomic execution without side effects',async()=>{
 const {jar}=await login(),bindings=await mf.getBindings(),c=await context(new Request('https://edge.example/admin',{headers:{cookie:cookies(jar)}}),{...env,...bindings});
 assert.equal(await save(c,'recovery-publish',{type:'post',slug:'recovery-publish',locale:'en',title:'Recovery',excerpt:'',body_text:'private'},0,0),true);
 const d=await db.prepare("SELECT * FROM edge_drafts WHERE site_id='demo' AND id='recovery-publish'").first();
 const review={id:d.id,dv:d.version,sv:d.source_version,session:c.session.token_hash,epoch:c.session.owner_epoch,expires:Date.now()+60000,body:await hash(d.body_json)};
 key=randomBytes(32).toString('base64url');await db.prepare('UPDATE edge_owners SET key_hash=?,auth_epoch=auth_epoch+1 WHERE site_id=?').bind(await ownerHash('demo',key),'demo').run();const before=await counts();assert.equal(await publish(c,d.id,d,review),false);assert.deepEqual(await counts(),before);assert.equal((await request('/post/recovery-publish')).status,404);
});
