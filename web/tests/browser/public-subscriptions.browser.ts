import { expect, test, type BrowserContext } from "@playwright/test";

async function settingsFixture(context: BrowserContext, admin = true) {
  const state = { application: true, brand: true, applicationWrites: 0, brandWrites: 0 };
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1", "");
    let body: unknown = [];
    if (path === "/auth/session") {
      body = { user_id: 1, admin, csrf_token: "session-token" };
    } else if (path === "/brands") {
      body = [{ id: 1, name: "Acme", permissions: ["read", "write"], from_email: "sender@example.org" }];
    } else if (path === "/application-settings") {
      if (request.method() === "PATCH") {
        state.application = request.postDataJSON().public_subscriptions_enabled;
        state.applicationWrites++;
      }
      body = { public_subscriptions_enabled: state.application };
    } else if (path === "/subscription-settings") {
      if (request.method() === "PATCH") {
        expect(new URL(request.url()).searchParams.get("brand_id")).toBe("1");
        state.brand = request.postDataJSON().public_subscriptions_enabled;
        state.brandWrites++;
      }
      body = {
        public_subscriptions_enabled: state.brand,
        application_enabled: state.application,
        enabled: state.application && state.brand,
      };
    } else if (path === "/sending") {
      body = { enabled: false };
    }
    await route.fulfill({ json: body });
  });
  return state;
}

test("public subscription controls persist each switch and show the effective application override", async ({ page, context }) => {
  const state = await settingsFixture(context);
  await page.goto("/brands/1/settings/subscriptions");
  const app = page.getByRole("switch", { name: "Allow public subscriptions across the application" });
  const brand = page.getByRole("switch", { name: "Allow public subscriptions for this brand" });
  await expect(page.getByText("Subscription form URLs", { exact: true })).toHaveCount(0);
  await expect(app).toHaveCount(0);
  await expect(brand).toBeChecked();
  await expect(page.getByText("Public signup is enabled for this brand.", { exact: true })).toBeVisible();
  await brand.click();
  await expect(page.getByText("Public signup is disabled for this brand.", { exact: true })).toBeVisible();
  await page.goto("/admin/policies");
  await expect(app).toBeChecked();
  await app.click();
  await expect(app).not.toBeChecked();
  await page.goto("/brands/1/settings/subscriptions");
  await brand.click();
  await expect(brand).toBeChecked();
  await expect(page.getByRole("status").filter({ hasText: "Blocked by application policy" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("status").filter({ hasText: "Blocked by application policy" })).toBeVisible();
  await expect(app).toHaveCount(0);
  await expect(brand).toBeChecked();
  await page.goto("/admin/policies");
  await expect(app).not.toBeChecked();
  expect(state.applicationWrites).toBe(1);
  expect(state.brandWrites).toBe(2);
});

test("brand members can change brand signup without application controls", async ({ page, context }) => {
  const state = await settingsFixture(context, false);
  await page.goto("/brands/1/settings/subscriptions");
  await expect(page.getByRole("switch", { name: "Allow public subscriptions across the application" })).toHaveCount(0);
  await page.getByRole("switch", { name: "Allow public subscriptions for this brand" }).click();
  await expect(page.getByText("Public signup is disabled for this brand.", { exact: true })).toBeVisible();
  expect(state.applicationWrites).toBe(0);
  expect(state.brandWrites).toBe(1);
});
