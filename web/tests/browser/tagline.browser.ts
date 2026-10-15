import { expect, test, type BrowserContext } from "@playwright/test";

const original = "Self-hosted. Your audience, under your control.";
async function fixture(context: BrowserContext) {
  const state = { signedIn: true, admin: true, enabled: true, text: original, failSave: false, writes: 0 };
  await context.route("**/api/v1/**", async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1", "");
    let status = 200;
    let body: unknown = [];
    if (path === "/auth/session") {
      status = state.signedIn ? 200 : 401;
      body = state.signedIn ? { user_id: 1, admin: state.admin, csrf_token: "token" } : { error: "Sign in required" };
    } else if (path === "/auth/config") {
      body = { turnstile_required: false, turnstile_site_key: "", tagline: { enabled: state.enabled, text: state.text } };
    } else if (path === "/tagline-settings") {
      if (request.method() === "PATCH") {
        expect(request.headers()["x-csrf-token"]).toBe("token");
        if (state.failSave) {
          await route.fulfill({ status: 500, json: { error: "Could not save tagline" } });
          return;
        }
        const input = request.postDataJSON();
        expect(Object.keys(input).sort()).toEqual(["enabled", "text"]);
        state.enabled = input.enabled;
        state.text = input.text;
        state.writes++;
      }
      body = { enabled: state.enabled, text: state.text };
    } else if (path === "/sending") {
      body = { enabled: false };
    } else if (path === "/deployment-settings") {
      body = { base_url: "https://mail.example.org" };
    }
    await route.fulfill({ status, json: body });
  });
  return state;
}

test("deployment tagline controls save plain text and preserve it when hidden", async ({ page, context }, testInfo) => {
  const state = await fixture(context);
  await page.goto("/admin/deployment");
  const toggle = page.getByRole("switch", { name: "Show tagline", exact: true });
  const input = page.getByRole("textbox", { name: "Tagline text", exact: true });
  const save = page.getByRole("button", { name: "Save tagline", exact: true });
  await expect(toggle).toBeChecked();
  await expect(input).toHaveValue(original);
  const custom = "Our newsletter workspace <b>plain text</b> **only**.\nWelkom 👋";
  await input.fill(custom);
  state.failSave = true;
  await save.click();
  await expect(page.getByRole("alert")).toContainText("Could not save tagline");
  await expect(input).toHaveValue(custom);
  state.failSave = false;
  await save.click();
  await expect(page.getByRole("status").filter({ hasText: "Tagline settings saved." })).toBeVisible();
  await page.reload();
  await expect(input).toHaveValue(custom);
  await page.screenshot({ path: testInfo.outputPath("tagline-deployment.png"), fullPage: true });
  state.signedIn = false;
  await page.goto("/");
  await expect(page.locator(".login-footer")).toHaveText(custom);
  await expect(page.locator(".login-footer b, .login-footer strong")).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath("tagline-login.png"), fullPage: true });
  state.signedIn = true;
  await page.goto("/admin/deployment");
  await toggle.click();
  await save.click();
  await expect(page.getByRole("status").filter({ hasText: "Tagline settings saved." })).toBeVisible();
  await page.reload();
  await expect(toggle).not.toBeChecked();
  await expect(input).toHaveValue(custom);
  state.signedIn = false;
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Sign in to Postbox" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeEnabled();
  await expect(page.locator(".login-footer")).toHaveCount(0);
  expect(state.writes).toBe(2);
});

test("default tagline is shown and deployment controls require an administrator", async ({ page, context }) => {
  const state = await fixture(context);
  state.signedIn = false;
  await page.goto("/");
  await expect(page.locator(".login-footer")).toHaveText(original);
  state.signedIn = true;
  state.admin = false;
  await page.goto("/admin/deployment");
  await expect(page.getByRole("heading", { name: "Administrator access required" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Tagline text", exact: true })).toHaveCount(0);
  expect(state.writes).toBe(0);
});
