import { expect, test, type BrowserContext } from "@playwright/test";

async function fixture(context: BrowserContext, permissions: string[]) {
  const writes: string[] = [];
  await context.addInitScript(() => {
    Object.defineProperty(window, "EventSource", { value: class extends EventTarget { close() {} } });
  });
  await context.route("**/api/v1/**", async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1", "");
    if (request.method() !== "GET") writes.push(path);
    let body: unknown = [];
    const campaign = { id: 7, subject: "Newsletter", html: "<p>Saved HTML</p>", text: "Saved text", status: "draft", created_by: 1, inline_css: true, created_at: 1 };
    if (path === "/auth/session") body = { user_id: 1, admin: false, csrf_token: "test" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", permissions }];
    else if (path === "/campaigns" || path === "/campaigns/7") body = [campaign];
    else if (path === "/campaigns/7/report") body = { stats: {}, links: [], countries: [] };
    else if (path === "/campaigns/7/preview") body = { html: campaign.html, subject: campaign.subject, text: campaign.text };
    else if (path === "/templates") body = [{ id: 1, name: "Weekly", html: "<p>Template</p>", text: "Template text", updated_at: 1 }];
    else if (path === "/segments") body = [{ id: 1, name: "Engaged", rules: { field: "status", op: "eq", value: "active" } }];
    else if (path === "/segments/preview") body = { count: 0 };
    else if (path === "/email-preview") body = { html: "<p>Preview</p>", subject: "", text: "" };
    await route.fulfill({ json: body });
  });
  return writes;
}

test("readers cannot create, clone, edit, or delete through list controls or creation URLs", async ({ page, context }) => {
  const writes = await fixture(context, ["read"]);
  await page.goto("/campaigns");
  await expect(page.getByRole("button", { name: "New campaign", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Clone campaign Newsletter", exact: true })).toBeDisabled();
  await page.goto("/campaigns/new");
  await expect(page.getByRole("status")).toHaveText("Brand write access is required to create a campaign.");
  await expect(page.getByRole("button", { name: "Save draft", exact: true })).toHaveCount(0);
  await page.goto("/templates?new=1");
  await expect(page.getByRole("button", { name: "New template", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Delete template Weekly", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Save template", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "View template Weekly", exact: true }).click();
  await expect(page.getByRole("heading", { name: "View template", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Save template", exact: true })).toBeDisabled();
  await expect(page.getByRole("textbox", { name: "Name", exact: true })).toHaveAttribute("readonly", "");
  await expect(page.locator(".cm-content")).toHaveAttribute("contenteditable", "false");
  await page.goto("/segments?new=1");
  for (const name of ["New segment", "Edit segment Engaged", "Delete segment Engaged"])
    await expect(page.getByRole("button", { name, exact: true })).toBeDisabled();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(writes).toEqual([]);
});

test("reader owners can inspect drafts and reports without write or send actions", async ({ page, context }) => {
  await fixture(context, ["read"]);
  await page.goto("/campaigns/7");
  await expect(page.getByRole("button", { name: "Duplicate", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Save draft", exact: true })).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Subject", exact: true })).toBeDisabled();
  await page.getByRole("textbox", { name: "Send deliberate [Test] emails", exact: true }).fill("test@example.org");
  await expect(page.getByRole("button", { name: "Send [Test] emails", exact: true })).toBeDisabled();
  await page.goto("/campaigns/7/report");
  await expect(page.getByRole("button", { name: "Duplicate", exact: true })).toBeDisabled();
});

test("writers retain create, edit, clone, and delete controls without an administrator account", async ({ page, context }) => {
  await fixture(context, ["read", "write"]);
  await page.goto("/campaigns");
  await expect(page.getByRole("button", { name: "New campaign", exact: true })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Clone campaign Newsletter", exact: true })).toBeEnabled();
  await page.goto("/templates");
  await expect(page.getByRole("button", { name: "New template", exact: true })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Delete template Weekly", exact: true })).toBeEnabled();
  await page.getByRole("button", { name: "Edit template Weekly", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save template", exact: true })).toBeEnabled();
  await expect(page.locator(".cm-content")).toHaveAttribute("contenteditable", "true");
  await page.goto("/segments");
  for (const name of ["New segment", "Edit segment Engaged", "Delete segment Engaged"])
    await expect(page.getByRole("button", { name, exact: true })).toBeEnabled();
});
