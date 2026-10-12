import { test, expect } from '@playwright/test';

for (const admin of [true, false]) {
  test(`display name saves, survives reload, and falls back to email (${admin ? 'admin' : 'member'})`, async ({ page }) => {
    let displayName = '';
    let fail = false;
    await page.route('**/api/v1/**', async route => {
      const path = new URL(route.request().url()).pathname.replace('/api/v1', '');
      let body: unknown = [];
      if (path === '/auth/session') body = { user_id: 1, email: 'ada@example.org', display_name: displayName, admin, csrf_token: 'token', timezone: 'UTC' };
      else if (path === '/auth/preferences') {
        expect(route.request().headers()['x-csrf-token']).toBe('token');
        if (fail) {
          await route.fulfill({ status: 400, json: { error: 'Display name could not be saved' } });
          return;
        }
        displayName = route.request().postDataJSON().display_name.trim();
        body = { display_name: displayName, timezone: 'UTC' };
      } else if (path === '/brands') body = [{ id: 1, name: 'Acme' }];
      else if (path === '/sending') body = { enabled: false };
      await route.fulfill({ json: body });
    });
    await page.goto('/account/preferences');
    const account = page.getByRole('button', { name: 'Account menu', exact: true });
    await expect(account).toContainText('ada@example.org');
    if (admin) await expect(account).toContainText('Administrator');
    else await expect(account).not.toContainText('Administrator');
    const input = page.getByRole('textbox', { name: 'Display name', exact: true });
    await input.fill(' Ada Lovelace ');
    await page.getByRole('button', { name: 'Save display name' }).click();
    await expect(account).toContainText('Ada Lovelace');
    await expect(input).toHaveValue('Ada Lovelace');
    await page.reload();
    await expect(account).toContainText('Ada Lovelace');
    await expect(input).toHaveValue('Ada Lovelace');
    fail = true;
    await input.fill('Unstored');
    await page.getByRole('button', { name: 'Save display name' }).click();
    await expect(page.getByRole('alert')).toHaveText('Display name could not be saved');
    await expect(account).toContainText('Ada Lovelace');
    fail = false;
    await input.fill('');
    await page.getByRole('button', { name: 'Save display name' }).click();
    await expect(account).toContainText('ada@example.org');
    await page.reload();
    await expect(account).toContainText('ada@example.org');
    await expect(input).toHaveValue('');
  });
}
