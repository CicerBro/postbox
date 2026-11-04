import { test, expect, type Page } from "@playwright/test";

async function fixture(page: Page, options: { permissions?: string[]; owner?: number; fail?: boolean } = {}) {
  let scheduledAt = Date.parse("2099-01-01T18:45:00Z");
  let campaignReads = 0;
  const writes: unknown[] = [];
  await page.route("**/api/v1/**", async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1", "");
    let body: unknown = [];
    if (path === "/auth/session")
      body = { user_id: 1, admin: false, csrf_token: "csrf", timezone: "Asia/Kathmandu" };
    else if (path === "/brands")
      body = [{ id: 1, name: "Acme", permissions: options.permissions ?? ["read", "write", "send"] }];
    else if (path === "/sending") body = { enabled: true };
    else if (path === "/campaigns/7") {
      campaignReads++;
      body = [{ id: 7, subject: "Newsletter", html: "<p>Hello</p>", text: "", inline_css: true,
        status: "scheduled", scheduled_at: scheduledAt, created_by: options.owner ?? 1 }];
    } else if (path === "/campaigns/7/checklist") body = {
      recipients: 10, message_bytes: 100, upload_bytes: 1000, duration_seconds: 1,
      quota: { MaxSendRate: 10, Max24HourSend: 5000, SentLast24Hours: 0 }, fits_daily_quota: true,
      checks: { ready: true, errors: [], warnings: [], links: [], html_bytes: 100,
        html_clipping: false, suppressed: 0, inactive: 0, skipped: 0, remaining_daily_quota: 5000 },
    };
    else if (path === "/campaigns/7/schedule") {
      writes.push(request.postDataJSON());
      if (options.fail) {
        await route.fulfill({ status: 409, json: { error: "Campaign has already started" } });
        return;
      }
      scheduledAt = request.postDataJSON().scheduled_at;
      body = { ok: true };
    } else if (path === "/email-preview") body = { html: "<p>Hello</p>", subject: "Newsletter", text: "" };
    await route.fulfill({ json: body });
  });
  const timeZone = await page.evaluate(() => new Intl.DateTimeFormat("en", { timeZone: "Asia/Kathmandu" }).resolvedOptions().timeZone);
  return { writes, reads: () => campaignReads, timeZone };
}

for (const mobile of [false, true]) {
  test(`scheduled campaign date can be changed and persists on ${mobile ? "mobile" : "desktop"}`, async ({ page }) => {
    await page.setViewportSize(mobile ? { width: 390, height: 844 } : { width: 1440, height: 1100 });
    const state = await fixture(page);
    await page.goto("/campaigns/7");
    const input = page.getByLabel(/Schedule in Asia\/Kath?mandu/);
    await expect(input).toHaveValue("2099-01-02T00:30");
    await expect(page.getByRole("button", { name: "Start sending", exact: true })).toHaveCount(0);
    await expect(page.getByRole("textbox", { name: "Subject", exact: true })).toBeDisabled();
    await input.fill("2099-02-03T12:15");
    // Background polling must preserve the user's unsaved date edit.
    await expect.poll(state.reads, { timeout: 10000 }).toBeGreaterThan(1);
    await expect(input).toHaveValue("2099-02-03T12:15");
    await page.getByRole("button", { name: "Update schedule", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Update campaign schedule?" });
    await expect(dialog).toContainText(state.timeZone);
    await dialog.getByRole("button", { name: "Confirm", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole("status").filter({ hasText: "Campaign schedule saved." })).toBeVisible();
    expect(state.writes).toEqual([{ scheduled_at: Date.parse("2099-02-03T06:30:00Z"), timezone: state.timeZone }]);
    await page.reload();
    await expect(input).toHaveValue("2099-02-03T12:15");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  });
}

test("invalid dates are rejected and failed updates retain the edited date", async ({ page }) => {
  const state = await fixture(page, { fail: true });
  await page.goto("/campaigns/7");
  const input = page.getByLabel(/Schedule in Asia\/Kath?mandu/);
  await input.fill("2000-01-01T12:00");
  await page.getByRole("button", { name: "Update schedule", exact: true }).click();
  await expect(page.getByText("Choose a valid future date and time in your selected timezone.")).toBeVisible();
  expect(state.writes).toEqual([]);
  await input.fill("2099-02-03T12:15");
  await page.getByRole("button", { name: "Update schedule", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Update campaign schedule?" });
  await dialog.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(dialog).toContainText("Campaign has already started");
  await dialog.getByRole("button", { name: "Keep reviewing", exact: true }).click();
  await expect(input).toHaveValue("2099-02-03T12:15");
  await page.reload();
  await expect(input).toHaveValue("2099-01-02T00:30");
});

for (const options of [{ permissions: ["read"] }, { permissions: ["read", "write"] }, { owner: 2 }]) {
  test(`schedule updates require send access and ownership: ${JSON.stringify(options)}`, async ({ page }) => {
    const state = await fixture(page, options);
    await page.goto("/campaigns/7");
    await expect(page.getByLabel(/Schedule in Asia\/Kath?mandu/)).toBeDisabled();
    await expect(page.getByRole("button", { name: "Update schedule", exact: true })).toBeDisabled();
    expect(state.writes).toEqual([]);
  });
}
