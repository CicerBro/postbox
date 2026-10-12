import { expect, test } from "@playwright/test";

test("suppression search and filters query all pages and reset pagination", async ({ page, context }, testInfo) => {
  const rows = [
    ...Array.from({ length: 150 }, (_, i) => ({ id: i + 1, brand_id: 1 as number | null, kind: "email", value: `import-${i + 1}@example.org`, reason: "ses_sync" })),
    { id: 151, brand_id: 1, kind: "email", value: "my-addition@example.org", reason: "manual" },
    { id: 152, brand_id: null, kind: "domain", value: "blocked.example", reason: "manual" },
  ];
  const requests: URLSearchParams[] = [];
  await context.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace("/api/v1", "");
    const params = url.searchParams;
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin: true, csrf_token: "session-token" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "sender@example.org" }];
    else if (path === "/sending") body = { enabled: false };
    else if (path === "/suppressions") {
      requests.push(params);
      body = rows.filter((row) => {
        const search = (params.get("q") ?? "").toLowerCase();
        return row.id > Number(params.get("after"))
          && (params.get("scope") !== "brand" || row.brand_id === 1)
          && (params.get("scope") !== "global" || row.brand_id === null)
          && (!params.get("kind") || row.kind === params.get("kind"))
          && (!params.get("reason") || row.reason === params.get("reason"))
          && (row.value.toLowerCase().includes(search) || row.reason.includes(search));
      }).slice(0, Number(params.get("limit")));
    }
    await route.fulfill({ json: body });
  });
  await page.goto("/settings");
  await page.getByRole("navigation", { name: "Settings sections" }).getByRole("button", { name: "Suppressions", exact: true }).click();
  await expect(page.getByRole("cell", { name: "import-1@example.org", exact: true })).toBeVisible();
  await expect(page.getByRole("cell", { name: "my-addition@example.org", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Load more", exact: true }).click();
  await expect(page.getByRole("cell", { name: "my-addition@example.org", exact: true })).toBeVisible();
  expect(requests.at(-1)?.get("after")).toBe("100");

  await page.getByRole("button", { name: "Manual additions", exact: true }).click();
  await expect(page.getByRole("cell", { name: "ses_sync", exact: true })).toHaveCount(0);
  await expect(page.getByRole("cell", { name: "blocked.example", exact: true })).toBeVisible();
  expect(requests.at(-1)?.get("after")).toBe("0");
  expect(requests.at(-1)?.get("reason")).toBe("manual");

  await page.getByLabel("Search suppressions").fill("MY-ADDITION");
  await expect(page.getByRole("cell", { name: "blocked.example", exact: true })).toHaveCount(0);
  await expect(page.getByRole("cell", { name: "my-addition@example.org", exact: true })).toBeVisible();
  expect(requests.at(-1)?.get("q")).toBe("MY-ADDITION");
  await page.screenshot({ path: testInfo.outputPath("suppression-filters.png"), fullPage: true });
  await page.getByLabel("Filter by kind").click();
  await page.getByRole("option", { name: "Domain", exact: true }).click();
  await expect(page.getByRole("heading", { name: "No matching suppressions" })).toBeVisible();
  await page.getByRole("button", { name: "Clear filters" }).click();
  await expect(page.getByRole("cell", { name: "import-1@example.org", exact: true })).toBeVisible();
  await expect(page.getByLabel("Search suppressions")).toHaveValue("");

  await page.getByRole("combobox", { name: /^Show/ }).click();
  await page.getByRole("option", { name: "Global only", exact: true }).click();
  await expect(page.getByRole("cell", { name: "blocked.example", exact: true })).toBeVisible();
  await expect(page.getByRole("cell", { name: "import-1@example.org", exact: true })).toHaveCount(0);
  expect(requests.at(-1)?.has("brand_id")).toBe(false);
});
