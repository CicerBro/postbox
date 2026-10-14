import { expect, test, type BrowserContext } from "@playwright/test";

type Hook = { id: number; url: string; events: string[]; is_active: boolean };
type Key = {
  id: number;
  name: string;
  prefix: string;
  scopes: string[];
  last_used_at: number | null;
  revoked_at: number | null;
};
async function fixture(
  context: BrowserContext,
  options: { admin?: boolean; write?: boolean; failCreate?: boolean } = {},
) {
  const hooks: Hook[] = [
    {
      id: 1,
      url: "https://app.example.com/webhooks/postbox",
      events: ["subscribe", "unsubscribe", "bounce"],
      is_active: true,
    },
    {
      id: 2,
      url: "https://analytics.example.com/events",
      events: ["campaign_started", "campaign_finished"],
      is_active: false,
    },
  ];
  const keys: Key[] = [
    {
      id: 1,
      name: "NZB Finder Laravel",
      prefix: "KZgQ3W2F-QML",
      scopes: ["read", "write"],
      last_used_at: null,
      revoked_at: null,
    },
    {
      id: 2,
      name: "Analytics",
      prefix: "analytics",
      scopes: ["read"],
      last_used_at: null,
      revoked_at: null,
    },
  ];
  const writes: {
    method: string;
    path: string;
    brand: string | null;
    body: Record<string, unknown> | null;
    csrf: string | undefined;
  }[] = [];
  let failCreate = options.failCreate;
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request(),
      url = new URL(request.url());
    const path = url.pathname.replace("/api/v1", ""),
      method = request.method();
    let body: unknown = [];
    if (method !== "GET")
      writes.push({
        method,
        path,
        brand: url.searchParams.get("brand_id"),
        body: request.postDataJSON(),
        csrf: request.headers()["x-csrf-token"],
      });
    if (path === "/auth/session")
      body = {
        user_id: 1,
        admin: options.admin ?? true,
        csrf_token: "token",
        timezone: "UTC",
      };
    else if (path === "/brands")
      body = [
        {
          id: 1,
          name: "Acme",
          permissions: options.write ? ["read", "write"] : ["read"],
        },
      ];
    else if (path === "/sending") body = { enabled: false };
    else if (path === "/webhooks" && method === "GET") body = hooks;
    else if (path === "/api-keys" && method === "GET") body = keys;
    else if (["/webhooks", "/api-keys"].includes(path) && method === "POST") {
      if (failCreate) {
        failCreate = false;
        await route.fulfill({
          status: 400,
          json: { error: "Please check your integration settings." },
        });
        return;
      }
      const input = request.postDataJSON();
      if (path === "/webhooks") {
        const hook = { id: 3, ...input } as Hook;
        hooks.push(hook);
        body = { ...hook, secret: "one-time-webhook-secret" };
      } else {
        keys.push({
          id: 3,
          ...input,
          prefix: "new-prefix",
          last_used_at: null,
          revoked_at: null,
        });
        body = { secret: "one-time-api-secret" };
      }
    } else if (path.startsWith("/webhooks/") && method === "PATCH") {
      const hook = hooks.find((h) => h.id === Number(path.split("/").at(-1)))!;
      const input = request.postDataJSON();
      if (input.rotate_secret)
        body = { ...hook, secret: "rotated-webhook-secret" };
      else {
        Object.assign(hook, input);
        body = hook;
      }
    } else if (path.startsWith("/webhooks/") && method === "DELETE")
      hooks.splice(
        hooks.findIndex((h) => h.id === Number(path.split("/").at(-1))),
        1,
      );
    else if (path.startsWith("/api-keys/") && method === "DELETE")
      keys.find((k) => k.id === Number(path.split("/").at(-1)))!.revoked_at = 1;
    await route.fulfill({ json: body });
  });
  return { writes };
}

test("integrations keep their tables and open forms on demand at desktop and mobile widths", async ({
  page,
  context,
}, testInfo) => {
  await fixture(context);
  await page.setViewportSize({ width: 1600, height: 1100 });
  await page.goto("/brands/1/settings/integrations");
  await expect(page.getByRole("table")).toHaveCount(2);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByLabel("Destination URL", { exact: true })).toHaveCount(
    0,
  );
  await expect(page.getByLabel("Key name", { exact: true })).toHaveCount(0);
  const keys = page.getByRole("table").nth(1);
  await expect(keys.getByRole("columnheader")).toHaveText([
    "Name",
    "Prefix",
    "Scopes",
    "Last used",
    "Status",
    "Actions",
  ]);
  await expect(
    keys.getByRole("button", { name: "Revoke", exact: true }),
  ).toHaveCount(2);
  await expect(
    page.getByRole("link", { name: "Integrations", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  await page.screenshot({
    path: testInfo.outputPath("integrations-desktop.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "Add webhook", exact: true }).click();
  await page.screenshot({
    path: testInfo.outputPath("webhook-editor-desktop.png"),
    fullPage: true,
  });
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Cancel", exact: true })
    .click();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.getByRole("button", { name: "Add webhook", exact: true }).click();
  const bounds = await page.getByRole("dialog").boundingBox();
  expect(bounds!.width).toBeLessThanOrEqual(390);
  await page.screenshot({
    path: testInfo.outputPath("webhook-editor-mobile.png"),
    fullPage: true,
  });
});

test("webhook creation keeps values on errors and shows the one-time secret", async ({
  page,
  context,
}) => {
  const { writes } = await fixture(context, { failCreate: true });
  await page.goto("/brands/1/settings/integrations");
  await page.getByRole("button", { name: "Add webhook", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog
    .getByLabel("Destination URL", { exact: true })
    .fill("https://receiver.example.com/postbox");
  await dialog
    .getByRole("checkbox", { name: "Subscribe", exact: true })
    .uncheck();
  await expect(
    dialog.getByRole("button", { name: "Create webhook", exact: true }),
  ).toBeDisabled();
  await dialog
    .getByRole("checkbox", { name: "Campaign finished", exact: true })
    .check();
  await dialog.getByRole("checkbox", { name: "Active", exact: true }).uncheck();
  await dialog
    .getByRole("button", { name: "Create webhook", exact: true })
    .click();
  await expect(dialog.getByRole("alert")).toContainText(
    "Please check your integration settings.",
  );
  await expect(
    dialog.getByLabel("Destination URL", { exact: true }),
  ).toHaveValue("https://receiver.example.com/postbox");
  await dialog
    .getByRole("button", { name: "Create webhook", exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  await expect(
    page.getByText("one-time-webhook-secret", { exact: true }),
  ).toBeVisible();
  expect(writes.at(-1)).toMatchObject({
    method: "POST",
    path: "/webhooks",
    brand: "1",
    csrf: "token",
    body: {
      url: "https://receiver.example.com/postbox",
      events: ["campaign_finished"],
      is_active: false,
    },
  });
  await page.getByRole("button", { name: "Dismiss", exact: true }).click();
  await page.getByRole("button", { name: "Add webhook", exact: true }).click();
  await expect(
    dialog.getByLabel("Destination URL", { exact: true }),
  ).toHaveValue("");
  await expect(
    dialog.getByRole("checkbox", { name: "Subscribe", exact: true }),
  ).toBeChecked();
  await expect(
    dialog.getByRole("checkbox", { name: "Active", exact: true }),
  ).toBeChecked();
});

test("webhook editing and menu actions preserve request semantics and confirmations", async ({
  page,
  context,
}) => {
  const { writes } = await fixture(context);
  await page.goto("/brands/1/settings/integrations");
  const table = page.getByRole("table", { name: "Brand webhooks" });
  await table.getByRole("button", { name: "Edit", exact: true }).nth(1).click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByRole("checkbox", { name: "Campaign started", exact: true }),
  ).toBeChecked();
  await expect(
    dialog.getByRole("checkbox", { name: "Active", exact: true }),
  ).not.toBeChecked();
  await dialog
    .getByLabel("Destination URL", { exact: true })
    .fill("https://analytics.example.com/updated");
  await dialog
    .getByRole("button", { name: "Save changes", exact: true })
    .click();
  await expect(table).toContainText("https://analytics.example.com/updated");
  expect(writes.at(-1)).toMatchObject({
    method: "PATCH",
    path: "/webhooks/2",
    body: {
      url: "https://analytics.example.com/updated",
      events: ["campaign_started", "campaign_finished"],
      is_active: false,
    },
  });
  await table
    .getByRole("button", { name: /More actions/ })
    .first()
    .click();
  await page.getByRole("menuitem", { name: "Pause", exact: true }).click();
  await expect(table.getByRole("row").nth(1)).toContainText("Paused");
  expect(writes.at(-1)?.body).toEqual({ is_active: false });
  await table
    .getByRole("button", { name: /More actions/ })
    .first()
    .click();
  await page
    .getByRole("menuitem", { name: "Rotate secret", exact: true })
    .click();
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(writes).toHaveLength(2);
  await table
    .getByRole("button", { name: /More actions/ })
    .first()
    .click();
  await page
    .getByRole("menuitem", { name: "Rotate secret", exact: true })
    .click();
  await dialog
    .getByRole("button", { name: "Rotate secret", exact: true })
    .click();
  await expect(
    page.getByText("rotated-webhook-secret", { exact: true }),
  ).toBeVisible();
  expect(writes.at(-1)?.body).toEqual({ rotate_secret: true });
  await table
    .getByRole("button", { name: /More actions/ })
    .first()
    .click();
  await page
    .getByRole("menuitem", { name: "Delete webhook", exact: true })
    .click();
  await dialog
    .getByRole("button", { name: "Delete webhook", exact: true })
    .click();
  await expect(table.getByRole("row")).toHaveCount(2);
  expect(writes.at(-1)).toMatchObject({
    method: "DELETE",
    path: "/webhooks/1",
    brand: "1",
  });
});

test("API keys retain sorting and revocation, with creation and errors handled in a dialog", async ({
  page,
  context,
}) => {
  const { writes } = await fixture(context, { failCreate: true });
  await page.goto("/brands/1/settings/integrations");
  await page
    .getByRole("button", { name: "Create API key", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog
    .getByLabel("Key name", { exact: true })
    .fill("Website integration");
  await dialog.getByRole("checkbox", { name: /Read/ }).uncheck();
  await expect(
    dialog.getByRole("button", { name: "Create key", exact: true }),
  ).toBeDisabled();
  await dialog.getByRole("checkbox", { name: /Write/ }).check();
  await dialog.getByRole("button", { name: "Create key", exact: true }).click();
  await expect(dialog.getByRole("alert")).toBeVisible();
  await expect(dialog.getByRole("checkbox", { name: /Write/ })).toBeChecked();
  await dialog.getByRole("button", { name: "Create key", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(
    page.getByText("one-time-api-secret", { exact: true }),
  ).toBeVisible();
  expect(writes.at(-1)).toMatchObject({
    method: "POST",
    path: "/api-keys",
    brand: "1",
    csrf: "token",
    body: { name: "Website integration", scopes: ["write"] },
  });
  const keys = page.getByRole("table").nth(1);
  await expect(keys.getByRole("row").nth(1)).toContainText("Analytics");
  await keys.getByRole("button", { name: /Sort by Name/ }).click();
  await expect(keys.getByRole("row").nth(1)).toContainText(
    "Website integration",
  );
  await keys
    .getByRole("row")
    .filter({ hasText: "Website integration" })
    .getByRole("button", { name: "Revoke", exact: true })
    .click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Revoke key", exact: true })
    .click();
  await expect(
    keys.getByRole("row").filter({ hasText: "Website integration" }),
  ).toContainText("Revoked");
  expect(writes.at(-1)).toMatchObject({
    method: "DELETE",
    path: "/api-keys/3",
    brand: "1",
  });
  await page
    .getByRole("button", { name: "Create API key", exact: true })
    .click();
  await expect(dialog.getByLabel("Key name", { exact: true })).toHaveValue("");
  await expect(dialog.getByRole("checkbox", { name: /Read/ })).toBeChecked();
  await expect(
    dialog.getByRole("checkbox", { name: /Write/ }),
  ).not.toBeChecked();
});

test("brand writers can manage webhooks without API key management", async ({
  page,
  context,
}) => {
  await fixture(context, { admin: false, write: true });
  await page.goto("/brands/1/settings/integrations");
  await expect(
    page.getByRole("button", { name: "Add webhook", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Edit", exact: true }),
  ).toHaveCount(2);
  await expect(
    page.getByRole("button", { name: "Create API key", exact: true }),
  ).toHaveCount(0);
});
