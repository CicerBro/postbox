import { test, expect } from '@playwright/test';

for (const status of ['sent', 'cancelled']) {
  test(`${status} campaigns open reports and previews without sending`, async ({ page, context }) => {
    const writes: string[] = [];
    await context.route('**/api/v1/**', async route => {
      const request = route.request();
      const path = new URL(request.url()).pathname.replace('/api/v1', '');
      if (request.method() !== 'GET') writes.push(`${request.method()} ${path}`);
      let body: unknown = [];
      const campaign = { id: 7, subject: 'Historical newsletter', html: '<p>Historical email body</p>', text: '', inline_css: false, status, created_at: 1700000000000, scheduled_at: null, created_by: null };
      if (path === '/auth/session') body = { user_id: 1, admin: true, csrf_token: 'test-token' };
      else if (path === '/brands') body = [{ id: 1, name: 'Acme', from_email: 'sender@example.org' }];
      else if (path === '/sending') body = { enabled: true };
      else if (path === '/campaigns') body = [{ ...campaign, excerpt: 'Historical email body' }];
      else if (path === '/campaigns/7') body = [campaign];
      else if (path === '/campaigns/7/report') body = { stats: [{ recipients: 42, sent: 42, opens_unique: 8, opens_total: 10, clicks_unique: 4, clicks_total: 5 }], links: [], hourly: [], countries: [
        { country: 'US', opens: 2, clicks: 30 },
        { country: 'nl', opens: 12, clicks: 1 },
        { country: 'DE', opens: 5, clicks: 7 },
        { country: '', opens: 0, clicks: 0 },
      ] };
      else if (path === '/campaigns/7/preview') body = { subject: campaign.subject, html: campaign.html, text: '' };
      else if (path === '/campaigns/7/events') {
        await route.fulfill({ contentType: 'text/event-stream', body: 'event: stats\ndata: [{"recipients":42,"sent":42,"opens_unique":8,"opens_total":10,"clicks_unique":4,"clicks_total":5}]\n\n' });
        return;
      }
      await route.fulfill({ json: body });
    });
    await page.goto('/campaigns');
    const link = page.getByRole('link', { name: 'Historical newsletter', exact: true });
    await expect(link).toHaveAttribute('href', '/campaigns/7/report');
    await link.click();
    await expect(page.getByRole('heading', { name: 'Historical newsletter', exact: true })).toBeVisible();
    await expect(page.getByText('8 recorded', { exact: true })).toBeVisible();
    await expect(page.frameLocator('iframe[title="Sent newsletter preview"]').getByText('Historical email body')).toBeVisible();
    await expect(page.getByRole('link', { name: 'Manage campaign', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toHaveCount(0);
    const countries = page.getByRole('table').filter({ has: page.getByRole('columnheader', { name: 'Country' }) });
    const names = countries.locator('tbody tr td:first-child');
    await expect(names).toHaveText(['Netherlands', 'Germany', 'United States', 'Unknown']);
    await expect(countries.getByRole('columnheader', { name: /Opens/ })).toHaveAttribute('aria-sort', 'descending');
    await countries.getByRole('button', { name: /Sort by Opens/ }).click();
    await expect(names).toHaveText(['Unknown', 'United States', 'Germany', 'Netherlands']);
    await countries.getByRole('button', { name: /Sort by Opens/ }).click();
    await expect(names).toHaveText(['Netherlands', 'Germany', 'United States', 'Unknown']);
    await countries.getByRole('button', { name: /Sort by Country/ }).click();
    await expect(names).toHaveText(['Germany', 'Netherlands', 'United States', 'Unknown']);
    await countries.getByRole('button', { name: /Sort by Country/ }).click();
    await expect(names).toHaveText(['Unknown', 'United States', 'Netherlands', 'Germany']);
    await countries.getByRole('button', { name: /Sort by Clicks/ }).click();
    await expect(names).toHaveText(['Unknown', 'Netherlands', 'Germany', 'United States']);
    await countries.getByRole('button', { name: /Sort by Clicks/ }).click();
    await expect(names).toHaveText(['United States', 'Germany', 'Netherlands', 'Unknown']);
    await page.goto('/campaigns/7');
    await expect(page).toHaveURL(/\/campaigns\/7\/report$/);
    await expect(page.frameLocator('iframe[title="Sent newsletter preview"]').getByText('Historical email body')).toBeVisible();
    expect(writes).toEqual([]);
  });
}
