import { test, expect } from '@playwright/test';

test.use({ timezoneId: 'Europe/London' });

test('searchable account timezone persists and formats dates after navigation and reload', async ({ page }) => {
  let timezone = '';
  const writes: unknown[] = [];
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '');
    let body: unknown = [];
    if (path === '/auth/session') body = { user_id: 1, admin: true, csrf_token: 'token', timezone };
    else if (path === '/auth/preferences') {
      expect(route.request().headers()['x-csrf-token']).toBe('token');
      writes.push(route.request().postDataJSON());
      timezone = route.request().postDataJSON().timezone;
      body = { timezone };
    } else if (path === '/sending') body = { enabled: false };
    else if (path === '/brands') body = [{ id: 1, name: 'Acme', from_email: 'a@example.com' }];
    else if (path === '/templates') body = [{ id: 1, name: 'Welcome', html: '<p>Hello</p>', updated_at: Date.UTC(2026, 0, 1, 1, 30) }];
    await route.fulfill({ json: body });
  });
  await page.goto('/account/preferences');
  const picker = page.getByRole('combobox', { name: 'Timezone', exact: true });
  await expect(picker).toHaveValue('Browser timezone (Europe/London)');
  await picker.fill('los angeles');
  await expect(page.getByRole('listbox').getByRole('option')).toHaveCount(1);
  await picker.press('Enter');
  await expect(page.getByRole('status')).toHaveText('Timezone saved.');
  await expect(picker).toHaveValue('America/Los_Angeles');
  expect(writes).toEqual([{ timezone: 'America/Los_Angeles' }]);
  await page.goto('/templates');
  await expect(page.getByRole('row').filter({ hasText: 'Welcome' })).toContainText('Dec 31, 2025');
  await expect(page.getByRole('row').filter({ hasText: 'Welcome' })).toContainText('5:30');
  await page.goto('/account/preferences');
  await expect(picker).toHaveValue('America/Los_Angeles');
  await picker.fill('no-such-zone');
  await expect(page.getByText('No matching timezones.')).toBeVisible();
  await picker.press('Escape');
  await expect(picker).toHaveValue('America/Los_Angeles');
  await picker.fill('browser');
  await picker.press('Enter');
  await expect(picker).toHaveValue('Browser timezone (Europe/London)');
  expect(timezone).toBe('');
});

test('failed preference save retains the active timezone and can be retried', async ({ page }) => {
  let failed = true;
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '');
    if (path === '/auth/preferences') {
      await route.fulfill(failed ? { status: 422, json: { error: 'Timezone could not be saved' } } : { json: { timezone: 'UTC' } });
      return;
    }
    await route.fulfill({ json: path === '/auth/session' ? { user_id: 1, admin: false, csrf_token: 'token', timezone: 'Asia/Tokyo' } : [] });
  });
  await page.goto('/account/preferences');
  const picker = page.getByRole('combobox', { name: 'Timezone', exact: true });
  await picker.fill('UTC');
  await picker.press('Enter');
  await expect(page.getByRole('alert')).toHaveText('Timezone could not be saved');
  await expect(picker).toHaveValue('Asia/Tokyo');
  failed = false;
  await picker.fill('UTC');
  await picker.press('Enter');
  await expect(picker).toHaveValue('UTC');
});

test('graph axes and tooltips convert UTC instants to the saved timezone', async ({ page }) => {
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '');
    let body: unknown = [];
    if (path === '/auth/session') body = { user_id: 1, admin: false, csrf_token: 'token', timezone: 'America/Los_Angeles' };
    else if (path === '/brands') body = [{ id: 1, name: 'Acme' }];
    else if (path === '/aws-settings') body = { has_credentials: false };
    else if (path === '/overview') body = {
      period: { days: 30, end: Date.UTC(2026, 0, 2, 12) }, current: { sent: 1 }, change: {}, growth: [],
      hourly_comparison: Array.from({ length: 24 }, (_, hour) => ({ hour, today: hour + 1, yesterday: hour })),
    };
    await route.fulfill({ json: body });
  });
  await page.goto('/');
  const graph = page.getByRole('group', { name: /Hourly sent emails/ });
  await expect(graph).toContainText('4:00 PM');
  await graph.focus();
  await graph.press('Home');
  await expect(page.getByRole('status')).toContainText('Jan 1, 2026');
  await expect(page.getByRole('status')).toContainText('04:00 PM');
  await expect(page.getByRole('status')).toContainText('PST');
});
