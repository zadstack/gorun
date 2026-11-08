import {test,expect} from '@playwright/test';
import {setLocale} from '../lib/helpers.ts';

test('workspace navigation retains readable contrast, explicit selection and mobile navigation',async({page})=>{
 for(const scheme of ['light','dark'] as const){
  await page.emulateMedia({colorScheme:scheme});
  for(const locale of ['en','ar'] as const){
   await page.setViewportSize({width:1440,height:900});
   await page.goto('/admin/settings/general');await setLocale(page,locale);
   const result=await page.locator('[data-navigation-section] .admin-nav-link>svg, .admin-section-categories a').evaluateAll(elements=>{
    const rgb=(value:string)=>value.match(/[\d.]+/g)!.slice(0,3).map(Number);
    const luminance=(value:string)=>rgb(value).map(v=>{v/=255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4;}).reduce((sum,v,i)=>sum+v*[.2126,.7152,.0722][i],0);
    const background=(element:Element):string=>{
     for(let node:Element|null=element;node;node=node.parentElement){
      const color=getComputedStyle(node).backgroundColor,parts=color.match(/[\d.]+/g)!.map(Number);
      if(parts.length===3 || parts[3]===1)return color;
     }
     return 'rgb(255,255,255)';
    };
    return elements.map(element=>{const style=getComputedStyle(element),a=luminance(style.color),b=luminance(background(element));return{contrast:(Math.max(a,b)+.05)/(Math.min(a,b)+.05)};});
   });
   expect(result.length).toBeGreaterThan(5);
   for(const item of result)expect(item.contrast).toBeGreaterThanOrEqual(4.5);
   const active=page.locator('.admin-section-categories a[aria-current]');await expect(active).toHaveCount(1);expect(await active.evaluate(el=>Number(getComputedStyle(el).fontWeight))).toBeGreaterThanOrEqual(600);await active.focus();await expect(active).toBeFocused();
   await page.setViewportSize({width:390,height:844});await page.locator('[data-sidebar-toggle]').click();await expect(page.locator('#admin-sidebar')).toBeVisible();
   await page.locator('[data-navigation-section=content] a').click();await expect(page).toHaveURL(/\/admin\/content/);await expect(page.locator('[data-navigation-section=content] a')).toHaveAttribute('aria-current','true');
   expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);
  }
 }
});
