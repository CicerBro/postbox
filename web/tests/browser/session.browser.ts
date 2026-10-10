import { test, expect, type BrowserContext, type Page } from '@playwright/test';

async function fixture(context: BrowserContext) {
  const state = { user: 1, valid: true, failure: 0, loginFailure: false, writes: 0 };
  await context.route('**/api/v1/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace('/api/v1', '');
    let status = 200;
    let body: unknown = [];
    if (path === '/auth/login') {
      if (state.loginFailure) { status = 401; body = { error: 'Invalid credentials' }; }
      else { state.valid = true; body = {}; }
    } else if (path === '/auth/logout') { state.valid = false; body = {}; }
    else if (!state.valid) { status = 401; body = { error: 'Unauthorized' }; }
    else if (path === '/auth/session') body = { user_id: state.user, admin: true, csrf_token: 'session-token' };
    else if (path === '/brands') body = [{ id: 1, name: 'Acme', from_email: 'a@example.com' }];
    else if (path === '/sending') body = { enabled: false };
    else if (request.method() !== 'GET' && path === '/campaigns') {
      state.writes++;
      status = state.failure || 200;
      body = status === 200 ? { id: 7 } : { error: `Failure ${status}` };
    }
    await route.fulfill({ status, json: body });
  });
  return state;
}
async function edit(page: Page) {
  await page.goto('/campaigns/new');
  await page.getByLabel('Subject', { exact: true }).fill('Unsaved subject');
}
async function login(page: Page) {
  await page.getByLabel('Email', { exact: true }).fill('a@example.com');
  await page.getByLabel('Password', { exact: true }).fill('password');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
}
async function requests(page: Page, kind = 'api') {
  await page.evaluate(async kind => {
    const api = await import('/src/lib/api.ts');
    await Promise.allSettled([1, 2, 3].map(() => kind === 'download' ? api.download('/test-download', 1, 'test.csv') : api.api('/test-protected')));
  }, kind);
}

test('expiry while editing gives one reauthentication, preserves same account draft and never replays a write', async ({ page, context }) => {
  const state = await fixture(context);
  await edit(page);
  state.failure = 401;
  await page.getByRole('button', { name: 'Save draft', exact: true }).dispatchEvent('click');
  await expect(page.getByRole('heading', { name: 'Sign in to Postbox' })).toHaveCount(1);
  expect(state.writes).toBe(1);
  state.failure = 0;
  await login(page);
  await expect(page.getByLabel('Subject', { exact: true })).toHaveValue('Unsaved subject');
  expect(state.writes).toBe(1);
});

test('403 and validation remain local; bad login does not trigger another expiry', async ({ page, context }) => {
  const state = await fixture(context);
  await edit(page);
  for (const status of [403, 422]) {
    state.failure = status;
    await page.getByRole('button', { name: 'Save draft', exact: true }).dispatchEvent('click');
    await expect(page.getByText(new RegExp(`Failure ${status}`))).toBeVisible();
    await expect(page.getByLabel('Subject', { exact: true })).toHaveValue('Unsaved subject');
  }
  state.valid = false;
  await requests(page);
  await expect(page.getByRole('heading', { name: 'Sign in to Postbox' })).toBeVisible();
  state.loginFailure = true;
  await login(page);
  await expect(page.getByText('Invalid credentials', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Sign in to Postbox' })).toHaveCount(1);
});

test('simultaneous download expiry clears workspace and different account cannot restore edits', async ({ page, context }) => {
  const state = await fixture(context);
  await edit(page);
  state.valid = false;
  await requests(page, 'download');
  await expect(page.getByRole('heading', { name: 'Sign in to Postbox' })).toHaveCount(1);
  state.user = 2;
  await login(page);
  await expect(page.getByLabel('Subject', { exact: true })).toHaveValue('');
});

test('logout in another tab immediately clears mounted workspace and cannot recover a signed-out draft', async ({ page, context }) => {
  await fixture(context);
  await edit(page);
  const other = await context.newPage();
  await other.goto('/campaigns/new');
  await other.getByRole('button', { name: 'Account menu' }).click();
  await other.getByRole('menuitem', { name: 'Sign out' }).click();
  await expect(page.getByRole('heading', { name: 'Sign in to Postbox' })).toBeVisible();
  await login(page);
  await expect(page.getByLabel('Subject', { exact: true })).toHaveValue('');
});

test('live connection and polling stop on revocation', async ({ page, context }) => {
  const state = await fixture(context);
  let requestCount = 0;
  context.on('request', request => { if (request.url().includes('/api/v1/')) requestCount++; });
  await page.clock.install();
  await page.addInitScript(() => {
    (window as any).sources = [];
    class Source extends EventTarget {
      onopen = null; onerror = null;
      closed = false;
      constructor() { super(); (window as any).sources.push(this); }
      close() { this.closed = true; }
    }
    (window as any).EventSource = Source;
  });
  await page.goto('/campaigns/7/report');
  await expect.poll(() => page.evaluate(() => (window as any).sources.length)).toBe(1);
  state.valid = false;
  await requests(page);
  await expect(page.getByRole('heading', { name: 'Sign in to Postbox' })).toBeVisible();
  expect(await page.evaluate(() => (window as any).sources.every((source: any) => source.closed))).toBe(true);
  const stoppedAt = requestCount;
  await page.clock.fastForward(120_000);
  expect(requestCount).toBe(stoppedAt);
});

test('both tabs keep the session CSRF header after one reload', async ({ page, context }) => {
  await fixture(context);
  const tokens: string[] = [];
  context.on('request', request => {
    if (new URL(request.url()).pathname === '/api/v1/test-write') tokens.push(request.headers()['x-csrf-token']);
  });
  const other = await context.newPage();
  await edit(page);
  await edit(other);
  await other.reload();
  await expect(other.getByLabel('Subject', { exact: true })).toBeVisible();
  for (const tab of [page, other]) {
    await tab.evaluate(async () => {
      const { api } = await import('/src/lib/api.ts');
      await api('/test-write', { method: 'POST', body: {} });
    });
  }
  expect(tokens).toEqual(['session-token', 'session-token']);
});
