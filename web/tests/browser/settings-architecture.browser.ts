import { expect, test, type BrowserContext } from "@playwright/test";

async function fixture(context: BrowserContext, admin = false, empty = false) {
  const requests: string[] = [];
  await context.route("**/api/v1/**", async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1", "");
    requests.push(`${request.method()} ${path}`);
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin, csrf_token: "token", timezone: "UTC" };
    else if (path === "/brands") body = empty ? [] : [{ id: 1, name: "Acme", permissions: ["read"] }, { id: 2, name: "Other", permissions: ["read"] }];
    else if (path === "/auth/security") body = {};
    else if (path === "/subscription-settings") body = { public_subscriptions_enabled: true, application_enabled: false, enabled: false };
    else if (path === "/application-settings") body = { public_subscriptions_enabled: true };
    else if (path === "/job-settings") body = { failed_job_retention_days: 30 };
    else if (path === "/sending") body = { enabled: false };
    else if (path === "/settings") body = {};
    else if (path === "/webhooks") body = [{ id: 1, url: "https://example.org/events", events: ["subscribe"], enabled: true }];
    await route.fulfill({ json: body });
  });
  return requests;
}

test("brand readers can inspect preferences and integrations without mutation controls", async ({ page, context }) => {
  const requests = await fixture(context);
  await page.goto("/brands/1/settings/subscriptions");
  await expect(page.getByRole("switch", { name: "Allow public subscriptions for this brand" })).toBeDisabled();
  await expect(page.getByRole("status").filter({ hasText: "Blocked by application policy" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Save messages", exact: true })).toHaveCount(0);
  await expect(page.getByRole("switch", { name: /Enable Welcome email/ })).toBeDisabled();
  await expect(page.getByRole("link", { name: "Danger zone", exact: true })).toHaveCount(0);
  await page.getByRole("link", { name: "Integrations", exact: true }).click();
  await expect(page.getByText("https://example.org/events", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /Add webhook|Delete|Save webhook/ })).toHaveCount(0);
  expect(requests.filter(path => !path.startsWith("GET "))).toEqual([]);
});

test("administration is absent from member navigation and search and direct routes are guarded", async ({ page, context }) => {
  const requests = await fixture(context);
  await page.goto("/account/preferences");
  await expect(page.getByRole("link", { name: "System Settings", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Find a page", exact: true }).click();
  await page.getByRole("textbox", { name: "Search pages" }).fill("System Settings");
  await expect(page.getByRole("dialog").getByRole("button", { name: "System Settings", exact: true })).toHaveCount(0);
  await page.keyboard.press("Escape");
  await page.goto("/admin/policies");
  await expect(page.getByRole("heading", { name: "Administrator access required" })).toBeVisible();
  expect(requests).not.toContain("GET /application-settings");
  expect(requests).not.toContain("GET /job-settings");
});

test("application policies and personal preferences work without a selected brand", async ({ page, context }, testInfo) => {
  await fixture(context, true, true);
  await page.goto("/admin/policies");
  await expect(page.getByRole("heading", { name: "Administration", exact: true })).toBeVisible();
  await expect(page.getByRole("switch", { name: "Allow public subscriptions across the application" })).toBeChecked();
  await expect(page.getByRole("spinbutton", { name: "Failed job retention (days)" })).toHaveValue("30");
  await page.screenshot({ path: testInfo.outputPath("administration-policies.png"), fullPage: true });
  await page.goto("/account/preferences");
  await expect(page.getByRole("combobox", { name: "Timezone", exact: true })).toHaveValue("UTC");
  await page.screenshot({ path: testInfo.outputPath("account-preferences.png"), fullPage: true });
});

test("settings sections survive refresh, browser history, and switching brands", async ({ page, context }) => {
  await fixture(context, true);
  await page.goto("/brands/1/settings/subscriptions");
  await page.reload();
  await expect(page.getByRole("link", { name: "Subscriptions", exact: true })).toHaveAttribute("aria-current", "page");
  await page.getByRole("link", { name: "Integrations", exact: true }).click();
  await expect(page).toHaveURL(/\/brands\/1\/settings\/integrations$/);
  await page.goBack();
  await expect(page.getByRole("link", { name: "Subscriptions", exact: true })).toHaveAttribute("aria-current", "page");
  await page.getByRole("button", { name: "Switch brand", exact: true }).click();
  await page.getByRole("menuitem", { name: /Other/ }).click();
  await expect(page).toHaveURL(/\/brands\/2\/settings\/subscriptions$/);
  await expect(page.getByRole("heading", { name: "Other settings", exact: true })).toBeVisible();
  await page.goto("/account/preferences");
  await page.getByRole("button", { name: "Switch brand", exact: true }).click();
  await page.getByRole("menuitem", { name: /Acme/ }).click();
  await expect(page).toHaveURL(/\/account\/preferences$/);
  await expect(page.getByRole("combobox", { name: "Timezone", exact: true })).toHaveValue("UTC");
});

test("navbar theme control works without duplicate appearance preferences", async ({ page, context }) => {
  await fixture(context);
  await page.goto("/account/preferences");
  await expect(page.getByLabel("Appearance", { exact: true })).toHaveCount(0);
  await page.getByRole("group", { name: "Theme", exact: true }).getByRole("button", { name: /Dark/ }).click();
  await expect(page.locator("html")).toHaveClass(/dark/);
  await expect(page.getByRole("group", { name: "Theme", exact: true }).getByRole("button", { name: /Dark/ })).toHaveAttribute("aria-pressed", "true");
});

test("saving general and sending settings preserves values from the other section", async ({ page, context }) => {
  const identity = { id: 1, name: "Acme", from_name: "Acme Team", from_email: "sender@example.org", reply_to: "reply@example.org", configuration_set: "original-set", tracking_domain: "track.example.org" };
  const writes: Record<string, unknown>[] = [];
  await context.route("**/api/v1/**", async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1", "");
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin: true, csrf_token: "token" };
    else if (path === "/brands") body = [identity];
    else if (path === "/brands/1") {
      if (request.method() === "PATCH") { writes.push(request.postDataJSON()); Object.assign(identity, request.postDataJSON()); }
      body = [identity];
    } else if (path === "/aws-settings") body = { has_credentials: false };
    else if (path === "/sending") body = { enabled: false };
    await route.fulfill({ json: body });
  });
  await page.goto("/brands/1/settings/general");
  await expect(page.getByRole("button", { name: "Delete brand", exact: true })).toHaveCount(0);
  await page.getByLabel("Sender name", { exact: true }).fill("Updated Team");
  await page.getByRole("button", { name: "Save identity", exact: true }).click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toMatchObject({ from_name: "Updated Team", configuration_set: "original-set", tracking_domain: "track.example.org" });
  await page.getByRole("link", { name: "Sending & domains", exact: true }).click();
  await expect(page.getByLabel("Brand name", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Delete brand", exact: true })).toHaveCount(0);
  await page.getByLabel("SES configuration set", { exact: true }).fill("updated-set");
  await page.getByRole("button", { name: "Save sending configuration", exact: true }).click();
  await expect.poll(() => writes.length).toBe(2);
  expect(writes[1]).toMatchObject({ from_name: "Updated Team", from_email: "sender@example.org", reply_to: "reply@example.org", configuration_set: "updated-set" });
  await page.getByRole("link", { name: "Danger zone", exact: true }).click();
  await expect(page.getByRole("button", { name: "Delete brand", exact: true })).toHaveCount(1);
});


test("administrator account creation keeps new-password autocomplete", async ({ page, context }) => {
  await fixture(context, true);
  await page.goto("/admin/users");
  await page.getByRole("button", { name: "Create user", exact: true }).click();
  await expect(page.getByLabel("Initial password", { exact: true })).toHaveAttribute("autocomplete", "new-password");
});
