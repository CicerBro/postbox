import { expect, test, type BrowserContext } from "@playwright/test";

async function fixture(context: BrowserContext, admin = true) {
  const state = { failure: "" };
  await context.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    if (path === state.failure)
      return route.fulfill({ status: 403, json: { error: "Example denied request" } });
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin, csrf_token: "token", timezone: "UTC" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", permissions: ["read"] }];
    else if (path === "/sending") body = { enabled: false };
    else if (path === "/segments") body = [{ id: 1, name: "Engaged", rules: { field: "status", op: "eq", value: "active" } }];
    else if (path === "/templates") body = [{ id: 1, name: "Weekly", html: "<p>Hello</p>", text: "", updated_at: 1 }];
    else if (path === "/lists") body = [
      { id: 1, name: "Missing count", opt_in: "single" },
      { id: 2, name: "Zero count", opt_in: "single", active: 0 },
      { id: 3, name: "Ten count", opt_in: "single", active: 10 },
    ];
    else if (path === "/custom-fields") body = [
      { id: 1, key: "first", label: "First", type: "text", is_filterable: true },
      { id: 2, key: "second", label: "Second", type: "text", is_filterable: false },
    ];
    else if (path === "/settings") body = {};
    else if (path === "/subscription-settings") body = { enabled: false, application_enabled: false };
    else if (path === "/email-preview") body = { html: "<p>Hello</p>", text: "Hello", subject: "" };
    else if (path === "/subscribers") body = [{ id: 7, name: "Ada", email: "ada@example.org", status: "complained", fields: {}, created_at: 1, updated_at: 1 }];
    else if (path === "/subscribers/7") body = { id: 7, name: "Ada", email: "ada@example.org", status: "complained", fields: {}, created_at: 1, updated_at: 1 };
    else if (path === "/subscribers/7/memberships") body = [{ list_id: 1, status: "bounced", created_at: 1, updated_at: 1 }];
    await route.fulfill({ json: body });
  });
  return state;
}

for (const [path, retryLabel, emptyLabel, item] of [
  ["/templates", "Retry loading templates", "Your next campaign starts here", "Weekly"],
  ["/segments", "Retry loading segments", "Create your first segment", "Engaged"],
]) {
  test(`${path} distinguishes a failed request from an empty collection and retries`, async ({ page, context }) => {
    const state = await fixture(context);
    state.failure = path;
    await page.goto(path);
    await expect(page.getByRole("alert")).toContainText("Example denied request");
    await expect(page.getByText(emptyLabel, { exact: true })).toHaveCount(0);
    state.failure = "";
    await page.getByRole("button", { name: retryLabel }).click();
    await expect(page.getByRole("cell").filter({ hasText: item }).first()).toBeVisible();
    await expect(page.getByRole("alert")).toHaveCount(0);
  });
}

test("filtered empty states span the entire segments and lists tables", async ({ page, context }) => {
  await fixture(context);
  for (const [path, searchLabel, message] of [
    ["/segments", "Search segments", "No segments match your search."],
    ["/subscribers?tab=lists", "Search lists", "No lists match your search."],
  ]) {
    await page.goto(path);
    await page.getByRole("textbox", { name: searchLabel, exact: true }).fill("no-match");
    const cell = page.getByRole("cell").filter({ hasText: message });
    await expect(cell).toBeVisible();
    const dimensions = await cell.evaluate(element => ({
      display: getComputedStyle(element).display,
      width: element.getBoundingClientRect().width,
      table: element.closest("table")!.getBoundingClientRect().width,
    }));
    expect(dimensions.display).toBe("table-cell");
    expect(dimensions.width).toBeCloseTo(dimensions.table, 0);
  }
});

test("subscriber tabs support roving focus, disabled tabs, panels, and URL navigation", async ({ page, context }) => {
  await fixture(context, false);
  await page.goto("/subscribers");
  await expect(page.locator('[role="tab"][tabindex="0"]')).toHaveCount(1);
  expect(await page.getByRole("tab").evaluateAll(tabs => tabs.every(tab =>
    document.getElementById(tab.getAttribute("aria-controls")!)?.getAttribute("aria-labelledby") === tab.id,
  ))).toBe(true);
  await page.getByRole("tab", { name: "Import / export", exact: true }).click();
  await page.keyboard.press("ArrowRight");
  const fields = page.getByRole("tab", { name: "Custom fields", exact: true });
  await expect(fields).toBeFocused();
  await expect(fields).toHaveAttribute("aria-selected", "true");
  await expect(page).toHaveURL(/tab=fields/);
  await expect(page.getByRole("tabpanel")).toHaveAttribute("aria-labelledby", await fields.getAttribute("id") as string);
  await page.keyboard.press("End");
  await expect(page.getByRole("tab", { name: "Suppressions", exact: true })).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "Subscribers", exact: true })).toBeFocused();
  await page.keyboard.press("ArrowLeft");
  await expect(page.getByRole("tab", { name: "Suppressions", exact: true })).toBeFocused();
  await page.keyboard.press("Home");
  await expect(page.getByRole("tab", { name: "Subscribers", exact: true })).toBeFocused();
  await page.goBack();
  await expect(page.getByRole("tab", { name: "Suppressions", exact: true })).toHaveAttribute("aria-selected", "true");
});

test("shared fields use one gap and mobile filters fill their column", async ({ page, context }) => {
  await fixture(context);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/subscribers");
  await expect(page.getByRole("combobox", { name: "List", exact: true })).toBeVisible();
  const fields = await page.locator(".table-toolbar .form-field").evaluateAll(elements => elements.map(element => {
    const label = element.firstElementChild!.getBoundingClientRect();
    const control = element.querySelector(".select-trigger")!.getBoundingClientRect();
    return { gap: control.top - label.bottom, width: control.width, parent: element.parentElement!.clientWidth - 32 };
  }));
  for (const field of fields) {
    expect(field.gap).toBe(8);
    expect(field.width).toBeCloseTo(field.parent, 0);
  }
  await page.goto("/account/security");
  const input = page.getByLabel("Sign-in email", { exact: true });
  await expect(input).toBeVisible();
  expect(await input.evaluate(element => element.getBoundingClientRect().top - element.previousElementSibling!.getBoundingClientRect().bottom)).toBe(8);
});

test("subscriber status colors share the same danger tone in dark mode", async ({ page, context }) => {
  await fixture(context);
  await page.goto("/subscribers");
  await page.getByRole("button", { name: "Dark theme", exact: true }).click();
  await page.getByRole("button", { name: "Edit ada@example.org", exact: true }).click();
  const badges = page.getByRole("dialog").locator(".status-badge");
  await expect(badges.filter({ hasText: "Complained" })).toBeVisible();
  await expect(badges.filter({ hasText: "Bounced" })).toBeVisible();
  const colors = await badges.evaluateAll(elements => elements.map(element => getComputedStyle(element).color));
  expect(new Set(colors).size).toBe(1);
  expect(colors[0]).toBe("rgb(255, 145, 145)");
});

test("list and field sorting use shared numeric, missing-value, and boolean behavior", async ({ page, context }) => {
  await fixture(context);
  await page.goto("/subscribers?tab=lists");
  await page.getByRole("button", { name: "Sort by Active", exact: false }).click();
  const names = () => page.locator("tbody tr td:first-child .row-title").allTextContents();
  await expect.poll(names).toEqual(["Zero count", "Ten count", "Missing count"]);
  await page.getByRole("button", { name: "Sort by Active", exact: false }).click();
  await expect.poll(names).toEqual(["Ten count", "Zero count", "Missing count"]);
  await page.getByRole("tab", { name: "Custom fields", exact: true }).click();
  await page.getByRole("button", { name: "Sort by Filterable", exact: false }).click();
  await expect(page.locator("tbody tr td:first-child")).toHaveText(["Second", "First"]);
});

test("settings messages use the shared forbidden-error alert", async ({ page, context }) => {
  const state = await fixture(context);
  state.failure = "/brands/1";
  await page.goto("/brands/1/settings/general");
  await expect(page.getByRole("alert")).toContainText("Access denied (403): Example denied request");
});

test("templates and account settings only download the editor when opened", async ({ page, context }) => {
  await fixture(context);
  const editorRequests: string[] = [];
  page.on("request", request => {
    if (new URL(request.url()).pathname === "/src/components/EmailEditor.tsx") editorRequests.push(request.url());
  });
  await page.goto("/account/preferences");
  await expect(page.getByRole("heading", { name: "My account", exact: true })).toBeVisible();
  await page.goto("/templates");
  await expect(page.getByRole("button", { name: "Edit template Weekly", exact: true })).toBeVisible();
  expect(editorRequests).toHaveLength(0);
  let release: () => void = () => {};
  const hold = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/src/components/EmailEditor.tsx*", async route => { await hold; await route.continue(); });
  await page.getByRole("button", { name: "New template", exact: true }).click();
  await expect(page.getByRole("status")).toHaveText("Loading email editor…");
  await expect(page.getByRole("heading", { name: "Templates", exact: true })).toBeVisible();
  release();
  await expect(page.getByRole("button", { name: "Visual editor", exact: true })).toBeVisible();
  expect(editorRequests).toHaveLength(1);
  await page.getByRole("button", { name: "Close editor", exact: true }).click();
  await page.getByRole("button", { name: "New template", exact: true }).click();
  await expect(page.getByRole("button", { name: "Visual editor", exact: true })).toBeVisible();
  await expect(page.getByText("Loading email editor…", { exact: true })).toHaveCount(0);
  expect(editorRequests).toHaveLength(1);
});

test("closing during an editor download safely preserves the loaded module", async ({ page, context }) => {
  await fixture(context);
  let release: () => void = () => {};
  const hold = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/src/components/EmailEditor.tsx*", async route => { await hold; await route.continue(); });
  await page.goto("/templates");
  await page.getByRole("button", { name: "New template", exact: true }).click();
  await expect(page.getByText("Loading email editor…", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Close editor", exact: true }).click();
  release();
  await expect(page.getByText("Loading email editor…", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Edit template Weekly", exact: true }).click();
  await expect(page.locator(".cm-editor")).toContainText("Hello");
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("failed editor downloads leave the page usable and offer reload", async ({ page, context }) => {
  await fixture(context);
  await page.route("**/src/components/EmailEditor.tsx*", route => route.abort());
  await page.goto("/templates");
  await page.getByRole("button", { name: "New template", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Could not load the email editor.");
  await expect(page.getByRole("button", { name: "Reload page", exact: true })).toBeVisible();
  await page.unroute("**/src/components/EmailEditor.tsx*");
  await page.getByRole("button", { name: "Reload page", exact: true }).click();
  await page.getByRole("button", { name: "New template", exact: true }).click();
  await expect(page.locator(".cm-editor")).toBeVisible();
  await page.getByRole("button", { name: "Close editor", exact: true }).click();
  await expect(page.getByRole("button", { name: "Edit template Weekly", exact: true })).toBeVisible();
});
