import { expect, test, type BrowserContext } from "@playwright/test";

async function fixture(context: BrowserContext) {
  const state: Record<string, unknown> = {
    thankyou_html: "<h1>Thanks for joining</h1>",
    confirmation_email: { subject: "Confirm", html: '<a href="{{ confirmation_url }}">Confirm signup</a>' },
    thankyou_email: { subject: "Welcome", html: "<p>Welcome aboard</p>", text: "Welcome" },
  };
  const writes: Record<string, unknown>[] = [];
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1", "");
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin: true, csrf_token: "session-token" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "sender@example.org" }];
    else if (path === "/settings") {
      if (request.method() === "PATCH") {
        const patch = request.postDataJSON();
        writes.push(patch);
        for (const [key, value] of Object.entries(patch)) {
          if (value === null) delete state[key];
          else state[key] = value;
        }
      }
      body = state;
    } else if (path === "/email-preview") {
      const value = request.postDataJSON();
      body = { html: value.html, subject: value.subject ?? "", text: value.text ?? "" };
    } else if (path === "/sending") body = { enabled: false };
    await route.fulfill({ json: body });
  });
  return { state, writes };
}

test("message cards start collapsed, use the campaign editor at full width, and persist toggles", async ({ page, context }) => {
  const { state, writes } = await fixture(context);
  await page.goto("/brands/1/settings/subscriptions");
  await expect(page.locator(".email-editor")).toHaveCount(0);
  const card = page.getByRole("button", { name: "Subscription thank-you page", exact: true });
  await expect(card).toHaveAttribute("aria-expanded", "false");
  await card.click();
  await expect(page.getByRole("button", { name: "Visual editor", exact: true })).toBeVisible();
  await expect(page.frameLocator('iframe[title="Email preview"]').getByRole("heading", { name: "Thanks for joining" })).toBeVisible();
  const widths = await page.locator(".email-editor").evaluate((editor) => ({
    editor: editor.getBoundingClientRect().width,
    section: editor.closest("section")!.getBoundingClientRect().width,
  }));
  expect(widths.editor).toBeGreaterThan(widths.section - 80);
  await page.locator('.cm-content[contenteditable="true"]').fill("<h1>New thank you</h1>");
  await card.click();
  await page.getByRole("button", { name: "Double opt-in email", exact: true }).click();
  await expect(page.frameLocator('iframe[title="Email preview"]').getByRole("link", { name: "Confirm signup" })).toHaveAttribute("href", "{{ confirmation_url }}");
  await page.getByRole("button", { name: "Double opt-in email", exact: true }).click();
  await page.getByRole("switch", { name: "Enable Welcome email", exact: true }).click();
  await page.getByRole("button", { name: "Save messages", exact: true }).click();
  await expect(page.getByText("Saved.", { exact: true })).toBeVisible();
  expect(state.thankyou_email).toBeUndefined();
  expect(state.thankyou_html).toBe("<h1>New thank you</h1>");
  expect(writes[0].thankyou_email).toBeNull();
  await page.reload();
  await expect(page.getByRole("switch", { name: "Enable Welcome email", exact: true })).not.toBeChecked();
  await expect(page.locator(".email-editor")).toHaveCount(0);
  for (const label of ["Subscription thank-you page", "Double opt-in email"]) {
    await page.getByRole("switch", { name: `Enable ${label}`, exact: true }).click();
  }
  await page.getByRole("button", { name: "Save messages", exact: true }).click();
  await expect(page.getByText("Saved.", { exact: true })).toBeVisible();
  expect(Object.keys(state)).toHaveLength(0);
});

test("an incomplete enabled email opens for correction even while collapsed", async ({ page, context }) => {
  const { writes } = await fixture(context);
  await page.goto("/brands/1/settings/subscriptions");
  await page.getByRole("switch", { name: "Enable Goodbye email", exact: true }).click();
  await page.getByRole("button", { name: "Save messages", exact: true }).click();
  await expect(page.getByText("Enabled emails require a subject and HTML.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Goodbye email", exact: true })).toHaveAttribute("aria-expanded", "true");
  expect(writes).toHaveLength(0);
});


test("message headers and editor fit on mobile", async ({ page, context }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await fixture(context);
  await page.goto("/brands/1/settings/subscriptions");
  await page.screenshot({ path: "/tmp/postbox-settings-messages.png", fullPage: true });
  await page.getByRole("button", { name: "Subscription thank-you page", exact: true }).click();
  await expect(page.frameLocator('iframe[title="Email preview"]').getByRole("heading", { name: "Thanks for joining" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: "/tmp/postbox-settings-message-editor.png", fullPage: true });
});
