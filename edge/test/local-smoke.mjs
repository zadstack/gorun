// Exercises the documented persistent Wrangler/D1 setup through real HTTP.
// Run db:migrate and seed:local first. Uses only synthetic local fixtures.
import {spawn} from 'node:child_process';
import assert from 'node:assert/strict';
import {setTimeout as delay} from 'node:timers/promises';
const port=18787;
const child=spawn('node_modules/.bin/wrangler',['dev','--local','--port',String(port)],{env:{...process.env,WRANGLER_SEND_METRICS:'false'},stdio:['ignore','pipe','pipe']});
let output='';for(const stream of [child.stdout,child.stderr])stream.on('data',v=>{output=(output+v).slice(-12000);});
try {
  let ready=false;
  for(let i=0;i<120;i++) {
    if(child.exitCode!==null)throw new Error('Wrangler exited before readiness');
    try {const r=await fetch(`http://127.0.0.1:${port}/readyz`);if(r.status===200){ready=true;break;}}catch{}
    await delay(250);
  }
  assert.ok(ready,'local readiness timeout');
  const get=path=>fetch(`http://127.0.0.1:${port}${path}`);
  const en=await get('/post/welcome');assert.equal(en.status,200);assert.match(await en.text(),/Welcome to the Edge preview/);
  const ar=await get('/ar/post/welcome');assert.equal(ar.status,200);assert.match(await ar.text(),/مرحبًا/);
  const api=await(await get('/api/v1/content')).json();assert.equal(api.data[0].id,'welcome');
  const head=await fetch(`http://127.0.0.1:${port}/post/welcome`,{method:'HEAD'});assert.equal(head.status,200);assert.equal(await head.text(),'');
  assert.equal((await fetch(`http://127.0.0.1:${port}/api/v1/content`,{method:'POST',body:'{}'})).status,405);
  assert.equal((await get('/admin')).status,404);
  console.log('Documented local setup passed: real HTTP, D1, English/Arabic, API, HEAD and no writes/admin');
} catch(error) {console.error(output);throw error;} finally {
  child.kill('SIGTERM');
  await Promise.race([new Promise(resolve=>child.once('exit',resolve)),delay(5000)]);
  if(child.exitCode===null)child.kill('SIGKILL');
}
