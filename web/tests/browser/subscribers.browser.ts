import { test, expect } from "@playwright/test";

test("subscriber columns and row actions", async ({ page, context }) => {
  let subscriber: Record<string, unknown> | null = {
    id: 7, email: "ada@example.com", name: "Ada Lovelace", status: "active", fields: {},
    created_at: 1700000000000, updated_at: 1700000000000,
    last_activity_kind: "complaint", last_activity_at: 1700100000000,
  };
  const writes: string[] = [];
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1", "");
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin: true, csrf_token: "token" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "sender@example.com" }];
    else if (path === "/sending") body = { enabled: false };
    else if (path === "/subscribers") body = subscriber ? [subscriber] : [];
    else if (path === "/subscribers/7/unsubscribe") {
      expect(request.postDataJSON()).toEqual({ list_id: 0 });
      writes.push("unsubscribe");
      subscriber = { ...subscriber, last_activity_kind: "unsubscribe" };
      body = { unsubscribed: true };
    } else if (path === "/subscribers/7" && request.method() === "DELETE") {
      writes.push("delete");
      subscriber = null;
      body = { erased: true };
    }
    await route.fulfill({ json: body });
  });
  await page.goto("/subscribers");
  const table = page.locator("table");
  await expect(table.getByRole("columnheader")).toHaveText(["Name", "Email", "Last activity", "Action"]);
  await expect(table.locator(".avatar")).toHaveCount(0);
  await expect(table.getByText("complaint", { exact: true })).toBeVisible();
  await page.screenshot({ path: test.info().outputPath("postbox-subscribers.png"), fullPage: true });
  await page.getByRole("button", { name: "Edit ada@example.com", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Subscriber profile" })).toBeVisible();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await page.getByRole("button", { name: "Delete ada@example.com", exact: true }).click();
  let dialog = page.getByRole("dialog", { name: "Permanently erase subscriber?" });
  await expect(dialog.getByRole("button", { name: "Permanently erase", exact: true })).toBeDisabled();
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(writes).toEqual([]);
  await page.getByRole("button", { name: "Unsubscribe ada@example.com", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "Unsubscribe subscriber?" });
  await dialog.getByRole("button", { name: "Unsubscribe", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(table.getByText("unsubscribe", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Delete ada@example.com", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "Permanently erase subscriber?" });
  await dialog.getByLabel("Type the email to confirm").fill("ada@example.com");
  await dialog.getByRole("button", { name: "Permanently erase", exact: true }).click();
  await expect(page.getByText("No subscribers match these filters.")).toBeVisible();
  expect(writes).toEqual(["unsubscribe", "delete"]);
});
