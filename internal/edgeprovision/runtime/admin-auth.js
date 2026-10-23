const encoder = new TextEncoder();
export function random() { const a = crypto.getRandomValues(new Uint8Array(32)); return btoa(String.fromCharCode(...a)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, ''); }
export async function hash(value) { const a = new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(value))); return [...a].map(x => x.toString(16).padStart(2, '0')).join(''); }
export function equal(a, b) { if (a.length !== b.length)
    return false; let diff = 0; for (let i = 0; i < a.length; i++)
    diff |= a.charCodeAt(i) ^ b.charCodeAt(i); return diff === 0; }
export const ownerHash = (site, key) => hash(`gorun-edge-owner:v1:${site}:${key}`);
export const managerHash = (site, key) => hash(`gorun-edge-manager:v1:${site}:${key}`);
export const sessionHash = (key) => hash(`gorun-edge-session:v1:${key}`);
export function escape(s) { return String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); }
export function reply(body, status = 200, headers = {}) { return new Response(body, { status, headers: { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'private, no-store', 'x-content-type-options': 'nosniff', 'referrer-policy': 'same-origin', 'cross-origin-resource-policy': 'same-origin', 'content-security-policy': "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'", ...headers } }); }
export function redirect(path = '/admin') { return reply(null, 303, { location: path }); }
export function cookieName(c, name) { return (c.local ? 'edge_' : '__Host-edge_') + name; }
export function cookie(c, name, value, age) { return `${cookieName(c, name)}=${value}; Path=/; HttpOnly; SameSite=Strict; Max-Age=${age}${c.local ? '' : '; Secure'}`; }
export function readCookie(r, name) { const matches = (r.headers.get('cookie') ?? '').split(';').map(x => x.trim()).filter(x => x.startsWith(name + '=')); return matches.length === 1 ? matches[0].slice(name.length + 1) : ''; }
export async function context(r, env) {
    if (env.EDGE_ADMIN_ENABLED !== 'true' || !env.DB || !env.MEDIA || !/^[a-z0-9][a-z0-9_-]{0,63}$/.test(env.SITE_ID ?? '') || !/^[A-Za-z0-9_-]{43,86}$/.test(env.EDGE_ADMIN_SECRET ?? ''))
        return null;
    let origin;
    try {
        origin = new URL(env.EDGE_ADMIN_ORIGIN ?? '');
    }
    catch {
        return null;
    }
    const local = origin.protocol === 'http:' && ['127.0.0.1', 'localhost', '[::1]'].includes(origin.hostname) && env.EDGE_ADMIN_LOCAL === 'true';
    if ((origin.protocol !== 'https:' && !local) || origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash || new URL(r.url).origin !== origin.origin)
        return null;
    const owner = await env.DB.prepare('SELECT auth_epoch,key_hash FROM edge_owners WHERE site_id=?1 AND active=1').bind(env.SITE_ID).first();
    if (!owner || !/^[a-f0-9]{64}$/.test(owner.key_hash))
        return null;
    const c = { env, owner, origin: origin.origin, local };
    const key = readCookie(r, cookieName(c, 'session'));
    if (/^[A-Za-z0-9_-]{43}$/.test(key))
        c.session = await env.DB.prepare('SELECT s.* FROM edge_sessions s JOIN edge_owners o ON o.site_id=s.site_id AND o.auth_epoch=s.owner_epoch AND o.active=1 WHERE s.site_id=?1 AND s.token_hash=?2 AND s.expires_at>?3').bind(env.SITE_ID, await sessionHash(key), Date.now()).first() ?? undefined;
    return c;
}
export async function sign(c, data) {
    const payload = btoa(String.fromCharCode(...encoder.encode(JSON.stringify(data)))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
    const key = await crypto.subtle.importKey('raw', encoder.encode(c.env.EDGE_ADMIN_SECRET), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
    const sig = new Uint8Array(await crypto.subtle.sign('HMAC', key, encoder.encode(payload)));
    return payload + '.' + btoa(String.fromCharCode(...sig)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
}
export async function verify(c, token) {
    if (token.length > 3000)
        return null;
    const [payload, sig, ...extra] = token.split('.');
    if (!payload || !sig || extra.length || !/^[A-Za-z0-9_-]+$/.test(payload + sig))
        return null;
    try {
        const data = JSON.parse(new TextDecoder('utf-8', { fatal: true, ignoreBOM: false }).decode(Uint8Array.from(atob(payload.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0))));
        return equal(await sign(c, data), token) ? data : null;
    }
    catch {
        return null;
    }
}
export async function form(r) {
    if (!r.headers.get('content-type')?.startsWith('application/x-www-form-urlencoded'))
        return null;
    const reader = r.body?.getReader();
    if (!reader)
        return null;
    let size = 0;
    const chunks = [];
    for (;;) {
        const { done, value } = await reader.read();
        if (done)
            break;
        size += value.length;
        if (size > 131072) {
            await reader.cancel();
            return null;
        }
        chunks.push(value);
    }
    const all = new Uint8Array(size);
    let p = 0;
    for (const v of chunks) {
        all.set(v, p);
        p += v.length;
    }
    const f = new URLSearchParams(new TextDecoder('utf-8', { fatal: false, ignoreBOM: false }).decode(all));
    for (const k of f.keys())
        if (f.getAll(k).length !== 1)
            return null;
    return f;
}
export function sameOrigin(r, c) { return r.headers.get('origin') === c.origin && r.headers.get('sec-fetch-site') !== 'cross-site'; }
export function guard(c) { return `EXISTS(SELECT 1 FROM edge_sessions s JOIN edge_owners o ON o.site_id=s.site_id AND o.auth_epoch=s.owner_epoch AND o.active=1 WHERE s.site_id=? AND s.token_hash=? AND s.owner_epoch=? AND s.expires_at>CAST(unixepoch('subsec')*1000 AS INTEGER))`; }
export function guardArgs(c) { return [c.env.SITE_ID, c.session.token_hash, c.session.owner_epoch]; }
export function audit(c, action, id = '', condition = '1', args = []) { return c.env.DB.prepare(`INSERT INTO edge_audit(site_id,id,action,content_id,at) SELECT ?,?,?,?,? WHERE ${condition}`).bind(c.env.SITE_ID, crypto.randomUUID(), action, id, new Date().toISOString(), ...args); }
