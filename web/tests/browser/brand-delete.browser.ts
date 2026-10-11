import { expect, test, type BrowserContext, type Page } from "@playwright/test";

async function fixture(context: BrowserContext, admin = true, single = false) {
  const state = {
    brands: [
      { id: 1, name: "Acme", from_email: "sender@example.org" },
      ...(!single
        ? [{ id: 2, name: "Other", from_email: "other@example.org" }]
        : []),
    ],
    deletes: 0,
    failure: false,
  };
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace("/api/v1", "");
    let body: unknown = [],
      status = 200;
    if (path === "/auth/session")
      body = { user_id: 1, admin, csrf_token: "session-token" };
    else if (path === "/brands") body = state.brands;
    else if (path.startsWith("/brands/")) {
      const id = Number(path.split("/").at(-1));
      if (request.method() === "DELETE") {
        state.deletes++;
        expect(id).toBe(1);
        expect(url.searchParams.get("brand_id")).toBe("1");
        expect(request.headers()["x-csrf-token"]).toBe("session-token");
        if (state.failure) {
          status = 500;
          body = { error: "Deletion failed. Please retry." };
        } else {
          state.brands = state.brands.filter((brand) => brand.id !== id);
          body = { deleted: true };
        }
      } else body = state.brands.filter((brand) => brand.id === id);
    } else if (path === "/sending") body = { enabled: false };
    await route.fulfill({ status, json: body });
  });
  return state;
}
async function openBrand(page: Page) {
  await page.goto("/settings");
  await page
    .getByRole("navigation", { name: "Settings sections" })
    .getByRole("button", { name: "Brand", exact: true })
    .click();
  await expect(page.getByLabel("Brand name", { exact: true })).toHaveValue(
    "Acme",
  );
}
async function confirm(page: Page) {
  await page.getByRole("button", { name: "Delete brand", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByRole("button", { name: "Delete brand", exact: true }),
  ).toBeDisabled();
  await dialog.getByLabel("Type Acme to confirm").fill("Acme");
  return dialog;
}
test("brand deletion requires confirmation and selects the remaining brand", async ({
  page,
  context,
}, testInfo) => {
  const state = await fixture(context);
  await openBrand(page);
  let dialog = await confirm(page);
  await expect(dialog).toContainText("cannot be undone");
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(state.deletes).toBe(0);
  dialog = await confirm(page);
  await page.screenshot({ path: testInfo.outputPath("brand-delete.png") });
  await dialog
    .getByRole("button", { name: "Delete brand", exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Switch brand" }),
  ).toContainText("Other");
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("postbox-brand")))
    .toBe("2");
  await page.getByRole("button", { name: "Switch brand" }).click();
  await expect(page.getByRole("menuitem", { name: /Acme/ })).toHaveCount(0);
  expect(state.deletes).toBe(1);
});
test("deleting the final brand clears selection and allows creating a new brand", async ({
  page,
  context,
}) => {
  const state = await fixture(context, true, true);
  await openBrand(page);
  const dialog = await confirm(page);
  await dialog
    .getByRole("button", { name: "Delete brand", exact: true })
    .click();
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("postbox-brand")))
    .toBeNull();
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Create your first brand" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Create brand", exact: true }),
  ).toBeVisible();
  expect(state.deletes).toBe(1);
});
test("failed deletion keeps the brand and permits retry", async ({
  page,
  context,
}) => {
  const state = await fixture(context);
  state.failure = true;
  await openBrand(page);
  const dialog = await confirm(page);
  await dialog
    .getByRole("button", { name: "Delete brand", exact: true })
    .click();
  await expect(dialog).toContainText("Deletion failed. Please retry.");
  await expect(
    page.getByRole("button", { name: "Switch brand", includeHidden: true }),
  ).toContainText("Acme");
  state.failure = false;
  await dialog
    .getByRole("button", { name: "Delete brand", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Switch brand" }),
  ).toContainText("Other");
  expect(state.deletes).toBe(2);
});
test("non-admins cannot access brand deletion", async ({ page, context }) => {
  const state = await fixture(context, false);
  await openBrand(page);
  await expect(
    page.getByRole("button", { name: "Delete brand", exact: true }),
  ).toHaveCount(0);
  expect(state.deletes).toBe(0);
});
