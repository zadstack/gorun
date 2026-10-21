import {type AdminEnv,type Context,type Session,context,random,hash,equal,ownerHash,managerHash,sessionHash,escape as e,reply,redirect,cookie,cookieName,readCookie,sign,verify,form,sameOrigin,guard,guardArgs,audit} from './admin-auth.js';
interface Body { type: string; slug: string; locale: string; title: string; excerpt: string; body_text: string }
interface Draft { id: string; version: number; source_version: number; body_json: string }
const idPattern=/^[a-zA-Z0-9_-]{1,120}$/;
const slugPattern=/^[\p{L}\p{N}][\p{L}\p{N}_-]{0,119}$/u;
const now=()=>new Date().toISOString();
const language=(r: Request)=>new URL(r.url).searchParams.get('lang')==='ar'?'ar':'en';
const tr=(lang: string,en: string,ar: string)=>lang==='ar'?ar:en;
const shellCSS=`:root{--bg:#f6f7f9;--panel:#fff;--panel-2:#edf1f5;--ink:#17202c;--muted:#596575;--line:#e1e6ec;--line-strong:#a5afbc;--green:#167252;--green-hover:#125e44;--green-soft:#dcefe7;--amber:#9a6a08;--amber-text:#7a5406;--amber-soft:#f7eccb;--blue:#256b9b;--radius:7px}*{box-sizing:border-box}body{margin:0;font:15px/1.5 system-ui,sans-serif;color:var(--ink);background:var(--bg)}a{color:var(--green-hover)}button,input,select,textarea{font:inherit;color:inherit}.shell{display:grid;grid-template-columns:13.5rem minmax(0,1fr);min-height:100vh}.admin-sidebar{position:sticky;top:0;display:flex;flex-direction:column;height:100vh;background:var(--panel);border-inline-end:1px solid var(--line)}.brand{display:flex;align-items:center;min-height:3.5rem;padding:0 1rem;border-bottom:1px solid var(--line);color:var(--ink);font-weight:700;text-decoration:none}.admin-sidebar nav{display:flex;flex-direction:column;gap:.25rem;padding:.625rem}.admin-nav-link{display:flex;align-items:center;min-height:2.25rem;padding:.375rem .625rem;border:1px solid transparent;border-radius:var(--radius);color:var(--muted);text-decoration:none;font-size:.8125rem}.admin-nav-link:hover{background:var(--panel-2);color:var(--ink)}.admin-nav-link.is-active{background:var(--green-soft);color:var(--green-hover);border-color:var(--green);font-weight:650}.admin-sidebar-footer{margin-top:auto;display:flex;flex-direction:column;gap:.25rem;padding:.625rem;border-top:1px solid var(--line)}#main{padding:1.75rem 2rem;max-width:70rem}.page-head{display:flex;flex-wrap:wrap;align-items:flex-end;justify-content:space-between;gap:1rem;margin-bottom:1.25rem}.admin-eyebrow{margin:0;font-size:.6875rem;font-weight:650;color:var(--muted)}h1{margin:.125rem 0 .375rem;font-size:1.375rem;line-height:1.3}.muted{color:var(--muted)}.stats{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:.75rem;margin-bottom:1rem}.stat{background:var(--panel);border:1px solid var(--line);border-radius:var(--radius);padding:.875rem 1rem}.stat b{display:block;font-size:1.375rem}.panel,section{background:var(--panel);border:1px solid var(--line);border-radius:var(--radius);padding:1rem;margin:0 0 1rem}.editor{display:grid;grid-template-columns:minmax(0,1fr) 18rem;gap:1rem;align-items:start}label{display:block;margin:.75rem 0 .25rem;font-size:.8125rem;font-weight:650}input,textarea,select{width:100%;max-width:100%;padding:.5rem .625rem;border:1px solid var(--line-strong);border-radius:var(--radius);background:var(--panel)}textarea{min-height:16rem}input[type=checkbox]{width:auto;accent-color:var(--green)}button,.admin-primary-button,.admin-secondary-button{display:inline-flex;align-items:center;justify-content:center;min-height:2.25rem;padding:.375rem .875rem;border:1px solid var(--line-strong);border-radius:var(--radius);background:var(--panel);font-weight:600;font-size:.8125rem;cursor:pointer;text-decoration:none}form button:not(.admin-secondary-button),.admin-primary-button{color:#fff;background:var(--green);border-color:var(--green)}.admin-secondary-button{color:var(--ink);background:var(--panel)}.table-wrap{overflow-x:auto}table{width:100%;border-collapse:collapse;font-size:.875rem}th,td{padding:.625rem .75rem;border-bottom:1px solid var(--line);text-align:start;vertical-align:middle}th{font-size:.6875rem;color:var(--muted);background:var(--panel-2)}tr:hover td{background:var(--bg)}.badge{display:inline-flex;align-items:center;min-height:1.5rem;padding:0 .5rem;border-radius:4px;font-size:.75rem;font-weight:650}.badge.published{background:var(--green-soft);color:var(--green-hover)}.badge.draft{background:var(--amber-soft);color:var(--amber-text)}.badge.private{background:var(--panel-2);color:var(--muted)}.warning{border-inline-start:4px solid var(--amber);background:var(--amber-soft);padding:.75rem;border-radius:4px}article{white-space:pre-wrap;overflow-wrap:anywhere}.guest{min-height:100vh;display:grid;place-items:center;padding:1.5rem}.guest .panel{width:min(28rem,100%)}.guest-links{display:flex;gap:1rem;margin-top:1rem}.tabs{display:flex;gap:.25rem;margin:0 0 1rem;border-bottom:1px solid var(--line);overflow:auto}.tabs a{padding:.5rem .75rem;color:var(--muted);text-decoration:none;border-bottom:2px solid transparent;white-space:nowrap}.tabs a.is-active{color:var(--green-hover);border-bottom-color:var(--green);font-weight:650}.filters{display:flex;flex-wrap:wrap;gap:.5rem;align-items:flex-end;margin:0 0 1rem}.filters label{margin:0}.filters input,.filters select,.filters button{width:auto;min-width:8rem}.key-once{font-family:ui-monospace,monospace;overflow-wrap:anywhere;margin:.5rem 0 0}:focus-visible{outline:2px solid var(--blue);outline-offset:2px}@media(max-width:720px){.shell{grid-template-columns:1fr}.admin-sidebar{position:sticky;top:0;height:auto;z-index:2}.admin-sidebar nav,.admin-sidebar-footer{flex-direction:row;overflow:auto}.editor,.stats{grid-template-columns:1fr}#main{padding:1rem}}`;
function withLang(href: string,lang: string) {const u=new URL(href,'https://edge.local');u.searchParams.set('lang',lang);return u.pathname+u.search;}
function navLink(lang: string,href: string,label: string,active: boolean) {return `<a class="admin-nav-link${active?' is-active':''}" href="${withLang(href,lang)}"${active?' aria-current="page"':''}>${label}</a>`;}
function page(lang: string,title: string,body: string,c?: Context,section='',here='/admin',actions='') {
  const session=c?.session;
  const other=lang==='ar'?'en':'ar';
  if(!session) return reply(`<!doctype html><html lang="${lang}" dir="${lang==='ar'?'rtl':'ltr'}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${e(title)} · GoRun Edge</title><style>${shellCSS}</style></head><body class="admin-workspace"><main class="guest"><section class="panel"><p class="admin-eyebrow">GoRun Edge</p><h1>${e(title)}</h1>${body}<p class="guest-links"><a href="${e(withLang(here,other))}">${lang==='ar'?'English':'العربية'}</a><a href="/">${tr(lang,'Public site','الموقع العام')}</a></p></section></main></body></html>`);
  const side=`<aside class="admin-sidebar"><a class="brand" href="${withLang('/admin',lang)}">GoRun Edge</a><nav aria-label="${tr(lang,'Workspace','مساحة العمل')}">${navLink(lang,'/admin',tr(lang,'Content','المحتوى'),section==='content'||section==='edit'||section==='review')}${navLink(lang,'/admin/media',tr(lang,'Media','الوسائط'),section==='media')}${navLink(lang,'/admin/audit',tr(lang,'Audit','السجل'),section==='audit')}${navLink(lang,'/admin/managers',tr(lang,'Managers','المديرون'),section==='managers')}<a class="admin-nav-link" href="/admin/export">${tr(lang,'Export content','تصدير المحتوى')}</a></nav><div class="admin-sidebar-footer"><a class="admin-nav-link" href="${e(withLang(here,other))}">${lang==='ar'?'English':'العربية'}</a><a class="admin-nav-link" href="/">${tr(lang,'Public site','الموقع العام')}</a><form method="post" action="/admin/logout?lang=${lang}">${hidden(c!)}<button class="admin-secondary-button">${tr(lang,'Sign out','تسجيل الخروج')}</button></form></div></aside>`;
  return reply(`<!doctype html><html lang="${lang}" dir="${lang==='ar'?'rtl':'ltr'}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${e(title)} · GoRun Edge</title><style>${shellCSS}</style></head><body class="admin-workspace"><div class="shell">${side}<main id="main"><div class="page-head"><div><p class="admin-eyebrow">GoRun Edge</p><h1>${e(title)}</h1></div>${actions}</div>${body}</main></div></body></html>`);
}
function badge(lang: string,status: string) {
  const kind=status==='published'||status==='private'||status==='draft'?status:'draft';
  const label=kind==='published'?tr(lang,'Published','منشور'):kind==='private'?tr(lang,'Private','خاص'):tr(lang,'Draft','مسودة');
  return `<span class="badge ${kind}">${label}</span>`;
}
function option(value: string,label: string,current: string) {return `<option value="${e(value)}"${value===current?' selected':''}>${label}</option>`;}
function listFilters(url: URL) {
  const allowed=['q','status','locale','lang'];
  for(const key of url.searchParams.keys()) if(!allowed.includes(key)||url.searchParams.getAll(key).length!==1) return null;
  const q=(url.searchParams.get('q')??'').trim(),status=url.searchParams.get('status')??'',locale=url.searchParams.get('locale')??'';
  if(q.length>80||(status&&!['published','draft','private'].includes(status))||(locale&&!['en','ar'].includes(locale))) return null;
  return {q,status,locale};
}
function contentQuery(status: string,locale: string,q: string) {const p=new URLSearchParams();if(status)p.set('status',status);if(locale)p.set('locale',locale);if(q)p.set('q',q);const s=p.toString();return '/admin'+(s?'?'+s:'');}
function publicHref(type: string,slug: string,locale: string,fallback: string) {
  if(!['post','page'].includes(type)||!slug) return '';
  return `${locale&&locale!==fallback?'/'+locale:''}/${type}/${encodeURIComponent(slug)}`;
}
function hidden(c: Context) {return `<input type="hidden" name="csrf" value="${e(c.session!.csrf)}">`;}
function fail(lang: string,message: string,status: number) {const r=page(lang,message,`<p>${e(message)}</p><p><a href="/admin?lang=${lang}">${tr(lang,'Back to content','العودة إلى المحتوى')}</a></p>`);return new Response(r.body,{status,headers:r.headers});}
function version(s: string|null) {return s!==null&&/^(0|[1-9][0-9]{0,8})$/.test(s)?Number(s):null;}
function bodyInput(f: URLSearchParams): Body|null {
  const b={type:f.get('type')??'',slug:(f.get('slug')??'').trim(),locale:f.get('locale')??'',title:(f.get('title')??'').trim(),excerpt:f.get('excerpt')??'',body_text:f.get('body_text')??''};
  return ['post','page'].includes(b.type)&&slugPattern.test(b.slug)&&['en','ar'].includes(b.locale)&&b.title.length>0&&b.title.length<=200&&b.excerpt.length<=600&&b.body_text.length<=100000&&!Object.values(b).some(v=>v.includes('\0'))?b:null;
}
async function loginPage(c: Context,lang: string,message='') {
  const token=await sign(c,{nonce:random(),issued:Date.now(),epoch:c.owner.auth_epoch});
  const r=page(lang,tr(lang,'Owner sign-in','دخول المالك'),`<p class="muted">${tr(lang,'Use the owner access key created by the offline owner tool. There is no public setup or recovery endpoint.','استخدم مفتاح المالك الصادر من أداة الإعداد الخاصة. لا يوجد إعداد أو استعادة عبر رابط عام.')}</p>${message?`<p class="warning">${e(message)}</p>`:''}<form method="post" action="/admin/login?lang=${lang}"><input type="hidden" name="challenge" value="${token}"><label>${tr(lang,'Owner access key','مفتاح المالك')}<input name="key" type="password" autocomplete="off" required maxlength="43"></label><button>${tr(lang,'Sign in','دخول')}</button></form>`,undefined,'login','/admin/login');
  r.headers.append('set-cookie',cookie(c,'login',token,300));return r;
}
async function login(r: Request,c: Context,f: URLSearchParams,lang: string) {
  const token=f.get('challenge')??'';
  if(!equal(readCookie(r,cookieName(c,'login')),token))return fail(lang,'Invalid sign-in challenge',403);
  const challenge=await verify(c,token);
  if(!challenge||typeof challenge.issued!=='number'||typeof challenge.nonce!=='string'||challenge.epoch!==c.owner.auth_epoch||Date.now()-challenge.issued>300000||challenge.issued>Date.now())return fail(lang,'Expired sign-in challenge',403);
  const raw=f.get('key')??'';const matched=/^[A-Za-z0-9_-]{43}$/.test(raw)&&equal(await ownerHash(c.env.SITE_ID,raw),c.owner.key_hash);
  if(!matched) {
    const minute=Math.floor(Date.now()/60000);
    const result=await c.env.DB.prepare('INSERT INTO edge_login_limits VALUES (?1,?2,1) ON CONFLICT(site_id) DO UPDATE SET window=excluded.window,attempts=CASE WHEN window=excluded.window THEN min(attempts+1,1000) ELSE 1 END RETURNING attempts').bind(c.env.SITE_ID,minute).first<{attempts:number}>();
    const out=await loginPage(c,lang,tr(lang,'Invalid access key.','مفتاح الدخول غير صحيح.'));return new Response(out.body,{status:(result?.attempts??0)>10?429:401,headers:out.headers});
  }
  const rawSession=random(),tokenHash=await sessionHash(rawSession),csrf=random(),at=Date.now();
  const auth="EXISTS(SELECT 1 FROM edge_owners WHERE site_id=? AND active=1 AND auth_epoch=? AND key_hash=?) AND CAST(unixepoch('subsec')*1000 AS INTEGER)<?";
  const authArgs=[c.env.SITE_ID,c.owner.auth_epoch,c.owner.key_hash,challenge.issued+300000];
  let results: D1Result[];
  try {results=await c.env.DB.batch([
    c.env.DB.prepare(`DELETE FROM edge_login_used WHERE site_id=? AND expires_at<? AND ${auth}`).bind(c.env.SITE_ID,at,...authArgs),
    c.env.DB.prepare(`INSERT INTO edge_login_used SELECT ?,?,? WHERE ${auth}`).bind(c.env.SITE_ID,challenge.nonce,at+300000,...authArgs),
    c.env.DB.prepare(`DELETE FROM edge_sessions WHERE site_id=? AND (expires_at<? OR token_hash=?) AND ${auth}`).bind(c.env.SITE_ID,at,c.session?.token_hash??'',...authArgs),
    c.env.DB.prepare(`INSERT INTO edge_sessions(site_id,token_hash,csrf,owner_epoch,issued_at,expires_at) SELECT ?,?,?,?,?,? WHERE ${auth}`).bind(c.env.SITE_ID,tokenHash,csrf,c.owner.auth_epoch,at,at+3600000,...authArgs),
    audit(c,'owner.login','','EXISTS(SELECT 1 FROM edge_sessions WHERE site_id=? AND token_hash=?)',[c.env.SITE_ID,tokenHash]),
    c.env.DB.prepare(`DELETE FROM edge_sessions WHERE site_id=? AND token_hash NOT IN (SELECT token_hash FROM edge_sessions WHERE site_id=? ORDER BY issued_at DESC LIMIT 20) AND ${auth}`).bind(c.env.SITE_ID,c.env.SITE_ID,...authArgs),
  ]);}catch{return fail(lang,'Sign-in challenge was already used; reload and try again',409);}
  if(!results[3].meta.changes)return fail(lang,'Owner access changed; reload and try again',409);
  const out=redirect('/admin?lang='+lang);out.headers.append('set-cookie',cookie(c,'session',rawSession,3600));out.headers.append('set-cookie',cookie(c,'login','',0));return out;
}
async function record(c: Context,id: string) {
  const item=await c.env.DB.prepare('SELECT * FROM edge_content WHERE site_id=? AND id=?').bind(c.env.SITE_ID,id).first<Record<string,unknown>>();
  const draft=await c.env.DB.prepare('SELECT * FROM edge_drafts WHERE site_id=? AND id=?').bind(c.env.SITE_ID,id).first<Draft>();return{item,draft};
}
function sourceGuard() {return 'COALESCE((SELECT version FROM edge_content WHERE site_id=? AND id=?),0)=?';}
export async function save(c: Context,id: string,b: Body,dv: number,sv: number,action='draft.saved') {
  const op=crypto.randomUUID(),json=JSON.stringify(b),v=dv+1;
  const cond=`${guard(c)} AND ${sourceGuard()} AND ((?=0 AND NOT EXISTS(SELECT 1 FROM edge_drafts WHERE site_id=? AND id=?)) OR EXISTS(SELECT 1 FROM edge_drafts WHERE site_id=? AND id=? AND version=? AND source_version=?))`;
  const args=[...guardArgs(c),c.env.SITE_ID,id,sv,dv,c.env.SITE_ID,id,c.env.SITE_ID,id,dv,sv];
  const proof='EXISTS(SELECT 1 FROM edge_drafts WHERE site_id=? AND id=? AND op_token=?)';const proofArgs=[c.env.SITE_ID,id,op];
  const results=await c.env.DB.batch([
    c.env.DB.prepare(`INSERT INTO edge_drafts(site_id,id,version,source_version,body_json,op_token,updated_at) SELECT ?,?,?,?,?,?,? WHERE ${cond} ON CONFLICT(site_id,id) DO UPDATE SET version=excluded.version,body_json=excluded.body_json,op_token=excluded.op_token,updated_at=excluded.updated_at WHERE edge_drafts.version=?`).bind(c.env.SITE_ID,id,v,sv,json,op,now(),...args,dv),
    c.env.DB.prepare(`INSERT INTO edge_revisions SELECT ?,?,?,?,?,? WHERE ${proof}`).bind(c.env.SITE_ID,crypto.randomUUID(),id,action,json,now(),...proofArgs),audit(c,action,id,proof,proofArgs),
  ]);return Boolean(results[0].meta.changes);
}
export async function publish(c: Context,id: string,d: Draft,review: Record<string,unknown>) {
  if(review.id!==id||review.dv!==d.version||review.sv!==d.source_version||review.session!==c.session!.token_hash||review.epoch!==c.session!.owner_epoch||typeof review.expires!=='number'||review.expires<Date.now()||review.body!==await hash(d.body_json))return false;
  const b=JSON.parse(d.body_json) as Body,op=crypto.randomUUID();
  const cond=`${guard(c)} AND CAST(unixepoch('subsec')*1000 AS INTEGER)<? AND ?+600000>CAST(unixepoch('subsec')*1000 AS INTEGER) AND ${sourceGuard()} AND EXISTS(SELECT 1 FROM edge_drafts WHERE site_id=? AND id=? AND version=? AND source_version=? AND body_json=?)`;
  const args=[...guardArgs(c),review.expires,c.session!.issued_at,c.env.SITE_ID,id,d.source_version,c.env.SITE_ID,id,d.version,d.source_version,d.body_json];
  const proof='EXISTS(SELECT 1 FROM edge_content WHERE site_id=? AND id=? AND admin_operation=?)',pa=[c.env.SITE_ID,id,op];
  const result=await c.env.DB.batch([
    c.env.DB.prepare(`INSERT INTO edge_content(site_id,id,type,slug,locale,title,excerpt,body_text,status,published_at,version,admin_operation) SELECT ?,?,?,?,?,?,?,?,'published',?,1,? WHERE ${cond} ON CONFLICT(site_id,id) DO UPDATE SET type=excluded.type,slug=excluded.slug,locale=excluded.locale,title=excluded.title,excerpt=excluded.excerpt,body_text=excluded.body_text,status='published',published_at=excluded.published_at,version=edge_content.version+1,admin_operation=excluded.admin_operation WHERE edge_content.version=?`).bind(c.env.SITE_ID,id,b.type,b.slug,b.locale,b.title,b.excerpt,b.body_text,new Date().toISOString().replace(/\.\d{3}Z$/,'Z'),op,...args,d.source_version),
    c.env.DB.prepare(`INSERT INTO edge_revisions SELECT ?,?,?,?,?,? WHERE ${proof}`).bind(c.env.SITE_ID,crypto.randomUUID(),id,'content.published',d.body_json,now(),...pa),audit(c,'content.published',id,proof,pa),
    c.env.DB.prepare(`DELETE FROM edge_drafts WHERE site_id=? AND id=? AND ${proof}`).bind(c.env.SITE_ID,id,...pa),
  ]);return Boolean(result[0].meta.changes);
}

export async function discard(c: Context,id: string,dv: number) {
  const op=crypto.randomUUID(),proof='EXISTS(SELECT 1 FROM edge_drafts WHERE site_id=? AND id=? AND op_token=?)',pa=[c.env.SITE_ID,id,op];
  const result=await c.env.DB.batch([
    c.env.DB.prepare(`UPDATE edge_drafts SET op_token=? WHERE site_id=? AND id=? AND version=? AND ${guard(c)}`).bind(op,c.env.SITE_ID,id,dv,...guardArgs(c)),
    audit(c,'draft.discarded',id,proof,pa),
    c.env.DB.prepare('DELETE FROM edge_drafts WHERE site_id=? AND id=? AND op_token=?').bind(...pa),
  ]);return Boolean(result[0].meta.changes&&result[2].meta.changes);
}
async function logout(c: Context) {
  const op=crypto.randomUUID(),proof='EXISTS(SELECT 1 FROM edge_sessions WHERE site_id=? AND token_hash=? AND op_token=?)',pa=[c.env.SITE_ID,c.session!.token_hash,op];
  await c.env.DB.batch([c.env.DB.prepare(`UPDATE edge_sessions SET op_token=? WHERE site_id=? AND token_hash=? AND ${guard(c)}`).bind(op,c.env.SITE_ID,c.session!.token_hash,...guardArgs(c)),audit(c,'owner.logout','',proof,pa),c.env.DB.prepare('DELETE FROM edge_sessions WHERE site_id=? AND token_hash=? AND op_token=?').bind(...pa)]);
}
async function editor(c: Context,id: string,lang: string) {
  const {item,draft}=await record(c,id);const b=(draft?JSON.parse(draft.body_json):item)??{type:'post',slug:'',locale:lang,title:'',excerpt:'',body_text:''};
  const source=Number(item?.version??0),dv=draft?.version??0;
  const stale=draft&&draft.source_version!==source;
  const site=await c.env.DB.prepare('SELECT default_locale FROM edge_sites WHERE id=?').bind(c.env.SITE_ID).first<{default_locale:string}>();
  const fallback=site?.default_locale==='ar'?'ar':'en';
  const status=String(item?.status??'draft');
  const href=status==='published'?publicHref(String(item?.type??''),String(item?.slug??''),String(item?.locale??''),fallback):'';
  const state=`<p>${badge(lang,status)}${draft?' '+badge(lang,'draft'):''}${href?` <a href="${e(href)}">${tr(lang,'Public page','الصفحة العامة')}</a>`:''}</p>`;
  const fields=`${hidden(c)}<input type="hidden" name="id" value="${id}"><input type="hidden" name="draft_version" value="${dv}"><input type="hidden" name="source_version" value="${source}">`;
  const revisions=await c.env.DB.prepare('SELECT id,action,at FROM edge_revisions WHERE site_id=? AND content_id=? ORDER BY at DESC LIMIT 20').bind(c.env.SITE_ID,id).all<{id:string;action:string;at:string}>();
  const aside=`${draft&&!stale?`<form method="post" action="/admin/review?lang=${lang}">${fields}<button>${tr(lang,'Review publication','مراجعة النشر')}</button></form>`:''}${draft?`<form method="post" action="/admin/discard?lang=${lang}">${fields}<label><input type="checkbox" name="confirm" value="yes" required> ${tr(lang,'Discard this draft; its saved revisions remain available.','تجاهل المسودة مع الاحتفاظ بالمراجعات المحفوظة.')}</label><button class="admin-secondary-button">${tr(lang,'Discard draft','تجاهل المسودة')}</button></form>`:''}${item?.status==='published'?`<form method="post" action="/admin/unpublish?lang=${lang}">${fields}<label><input type="checkbox" name="confirm" value="yes" required> ${tr(lang,'Remove the article from public view.','إزالة المقال من العرض العام.')}</label><button class="admin-secondary-button">${tr(lang,'Unpublish','إلغاء النشر')}</button></form>`:''}<h2>${tr(lang,'Saved revisions','المراجعات المحفوظة')}</h2>${revisions.results.map(v=>`<form method="post" action="/admin/restore?lang=${lang}">${fields}<input type="hidden" name="revision" value="${v.id}"><p class="muted">${e(v.at)} · ${e(v.action)}</p><button class="admin-secondary-button">${tr(lang,'Restore as draft','استعادة كمسودة')}</button></form>`).join('')||`<p class="muted">—</p>`}`;
  return page(lang,tr(lang,'Edit content','تحرير المحتوى'),`<p class="muted">${tr(lang,'Saving a draft keeps the current public article unchanged. Plain text only. Review is required before publication.','حفظ المسودة لا يغيّر النسخة العامة. النص عادي فقط، وتلزم المراجعة قبل النشر.')}</p>${state}${stale?`<p class="warning">${tr(lang,'The public version changed. Discard this draft before starting from the current version.','تغيّرت النسخة العامة. تجاهل المسودة قبل البدء من النسخة الحالية.')}</p>`:''}<div class="editor"><form class="panel" method="post" action="/admin/save?lang=${lang}">${fields}<label>${tr(lang,'Title','العنوان')}<input name="title" value="${e(b.title)}" maxlength="200" required></label><label>${tr(lang,'Slug','المسار')}<input name="slug" value="${e(b.slug)}" maxlength="120" required></label><label>${tr(lang,'Type','النوع')}<select name="type"><option value="post"${b.type==='post'?' selected':''}>Post / مقال</option><option value="page"${b.type==='page'?' selected':''}>Page / صفحة</option></select></label><label>${tr(lang,'Language','اللغة')}<select name="locale"><option value="en"${b.locale==='en'?' selected':''}>English</option><option value="ar"${b.locale==='ar'?' selected':''}>العربية</option></select></label><label>${tr(lang,'Summary','الملخص')}<input name="excerpt" value="${e(b.excerpt)}" maxlength="600"></label><label>${tr(lang,'Body','المحتوى')}<textarea name="body_text" maxlength="100000">${e(b.body_text)}</textarea></label><button${stale?' disabled':''}>${tr(lang,'Save draft','حفظ المسودة')}</button></form><aside class="panel">${aside}</aside></div>`,c,'edit',`/admin/edit?id=${encodeURIComponent(id)}`);
}
interface ContentRow { id: string; title: string|null; status: string; type: string|null; locale: string|null; has_draft: number }
async function contentList(c: Context,lang: string,url: URL) {
  const filters=listFilters(url);if(!filters) return fail(lang,'Invalid content filters',400);
  const rows=await c.env.DB.prepare("SELECT id,title,status,type,locale,has_draft FROM (SELECT id,title,status,type,locale,EXISTS(SELECT 1 FROM edge_drafts d WHERE d.site_id=c.site_id AND d.id=c.id) has_draft FROM edge_content c WHERE site_id=?1 UNION ALL SELECT id,json_extract(body_json,'$.title'),'draft',json_extract(body_json,'$.type'),json_extract(body_json,'$.locale'),1 FROM edge_drafts d WHERE site_id=?1 AND NOT EXISTS(SELECT 1 FROM edge_content c WHERE c.site_id=d.site_id AND c.id=d.id)) ORDER BY title LIMIT 100").bind(c.env.SITE_ID).all<ContentRow>();
  const visible=rows.results.filter(v=>{
    if(filters.status==='draft'){if(v.status!=='draft'&&!v.has_draft) return false;}
    else if(filters.status&&v.status!==filters.status) return false;
    if(filters.locale&&v.locale!==filters.locale) return false;
    if(filters.q&&!`${v.title??''} ${v.type??''}`.toLocaleLowerCase().includes(filters.q.toLocaleLowerCase())) return false;
    return true;
  });
  const published=visible.filter(v=>v.status==='published').length,drafts=visible.filter(v=>v.status==='draft'||v.has_draft).length;
  const tabs=['',tr(lang,'All','الكل'),'published',tr(lang,'Published','منشور'),'draft',tr(lang,'Drafts','مسودات'),'private',tr(lang,'Private','خاص')];
  let nav='';for(let i=0;i<tabs.length;i+=2){const active=filters.status===tabs[i];nav+=`<a class="${active?'is-active':''}" href="${e(withLang(contentQuery(tabs[i],filters.locale,filters.q),lang))}"${active?' aria-current="page"':''}>${tabs[i+1]}</a>`;}
  const form=`<form class="filters" method="get" action="/admin" role="search"><input type="hidden" name="lang" value="${lang}"><label>${tr(lang,'Search','بحث')}<input name="q" type="search" maxlength="80" value="${e(filters.q)}"></label><label>${tr(lang,'Status','الحالة')}<select name="status">${option('',tr(lang,'All statuses','كل الحالات'),filters.status)}${option('published',tr(lang,'Published','منشور'),filters.status)}${option('draft',tr(lang,'Drafts','مسودات'),filters.status)}${option('private',tr(lang,'Private','خاص'),filters.status)}</select></label><label>${tr(lang,'Language','اللغة')}<select name="locale">${option('',tr(lang,'All languages','كل اللغات'),filters.locale)}${option('en','English',filters.locale)}${option('ar','العربية',filters.locale)}</select></label><button class="admin-secondary-button" type="submit">${tr(lang,'Filter','تصفية')}</button></form>`;
  const table=`<div class="panel table-wrap"><table><thead><tr><th>${tr(lang,'Title','العنوان')}</th><th>${tr(lang,'Type','النوع')}</th><th>${tr(lang,'Language','اللغة')}</th><th>${tr(lang,'State','الحالة')}</th></tr></thead><tbody>${visible.map(v=>`<tr><td><a href="/admin/edit?id=${encodeURIComponent(v.id)}&amp;lang=${lang}">${e(v.title)||tr(lang,'Untitled','بدون عنوان')}</a></td><td>${e(v.type)}</td><td>${e(v.locale)}</td><td>${badge(lang,v.status)}${v.has_draft&&v.status!=='draft'?' '+badge(lang,'draft'):''}</td></tr>`).join('')||`<tr><td colspan="4">${tr(lang,'No content matches these filters.','لا يوجد محتوى يطابق هذه التصفية.')}</td></tr>`}</tbody></table></div>`;
  return page(lang,tr(lang,'Content','المحتوى'),`<nav class="tabs" aria-label="${tr(lang,'Status','الحالة')}">${nav}</nav>${form}<div class="stats"><article class="stat"><b>${visible.length}</b><span class="muted">${tr(lang,'Items','عناصر')}</span></article><article class="stat"><b>${published}</b><span class="muted">${tr(lang,'Published','منشور')}</span></article><article class="stat"><b>${drafts}</b><span class="muted">${tr(lang,'Drafts','مسودات')}</span></article></div>${table}`,c,'content',contentQuery(filters.status,filters.locale,filters.q),`<a class="admin-primary-button" href="/admin/edit?lang=${lang}">${tr(lang,'New content','محتوى جديد')}</a>`);
}
interface ManagerRow { id: string; label: string; key_hint: string; primary_manager: number; created_at: string }
function managerLabel(raw: string|null) {const label=(raw??'').trim()||'GoRun';return label.length<=80&&!/[\u0000-\u001f<>]/.test(label)?label:null;}
async function managersView(c: Context,lang: string,shown='') {
  const rows=await c.env.DB.prepare('SELECT id,label,key_hint,primary_manager,created_at FROM edge_managers WHERE site_id=? ORDER BY primary_manager DESC,created_at').bind(c.env.SITE_ID).all<ManagerRow>();
  const hasPrimary=rows.results.some(v=>v.primary_manager===1);
  const once=shown?`<section class="warning"><p>${tr(lang,'Copy this primary key into GoRun → Settings → Edge. It is shown once and is not stored on this page after you leave.','انسخ هذا المفتاح الأساسي إلى GoRun ← الإعدادات ← Edge. يظهر مرة واحدة ولا يبقى في هذه الصفحة بعد المغادرة.')}</p><p class="key-once" data-manager-key="${e(shown)}">${e(shown)}</p></section>`:'';
  const create=hasPrimary?'':`<form class="panel" method="post" action="/admin/managers/create?lang=${lang}">${hidden(c)}<label>${tr(lang,'Label','الاسم')}<input name="label" maxlength="80" value="GoRun" required></label><button>${tr(lang,'Create GoRun key','إنشاء مفتاح GoRun')}</button></form>`;
  const table=`<div class="panel table-wrap"><table><thead><tr><th>${tr(lang,'Label','الاسم')}</th><th>${tr(lang,'Key hint','آخر المفتاح')}</th><th>${tr(lang,'Role','الدور')}</th><th>${tr(lang,'Created','أُنشئ')}</th><th>${tr(lang,'Actions','إجراءات')}</th></tr></thead><tbody>${rows.results.map(v=>`<tr><td>${e(v.label)}</td><td dir="ltr">••••${e(v.key_hint)}</td><td>${v.primary_manager?tr(lang,'Primary','أساسي'):tr(lang,'Manager','مدير')}</td><td>${e(v.created_at)}</td><td>${v.primary_manager?`<form method="post" action="/admin/managers/rotate?lang=${lang}">${hidden(c)}<input type="hidden" name="id" value="${e(v.id)}"><button>${tr(lang,'Rotate key','تدوير المفتاح')}</button></form><form method="post" action="/admin/managers/revoke?lang=${lang}">${hidden(c)}<input type="hidden" name="id" value="${e(v.id)}"><label><input type="checkbox" name="confirm" value="yes" required> ${tr(lang,'Revoke this primary key.','إلغاء هذا المفتاح الأساسي.')}</label><button class="admin-secondary-button">${tr(lang,'Revoke','إلغاء')}</button></form>`:'—'}</td></tr>`).join('')||`<tr><td colspan="5">${tr(lang,'No manager key yet.','لا يوجد مفتاح مدير بعد.')}</td></tr>`}</tbody></table></div>`;
  return page(lang,tr(lang,'Managers','المديرون'),`${once}<p class="muted">${tr(lang,'GoRun Full is the primary manager of this Edge site. Only a hash of the key is stored. The check confirms identity and does not edit Edge content from GoRun.','GoRun Full هو المدير الأساسي لهذا الموقع. تُحفظ بصمة المفتاح فقط. التحقق يثبت الهوية ولا يعدّل محتوى Edge من GoRun.')}</p>${create}${table}`,c,'managers','/admin/managers');
}
async function managerWrite(c: Context,path: string,f: URLSearchParams,lang: string) {
  if(path==='/admin/managers/create') {
    const label=managerLabel(f.get('label'));if(!label) return fail(lang,'Invalid manager label',400);
    const raw=random(),id=crypto.randomUUID(),at=now(),keyHash=await managerHash(c.env.SITE_ID,raw),hint=raw.slice(-4);
    const proof='EXISTS(SELECT 1 FROM edge_managers WHERE site_id=? AND id=? AND key_hash=?)',pa=[c.env.SITE_ID,id,keyHash];
    const results=await c.env.DB.batch([
      c.env.DB.prepare(`INSERT INTO edge_managers(site_id,id,label,key_hash,key_hint,primary_manager,created_at) SELECT ?,?,?,?,?,1,? WHERE ${guard(c)} AND NOT EXISTS(SELECT 1 FROM edge_managers WHERE site_id=? AND primary_manager=1)`).bind(c.env.SITE_ID,id,label,keyHash,hint,at,...guardArgs(c),c.env.SITE_ID),
      audit(c,'manager.created',id,proof,pa),
    ]);
    if(!results[0].meta.changes) return fail(lang,'A primary manager already exists',409);
    return managersView(c,lang,raw);
  }
  const id=f.get('id')??'';if(!/^[0-9a-f-]{36}$/.test(id)) return fail(lang,'Invalid manager',400);
  if(path==='/admin/managers/rotate') {
    const raw=random(),at=now(),keyHash=await managerHash(c.env.SITE_ID,raw),hint=raw.slice(-4);
    const proof='EXISTS(SELECT 1 FROM edge_managers WHERE site_id=? AND id=? AND key_hash=?)',pa=[c.env.SITE_ID,id,keyHash];
    const results=await c.env.DB.batch([
      c.env.DB.prepare(`UPDATE edge_managers SET key_hash=?,key_hint=?,created_at=? WHERE site_id=? AND id=? AND primary_manager=1 AND ${guard(c)}`).bind(keyHash,hint,at,c.env.SITE_ID,id,...guardArgs(c)),
      audit(c,'manager.rotated',id,proof,pa),
    ]);
    if(!results[0].meta.changes) return fail(lang,'Primary manager changed; reload',409);
    return managersView(c,lang,raw);
  }
  if(f.get('confirm')!=='yes') return fail(lang,'Confirmation required',403);
  const results=await c.env.DB.batch([
    c.env.DB.prepare(`DELETE FROM edge_managers WHERE site_id=? AND id=? AND primary_manager=1 AND ${guard(c)}`).bind(c.env.SITE_ID,id,...guardArgs(c)),
    audit(c,'manager.revoked',id,'changes()=1'),
  ]);
  if(!results[0].meta.changes) return fail(lang,'Primary manager changed; reload',409);
  return redirect('/admin/managers?lang='+lang);
}
export async function admin(r: Request,env: AdminEnv): Promise<Response> {
  const lang=language(r);let c: Context|null;try{c=await context(r,env);}catch{return reply('Unavailable',503);}if(!c)return reply('Not found',404);
  const path=new URL(r.url).pathname;
  if(!['/admin','/admin/login','/admin/logout','/admin/edit','/admin/save','/admin/review','/admin/publish','/admin/restore','/admin/discard','/admin/unpublish','/admin/export','/admin/audit','/admin/media','/admin/managers','/admin/managers/create','/admin/managers/rotate','/admin/managers/revoke'].includes(path))return reply('Not found',404);
  if(!['GET','POST'].includes(r.method))return reply(null,405,{allow:'GET, POST'});
  if(r.method==='GET'&&path==='/admin/login')return loginPage(c,lang);
  if(r.method==='POST'&&!sameOrigin(r,c))return fail(lang,'Same-origin request required',403);
  const f=r.method==='POST'?await form(r):null;if(r.method==='POST'&&!f)return fail(lang,'Invalid or oversized form',400);
  if(path==='/admin/login'&&f)return login(r,c,f,lang);
  if(!c.session)return redirect('/admin/login?lang='+lang);
  if(f&&!equal(f.get('csrf')??'',c.session.csrf))return fail(lang,'Invalid CSRF token',403);
  if(r.method==='GET') {
    if(path==='/admin/export') {
      const contents=(await env.DB.prepare('SELECT site_id,id,type,slug,locale,title,excerpt,body_text,status,published_at,version FROM edge_content WHERE site_id=? LIMIT 1000').bind(env.SITE_ID).all()).results;
      const drafts=(await env.DB.prepare('SELECT id,version,source_version,body_json,updated_at FROM edge_drafts WHERE site_id=? LIMIT 1000').bind(env.SITE_ID).all()).results;
      const revisions=(await env.DB.prepare('SELECT id,content_id,action,snapshot_json,at FROM edge_revisions WHERE site_id=? ORDER BY at DESC LIMIT 1000').bind(env.SITE_ID).all()).results;
      return reply(JSON.stringify({format:'gorun-edge-content-v1',contents,drafts,revisions,limits:{per_section:1000}},null,2),200,{'content-type':'application/json; charset=utf-8','content-disposition':'attachment; filename="gorun-edge-content.json"'});
    }
    if(path==='/admin/audit') {const rows=await env.DB.prepare('SELECT action,content_id,at FROM edge_audit WHERE site_id=? ORDER BY at DESC LIMIT 100').bind(env.SITE_ID).all<{action:string;content_id:string;at:string}>();return page(lang,tr(lang,'Audit','السجل'),`<div class="panel table-wrap"><table><thead><tr><th>${tr(lang,'When','الوقت')}</th><th>${tr(lang,'Action','الإجراء')}</th><th>${tr(lang,'Content','المحتوى')}</th></tr></thead><tbody>${rows.results.map(v=>`<tr><td>${e(v.at)}</td><td>${e(v.action)}</td><td>${e(v.content_id)||'—'}</td></tr>`).join('')||`<tr><td colspan="3">${tr(lang,'No audit entries yet.','لا توجد قيود في السجل بعد.')}</td></tr>`}</tbody></table></div>`,c,'audit','/admin/audit');}
    if(path==='/admin/media') {const rows=await env.DB.prepare('SELECT id,content_type,visibility FROM edge_media WHERE site_id=? LIMIT 100').bind(env.SITE_ID).all<{id:string;content_type:string;visibility:string}>();return page(lang,tr(lang,'Media metadata','بيانات الوسائط'),`<p class="muted">${tr(lang,'Existing R2 records only. Upload and private download are not implemented; no private object is exposed.','سجلات R2 الموجودة فقط. الرفع والتنزيل الخاص غير متاحين؛ لا تُعرض أي ملفات خاصة.')}</p><div class="panel table-wrap"><table><thead><tr><th>${tr(lang,'Record','السجل')}</th><th>${tr(lang,'Type','النوع')}</th><th>${tr(lang,'Visibility','الظهور')}</th></tr></thead><tbody>${rows.results.map(v=>`<tr><td>${e(v.id)}</td><td>${e(v.content_type)}</td><td>${badge(lang,v.visibility==='public'?'published':'private')}</td></tr>`).join('')||`<tr><td colspan="3">${tr(lang,'No media records.','لا توجد سجلات وسائط.')}</td></tr>`}</tbody></table></div>`,c,'media','/admin/media');}
    if(path==='/admin/edit') {const id=new URL(r.url).searchParams.get('id')??crypto.randomUUID();return idPattern.test(id)?editor(c,id,lang):fail(lang,'Invalid content ID',400);}
    if(path==='/admin/managers') return managersView(c,lang);
    if(path==='/admin') return contentList(c,lang,new URL(r.url));
    return reply('Not found',404);
  }
  if(path==='/admin/logout') {await logout(c);const out=redirect('/admin/login?lang='+lang);out.headers.append('set-cookie',cookie(c,'session','',0));return out;}
  if(path.startsWith('/admin/managers/')) return managerWrite(c,path,f!,lang);
  const id=f!.get('id')??'',dv=version(f!.get('draft_version')),sv=version(f!.get('source_version'));
  if(!idPattern.test(id)||dv===null||sv===null)return fail(lang,'Invalid content version',400);
  try {
    if(path==='/admin/save'||path==='/admin/restore') {
      let b: Body|null;
      if(path==='/admin/restore') {const revision=await env.DB.prepare('SELECT snapshot_json FROM edge_revisions WHERE site_id=? AND content_id=? AND id=?').bind(env.SITE_ID,id,f!.get('revision')).first<{snapshot_json:string}>();if(!revision)return fail(lang,'Revision not found',404);const saved=JSON.parse(revision.snapshot_json);const fields=new URLSearchParams(Object.entries(saved) as [string,string][]);b=bodyInput(fields);}else b=bodyInput(f!);
      if(!b)return fail(lang,'Invalid content fields',400);if(!await save(c,id,b,dv,sv,path==='/admin/restore'?'draft.restored':'draft.saved'))return fail(lang,'Content changed; reload before saving',409);
      return redirect(`/admin/edit?id=${id}&lang=${lang}`);
    }
    if(path==='/admin/review'||path==='/admin/publish') {
      if(Date.now()-c.session.issued_at>600000)return fail(lang,'Sign in again before publishing',401);
      const {draft,item}=await record(c,id);if(!draft||draft.version!==dv||draft.source_version!==sv||Number(item?.version??0)!==sv)return fail(lang,'Content changed; review again',409);
      if(path==='/admin/publish') {const review=await verify(c,f!.get('review')??'');if(f!.get('approve')!=='yes'||!review)return fail(lang,'Explicit publication approval required',403);if(!await publish(c,id,draft,review))return fail(lang,'Content or authority changed; review again',409);return redirect('/admin?lang='+lang);}
      const review=await sign(c,{id,dv,sv,body:await hash(draft.body_json),session:c.session.token_hash,epoch:c.session.owner_epoch,expires:Date.now()+120000});const b=JSON.parse(draft.body_json) as Body;
      return page(lang,tr(lang,'Review publication','مراجعة النشر'),`<p class="muted">${tr(lang,'This saved draft will become public at the shown locale and path.','ستصبح المسودة المحفوظة عامة باللغة والمسار المعروضين.')}</p><section><h2>${e(b.title)}</h2><p class="muted">${e(b.locale)} · ${e(b.type)}/${e(b.slug)}</p><p>${e(b.excerpt)}</p><article>${e(b.body_text)}</article></section><form method="post" action="/admin/publish?lang=${lang}">${hidden(c)}<input type="hidden" name="id" value="${id}"><input type="hidden" name="draft_version" value="${dv}"><input type="hidden" name="source_version" value="${sv}"><input type="hidden" name="review" value="${review}"><label><input type="checkbox" name="approve" value="yes" required> ${tr(lang,'I reviewed this exact saved draft and approve publication.','راجعت هذه المسودة المحفوظة وأوافق على نشرها.')}</label><button>${tr(lang,'Publish','نشر')}</button></form>`,c,'review',`/admin/edit?id=${encodeURIComponent(id)}`);
    }
    if(path==='/admin/discard'&&f!.get('confirm')==='yes')return await discard(c,id,dv)?redirect(`/admin/edit?id=${id}&lang=${lang}`):fail(lang,'Draft changed; reload',409);
    if(path==='/admin/unpublish'&&f!.get('confirm')==='yes') {const op=crypto.randomUUID(),proof='EXISTS(SELECT 1 FROM edge_content WHERE site_id=? AND id=? AND admin_operation=?)',args=[env.SITE_ID,id,op];const result=await env.DB.batch([env.DB.prepare(`UPDATE edge_content SET status='private',version=version+1,admin_operation=? WHERE site_id=? AND id=? AND version=? AND ${guard(c)}`).bind(op,env.SITE_ID,id,sv,...guardArgs(c)),env.DB.prepare(`INSERT INTO edge_revisions SELECT ?,?,?,'content.unpublished',json_object('type',type,'slug',slug,'locale',locale,'title',title,'excerpt',excerpt,'body_text',body_text),? FROM edge_content WHERE site_id=? AND id=? AND admin_operation=?`).bind(env.SITE_ID,crypto.randomUUID(),id,now(),...args),audit(c,'content.unpublished',id,proof,args)]);return result[0].meta.changes?redirect(`/admin/edit?id=${id}&lang=${lang}`):fail(lang,'Content changed; reload',409);}
  }catch{return fail(lang,'Conflict or storage error; reload before retrying',409);}
  return reply('Not found',404);
}
