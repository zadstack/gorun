// Uses the repository's existing e2e-only Playwright dependency.
import {chromium} from '../../e2e/node_modules/playwright/index.mjs';
import {createServer} from 'node:http';
import {readFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import assert from 'node:assert/strict';
const root = fileURLToPath(new URL('../../',import.meta.url));
const assets = root+'web/admin/static/';
const manifest = JSON.parse(readFileSync(root+'tools/admin-icons/manifest.json'));
let fullRequests = 0, failNext = false, fullDelay = 0;
const server = createServer((req,res) => {
  const path = req.url.split('?')[0];
  if (path === '/') {
    res.setHeader('Content-Type','text/html');
    return res.end(`<html><head><script defer src="/admin.js"></script><script defer src="/vendor/lucide-core-1.55.0.js" integrity="${manifest.coreIntegrity}" crossorigin="anonymous" data-lucide-full="/vendor/lucide-1.55.0.js" data-lucide-integrity="${manifest.sourceIntegrity}"></script></head><body><i data-lucide="menu" class="test-class" aria-label="Open menu"></i><div id="dynamic"></div></body></html>`);
  }
  if (path === '/vendor/lucide-1.55.0.js') {
    fullRequests++;
    const fail = failNext; failNext=false;
    if (fullDelay || fail) return setTimeout(() => {
      if (fail) {res.writeHead(503);res.end('fixture failure');}
      else {res.setHeader('Content-Type','text/javascript');res.end(readFileSync(assets+path.slice(1)));}
    },fullDelay);
  }
  if (!['/admin.js','/vendor/lucide-core-1.55.0.js','/vendor/lucide-1.55.0.js'].includes(path)) {res.writeHead(404);return res.end();}
  res.setHeader('Content-Type','text/javascript');
  res.end(readFileSync(assets+path.slice(1)));
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const origin = 'http://127.0.0.1:'+server.address().port;
const browser = await chromium.launch({headless:true});
let checks=0;
try {
  const page = await browser.newPage();
  await page.goto(origin);
  assert.equal(await page.locator('svg[data-lucide="menu"]').count(),1); checks++;
  assert.equal(fullRequests,0); checks++;
  assert.equal(await page.locator('svg').getAttribute('aria-label'),'Open menu'); checks++;
  const first = await page.evaluate(()=>{
    document.getElementById('dynamic').innerHTML='<i data-lucide="arrow-left" class="rtl:rotate-180"></i>';
    document.dispatchEvent(new Event('htmx:afterSwap'));
    return document.querySelector('#dynamic svg')?.getAttribute('class');
  });
  assert.ok(first.includes('rtl:rotate-180')); checks++;
  await page.evaluate(()=>{
    document.getElementById('dynamic').innerHTML='<i data-lucide="save"></i>';
    // The native Alpine menu calls this exact API after a nextTick.
    window.lucide.createIcons();
  });
  assert.equal(await page.locator('#dynamic svg[data-lucide="save"]').count(),1); checks++;
  assert.equal(fullRequests,0); checks++;
  const parity = await page.evaluate(async names=>{
    const root=document.getElementById('dynamic');
    root.innerHTML=names.map(n=>`<i data-lucide="${n}" class="w-4 h-4" aria-label="Icon"></i>`).join('');
    lucide.createIcons();
    const before=root.innerHTML;
    const script=document.createElement('script');script.src='/vendor/lucide-1.55.0.js';
    await new Promise((resolve,reject)=>{script.onload=resolve;script.onerror=reject;document.head.appendChild(script)});
    root.innerHTML=names.map(n=>`<i data-lucide="${n}" class="w-4 h-4" aria-label="Icon"></i>`).join('');
    lucide.createIcons();return before===root.innerHTML;
  },manifest.icons);
  assert.ok(parity); checks++;
  await page.close();
  for (const fail of [false,true]) {
    const p=await browser.newPage();await p.goto(origin);
    const before=fullRequests;failNext=fail;fullDelay=600;
    const immediate=await p.evaluate(()=>{
      window.savedIconRenderer = lucide.createIcons;
      document.getElementById('dynamic').innerHTML='<i data-lucide="menu" class="core-class"></i><i data-lucide="a-arrow-down"></i><i data-lucide="not-a-real-icon"></i><template><i data-lucide="menu"></i><i data-lucide="a-arrow-down"></i></template>';
      lucide.createIcons({root:document.getElementById('dynamic'),attrs:{stroke:'red'},inTemplates:true});
      lucide.createIcons();
      return {core:!!document.querySelector('#dynamic svg[data-lucide="menu"]'),nested:!!document.querySelector('template').content.querySelector('svg[data-lucide="menu"]'),unknown:!!document.querySelector('#dynamic i[data-lucide="a-arrow-down"]')};
    });
    assert.deepEqual(immediate,{core:true,nested:true,unknown:true},'known icons must render synchronously in mixed roots/templates');checks++;
    assert.equal(await p.locator('#dynamic svg[data-lucide="menu"]').getAttribute('stroke'),'red');checks++;
    await p.evaluate(()=>{document.getElementById('dynamic').insertAdjacentHTML('beforeend','<i data-lucide="save"></i>');document.dispatchEvent(new Event('htmx:afterSwap'));});
    assert.equal(await p.locator('#dynamic svg[data-lucide="save"]').count(),1,'known icon added while fallback pending');checks++;
    if(fail){
      await p.waitForFunction(()=>!document.querySelector('script[src="/vendor/lucide-1.55.0.js"]'));
      assert.equal(await p.locator('#dynamic i[data-lucide="a-arrow-down"]').count(),1);checks++;
      assert.equal(await p.locator('#dynamic svg[data-lucide="menu"]').count(),1,'known core icon survives failed fallback');checks++;
      await p.evaluate(()=>document.dispatchEvent(new Event('htmx:afterSwap')));
    }
    await p.locator('svg[data-lucide="a-arrow-down"]').waitFor();
    assert.equal(fullRequests-before,fail?2:1);checks++;
    assert.equal(await p.locator('i[data-lucide="not-a-real-icon"]').count(),1);checks++;
    await p.evaluate(()=>lucide.createIcons());
    assert.equal(fullRequests-before,fail?2:1);checks++;
    await p.evaluate(()=>{ document.getElementById('dynamic').innerHTML='<i data-lucide="a-arrow-down"></i>'; window.savedIconRenderer(); });
    assert.equal(await p.locator('#dynamic svg[data-lucide="a-arrow-down"]').count(),1,'saved pre-fallback renderer uses current full collection');checks++;
    let warnings=0;p.on('console',m=>{if(m.type()==='warning')warnings++});
    await p.evaluate(()=>{document.getElementById('dynamic').innerHTML='<i data-lucide="not-a-real-icon"></i>';window.savedIconRenderer();});
    assert.equal(warnings,1,'retained wrapper delegates upstream invalid-name behavior after fallback');checks++;
    assert.equal(fullRequests-before,fail?2:1);checks++;
    fullDelay=0;await p.close();
  }
  const scoped=await browser.newPage();await scoped.goto(origin);fullDelay=600;
  const scopedState=await scoped.evaluate(()=>{
    const root=document.getElementById('dynamic');
    root.innerHTML='<i data-symbol="menu"></i><i data-symbol="a-arrow-down"></i><template><i data-symbol="menu"></i><i data-symbol="a-arrow-down"></i></template>';
    lucide.createIcons({root,nameAttr:'data-symbol',attrs:{width:33},inTemplates:true});
    return {core:root.querySelector('svg')?.getAttribute('width'),nested:root.querySelector('template').content.querySelector('svg')?.getAttribute('width'),pending:!!root.querySelector('i[data-symbol="a-arrow-down"]')};
  });
  assert.deepEqual(scopedState,{core:'33',nested:'33',pending:true});checks++;
  await scoped.locator('#dynamic>svg[data-symbol="a-arrow-down"]').waitFor();
  assert.equal(await scoped.locator('#dynamic>svg[data-symbol="a-arrow-down"]').getAttribute('width'),'33');checks++;
  assert.ok(await scoped.evaluate(()=>document.querySelector('template').content.querySelector('svg[data-symbol="a-arrow-down"]')));checks++;
  fullDelay=0;await scoped.close();
  const p=await browser.newPage();await p.goto(origin);const before=fullRequests;
  await p.evaluate(()=>{
    document.getElementById('dynamic').innerHTML='<i data-custom="bespoke" aria-label="Custom"></i><template><i data-custom="bespoke"></i></template>';
    lucide.createIcons({icons:{Bespoke:[['path',{d:'M1 1h2'}]]},root:document.getElementById('dynamic'),nameAttr:'data-custom',attrs:{stroke:'red'},inTemplates:true});
  });
  assert.equal(await p.locator('#dynamic>svg').getAttribute('stroke'),'red');checks++;
  assert.ok(await p.evaluate(()=>document.querySelector('template').content.querySelector('svg')));checks++;
  assert.equal(fullRequests,before);checks++;
  const explicitErrors=await p.evaluate(()=>[{},null].map(icons=>{try{lucide.createIcons({icons});return false}catch{return true}}));
  assert.deepEqual(explicitErrors,[true,true]);checks++;
  assert.equal(fullRequests,before);checks++;
  console.log(JSON.stringify({pass:true,checks,coreIcons:manifest.icons.length,coreSHA256:createHash('sha256').update(readFileSync(assets+'vendor/lucide-core-1.55.0.js')).digest('hex')}));
} finally {await browser.close();await new Promise(resolve=>server.close(resolve));}
