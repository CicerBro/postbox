import { test, expect } from '@playwright/test';

test('campaign table requests summaries and segment rules use paginated content-free options', async ({ page, context }) => {
  const campaignRequests: URL[] = [];
  await context.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace('/api/v1', '');
    let body: unknown = [];
    if (path === '/auth/session') body = { user_id: 1, admin: true, csrf_token: 'test-csrf' };
    else if (path === '/brands') body = [{ id: 1, name: 'Acme', from_email: 'acme@example.org' }];
    else if (path === '/sending') body = { enabled: false };
    else if (path === '/campaigns') {
      campaignRequests.push(url);
      expect(url.searchParams.get('summary')).toBe('true');
      body = [{ id: 1, subject: 'Visible campaign', excerpt: 'Visible newsletter body', status: 'draft', created_at: 1, scheduled_at: null }];
    } else if (path === '/campaigns/options') {
      campaignRequests.push(url);
      const after = Number(url.searchParams.get('after'));
      expect(url.searchParams.get('limit')).toBe('500');
      body = after === 0
        ? Array.from({ length: 500 }, (_, index) => ({ id: index + 1, subject: `Campaign choice ${index + 1}` }))
        : [{ id: 501, subject: 'Last campaign choice' }];
    } else if (path === '/segments/preview') body = { count: 0 };
    await route.fulfill({ json: body });
  });
  await page.goto('/campaigns');
  await expect(page.getByRole('link', { name: 'Visible campaign', exact: true })).toBeVisible();
  await page.goto('/segments');
  await page.getByRole('button', { name: 'New segment', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('combobox').nth(0).click();
  await page.getByRole('option', { name: 'Opened campaign', exact: true }).click();
  await dialog.getByRole('combobox').nth(2).click();
  await expect(page.getByRole('option', { name: 'Last campaign choice', exact: true })).toBeVisible();
  await page.getByRole('option', { name: 'Last campaign choice', exact: true }).click();
  const optionsRequests = campaignRequests.filter(url => url.pathname.endsWith('/options'));
  expect(optionsRequests.map(url => url.searchParams.get('after'))).toEqual(['0', '500']);
  expect(campaignRequests.filter(url => url.pathname === '/api/v1/campaigns')).toHaveLength(1);
});
