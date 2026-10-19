import { test, expect } from '@playwright/test';
import { db, setLocale } from '../lib/helpers.ts';

test('agent text becomes one human-reviewed draft; private structure can be reused', async ({page},testInfo)=>{
 await page.goto('/admin/canvases');await setLocale(page,'en');
 await page.locator('#new-canvas-title').fill('Lifecycle browser journey');
 await page.locator('form[action="/admin/canvases"] [type=submit]').click();
 await expect(page).toHaveURL(/\/admin\/canvases\/[A-Z0-9]+$/);
 const boardURL=new URL(page.url()).pathname, board=boardURL.split('/').pop()!;
 const card='77777777-7777-4777-8777-777777777777';
 const templateTitle='Launch structure '+board;
 // Only synthetic external-agent output is inserted into the throwaway fixture.
 const database=db();
 const owner=database.prepare('SELECT id FROM users WHERE email=?').get('admin@e2e.test') as {id:string};
 database.prepare('UPDATE admin_canvases SET document_json=? WHERE id=?').run(JSON.stringify([{id:card,kind:'note',title:'Campaign draft',body:'A practical launch plan.\nReview before publication.',x:1400,y:1200}]),board);
 const run=board;
 database.prepare("INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,result_card_id,created_at) VALUES(?,?,?,?,?,'succeeded',?,?,?)").run(run,board,owner.id,'synthetic-lifecycle-result',card,JSON.stringify({candidate:{input:{kind:'text'}}}),card,new Date().toISOString());
 database.prepare("INSERT INTO admin_canvas_provenance(board_id,card_id,actor_kind,actor_name,run_id,created_at) VALUES(?,?,'api_key','Synthetic test agent',?,?)").run(board,card,run,new Date().toISOString());database.close();
 await page.goto(boardURL+'?fixture=ready#card-'+card);
 const result=page.locator('#card-'+card);await expect(result).toBeVisible();await expect(result).toBeInViewport();
 for(const view of ['grid','list','flow']){
  await page.locator(`[data-canvas-view="${view}"]`).click();
  await page.evaluate(()=>{location.hash='';});await page.evaluate(value=>{location.hash=value;},'card-'+card);
  await expect(result).toBeInViewport();
 }
 await result.getByRole('button',{name:'Create content draft',exact:true}).click();
 const dialog=page.locator('[data-draft-dialog]');await expect(dialog).toBeVisible();
 await page.screenshot({path:testInfo.outputPath('review-text-as-draft.png')});
 await dialog.locator('select[name=type]').selectOption('post');await dialog.locator('[type=submit]').click();
 await expect(page).toHaveURL(/\/admin\/content\/post\/[A-Z0-9]+$/);
 const firstDraft=new URL(page.url()).pathname;
 const check=db();const item=check.prepare('SELECT status,body FROM content WHERE id=?').get(firstDraft.split('/').pop()!) as {status:string,body:string};expect(item.status).toBe('draft');expect(item.body).toContain('practical launch');check.close();
 await page.goto(boardURL);await page.locator('#card-'+card).getByRole('button',{name:'Create content draft',exact:true}).click();await dialog.locator('[type=submit]').click();await expect(page).toHaveURL(new RegExp(firstDraft+'$'));
 await page.goto(boardURL);
 // Adding a personal note still opens the card editor, not the draft dialog.
 await page.locator('[data-action=add]').click();
 const cardEditor=page.locator('dialog[aria-labelledby=canvas-dialog-title]');await expect(cardEditor).toBeVisible();
 await cardEditor.locator('[data-action=cancel]').first().click();
 await page.locator('.canvas-templates summary').click();
 page.on('dialog',async dialog=>{if(dialog.type()==='confirm')await dialog.accept();else await dialog.accept(templateTitle);});
 await page.locator('[data-save-template]').click();
 await expect(page.locator('[data-owned-templates]')).toContainText('Launch structure');
 await page.locator('[data-owned-templates] .canvas-template-row').filter({hasText:templateTitle}).getByRole('button',{name:'Create board',exact:true}).click();
 await expect(page).not.toHaveURL(new RegExp(board+'$'));
 await expect(page.locator('[data-canvas-cards] .canvas-card')).toHaveCount(1);
 await expect(page.locator('[data-canvas-cards]')).not.toContainText('A practical launch plan');
 await expect(page.locator('[data-canvas-cards]')).not.toContainText('Synthetic test agent');
 await setLocale(page,'ar');await page.locator('.canvas-templates summary').click();await expect(page.locator('[data-save-template]')).toHaveText('حفظ الهيكل كقالب');
 await page.setViewportSize({width:390,height:844});await page.goto(boardURL+'?mobile=proof#card-'+card);await expect(page.locator('#card-'+card)).toBeInViewport();
 await page.screenshot({path:testInfo.outputPath('mobile-attributed-text.png')});
});
