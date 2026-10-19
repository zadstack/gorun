// Offline operator tool. Never exposes a bootstrap or recovery HTTP endpoint.
import {randomBytes,createHash} from 'node:crypto';
import {readFile,writeFile,unlink,mkdtemp} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {spawnSync} from 'node:child_process';
const args=process.argv.slice(2),command=args.shift();
const local=args.includes('--local'),remote=args.includes('--remote');
const value=name=>{const i=args.indexOf(name);return i<0?'':args[i+1]??'';};
const site=value('--site')||'demo',config=value('--config');
if(!['init','recover','revoke'].includes(command)||local===remote||!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(site)||remote&&!config)throw Error('Usage: owner.mjs init|recover|revoke (--local | --remote --config FILE) [--site ID] [--origin LOOPBACK_URL]');
const pkg=JSON.parse(await readFile('package.json','utf8'));if(pkg.name!=='gorun-edge-readonly-poc')throw Error('Run from the standalone edge/ directory');
const quote=s=>"'"+s.replaceAll("'","''")+"'";
const privateDir=await mkdtemp(join(tmpdir(),'gorun-edge-owner-'));let sequence=0;
async function sql(command,query=false) {
 const file=join(privateDir,`${sequence++}.sql`);await writeFile(file,command,{mode:0o600});
 try {const input=remote&&query?['--command',command]:['--file',file];const r=spawnSync(process.execPath,['node_modules/wrangler/bin/wrangler.js','d1','execute','DB',local?'--local':'--remote','--json',...input,...(config?['--config',config]:[])],{encoding:'utf8',env:{...process.env,WRANGLER_SEND_METRICS:'false'},maxBuffer:1024*1024});if(r.status!==0)throw Error('D1 command failed; no credential issued. Check the selected Edge database and migrations.');return remote&&!query?null:JSON.parse(r.stdout);}finally{await unlink(file);}
}
function rows(output){return output.flatMap(x=>x.results??[]);}
const existing=rows(await sql(`SELECT auth_epoch,key_hash FROM edge_owners WHERE site_id=${quote(site)};`,true));
let newConfig=false;
try {
 let raw,keyHash;
 if(command!=='revoke'){raw=randomBytes(32).toString('base64url');keyHash=createHash('sha256').update(`gorun-edge-owner:v1:${site}:${raw}`).digest('hex');}
 if(command==='init') {
  if(existing.length)throw Error('Owner already exists; use explicit recover to rotate access without replacing content');
  if(local) {
   const origin=new URL(value('--origin')||'http://localhost:8787');if(origin.protocol!=='http:'||!['localhost','127.0.0.1','[::1]'].includes(origin.hostname)||origin.pathname!=='/'||origin.search||origin.hash||origin.username||origin.password)throw Error('Local admin origin must be an exact loopback HTTP origin');
   await writeFile('.dev.vars',`SITE_ID=${site}\nEDGE_ADMIN_ENABLED=true\nEDGE_ADMIN_LOCAL=true\nEDGE_ADMIN_ORIGIN=${origin.origin}\nEDGE_ADMIN_SECRET=${randomBytes(32).toString('base64url')}\n`,{mode:0o600,flag:'wx'});newConfig=true;
  }
 }
 if(command!=='init'&&existing.length!==1)throw Error('No owner exists for selected site');
 const epoch=existing[0]?.auth_epoch;
 const statement=command==='init'?`INSERT INTO edge_owners(site_id,key_hash) VALUES(${quote(site)},${quote(keyHash)}) ON CONFLICT(site_id) DO NOTHING RETURNING auth_epoch;`:`UPDATE edge_owners SET auth_epoch=auth_epoch+1${command==='recover'?`,key_hash=${quote(keyHash)},active=1`:''} WHERE site_id=${quote(site)} AND auth_epoch=${Number(epoch)} RETURNING auth_epoch;`;
 let changed;
 if(remote) {
  // Remote --file uses D1 import: output is progress/metadata, never query rows.
  // Only this static public-site SELECT is placed on argv. Hashes stay captured.
  await sql(statement.replace(/ RETURNING auth_epoch;$/,';'));
  changed=rows(await sql(`SELECT auth_epoch,key_hash FROM edge_owners WHERE site_id=${quote(site)};`,true));
  const expectedEpoch=command==='init'?1:Number(epoch)+1;
  const expectedHash=command==='revoke'?existing[0].key_hash:keyHash;
  if(changed.length!==1||changed[0].auth_epoch!==expectedEpoch||changed[0].key_hash!==expectedHash)changed=[];
 } else changed=rows(await sql(statement));
 if(changed.length!==1)throw Error('Owner changed concurrently; no credential issued. Rerun from the current state.');
 newConfig=false;
 console.log(`Owner ${command} succeeded for site ${site}; current auth epoch ${changed[0].auth_epoch}. Content is unchanged.`);
 if(raw)console.log(`Owner access key (shown once; save privately): ${raw}`);
 if(local&&command==='init')console.log('Private local .dev.vars created. Start Wrangler, then open the pinned admin origin /admin.');
 if(remote&&command==='init')console.log('Remote admin remains disabled until its exact HTTPS origin, enable flag and random secret are configured by the operator. No deployment or secret configuration was performed.');
} finally {if(newConfig)await unlink('.dev.vars');const {rmdir}=await import('node:fs/promises');await rmdir(privateDir);}
