// Fresh disposable local fixture: actual owner CLI + Wrangler HTTP + Chromium.
import {mkdtemp,cp,symlink,readFile,stat,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {spawn} from 'node:child_process';
import {command,terminate} from './process-helper.mjs';
import {setTimeout as delay} from 'node:timers/promises';
import assert from 'node:assert/strict';
import {browserAcceptance} from './admin-browser.mjs';
const temp=await mkdtemp(join(tmpdir(),'gorun-edge-browser-')),port=18789;
let child,output='';
const env={...process.env,WRANGLER_SEND_METRICS:'false'};
async function cli(args){const r=await command(args,{cwd:temp,env});if(r.status!==0)throw Error('Isolated local CLI command failed (credential output suppressed)');return r.stdout;}
const owner=async action=>{console.log('Owner fixture stage: '+action);const s=await cli(['tools/owner.mjs',action,'--local','--origin',`http://127.0.0.1:${port}`]);const key=s.match(/Owner access key \(shown once; save privately\): ([A-Za-z0-9_-]{43})/);if(action!=='revoke')assert.ok(key,'Owner tool must issue a key only after successful CAS');return key?.[1];};
try {
 for(const name of ['src','tools','migrations','fixtures','package.json','wrangler.jsonc'])await cp(name,join(temp,name),{recursive:true});await symlink(resolve('node_modules'),join(temp,'node_modules'),'dir');
 console.log('Owner fixture stage: migrations');await cli(['node_modules/wrangler/bin/wrangler.js','d1','migrations','apply','DB','--local']);console.log('Owner fixture stage: seed');await cli(['node_modules/wrangler/bin/wrangler.js','d1','execute','DB','--local','--file','fixtures/demo.sql']);
 const key=await owner('init');assert.equal((await stat(join(temp,'.dev.vars'))).mode&0o777,0o600);
 const duplicate=await command(['tools/owner.mjs','init','--local'],{cwd:temp,env});assert.notEqual(duplicate.status,0);assert.ok(!duplicate.stdout.includes('Owner access key'));
 child=spawn(process.execPath,['node_modules/wrangler/bin/wrangler.js','dev','--local','--port',String(port)],{cwd:temp,env,detached:process.platform!=='win32',stdio:['ignore','pipe','pipe']});for(const stream of [child.stdout,child.stderr])stream.on('data',v=>{output=(output+v).slice(-10000);});
 let ready=false;for(let i=0;i<160;i++){if(child.exitCode!==null)throw Error('Local Wrangler exited');try{if((await fetch(`http://127.0.0.1:${port}/admin/login`,{signal:AbortSignal.timeout(2000)})).status===200){ready=true;break;}}catch{}await delay(250);}assert.ok(ready,'Local admin readiness timeout');
 await browserAcceptance(`http://127.0.0.1:${port}`,key,async()=>owner('recover'));
 await owner('revoke');console.log('Offline owner init/recover/session-revoke passed in fresh localD1; no existing user data or Cloudflare resource used');
} finally {
 if(child){terminate(child);child.stdout.destroy();child.stderr.destroy();child.unref();}await rm(temp,{recursive:true,force:true});
}
