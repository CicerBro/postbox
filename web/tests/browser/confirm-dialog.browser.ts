import { expect, test, type BrowserContext } from "@playwright/test";

async function fixture(context: BrowserContext) {
  const state = {
    deletes: 0,
    failure: false,
    revoked: false,
    release: undefined as (() => void) | undefined,
    hold: false,
  };
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1", "");
    let body: unknown = [];
    let status = 200;
    if (path === "/auth/session")
      body = { user_id: 1, admin: true, csrf_token: "csrf" };
    else if (path === "/brands")
      body = [{ id: 1, name: "Acme", from_email: "sender@example.org" }];
    else if (path === "/sending") body = { enabled: false };
    else if (path === "/campaigns")
      body = [{ id: 1, subject: "Newsletter", status: "sent", created_at: 1 }];
    else if (path === "/api-keys")
      body = [
        {
          id: 1,
          name: "Website",
          prefix: "pk",
          scopes: ["read"],
          last_used_at: null,
          revoked_at: state.revoked ? 1 : null,
        },
      ];
    else if (path === "/subscribers")
      body = [
        {
          id: 7,
          email: "ada@example.com",
          name: "Ada",
          status: "active",
          fields: {},
          created_at: 1700000000000,
          updated_at: 1700000000000,
          last_activity_kind: state.revoked ? "unsubscribe" : "subscribe",
        },
      ];
    else if (
      request.method() === "DELETE" ||
      path === "/subscribers/7/unsubscribe"
    ) {
      if (path.endsWith("/unsubscribe"))
        expect(request.postDataJSON()).toEqual({ list_id: 0 });
      state.deletes++;
      if (state.hold)
        await new Promise<void>((resolve) => {
          state.release = resolve;
        });
      if (state.failure) {
        status = 409;
        body = { error: "Changed on the server. Try again." };
      } else {
        state.revoked = true;
        body = { deleted: true };
      }
    }
    await route.fulfill({ status, json: body });
  });
  return state;
}

test("simple confirmation blocks every dismissal during a request and restores cancellation after failure", async ({
  page,
  context,
}) => {
  const state = await fixture(context);
  state.hold = true;
  state.failure = true;
  await page.goto("/campaigns");
  await page
    .getByRole("button", { name: "Delete campaign Newsletter", exact: true })
    .click();
  const dialog = page.getByRole("dialog", { name: "Delete campaign?" });
  await expect(dialog).toHaveClass(/dialog-width-md/);
  await dialog
    .getByRole("button", { name: "Delete campaign", exact: true })
    .click();
  await expect.poll(() => state.deletes).toBe(1);
  await expect(dialog).toHaveAttribute("aria-busy", "true");
  await expect(
    dialog.getByRole("button", { name: "Cancel", exact: true }),
  ).toBeDisabled();
  await expect(
    dialog.getByRole("button", { name: "Deleting…", exact: true }),
  ).toBeDisabled();
  await expect(
    dialog.getByRole("button", { name: "Close dialog" }),
  ).toHaveCount(0);
  await page.keyboard.press("Escape");
  await page
    .locator('[data-slot="dialog-overlay"]')
    .click({ position: { x: 5, y: 5 }, force: true });
  await expect(dialog).toBeVisible();
  expect(state.deletes).toBe(1);
  state.release!();
  await expect(dialog).toContainText("Changed on the server");
  await expect(
    dialog.getByRole("button", { name: "Cancel", exact: true }),
  ).toBeEnabled();
  await expect(
    dialog.getByRole("button", { name: "Close dialog" }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
});

test("native confirmation replacement supports cancellation, pending protection, and retry on mobile", async ({
  page,
  context,
}) => {
  const state = await fixture(context);
  await page.setViewportSize({ width: 390, height: 844 });
  page.on("dialog", () => {
    throw new Error("Unexpected native browser confirmation");
  });
  await page.goto("/brands/1/settings/integrations");
  const revoke = page.getByRole("button", { name: "Revoke", exact: true });
  await revoke.click();
  const dialog = page.getByRole("dialog", { name: "Revoke key?" });
  await expect(dialog).toHaveClass(/dialog-width-sm/);
  const box = await dialog.boundingBox();
  expect(box!.width).toBeLessThanOrEqual(358);
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(state.deletes).toBe(0);
  await revoke.click();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  expect(state.deletes).toBe(0);
  await revoke.click();
  await page.mouse.click(5, 5);
  await expect(dialog).toHaveCount(0);
  await revoke.click();
  await dialog.getByRole("button", { name: "Close dialog" }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.deletes).toBe(0);
  await revoke.click();
  state.hold = true;
  state.failure = true;
  await dialog.getByRole("button", { name: "Revoke key", exact: true }).click();
  await expect.poll(() => state.deletes).toBe(1);
  await page.keyboard.press("Escape");
  await page
    .locator('[data-slot="dialog-overlay"]')
    .click({ position: { x: 5, y: 5 }, force: true });
  await expect(dialog).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: "Cancel", exact: true }),
  ).toBeDisabled();
  state.release!();
  await expect(dialog).toContainText("Changed on the server");
  state.hold = false;
  state.failure = false;
  await dialog.getByRole("button", { name: "Revoke key", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByText("Key revoked.", { exact: true })).toBeVisible();
  expect(state.deletes).toBe(2);
});

test("subscriber unsubscribe keeps its target and supports cancellation and retry", async ({
  page,
  context,
}) => {
  const state = await fixture(context);
  await page.goto("/subscribers");
  const unsubscribe = page.getByRole("button", {
    name: "Unsubscribe ada@example.com",
    exact: true,
  });
  await unsubscribe.click();
  const dialog = page.getByRole("dialog", { name: "Unsubscribe subscriber?" });
  await expect(dialog).toContainText("ada@example.com");
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(state.deletes).toBe(0);
  await unsubscribe.click();
  state.failure = true;
  await dialog
    .getByRole("button", { name: "Unsubscribe", exact: true })
    .click();
  await expect(dialog).toContainText("Changed on the server");
  state.failure = false;
  await dialog
    .getByRole("button", { name: "Unsubscribe", exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  expect(state.deletes).toBe(2);
});
