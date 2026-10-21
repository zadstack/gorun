import {admin} from './admin.js';
import {managerHash,type AdminEnv} from './admin-auth.js';
/** Public read, optional owner admin, and a primary-manager identity check. The check does not write content. */
interface Env extends AdminEnv {}
interface Site { id: string; title: string; default_locale: string }
interface Summary { id: string; type: string; slug: string; locale: string; title: string; excerpt: string; published_at: string }
interface Article extends Summary { body_text: string }
interface Media { object_key: string; content_type: string; byte_size: number }
const siteID = /^[a-z0-9][a-z0-9_-]{0,63}$/;
const slugID = /^[\p{L}\p{N}][\p{L}\p{N}_-]{0,119}$/u;
const mediaTypes = new Set(['image/png','image/jpeg','image/gif','image/webp','image/avif','text/plain','application/pdf']);
const columns = 'id,type,slug,locale,title,excerpt,published_at';
const published = "site_id=?1 AND status='published' AND published_at<=?2";
const baseHeaders = {
  'cache-control':'no-store',
  'x-content-type-options':'nosniff',
  'referrer-policy':'no-referrer',
  'cross-origin-resource-policy':'same-origin',
  'content-security-policy':"default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'",
};
function response(body: BodyInit | null, status=200, extra: Record<string,string>={}) {
  return new Response(body,{status,headers:{...baseHeaders,...extra}});
}
function json(data: unknown,status=200) {
  return response(JSON.stringify(status<400 ? {ok:true,data} : {ok:false,error:{code:data}}),status,{'content-type':'application/json; charset=utf-8'});
}
function escape(s: string) { return s.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]!)); }
function path(a: Summary, site: Site) {
  const prefix=a.locale===site.default_locale?'':'/'+a.locale;
  return `${prefix}/${a.type}/${encodeURIComponent(a.slug)}`;
}
function html(site: Site, locale: string, title: string, body: string) {
  return response(`<!doctype html><html lang="${locale}" dir="${locale==='ar'?'rtl':'ltr'}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${escape(title)} · ${escape(site.title)}</title><style>body{font:1rem/1.7 system-ui,sans-serif;max-width:52rem;margin:2rem auto;padding:0 1rem;background:#fafafa;color:#172326}a{color:#146b63}article{white-space:pre-wrap;overflow-wrap:anywhere}header{border-bottom:1px solid #ccc}li{margin:1rem 0}</style></head><body><header><a href="/">${escape(site.title)}</a><p>GoRun Edge · read-only proof of concept · <a href="/en/">English</a> · <a href="/ar/">العربية</a></p></header><main><h1>${escape(title)}</h1>${body}</main></body></html>`,200,{'content-type':'text/html; charset=utf-8'});
}
function query(url: URL, allowed: string[]) {
  for (const key of url.searchParams.keys()) if (!allowed.includes(key) || url.searchParams.getAll(key).length!==1) return false;
  return true;
}
function pagination(url: URL) {
  const page=url.searchParams.get('page')??'1', size=url.searchParams.get('per_page')??'20';
  if (!/^[1-9][0-9]{0,2}$/.test(page)||!/^[1-9][0-9]?$/.test(size)) return null;
  const p=Number(page),n=Number(size);return p<=100&&n<=50?{limit:n,offset:(p-1)*n}:null;
}
function publicMediaKey(key: string, site: string) {
  const prefix=`sites/${site}/public/`;if (!key.startsWith(prefix)) return false;
  const tail=key.slice(prefix.length);
  return tail.length>0&&tail.length<=200&&tail.split('/').every(s=>/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(s)&&s!=='.'&&s!=='..');
}
async function managerWhoami(request: Request, env: Env): Promise<Response> {
  if (request.method!=='GET') return response(null,405,{allow:'GET','content-type':'application/json; charset=utf-8'});
  const url=new URL(request.url);
  if (url.search||!env.DB||!siteID.test(env.SITE_ID??'')) return json(url.search?'bad_request':'unavailable',url.search?400:503);
  const match=/^Bearer ([A-Za-z0-9_-]{43})$/.exec(request.headers.get('authorization')??'');
  if (!match) return json('unauthorized',401);
  const row=await env.DB.prepare('SELECT id,label FROM edge_managers WHERE site_id=?1 AND key_hash=?2 AND primary_manager=1').bind(env.SITE_ID,await managerHash(env.SITE_ID,match[1])).first<{id:string;label:string}>();
  if (!row||row.label.length>80) return json('unauthorized',401);
  return json({site_id:env.SITE_ID,manager_id:row.id,label:row.label,primary:true});
}
async function serve(request: Request, env: Env): Promise<Response> {
  const url=new URL(request.url);
  if (request.method!=='GET'&&request.method!=='HEAD') return response(null,405,{allow:'GET, HEAD'});
  if (url.pathname==='/healthz') return json({edition:'gorun-edge',milestone:'read-only-poc'});
  if (!env.DB||!env.MEDIA||!siteID.test(env.SITE_ID??'')) return json('unavailable',503);
  const site=await env.DB.prepare('SELECT id,title,default_locale FROM edge_sites WHERE id=?1').bind(env.SITE_ID).first<Site>();
  if (!site||!['en','ar'].includes(site.default_locale)) return json('unavailable',503);
  if (url.pathname==='/readyz') {
    const schema=await env.DB.prepare('SELECT version FROM edge_schema WHERE version=1').first();
    return schema ? json({ready:true,milestone:'read-only-poc'}) : json('unavailable',503);
  }
  let parts: string[];
  try {parts=url.pathname.split('/').filter(Boolean).map(s=>decodeURIComponent(s));} catch {return json('bad_request',400);}
  let locale=site.default_locale;
  if (parts[0]==='en'||parts[0]==='ar') locale=parts.shift()!;
  const now=new Date().toISOString().replace(/\.\d{3}Z$/,'Z');
  if (parts[0]==='media'&&parts.length===2) {
    if (!query(url,[])||!slugID.test(parts[1])) return json('bad_request',400);
    const media=await env.DB.prepare("SELECT object_key,content_type,byte_size FROM edge_media WHERE site_id=?1 AND id=?2 AND visibility='public'").bind(site.id,parts[1]).first<Media>();
    if (!media||!publicMediaKey(media.object_key,site.id)||!mediaTypes.has(media.content_type)||media.byte_size<0||media.byte_size>10485760) return json('not_found',404);
    const obj=request.method==='HEAD'?await env.MEDIA.head(media.object_key):await env.MEDIA.get(media.object_key);
    if (!obj||obj.size!==media.byte_size) return json('not_found',404);
    const headers: Record<string,string>={'content-type':media.content_type,'content-length':String(obj.size)};
    if (!media.content_type.startsWith('image/')) headers['content-disposition']='attachment';
    return response('body' in obj ? (obj as R2ObjectBody).body : null,200,headers);
  }
  const api=parts[0]==='api'&&parts[1]==='v1'&&parts[2]==='content';
  if (api) {
    if (!query(url,parts.length===3?['locale','type','page','per_page']:['locale'])) return json('bad_request',400);
    locale=url.searchParams.get('locale')??locale;
    if (!['en','ar'].includes(locale)) return json('bad_request',400);
    if (parts.length===3) {
      const type=url.searchParams.get('type')??'post',p=pagination(url);
      if (!['post','page'].includes(type)||!p) return json('bad_request',400);
      const result=await env.DB.prepare(`SELECT ${columns} FROM edge_content WHERE ${published} AND locale=?3 AND type=?4 ORDER BY published_at DESC,id DESC LIMIT ?5 OFFSET ?6`).bind(site.id,now,locale,type,p.limit,p.offset).all<Summary>();
      return json(result.results);
    }
    if (parts.length!==5) return json('not_found',404);
    parts=parts.slice(3);
  } else if (!query(url,[])) return json('bad_request',400);
  if (parts.length===0) {
    const rows=await env.DB.prepare(`SELECT ${columns} FROM edge_content WHERE ${published} AND locale=?3 ORDER BY published_at DESC,id DESC LIMIT 20`).bind(site.id,now,locale).all<Summary>();
    return html(site,locale,site.title,'<ul>'+rows.results.map(a=>`<li><a href="${path(a,site)}">${escape(a.title)}</a><p>${escape(a.excerpt)}</p></li>`).join('')+'</ul>');
  }
  if (parts.length===2&&['post','page'].includes(parts[0])&&slugID.test(parts[1])) {
    const row=await env.DB.prepare(`SELECT ${columns},body_text FROM edge_content WHERE ${published} AND locale=?3 AND type=?4 AND slug=?5`).bind(site.id,now,locale,parts[0],parts[1]).first<Article>();
    if (!row) return json('not_found',404);
    return api?json(row):html(site,locale,row.title,`<article>${escape(row.body_text)}</article>`);
  }
  return json('not_found',404);
}
export default {
  async fetch(request: Request,env: Env): Promise<Response> {
    let result: Response;
    try {const p=new URL(request.url).pathname;result=p==='/api/v1/manager'?await managerWhoami(request,env):(p==='/admin'||p.startsWith('/admin/'))?await admin(request,env):await serve(request,env);} catch {result=json('unavailable',503);}
    return request.method==='HEAD'?new Response(null,{status:result.status,headers:result.headers}):result;
  },
} satisfies ExportedHandler<Env>;
