// Regression for Wrangler's different remote query/import protocols. Not a remote deployment test.
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,readFile,rm,copyFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {createHash} from 'node:crypto';
const mock=String.raw`
const fs=require('node:fs'),args=process.argv.slice(2),file='state.json';
let state=fs.existsSync(file)?JSON.parse(fs.readFileSync(file,'utf8')):null;
if(args.includes('--command')) {
 const query=args[args.indexOf('--command')+1];
 if(query!=="SELECT auth_epoch,key_hash FROM edge_owners WHERE site_id='demo';")throw Error('Only static public-site query may be in argv');
 console.log(JSON.stringify([{results:state?[state]:[],success:true}]));
} else {
 const sqlFile=args[args.indexOf('--file')+1];if((fs.statSync(sqlFile).mode&0o777)!==0o600)throw Error('Mutation SQL must be private');
 const sql=fs.readFileSync(sqlFile,'utf8');
 if(!process.env.FAKE_SKIP_MUTATION) {
  if(sql.startsWith('INSERT')) {if(!state)state={auth_epoch:1,key_hash:sql.match(/VALUES\('demo','([a-f0-9]{64})'\)/)[1]};}
  else if(state&&state.auth_epoch===Number(sql.match(/AND auth_epoch=(\d+)/)[1])) {state.auth_epoch++;const hash=sql.match(/key_hash='([a-f0-9]{64})'/);if(hash)state.key_hash=hash[1];}
  if(process.env.FAKE_WRONG_HASH)state.key_hash='f'.repeat(64);
  if(process.env.FAKE_RACE_AFTER_WRITE){state.auth_epoch++;state.key_hash='0'.repeat(64);}
  fs.writeFileSync(file,JSON.stringify(state));
 }
 // Real remote --file emits progress even with --json, followed by import metadata.
 console.log('├ Checking if file needs uploading');console.log(JSON.stringify([{results:[{'Total queries executed':1,'Rows written':1}],success:true}]));
}
`;
test('remote owner CLI ignores import progress/metadata and verifies actual epoch plus generated hash',async()=>{
 const dir=await mkdtemp(join(tmpdir(),'gorun-owner-protocol-'));
 try {
  await mkdir(join(dir,'node_modules/wrangler/bin'),{recursive:true});await mkdir(join(dir,'tools'));
  await writeFile(join(dir,'package.json'),JSON.stringify({name:'gorun-edge-readonly-poc',type:'module'}));
  await writeFile(join(dir,'node_modules/wrangler/package.json'),'{"type":"commonjs"}');await writeFile(join(dir,'node_modules/wrangler/bin/wrangler.js'),mock);
  await copyFile(new URL('../tools/owner.mjs',import.meta.url),join(dir,'tools/owner.mjs'));
  const run=(command,extra={})=>spawnSync(process.execPath,['tools/owner.mjs',command,'--remote','--config','private-config.jsonc'],{cwd:dir,env:{...process.env,...extra},encoding:'utf8'});
  const key=out=>out.stdout.match(/Owner access key \(shown once; save privately\): ([A-Za-z0-9_-]{43})/)?.[1];
  const state=async()=>JSON.parse(await readFile(join(dir,'state.json'),'utf8'));
  const init=run('init');assert.equal(init.status,0);assert.ok(key(init));assert.equal((await state()).key_hash,createHash('sha256').update('gorun-edge-owner:v1:demo:'+key(init)).digest('hex'));
  const duplicate=run('init');assert.notEqual(duplicate.status,0);assert.ok(!key(duplicate));
  const recover=run('recover');assert.equal(recover.status,0);assert.ok(key(recover));assert.notEqual(key(init),key(recover));assert.equal((await state()).auth_epoch,2);
  const revoked=run('revoke');assert.equal(revoked.status,0);assert.ok(!key(revoked));assert.equal((await state()).auth_epoch,3);
  const skipped=run('recover',{FAKE_SKIP_MUTATION:'1'});assert.notEqual(skipped.status,0);assert.ok(!key(skipped));
  const wrongHash=run('recover',{FAKE_WRONG_HASH:'1'});assert.notEqual(wrongHash.status,0);assert.ok(!key(wrongHash));
  const raced=run('recover',{FAKE_RACE_AFTER_WRITE:'1'});assert.notEqual(raced.status,0);assert.ok(!key(raced));
 } finally {await rm(dir,{recursive:true,force:true});}
});
