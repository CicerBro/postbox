import { expect, test, type BrowserContext } from "@playwright/test";

async function subscriberFixture(context: BrowserContext) {
  const writes: { path: string; csv: string; options: unknown }[] = [];
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace("/api/v1", "");
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin: true, csrf_token: "token" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "sender@example.org" }];
    else if (path === "/sending") body = { enabled: false };
    else if (path === "/lists") body = [{ id: 42, name: "Weekly news", opt_in: "single", active: 0 }];
    else if (path === "/lists/42/form") body = { hosted_url: "https://postbox.example/subscribe/list-token", html: '<form action="https://postbox.example/subscribe/list-token"><input name="email"></form>' };
    else if (path.startsWith("/subscribers/import")) {
      writes.push({ path, csv: request.postData() ?? "", options: JSON.parse(url.searchParams.get("options") ?? "{}") });
      body = { rows: 1, invalid: 0, imported: path.endsWith("/preview") ? 0 : 1, preview: [{ email: "ada@example.org", name: "Ada, Lovelace", status: "active", fields: {} }] };
    }
    await route.fulfill({ json: body });
  });
  return writes;
}

test("new subscriber can be added to a list without recording consent", async ({ page, context }) => {
  await subscriberFixture(context);
  const payloads: Record<string, unknown>[] = [];
  await page.route((url) => url.pathname === "/api/v1/subscribers", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    payloads.push(route.request().postDataJSON());
    await route.fulfill({ json: { id: 1, email: "ada@example.org" } });
  });
  await page.goto("/subscribers");
  await page.getByRole("button", { name: "New subscriber" }).click();
  const dialog = page.getByRole("dialog", { name: "New subscriber" });
  await dialog.getByRole("textbox", { name: "Email" }).fill("ada@example.org");
  await dialog.getByRole("checkbox", { name: /Weekly news/ }).check();
  await expect(dialog.getByRole("checkbox", { name: /Record newly obtained GDPR consent/ })).not.toBeChecked();
  const save = dialog.getByRole("button", { name: "Save profile" });
  await expect(save).toBeEnabled();
  await save.click();
  await expect(dialog).toHaveCount(0);
  expect(payloads).toHaveLength(1);
  expect(payloads[0]).toMatchObject({ email: "ada@example.org", list_ids: [42], consent: false });
});

test("pasted CSV shares mapping, preview and confirmed import with file uploads", async ({ page, context }) => {
  const writes = await subscriberFixture(context);
  await page.goto("/subscribers");
  await page.getByRole("tab", { name: "Import / export", exact: true }).click();
  const direct = page.getByRole("switch", { name: "Paste CSV directly" });
  await expect(direct).not.toBeChecked();
  await expect(page.getByRole("textbox", { name: "CSV text", exact: true })).toHaveCount(0);
  await direct.click();
  const csv = 'email,name\nada@example.org,"Ada, Lovelace"';
  await page.getByRole("textbox", { name: "CSV text", exact: true }).fill(csv);
  await page.getByRole("checkbox", { name: "Weekly news" }).check();
  await page.getByRole("button", { name: "Preview import", exact: true }).click();
  await expect(page.getByText("1 rows, 1 valid, 0 invalid. No rows have been written.")).toBeVisible();
  await page.getByRole("button", { name: "Import valid rows", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Commit CSV import?" });
  await expect(dialog).toContainText("pasted.csv");
  await dialog.getByRole("button", { name: "Confirm import", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "Imported 1 rows." })).toBeVisible();
  expect(writes).toEqual([
    { path: "/subscribers/import/preview", csv, options: { mapping: { email: "email", name: "name" }, list_ids: [42] } },
    { path: "/subscribers/import", csv, options: { mapping: { email: "email", name: "name" }, list_ids: [42] } },
  ]);

  await page.getByRole("textbox", { name: "CSV text", exact: true }).fill("email,email\nada@example.org,ada@example.org");
  await expect(page.getByRole("alert")).toContainText("CSV headers must be unique.");
  await expect(page.getByRole("button", { name: "Preview import", exact: true })).toBeDisabled();
  await direct.click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.getByLabel("CSV file", { exact: true }).setInputFiles({ name: "subscribers.csv", mimeType: "text/csv", buffer: Buffer.from(csv) });
  await page.getByRole("button", { name: "Preview import", exact: true }).click();
  await expect(page.getByText("1 rows, 1 valid, 0 invalid. No rows have been written.")).toBeVisible();
  await direct.click();
  await expect(page.getByText("1 rows, 1 valid, 0 invalid. No rows have been written.")).toHaveCount(0);
});

for (const width of [1280, 390]) {
  test(`subscribe form dialog and clipboard alert fit a ${width}px viewport`, async ({ page, context }) => {
    await page.setViewportSize({ width, height: 844 });
    await subscriberFixture(context);
    await page.addInitScript(() => {
      Object.defineProperty(navigator, "clipboard", { value: { writeText: async (value: string) => { (window as unknown as { copiedHTML: string }).copiedHTML = value; } } });
    });
    await page.goto("/subscribers");
    await page.getByRole("tab", { name: "Lists", exact: true }).click();
    await page.getByRole("button", { name: "More actions for Weekly news", exact: true }).click();
    await page.getByRole("menuitem", { name: "Subscribe form", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Weekly news subscribe form" });
    await expect(dialog.getByRole("link", { name: "Open hosted subscribe form" })).toBeVisible();
    const copy = dialog.getByRole("button", { name: "Copy embed HTML", exact: true });
    await expect(copy).toHaveClass(/button-primary/);
    await copy.click();
    const alert = dialog.getByRole("status");
    await expect(alert).toHaveAttribute("data-slot", "alert");
    await expect(alert).toHaveClass(/bg-green-50/);
    await expect(alert).toHaveText("Embed HTML copied to clipboard.");
    expect(await page.evaluate(() => (window as unknown as { copiedHTML: string }).copiedHTML)).toContain("<form action=");
    const bounds = await dialog.boundingBox();
    expect(bounds!.x).toBeGreaterThanOrEqual(16);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width - 15);
    await page.screenshot({ path: test.info().outputPath(`subscribe-dialog-${width}.png`), fullPage: true });
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
  });
}
