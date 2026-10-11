import { test, expect, type BrowserContext } from "@playwright/test";

type Target = {
  target_type: "list" | "segment";
  target_id: number;
  mode: "include" | "exclude";
};
async function fixture(context: BrowserContext) {
  const state = {
    campaign: {
      id: 7,
      subject: "Saved subject",
      html: "<p>Saved HTML</p>",
      text: "",
      inline_css: true,
      status: "draft",
      created_by: 1,
    },
    targets: [] as Target[],
    campaignWrites: [] as Record<string, unknown>[],
    targetWrites: [] as Target[][],
    checklistReads: 0,
    targetFailure: false,
  };
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request(),
      path = new URL(request.url()).pathname.replace("/api/v1", "");
    let status = 200,
      body: unknown = [];
    if (path === "/auth/session")
      body = { user_id: 1, admin: true, csrf_token: "session-token" };
    else if (path === "/brands")
      body = [{ id: 1, name: "Acme", from_email: "a@example.com" }];
    else if (path === "/sending") body = { enabled: false };
    else if (path === "/lists") body = [{ id: 11, name: "Newsletter" }];
    else if (path === "/segments")
      body = [{ id: 21, name: "Engaged subscribers" }];
    else if (path === "/templates")
      body = [
        {
          id: 31,
          name: "Welcome template",
          html: "<p>Welcome from our template</p>",
        },
      ];
    else if (path === "/campaigns/7/checklist") {
      state.checklistReads++;
      body = {
        recipients: 2,
        message_bytes: 100,
        upload_bytes: 200,
        duration_seconds: 1,
        quota: {},
        fits_daily_quota: true,
        checks: {
          ready: true,
          errors: [],
          warnings: [],
          links: [],
          html_bytes: 100,
          html_clipping: false,
          suppressed: 0,
          inactive: 0,
          skipped: 0,
          remaining_daily_quota: 50,
        },
      };
    } else if (path === "/campaigns/7/targets") {
      if (request.method() === "PUT") {
        state.targetWrites.push(request.postDataJSON());
        if (state.targetFailure) {
          status = 422;
          body = { error: "Audience save failed" };
        } else {
          state.targets = request.postDataJSON();
          body = {};
        }
      } else body = state.targets;
    } else if (
      (path === "/campaigns" || path === "/campaigns/7") &&
      request.method() !== "GET"
    ) {
      const value = request.postDataJSON();
      state.campaignWrites.push(value);
      state.campaign = { ...state.campaign, ...value };
      body = { id: 7 };
    } else if (path === "/campaigns/7") body = [state.campaign];
    await route.fulfill({ status, json: body });
  });
  return state;
}

test("visual editor frames stay sandboxed and HTML changes require review", async ({ page, context }) => {
  const state = await fixture(context);
  const source = '<!doctype html><html><head><meta name="upgrade-test" content="preserved"><!--[if mso]><style>p { color: red; }</style><![endif]--></head><body><p>Saved HTML</p></body></html>';
  state.campaign.html = source;
  await page.goto("/campaigns/7");
  const raw = page.locator('.cm-content[aria-label="Full HTML source"]');
  await expect(raw).toHaveText(source);

  for (let cycle = 0; cycle < 2; cycle++) {
    await page.getByRole("button", { name: "Visual editor", exact: true }).click();
    await expect(page.getByRole("button", { name: "Review proposed HTML", exact: true })).toBeEnabled();
    await expect(page.frameLocator("iframe.gjs-frame").getByText("Saved HTML", { exact: true })).toBeVisible();
    const frames = await page.locator("iframe").evaluateAll((elements) => elements.map((element) => element.getAttribute("sandbox")));
    expect(frames.length).toBeGreaterThan(0);
    for (const sandbox of frames) {
      expect(sandbox).not.toBeNull();
      expect(sandbox?.split(/\s+/)).not.toContain("allow-scripts");
    }
    await page.getByRole("button", { name: "Discard visual changes", exact: true }).click();
    await expect(page.locator("iframe.gjs-frame")).toHaveCount(0);
    await expect(raw).toHaveText(source);
  }

  await page.getByRole("button", { name: "Visual editor", exact: true }).click();
  await expect(page.frameLocator("iframe.gjs-frame").getByText("Saved HTML", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Review proposed HTML", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  expect(state.campaignWrites).toEqual([]);
  await page.getByRole("button", { name: "Apply proposed HTML", exact: true }).click();
  await expect(raw).toContainText('name="upgrade-test"');
  await expect(raw).toContainText("<!--[if mso]>");
  await expect(raw).toContainText("Saved HTML");
  await page.getByRole("button", { name: "Save draft", exact: true }).click();
  await expect.poll(() => state.campaignWrites.length).toBe(1);
  expect(state.campaignWrites[0].html).toContain('name="upgrade-test"');
  expect(state.campaignWrites[0].html).toContain("<!--[if mso]>");
  expect(state.campaignWrites[0].html).toContain("Saved HTML");
});

test("template, text and mutually exclusive audiences survive draft saving", async ({
  page,
  context,
}) => {
  const state = await fixture(context);
  await page.goto("/campaigns/new");
  await page.getByLabel("Subject", { exact: true }).fill("Welcome campaign");
  await page.getByLabel("Start from a template").click();
  await page
    .getByRole("option", { name: "Welcome template", exact: true })
    .click();
  await page
    .getByText("Plain-text override (optional)", { exact: true })
    .click();
  await page.getByLabel("Plain text", { exact: true }).fill("Plain welcome");
  await page
    .getByRole("checkbox", { name: "Include Newsletter", exact: true })
    .check();
  await page
    .getByRole("checkbox", { name: "Exclude Newsletter", exact: true })
    .check();
  await expect(
    page.getByRole("checkbox", { name: "Include Newsletter", exact: true }),
  ).not.toBeChecked();
  await page
    .getByRole("checkbox", { name: "Include Engaged subscribers", exact: true })
    .check();
  await page.getByRole("button", { name: "Save draft", exact: true }).click();
  await expect(page).toHaveURL(/\/campaigns\/7$/);
  await expect(page.getByText("Saved campaign", { exact: true })).toBeVisible();
  expect(state.campaignWrites).toEqual([
    {
      subject: "Welcome campaign",
      html: "<p>Welcome from our template</p>",
      text: "Plain welcome",
      inline_css: true,
    },
  ]);
  expect(state.targetWrites).toEqual([
    [
      { target_type: "list", target_id: 11, mode: "exclude" },
      { target_type: "segment", target_id: 21, mode: "include" },
    ],
  ]);
  await expect(
    page.getByRole("checkbox", { name: "Exclude Newsletter", exact: true }),
  ).toBeChecked();
  await expect(
    page.getByRole("checkbox", {
      name: "Include Engaged subscribers",
      exact: true,
    }),
  ).toBeChecked();
});

test("polling preserves unsaved edits and preflight becomes available only after audience save succeeds", async ({
  page,
  context,
}) => {
  const state = await fixture(context);
  await page.clock.install();
  await page.goto("/campaigns/7");
  await expect(page.getByLabel("Subject", { exact: true })).toHaveValue(
    "Saved subject",
  );
  await page.getByLabel("Subject", { exact: true }).fill("Local edits");
  await page
    .getByRole("checkbox", { name: "Include Newsletter", exact: true })
    .check();
  await expect(
    page.getByRole("button", { name: "Run preflight", exact: true }),
  ).toBeDisabled();
  await page.clock.fastForward(10_000);
  await expect(page.getByLabel("Subject", { exact: true })).toHaveValue(
    "Local edits",
  );
  await expect(
    page.getByRole("checkbox", { name: "Include Newsletter", exact: true }),
  ).toBeChecked();
  state.targetFailure = true;
  await page.getByRole("button", { name: "Save draft", exact: true }).click();
  await expect(
    page.getByText("Audience save failed", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Run preflight", exact: true }),
  ).toBeDisabled();
  expect(state.checklistReads).toBe(0);
  state.targetFailure = false;
  await page.getByRole("button", { name: "Save draft", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Run preflight", exact: true }),
  ).toBeEnabled();
  await page
    .getByRole("button", { name: "Run preflight", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Ready to send", exact: true }),
  ).toBeVisible();
  expect(state.checklistReads).toBe(1);
});
