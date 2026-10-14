import { expect, test, type Page, type BrowserContext } from "@playwright/test";

async function fixture(page: Page, context: BrowserContext) {
  const state = {
    status: "sending", admin: true, owner: 1, reads: 0, failPause: false, failReport: false,
    stats: { recipients: 100, sent: 50, failed: 5, skipped: 3, unknown: 2 },
    actions: [] as string[],
  };
  await page.addInitScript(() => {
    const sources: LiveSource[] = [];
    class LiveSource extends EventTarget {
      onopen: ((event: Event) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;
      closed = false;
      constructor(public url: string) {
        super(); sources.push(this);
        queueMicrotask(() => this.onopen?.(new Event("open")));
      }
      close() { this.closed = true; }
    }
    Object.defineProperty(window, "EventSource", { value: LiveSource });
    Object.assign(window, {
      emitCampaignStats: (stats: unknown) => sources.filter(source => !source.closed).forEach(source => source.dispatchEvent(new MessageEvent("stats", { data: JSON.stringify([stats]) }))),
      disconnectCampaignStats: () => sources.filter(source => !source.closed).forEach(source => source.onerror?.(new Event("error"))),
    });
  });
  await context.route("**/api/v1/**", async route => {
    const request = route.request(), url = new URL(request.url());
    const path = url.pathname.replace("/api/v1", "");
    const campaign = { id: 7, subject: "October newsletter", html: "<p>Newsletter content</p>", text: "", inline_css: true, status: state.status, created_by: state.owner, created_at: 1700000000000, scheduled_at: null };
    let body: unknown = [], status = 200;
    if (path === "/auth/session") body = { user_id: 1, admin: state.admin, csrf_token: "live-token" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "a@example.org" }];
    else if (path === "/sending") body = { enabled: true };
    else if (path === "/campaigns") {
      state.reads++;
      expect(url.searchParams.get("summary")).toBe("true");
      body = [{ ...campaign, excerpt: "Newsletter content", progress: state.stats }, { id: 8, subject: "Next newsletter", status: "draft", excerpt: "Draft content", created_at: 1700000000000, scheduled_at: null, progress: null }];
    } else if (path === "/campaigns/7") body = [campaign];
    else if (path === "/campaigns/7/report") {
      status = state.failReport ? 503 : 200;
      body = state.failReport ? { error: "Reports temporarily unavailable" } : { stats: [state.stats], links: [], countries: [], hourly: [] };
    } else if (path === "/campaigns/7/preview") body = { subject: campaign.subject, html: campaign.html, text: "" };
    else if (["/campaigns/7/pause", "/campaigns/7/resume"].includes(path)) {
      expect(request.method()).toBe("POST");
      expect(url.searchParams.get("brand_id")).toBe("1");
      expect(request.headers()["x-csrf-token"]).toBe("live-token");
      state.actions.push(path);
      status = state.failPause ? 422 : 200;
      body = state.failPause ? { error: "Cannot pause this campaign" } : {};
      if (!state.failPause) state.status = path.endsWith("pause") ? "paused" : "sending";
    }
    await route.fulfill({ status, json: body });
  });
  return state;
}

for (const mobile of [false, true]) {
  test(`campaign table swaps dates for live progress and refreshes on ${mobile ? "mobile" : "desktop"}`, async ({ page, context }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    await page.clock.install();
    const state = await fixture(page, context);
    await page.goto("/campaigns");
    await expect(page.getByRole("columnheader", { name: /Schedule \/ delivery/ })).toBeVisible();
    const row = page.getByRole("row").filter({ has: page.getByRole("link", { name: "October newsletter", exact: true }) });
    await expect(row).toContainText("60 / 100 processed");
    await expect(row).toContainText("40 pending");
    await expect(row.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "60");
    await expect(row.getByRole("link", { name: "October newsletter" })).toHaveAttribute("href", "/campaigns/7/report");
    await expect(row.getByRole("link", { name: "View live" })).toBeVisible();
    await expect(page.getByRole("row").filter({ hasText: "Next newsletter" })).toContainText("Created");
    if (mobile) expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath("campaign-live-table.png"), fullPage: true });
    expect(state.reads).toBe(1);
    state.stats.sent = 70;
    await page.clock.fastForward(5000);
    await expect(row).toContainText("80 / 100 processed");
    await expect(row).toContainText("20 pending");
    await expect(row).toContainText(/~[56]s left/);
    state.status = "sent";
    await page.clock.fastForward(5000);
    await expect(page.getByRole("columnheader", { name: /Created/ })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: /Scheduled/ })).toBeVisible();
    await expect(page.getByRole("progressbar")).toHaveCount(0);
  });
}

test("opening a live campaign shows streaming progress and supports pause, resume and management", async ({ page, context }, testInfo) => {
  await page.clock.install();
  const state = await fixture(page, context);
  await page.goto("/campaigns/7");
  await expect(page).toHaveURL(/\/campaigns\/7\/report$/);
  const monitor = page.getByLabel("Live delivery progress", { exact: true });
  await expect(monitor).toContainText("60 / 100 processed");
  await expect(monitor).toContainText("Unknown");
  await monitor.screenshot({ path: testInfo.outputPath("campaign-live-report.png") });
  await expect(page.getByRole("link", { name: "Manage campaign" })).toHaveAttribute("href", "/campaigns/7?manage=true");
  await page.clock.fastForward(2000);
  state.stats.sent = 70;
  await page.evaluate(stats => (window as unknown as { emitCampaignStats: (stats: unknown) => void }).emitCampaignStats(stats), state.stats);
  await expect(monitor.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "80");
  await expect(monitor).toContainText(/~[23]s left/);
  state.failPause = true;
  await monitor.getByRole("button", { name: "Pause sending" }).click();
  await expect(page.getByRole("alert")).toContainText("Cannot pause this campaign");
  await expect(monitor.getByRole("button", { name: "Pause sending" })).toBeEnabled();
  state.failPause = false;
  await monitor.getByRole("button", { name: "Pause sending" }).click();
  await expect(monitor).toContainText("Sending paused");
  await expect(monitor.getByRole("button", { name: "Resume sending" })).toBeEnabled();
  await monitor.getByRole("button", { name: "Resume sending" }).click();
  await expect(monitor.getByRole("button", { name: "Pause sending" })).toBeEnabled();
  await expect(monitor).toContainText("Estimating time remaining");
  await page.getByRole("link", { name: "View recipient statuses" }).click();
  await expect(page).toHaveURL(/#campaign-recipient-statuses$/);
  await expect(page.getByRole("heading", { name: "Recipient outcomes" })).toBeInViewport();
  await page.getByRole("link", { name: "Manage campaign" }).click();
  await expect(page).toHaveURL(/\/campaigns\/7\?manage=true$/);
  await expect(page.getByRole("heading", { name: "Campaign content" })).toBeVisible();
  expect(state.actions).toEqual(["/campaigns/7/pause", "/campaigns/7/pause", "/campaigns/7/resume"]);
});

test("preparing campaigns show an honest waiting state and non-owners cannot resume", async ({ page, context }) => {
  const state = await fixture(page, context);
  state.status = "preparing";
  await page.goto("/campaigns/7");
  const monitor = page.getByLabel("Live delivery progress", { exact: true });
  await expect(monitor).toContainText("Preparing audience…");
  await expect(monitor.getByRole("progressbar")).toHaveCount(0);
  state.status = "paused"; state.admin = false; state.owner = 2;
  await page.reload();
  await expect(monitor.getByRole("button", { name: "Resume sending" })).toBeDisabled();
  expect(state.actions).toEqual([]);
});

test("disconnected live updates fall back to refreshing counters and expose refresh failures", async ({ page, context }) => {
  await page.clock.install();
  const state = await fixture(page, context);
  await page.goto("/campaigns/7/report");
  const monitor = page.getByLabel("Live delivery progress", { exact: true });
  await expect(monitor).toContainText("60 / 100 processed");
  await page.evaluate(() => (window as unknown as { disconnectCampaignStats: () => void }).disconnectCampaignStats());
  await expect(monitor).toContainText("Reports refresh automatically");
  state.stats.sent = 80;
  await page.clock.fastForward(15000);
  await expect(monitor).toContainText("90 / 100 processed");
  state.failReport = true;
  await page.clock.fastForward(15000);
  // React Query performs its configured retries before surfacing the error.
  await page.clock.fastForward(15000);
  await expect(monitor).toContainText("Waiting for fresh updates");
  await expect(page.getByRole("alert")).toContainText("Reports temporarily unavailable");
});
