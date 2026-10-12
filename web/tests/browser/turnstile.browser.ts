import { expect, test, type BrowserContext, type Page } from "@playwright/test";

async function fixture(context: BrowserContext) {
  const state = { valid: false, configFails: false, totp: true, attempts: [] as Record<string, string>[], passkeys: [] as Record<string, string>[] };
  await context.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    let status = 200;
    let body: unknown = [];
    if (path === "/auth/config") {
      status = state.configFails ? 503 : 200;
      body = state.configFails ? { error: "Verification unavailable" } : { turnstile_required: true, turnstile_site_key: "public-key" };
    } else if (path === "/auth/login") {
      const input = route.request().postDataJSON();
      state.attempts.push(input);
      if (state.totp && !input.totp) { status = 202; body = { totp_required: true }; }
      else if (input.password !== "correct-password") { status = 401; body = { error: "invalid credentials" }; }
      else { state.valid = true; body = {}; }
    } else if (path === "/auth/passkeys/login/begin") {
      state.passkeys.push(route.request().postDataJSON());
      status = 400; body = { error: "Passkey cancelled" };
    } else if (path === "/auth/session") {
      status = state.valid ? 200 : 401;
      body = state.valid ? { user_id: 1, admin: true, csrf_token: "csrf" } : { error: "Unauthorized" };
    } else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "a@example.com" }];
    else if (path === "/sending") body = { enabled: false };
    await route.fulfill({ status, json: body });
  });
  await context.route("https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit", route => route.fulfill({ contentType: "application/javascript", body: `
    window.widgets = []; window.removedWidgets = [];
    window.turnstile = {
      render(container, options) {
        const id = String(window.widgets.length);
        const frame = document.createElement('div');
        frame.style.cssText = 'width:100%;min-width:300px;height:65px;border:1px solid #ddd;background:#fafafa;display:flex;align-items:center;padding:12px';
        frame.textContent = 'Security verification'; container.append(frame);
        window.widgets.push({ id, options, container }); return id;
      },
      remove(id) { window.widgets[Number(id)].container.replaceChildren(); window.removedWidgets.push(id); }
    };
  ` }));
  return state;
}
async function solve(page: Page, token: string) {
  await page.evaluate(token => (window as any).widgets.at(-1).options.callback(token), token);
}
async function widgetCount(page: Page) { return page.evaluate(() => (window as any).widgets?.length ?? 0); }

test("login requires a challenge and refreshes it for TOTP and failed credentials", async ({ page, context }) => {
  const state = await fixture(context);
  await page.goto("/login");
  const submit = page.getByRole("button", { name: "Sign in", exact: true });
  await expect.poll(() => widgetCount(page)).toBe(1);
  await expect(submit).toBeDisabled();
  await page.getByLabel("Email", { exact: true }).fill("a@example.com");
  await page.getByLabel("Password", { exact: true }).fill("wrong-password");
  await solve(page, "first");
  await submit.click();
  await expect(page.getByLabel("Authenticator code", { exact: true })).toBeVisible();
  await expect.poll(() => widgetCount(page)).toBe(2);
  await expect(submit).toBeDisabled();
  await page.getByLabel("Authenticator code", { exact: true }).fill("123456");
  await solve(page, "second");
  await submit.click();
  await expect(page.getByRole("alert")).toHaveText("invalid credentials");
  await expect.poll(() => widgetCount(page)).toBe(3);
  await expect(submit).toBeDisabled();
  await page.getByLabel("Password", { exact: true }).fill("correct-password");
  await solve(page, "third");
  await submit.click();
  await expect(page.getByRole("heading", { name: "Sign in to Postbox" })).toHaveCount(0);
  expect(state.attempts.map(attempt => attempt.turnstile_token)).toEqual(["first", "second", "third"]);
});

test("expired and failed challenges disable submission and offer retry", async ({ page, context }) => {
  await fixture(context);
  await page.goto("/login");
  await expect.poll(() => widgetCount(page)).toBe(1);
  const submit = page.getByRole("button", { name: "Sign in", exact: true });
  await solve(page, "first");
  await expect(submit).toBeEnabled();
  await page.evaluate(() => (window as any).widgets.at(-1).options['expired-callback']());
  await expect(submit).toBeDisabled();
  await page.evaluate(() => (window as any).widgets.at(-1).options['error-callback']());
  await page.getByRole("button", { name: "Retry verification" }).click();
  await expect.poll(() => widgetCount(page)).toBe(2);
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(await page.evaluate(() => (window as any).removedWidgets)).toEqual(["0"]);
});

test("public auth configuration can retry after a failed load", async ({ page, context }) => {
  const state = await fixture(context);
  state.configFails = true;
  await page.goto("/login");
  await expect(page.getByRole("alert")).toContainText("Verification unavailable");
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeDisabled();
  state.configFails = false;
  await page.getByRole("button", { name: "Retry verification" }).click();
  await expect.poll(() => widgetCount(page)).toBe(1);
  await solve(page, "token");
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeEnabled();
});

test("passkey begin submits the challenge and refreshes it after failure", async ({ page, context }) => {
  const state = await fixture(context);
  await page.goto("/login");
  await expect.poll(() => widgetCount(page)).toBe(1);
  const passkey = page.getByRole("button", { name: "Sign in with a passkey" });
  await expect(passkey).toBeDisabled();
  await solve(page, "passkey-token");
  await passkey.click();
  await expect(page.getByRole("alert")).toHaveText("Passkey cancelled");
  await expect(passkey).toBeDisabled();
  expect(state.passkeys).toEqual([{ turnstile_token: "passkey-token" }]);
});

test("horizontal widget fits desktop and small mobile login cards", async ({ page, context }) => {
  await fixture(context);
  for (const width of [980, 375, 320]) {
    await page.setViewportSize({ width, height: 850 });
    await page.goto("/login");
    await expect.poll(() => widgetCount(page)).toBe(1);
    const box = await page.getByLabel("Security verification", { exact: true }).boundingBox();
    expect(box?.height).toBe(65);
    expect(box!.width).toBeGreaterThanOrEqual(300);
    expect(box!.x + box!.width).toBeLessThanOrEqual(width);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width);
    expect(await page.evaluate(() => (window as any).widgets[0].options.size)).toBe("flexible");
  }
});


test("blocked challenge scripts keep sign-in disabled and can be retried", async ({ page, context }) => {
  await fixture(context);
  await context.route("https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit", route => route.abort());
  await page.goto("/login");
  await expect(page.getByRole("alert")).toContainText("Security verification could not load");
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeDisabled();
  await context.unroute("https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit");
  await fixture(context);
  await page.getByRole("button", { name: "Retry verification" }).click();
  await expect.poll(() => widgetCount(page)).toBe(1);
  await solve(page, "retry-token");
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeEnabled();
});
