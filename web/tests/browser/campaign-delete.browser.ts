import { test, expect, type BrowserContext } from "@playwright/test";

async function fixture(context: BrowserContext, admin = true) {
  const state = {
    rows: [
      "draft",
      "sent",
      "cancelled",
      "scheduled",
      "preparing",
      "sending",
      "paused",
    ].map((status, i) => ({
      id: i + 1,
      subject: `Newsletter ${status}`,
      status,
      excerpt: "Hello",
      created_at: 1,
      scheduled_at: null,
    })),
    deletes: [] as number[],
    duplicates: [] as number[],
    failure: false,
  };
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request(),
      url = new URL(request.url()),
      path = url.pathname.replace("/api/v1", "");
    let body: unknown = [],
      status = 200;
    if (path === "/auth/session")
      body = { user_id: 1, admin, csrf_token: "csrf" };
    else if (path === "/brands")
      body = [{ id: 1, name: "Acme", from_email: "sender@example.org" }];
    else if (path === "/sending") body = { enabled: false };
    else if (path === "/campaigns") body = state.rows;
    else if (request.method() === "POST" && path.endsWith("/duplicate")) {
      expect(url.searchParams.get("brand_id")).toBe("1");
      expect(request.headers()["x-csrf-token"]).toBe("csrf");
      const id = Number(path.split("/").at(-2));
      const source = state.rows.find((row) => row.id === id);
      expect(source).toBeDefined();
      state.duplicates.push(id);
      const copy = { ...source!, id: state.rows.length + 1, status: "draft", scheduled_at: null };
      state.rows.push(copy);
      body = { id: copy.id };
    }
    else if (path.startsWith("/campaigns/") && request.method() === "GET") {
      const id = Number(path.split("/").at(-1));
      body = state.rows.filter((row) => row.id === id).map((row) => ({
        ...row,
        html: "<p>Hello</p>",
        text: "Hello",
        inline_css: true,
        created_by: 1,
      }));
    }
    else if (request.method() === "DELETE" && path.startsWith("/campaigns/")) {
      expect(url.searchParams.get("brand_id")).toBe("1");
      expect(request.headers()["x-csrf-token"]).toBe("csrf");
      const id = Number(path.split("/").at(-1));
      state.deletes.push(id);
      if (state.failure) {
        status = 409;
        body = { error: "Campaign state changed. Refresh and try again." };
      } else {
        state.rows = state.rows.filter((row) => row.id !== id);
        body = { deleted: true };
      }
    }
    await route.fulfill({ status, json: body });
  });
  return state;
}

test("admins delete drafts, sent and cancelled campaigns with confirmation", async ({
  page,
  context,
}, testInfo) => {
  const state = await fixture(context);
  await page.goto("/campaigns");
  for (const status of ["scheduled", "preparing", "sending", "paused"])
    await expect(
      page.getByRole("button", {
        name: `Delete campaign Newsletter ${status}`,
        exact: true,
      }),
    ).toHaveCount(0);
  for (const status of ["draft", "sent", "cancelled"]) {
    const button = page.getByRole("button", {
      name: `Delete campaign Newsletter ${status}`,
      exact: true,
    });
    await button.click();
    const dialog = page.getByRole("dialog", { name: "Delete campaign?" });
    await expect(dialog).toContainText(`Newsletter ${status}`);
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(button).toBeVisible();
    await button.click();
    if (status === "sent")
      await page.screenshot({
        path: testInfo.outputPath("campaign-delete.png"),
      });
    await dialog
      .getByRole("button", { name: "Delete campaign", exact: true })
      .click();
    await expect(dialog).toHaveCount(0);
    await expect(
      page.getByRole("link", { name: `Newsletter ${status}`, exact: true }),
    ).toHaveCount(0);
  }
  expect(state.deletes).toEqual([1, 2, 3]);
});

test("campaign delete errors remain in the dialog and support retry", async ({
  page,
  context,
}) => {
  const state = await fixture(context);
  state.failure = true;
  await page.goto("/campaigns");
  await page
    .getByRole("button", {
      name: "Delete campaign Newsletter sent",
      exact: true,
    })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog
    .getByRole("button", { name: "Delete campaign", exact: true })
    .click();
  await expect(dialog).toContainText("Campaign state changed");
  state.failure = false;
  await dialog
    .getByRole("button", { name: "Delete campaign", exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  expect(state.deletes).toEqual([2, 2]);
});

test("campaign list deletion is admin-only", async ({ page, context }) => {
  await fixture(context, false);
  await page.goto("/campaigns");
  await expect(
    page.getByRole("link", { name: "Newsletter sent", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: /Delete campaign/ }),
  ).toHaveCount(0);
});

test("draft and sent campaigns can be duplicated from the campaign list", async ({ page, context }) => {
  const state = await fixture(context);
  await page.goto("/campaigns");
  for (const [status, copyID] of [["sent", 8], ["draft", 9]] as const) {
    await page.getByRole("button", { name: `Duplicate campaign Newsletter ${status}` }).click();
    await expect(page).toHaveURL(new RegExp(`/campaigns/${copyID}$`));
    await expect(page.getByRole("heading", { name: `Newsletter ${status}` })).toBeVisible();
    await page.goto("/campaigns");
  }
  expect(state.duplicates).toEqual([2, 1]);
});

test("sent campaign report offers duplication", async ({ page, context }) => {
  const state = await fixture(context);
  await page.goto("/campaigns/2/report");
  await page.getByRole("button", { name: "Duplicate", exact: true }).click();
  await expect(page).toHaveURL(/\/campaigns\/8$/);
  expect(state.duplicates).toEqual([2]);
});
