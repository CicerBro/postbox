import { expect, test, type Page, type BrowserContext } from "@playwright/test";

async function fixture(page: Page, context: BrowserContext) {
  const state = {
    status: "sending", admin: true, owner: 1, reads: 0, lastSearch: "", failPause: false, failReport: false,
    stats: { recipients: 100, sent: 50, failed: 5, skipped: 3, unknown: 2, opens_unique: 12, clicks_unique: 4 },
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
    const campaign = { id: 7, subject: "October newsletter", html: "<p>Newsletter content</p>", text: "", inline_css: true, status: state.status, created_by: state.owner, created_at: 1700000000000, scheduled_at: null, finished_at: state.status === "sent" ? 1700000005000 : null };
    let body: unknown = [], status = 200;
    if (path === "/auth/session") body = { user_id: 1, admin: state.admin, csrf_token: "live-token" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "a@example.org" }];
    else if (path === "/sending") body = { enabled: true };
    else if (path === "/campaigns") {
      state.reads++;
      expect(url.searchParams.get("summary")).toBe("true");
      state.lastSearch = url.searchParams.get("search") ?? "";
      body = [{ ...campaign, excerpt: "Newsletter content", progress: state.stats, stats: state.stats }, { id: 8, subject: "Next newsletter", status: "scheduled", excerpt: "Draft content", created_at: 1700000000000, scheduled_at: 1700000010000, finished_at: null, progress: null, stats: null }].filter(c => c.subject.toLowerCase().includes(state.lastSearch.toLowerCase()));
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
  test(`campaign table shows delivery metrics and refreshes on ${mobile ? "mobile" : "desktop"}`, async ({ page, context }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    else await page.addInitScript(() => localStorage.setItem("postbox-theme", "dark"));
    await page.clock.install();
    const state = await fixture(page, context);
    await page.goto("/campaigns");
    await expect(page.getByRole("row").first().getByRole("columnheader").nth(0)).toContainText("Campaign");
    await expect(page.getByRole("row").first().getByRole("columnheader").nth(1)).toContainText("Status");
    await expect(page.getByRole("row").first().getByRole("columnheader").nth(2)).toHaveText("Recipients");
    await expect(page.getByRole("row").first().getByRole("columnheader").nth(4)).toHaveText("Opens");
    await expect(page.getByRole("row").first().getByRole("columnheader").nth(5)).toHaveText("Clicks");
    const row = page.getByRole("row").filter({ has: page.getByRole("link", { name: "October newsletter", exact: true }) });
    await expect(row).toContainText("40 remaining");
    await expect(row).not.toContainText("60 / 100 processed");
    await expect(row.locator("td").first().getByRole("progressbar")).toHaveAttribute("aria-valuenow", "60");
    await expect(row.locator("td").nth(1)).toContainText("Sending");
    await expect(row.getByRole("link", { name: "October newsletter" })).toHaveAttribute("href", "/campaigns/7/report");
    await expect(row).toContainText("12");
    await expect(row).toContainText("4");
    await expect(page.getByRole("row").filter({ hasText: "Next newsletter" })).toContainText("Scheduled");
    if (mobile) expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath("campaign-live-table.png"), fullPage: true });
    expect(state.reads).toBe(1);
    state.stats.sent = 70;
    await page.clock.fastForward(5000);
    await expect(row).toContainText("20 remaining");
    state.status = "sent";
    await page.clock.fastForward(5000);
    await expect(row.locator("td").nth(3)).toContainText("Nov 14, 2023");
    const deleteButton = row.getByRole("button", { name: "Delete campaign October newsletter" });
    const scheduledClone = page.getByRole("row").filter({ hasText: "Next newsletter" }).getByRole("button", { name: "Clone campaign Next newsletter" });
    const deleteBox = await deleteButton.boundingBox();
    const cloneBox = await scheduledClone.boundingBox();
    expect(deleteBox).not.toBeNull();
    expect(cloneBox).not.toBeNull();
    expect(Math.abs((deleteBox!.x + deleteBox!.width) - (cloneBox!.x + cloneBox!.width))).toBeLessThan(1);
    await expect(row).not.toContainText("Newsletter content");
    await expect(page.getByRole("columnheader", { name: /Created/ })).toHaveCount(0);
    await expect(page.getByRole("progressbar")).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath("campaign-sent-table.png"), fullPage: true });
    await page.getByRole("button", { name: "Search campaigns" }).click();
    const search = page.getByRole("textbox", { name: "Search campaigns" });
    await expect(search).toBeFocused();
    await search.pressSequentially("October");
    await expect(page.getByRole("row").filter({ hasText: "Next newsletter" })).toHaveCount(0);
    expect(state.lastSearch).toBe("October");
    await page.getByRole("button", { name: "Close campaign search" }).click();
    await expect(page.getByRole("row").filter({ hasText: "Next newsletter" })).toBeVisible();
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
  await expect(page.getByRole("link", { name: "View recipient statuses" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Recipient outcomes" })).toHaveCount(0);
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
