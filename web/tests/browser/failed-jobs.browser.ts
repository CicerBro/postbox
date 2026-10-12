import { expect, test } from "@playwright/test";

test("failed job retention saves an application-wide integer and survives reload", async ({ page, context }) => {
  let days = 30;
  let writes = 0;
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace("/api/v1", "");
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin: true, csrf_token: "session-token" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "sender@example.org" }];
    else if (path === "/sending") body = { enabled: false };
    else if (path === "/job-settings") {
      expect(url.searchParams.has("brand_id")).toBe(false);
      if (request.method() === "PATCH") {
        expect(request.postDataJSON()).toEqual({ failed_job_retention_days: 14 });
        expect(request.headers()["x-csrf-token"]).toBe("session-token");
        days = request.postDataJSON().failed_job_retention_days;
        writes++;
      }
      body = { failed_job_retention_days: days };
    }
    await route.fulfill({ json: body });
  });
  await page.goto("/settings");
  await page.getByRole("button", { name: "Failed jobs", exact: true }).click();
  const input = page.getByRole("spinbutton", { name: "Failed job retention (days)" });
  await expect(input).toHaveValue("30");
  await input.fill("14");
  await page.getByRole("button", { name: "Save retention", exact: true }).click();
  await expect(page.getByText("Failed job retention saved.", { exact: true })).toBeVisible();
  await page.reload();
  await page.getByRole("button", { name: "Failed jobs", exact: true }).click();
  await expect(input).toHaveValue("14");
  expect(writes).toBe(1);
});

test("members have no failed job retention controls", async ({ page, context }) => {
  await context.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    const body = path === "/auth/session"
      ? { user_id: 2, admin: false, csrf_token: "session-token" }
      : path === "/brands" ? [{ id: 1, name: "Acme", from_email: "sender@example.org" }]
      : path === "/sending" ? { enabled: false } : [];
    await route.fulfill({ json: body });
  });
  await page.goto("/settings");
  await expect(page.getByRole("button", { name: "Failed jobs", exact: true })).toHaveCount(0);
  await expect(page.getByRole("spinbutton", { name: "Failed job retention (days)" })).toHaveCount(0);
});
