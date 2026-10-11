import { test, expect } from "@playwright/test";

test.use({ baseURL: "http://127.0.0.1:5175" });

test.beforeEach(async ({ page }) => {
  await page.route("**/api/v1/auth/session", (route) => route.fulfill({ json: { user_id: 1, csrf_token: "docs-csrf" } }));
});

test("guides support full text search, direct links, tables and mobile navigation", async ({ page }) => {
  await page.goto("/docs/");
  await expect(page.getByRole("heading", { name: "Welcome to Postbox", exact: true })).toBeVisible();
  await expect.poll(() => page.locator("article img").evaluate((image: HTMLImageElement) => image.naturalWidth)).toBeGreaterThan(0);
  await page.getByRole("searchbox", { name: "Search documentation" }).fill("VACUUM INTO");
  await page.getByRole("navigation", { name: "Guides", exact: true }).getByRole("link", { name: "Running Postbox" }).click();
  await expect(page.getByRole("heading", { name: "Running Postbox", exact: true })).toBeVisible();
  await expect(page.locator("details#configuration-reference")).not.toHaveAttribute("open", "");
  await page.getByRole("complementary", { name: "On this page" }).getByRole("link", { name: "Configuration reference", exact: true }).click();
  await expect(page.locator("details#configuration-reference")).toHaveAttribute("open", "");
  await page.goto("/docs/guides/environment-variables-and-cli-flags#shared-flags-and-matching-environment-variables");
  await expect(page).toHaveURL(/running-postbox#configuration-startup-flags$/);
  await expect(page.locator("details#configuration-reference")).toHaveAttribute("open", "");
  await expect(page.locator("details table").first()).toBeVisible();
  await expect(page.getByRole("heading", { name: "Startup flags" })).toBeVisible();
  await expect.poll(() => page.getByRole("heading", { name: "Startup flags" }).evaluate((heading) => heading.getBoundingClientRect().top)).toBeLessThan(200);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false);
  await expect(page.locator(".docs-sidebar")).toBeHidden();
  await page.getByRole("button", { name: "Toggle guide navigation" }).click();
  await page.getByRole("navigation", { name: "Guides", exact: true }).getByRole("link", { name: "Sending newsletters" }).click();
  await expect(page.getByRole("heading", { name: "Sending newsletters", exact: true })).toBeVisible();
  await expect(page.locator(".docs-sidebar")).toBeHidden();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  expect(overflow).toBe(false);
  await page.getByRole("button", { name: "Toggle color theme" }).click();
  await expect(page.locator("html")).toHaveClass("dark");
});

test("Scalar renders the spec locally and session writes include CSRF", async ({ page }) => {
  const errors: string[] = [];
  const external: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (request) => { if (!new URL(request.url()).hostname.startsWith("127.0.0.1")) external.push(request.url()); });
  await page.route("**/docs/openapi.json", (route) => route.fulfill({ json: {
    openapi: "3.0.3", info: { title: "Postbox API", version: "1.0.0" }, servers: [{ url: "/" }],
    paths: { "/api/v1/brands": { post: { tags: ["Brands and settings"], summary: "Create a brand", requestBody: { content: { "application/json": { schema: { type: "object" } } } }, responses: { "200": { description: "OK" } } } } },
  } }));
  await page.goto("/docs/api");
  await expect(page.getByRole("heading", { name: "Postbox API", exact: true })).toBeVisible({ timeout: 30000 });
  await expect(page.getByRole("heading", { name: "Create a brand", exact: true })).toBeVisible();
  // Scalar's request client uses the signed-in session.
  await page.route("**/api/v1/brands", (route) => route.fulfill({ json: { id: 1 } }));
  const request = page.waitForRequest((r) => r.url().endsWith("/api/v1/brands") && r.method() === "POST");
  await page.getByRole("button", { name: /test request/i }).click();
  await page.getByRole("button", { name: /^Send .*request to /i }).click();
  expect((await request).headers()["x-csrf-token"]).toBe("docs-csrf");
  expect(external).toEqual([]);
  expect(errors).toEqual([]);
});

test("expired sessions hide documentation and return through login", async ({ page }) => {
  await page.route("**/api/v1/auth/session", (route) => route.fulfill({ status: 401, json: { error: "login required" } }));
  await page.goto("/docs/guides/deployment");
  await expect(page).toHaveURL(/\/login\?next=%2Fdocs%2Fguides%2Fdeployment/);
  await expect(page.locator("article")).toHaveCount(0);
});
