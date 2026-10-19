import {test,expect} from '@playwright/test';
import {db,setLocale} from '../lib/helpers.ts';
import {mkdirSync} from 'node:fs';
import {resolve} from 'node:path';

test('extension installation methods are honest and readable in English/Arabic on mobile',async({page})=>{
 const database=db(),slugs=['method-directory-example','method-upload-example','method-legacy-example'];
 try{
  for(const[index,source]of ['directory','upload','legacy_unknown'].entries()){
   const slug=slugs[index],manifest={slug,name:{en:`Example ${source}`,ar:`مثال ${source}`},version:'1.0.0',endpoint:{url:'https://example.test/extension',auth:'hmac'},hooks:[],permissions:[]};
   database.prepare("INSERT INTO extensions(slug,name,version,manifest_json,status,endpoint_url,secret_enc,installed_at,install_source) VALUES(?,?,?,?,'installed',?,'',?,?)").run(slug,`Example ${source}`,'1.0.0',JSON.stringify(manifest),manifest.endpoint.url,new Date().toISOString(),source);
  }
  const output=resolve('../../../outputs/pr82-repair');mkdirSync(output,{recursive:true});
  for(const locale of ['en','ar']){
   await page.goto('/admin/extensions');await setLocale(page,locale);await page.setViewportSize({width:390,height:844});
   const labels=locale==='en'?['Server directory','Uploaded ZIP','Legacy: method not recorded']:['من مجلد الخادم','ملف ZIP مرفوع','تثبيت سابق: الطريقة غير مسجلة'];
   for(const[index,source]of ['directory','upload','legacy_unknown'].entries()){
    const section=page.locator(`[data-install-source="${source}"]`),row=section.locator(`[data-ext="${slugs[index]}"]`);
    await expect(row).toHaveCount(1);await expect(row.locator('[data-extension-source]')).toHaveText(labels[index]);
    await expect(page.locator(`section:not([data-install-source="${source}"])>div>table [data-ext="${slugs[index]}"]`)).toHaveCount(0);
   }
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true);
   await page.screenshot({path:resolve(output,`extensions-${locale}-mobile.png`),fullPage:true});
   await page.locator(`[data-ext="${slugs[2]}"] a`).first().click();await expect(page.locator('[data-extension-source]')).toHaveText(labels[2]);await expect(page.locator('[data-reveal-secret]')).toHaveCount(0);
   await expect(page.locator('input[name="_csrf"]').first()).toHaveAttribute('value',/\S+/);
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true);
  }
 }finally{for(const slug of slugs)database.prepare('DELETE FROM extensions WHERE slug=?').run(slug);database.close();}
});
