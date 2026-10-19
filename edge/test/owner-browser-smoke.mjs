// Fresh disposable local fixture: actual owner CLI + Wrangler HTTP + Chromium.
import {mkdtemp,cp,symlink,readFile,stat,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {spawn,spawnSync} from 'node:child_process';
import {setTimeout as delay} from 'node:timers/promises';
import assert from 'node:assert/strict';
import {browserAcceptance} from './admin-browser.mjs';
const temp=await mkdtemp(join(tmpdir(),'gorun-edge-browser-')),port=18789;
let child,output='';
const env={...process.env,WRANGLER_SEND_METRICS:'false'};
function cli(args){const r=spawnSync(process.execPath,args,{cwd:temp,env,encoding:'utf8',maxBuffer:1024*1024});if(r.status!==0)throw Error('Isolated local CLI command failed (credential output suppressed)');return r.stdout;}
const owner=command=>{const s=cli(['tools/owner.mjs',command,'--local','--origin',`http://127.0.0.1:${port}`]);const key=s.match(/Owner access key \(shown once; save privately\): ([A-Za-z0-9_-]{43})/);if(command!=='revoke')assert.ok(key,'Owner tool must issue a key only after successful CAS');return key?.[1];};
try {
 for(const name of ['src','tools','migrations','fixtures','package.json','wrangler.jsonc'])await cp(name,join(temp,name),{recursive:true});await symlink(resolve('node_modules'),join(temp,'node_modules'),'dir');
 cli(['node_modules/wrangler/bin/wrangler.js','d1','migrations','apply','DB','--local']);cli(['node_modules/wrangler/bin/wrangler.js','d1','execute','DB','--local','--file','fixtures/demo.sql']);
 const key=owner('init');assert.equal((await stat(join(temp,'.dev.vars'))).mode&0o777,0o600);
 const duplicate=spawnSync(process.execPath,['tools/owner.mjs','init','--local'],{cwd:temp,env,encoding:'utf8'});assert.notEqual(duplicate.status,0);assert.ok(!duplicate.stdout.includes('Owner access key'));
 child=spawn(process.execPath,['node_modules/wrangler/bin/wrangler.js','dev','--local','--port',String(port)],{cwd:temp,env,detached:process.platform!=='win32',stdio:['ignore','pipe','pipe']});for(const stream of [child.stdout,child.stderr])stream.on('data',v=>{output=(output+v).slice(-10000);});
 let ready=false;for(let i=0;i<160;i++){if(child.exitCode!==null)throw Error('Local Wrangler exited');try{if((await fetch(`http://127.0.0.1:${port}/admin/login`)).status===200){ready=true;break;}}catch{}await delay(250);}assert.ok(ready,'Local admin readiness timeout');
 await browserAcceptance(`http://127.0.0.1:${port}`,key,async()=>owner('recover'));
 owner('revoke');console.log('Offline owner init/recover/session-revoke passed in fresh localD1; no existing user data or Cloudflare resource used');
} finally {
 if(child){try{process.platform==='win32'?child.kill('SIGTERM'):process.kill(-child.pid,'SIGTERM');}catch{}await Promise.race([new Promise(r=>child.once('exit',r)),delay(5000)]);if(child.exitCode===null){try{process.platform==='win32'?child.kill('SIGKILL'):process.kill(-child.pid,'SIGKILL');}catch{}}child.stdout.destroy();child.stderr.destroy();child.unref();}await rm(temp,{recursive:true,force:true});
}
