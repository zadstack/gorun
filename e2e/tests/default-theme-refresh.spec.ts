import {test,expect} from '@playwright/test';
import {spawn,execFileSync,type ChildProcess} from 'node:child_process';
import {mkdtempSync,mkdirSync,rmSync,copyFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {createServer} from 'node:net';
async function freePort():Promise<number>{return await new Promise((ok,bad)=>{const s=createServer();s.on('error',bad);s.listen(0,'127.0.0.1',()=>{const a=s.address();if(!a||typeof a==='string')throw Error('Missing address');s.close(()=>ok(a.port));});});}
async function stop(child:ChildProcess){if(child.exitCode!==null)return;child.kill('SIGTERM');await new Promise<void>(ok=>{const timer=setTimeout(()=>child.kill('SIGKILL'),5000);child.once('exit',()=>{clearTimeout(timer);ok();});});}
// Uses a fresh temporary site, never the shared fixture database or live site.
test('actual starter import, honest content, RTL/mobile and unchanged saved data',async({page},testInfo)=>{
 test.setTimeout(120_000);
 const data=mkdtempSync(join(tmpdir(),'gorun-default-starter-'));const bin=resolve('.bin/gorun');
 const port=await freePort();const base=`http://127.0.0.1:${port}`;
 const env={...Object.fromEntries(Object.entries(process.env).filter(([k])=>!k.startsWith('GORUN_')&&!k.startsWith('STRIPE_'))),GORUN_ENV:'dev',GORUN_DATA:data,GORUN_THEMES:resolve('../themes'),GORUN_ADDR:`127.0.0.1:${port}`,GORUN_BASE_URL:base};
 const cli=(args:string[])=>execFileSync(bin,[...args,'--data',data],{env,encoding:'utf8',stdio:['ignore','pipe','pipe'],timeout:30_000});let child:ChildProcess|undefined;
 try{
  cli(['starter']);const before=JSON.parse(cli(['info']));
  expect(before.content.post.published).toBe(5);expect(before.content.page.published).toBe(4);expect(cli(['starter'])).toContain('nothing imported');
  // Deliberately synthetic, author-owned identity, never a bundled customer brand.
  cli(['settings','set','site.title','مساحة الأفكار']);cli(['settings','set','site.tagline','موقع توضيحي للملاحظات والأدلة وقصص المشاريع. استبدل هذه الأمثلة بمحتواك.']);
  child=spawn(bin,['serve'],{env,stdio:'ignore'});
  await expect.poll(async()=>{try{return(await fetch(base+'/healthz',{signal:AbortSignal.timeout(1500)})).status;}catch{return 0;}},{timeout:30_000}).toBe(200);
  const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));page.on('console',msg=>{if(msg.type()==='error')errors.push(msg.text());});
  const output=process.env.DEFAULT_THEME_CAPTURE_DIR?resolve(process.env.DEFAULT_THEME_CAPTURE_DIR):testInfo.outputPath('default-home');mkdirSync(output,{recursive:true});
  for(const width of [1440,390]){
   await page.setViewportSize({width,height:width===1440?1000:844});expect((await page.goto(base+'/'))?.status()).toBe(200);await page.evaluate(()=>document.fonts.ready);
   await expect(page.locator('html')).toHaveAttribute('dir','rtl');await expect(page.locator('h1')).toHaveText('مساحة الأفكار');
   await expect(page.locator('.dt-home-feature')).toContainText('مساحة لأفكارك، تبدأ بمثال');await expect(page.locator('.dt-grid .gr-card')).toHaveCount(4);await expect(page.locator('.dt-home-guide-list a')).toHaveCount(3);
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true);
   const rows=await page.locator('.dt-home-feature,.dt-grid .gr-card,.dt-home-guides').evaluateAll(els=>els.map(el=>{const r=el.getBoundingClientRect();return{x:r.x,y:r.y,right:r.right,bottom:r.bottom};}));
   for(let i=0;i<rows.length;i++)for(let j=i+1;j<rows.length;j++){const a=rows[i],b=rows[j];expect(Math.min(a.right,b.right)-Math.max(a.x,b.x)>1&&Math.min(a.bottom,b.bottom)-Math.max(a.y,b.y)>1).toBe(false);}
   const ratio=await page.locator('.dt-home-intro .gr-btn').evaluate(el=>{const s=getComputedStyle(el);const l=(c:string)=>{const v=c.match(/[\d.]+/g)!.slice(0,3).map(Number).map(x=>{x/=255;return x<=.04045?x/12.92:((x+.055)/1.055)**2.4;});return .2126*v[0]+.7152*v[1]+.0722*v[2];};const a=l(s.color),b=l(s.backgroundColor);return(Math.max(a,b)+.05)/(Math.min(a,b)+.05);});expect(ratio).toBeGreaterThanOrEqual(4.5);
   const screenshot=join(output,width===1440?'starter-desktop-ar.png':'starter-mobile-ar.png');await page.screenshot({path:screenshot,fullPage:true});
   if(width===1440&&process.env.DEFAULT_THEME_SCREENSHOT==='1')copyFileSync(screenshot,resolve('../themes/default/screenshot.png'));
  }
  const paths=await page.locator('.dt-home-feature a,.dt-home-guide-list a,.dt-grid .gr-card h2 a').evaluateAll(els=>[...new Set(els.map(el=>(el as HTMLAnchorElement).href))]);for(const path of paths)expect((await page.request.get(path)).status(),path).toBe(200);
  await page.locator('#dt-featured-title a').click();await expect(page.locator('.prose-gp .gp-example-note')).toContainText('محتوى توضيحي');
  await page.goto(base+'/en/');await expect(page.locator('html')).toHaveAttribute('dir','ltr');await expect(page.locator('h1')).toHaveText('مساحة الأفكار');await expect(page.locator('.gr-hero-kicker').first()).toHaveText('Notes, guides & considered ideas');
  // Changing UI locale never invents translations of authored Arabic articles.
  await expect(page.locator('.dt-home-feature')).toHaveCount(0);await expect(page.locator('.dt-empty')).toHaveText('Nothing published yet.');expect(errors).toEqual([]);expect(JSON.parse(cli(['info'])).content).toEqual(before.content);
 }finally{if(child)await stop(child);rmSync(data,{recursive:true,force:true});}
});
