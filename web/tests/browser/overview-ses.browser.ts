import { expect, test, type BrowserContext } from "@playwright/test";

async function fixture(context: BrowserContext, configured = true) {
  const state = { checks: [] as number[], sent: 1250, failed: false };
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace("/api/v1", "");
    const brandId = Number(url.searchParams.get("brand_id"));
    let status = 200;
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin: false, csrf_token: "session-token" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme" }, { id: 2, name: "Other" }];
    else if (path === "/overview") body = {
      period: { days: 30, end: Date.now() }, current: { sent: 0 }, change: {}, growth: [],
      hourly_comparison: Array.from({ length: 24 }, (_, hour) => ({ hour, today: 0, yesterday: 0 })),
    };
    else if (path === "/aws-settings") body = { has_credentials: configured, updated_at: 1 };
    else if (path === "/aws-settings/check") {
      expect(request.method()).toBe("POST");
      expect(request.headers()["x-csrf-token"]).toBe("session-token");
      state.checks.push(brandId);
      if (state.failed) { status = 502; body = { error: "AWS quota unavailable" }; }
      else body = {
        region: brandId === 1 ? "eu-central-1" : "us-east-1",
        sending_enabled: brandId === 1, production_access_enabled: brandId === 1,
        enforcement_status: brandId === 1 ? "HEALTHY" : "SHUTDOWN",
        checked_at: Date.now(), quota: { max_send_rate: 175, max_24_hour_send: 350000, sent_last_24_hours: state.sent },
      };
    }
    await route.fulfill({ status, json: body });
  });
  return state;
}

test("overview shows AWS quota, refreshes it and keeps failures local", async ({ page, context }, testInfo) => {
  const state = await fixture(context);
  await page.goto("/?brand_id=1");
  const card = page.getByLabel("Amazon SES sending status", { exact: true });
  await expect(card.getByText("Healthy", { exact: true })).toBeVisible();
  await expect(card.getByText("Enabled", { exact: true })).toBeVisible();
  await expect(card.getByText("Europe (Frankfurt) (eu-central-1)", { exact: true })).toBeVisible();
  await expect(card.getByText("348,750", { exact: true })).toBeVisible();
  expect(state.checks).toEqual([1]);
  state.sent = 2000;
  const refresh = card.getByRole("button", { name: "Refresh Amazon SES status from AWS" });
  await refresh.click();
  await expect(card.getByText("348,000", { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("overview-ses-desktop.png"), fullPage: true });
  state.failed = true;
  await refresh.click();
  await expect(card.getByRole("alert")).toContainText("Showing the last successful check.");
  await expect(page.getByText("Subscriber growth", { exact: true })).toBeVisible();
  state.failed = false;
  state.sent = 400000;
  await refresh.click();
  await expect(card.getByRole("alert")).toHaveCount(0);
  await expect(card.locator("dl > div").filter({ hasText: "Sends left" })).toContainText("0");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: testInfo.outputPath("overview-ses-mobile.png"), fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("overview skips AWS calls when access keys are missing", async ({ page, context }) => {
  const state = await fixture(context, false);
  await page.goto("/?brand_id=1");
  await expect(page.getByText("Add AWS access keys in", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Refresh Amazon SES status from AWS" })).toBeDisabled();
  expect(state.checks).toEqual([]);
});

test("switching brands shows that brand's region and sending status", async ({ page, context }) => {
  const state = await fixture(context);
  await page.goto("/?brand_id=1");
  const card = page.getByLabel("Amazon SES sending status", { exact: true });
  await expect(card.getByText("Healthy", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Switch brand", exact: true }).click();
  await page.getByRole("menuitem", { name: /Other/ }).click();
  await expect(card.getByText("Disabled", { exact: true })).toBeVisible();
  await expect(card.getByText("Sandbox", { exact: true })).toBeVisible();
  await expect(card.getByText("US East (N. Virginia) (us-east-1)", { exact: true })).toBeVisible();
  await expect(card.getByText("Healthy", { exact: true })).toHaveCount(0);
  expect(state.checks).toEqual([1, 2]);
});
