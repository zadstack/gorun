import { test, expect } from '@playwright/test';

test('saved role clone has an explicit review of additions and removals',async({page})=>{
 await page.goto('/admin/roles');
 await page.locator('a[href="/admin/roles/new?clone=editor"]').click();
 await expect(page.locator('[data-role-added] li')).not.toHaveCount(0);
 const permission=page.locator('input[name=permissions]:checked').first();
 const value=await permission.inputValue();
 await expect(page.locator('[data-role-added]')).toContainText(value);
 await permission.uncheck();
 await expect(page.locator('[data-role-added]')).not.toContainText(value);
 await page.goto('/admin/roles/editor');
 await expect(page.locator('[data-role-added] li')).toHaveCount(0);
 await expect(page.locator('[data-role-removed] li')).toHaveCount(0);
 const existing=page.locator('input[name=permissions]:checked').first();
 const removed=await existing.inputValue();await existing.uncheck();
 await expect(page.locator('[data-role-removed]')).toContainText(removed);
 await page.locator(`input[name=permissions][value="${removed}"]`).check();await expect(page.locator('[data-role-removed] li')).toHaveCount(0);
 await page.goto('/admin/roles/admin');
 await expect(page.locator('[data-role-added] li')).toHaveCount(0);
 await expect(page.locator('[data-role-removed] li')).toHaveCount(0);
});

test('account views identify the saved customer role without granting permissions',async({page})=>{
 await page.goto('/admin/users?audience=customers');
 await expect(page.locator('input[name=audience]')).toHaveValue('customers');
 await expect(page.locator('nav a[href="/admin/users?audience=customers"]')).toHaveAttribute('aria-current','page');
 await page.goto('/admin/users?audience=team');
 await expect(page.locator('input[name=audience]')).toHaveValue('team');
 await expect(page.locator('nav a[href="/admin/users?audience=team"]')).toHaveAttribute('aria-current','page');
 await page.locator('#users-results tbody a[href^="/admin/users/"]').first().click();
 await page.locator('[data-user-role-summary] summary').click();
 await expect(page.locator('[data-user-role-summary]')).toContainText(/كل الصلاحيات|All permissions/);
});
