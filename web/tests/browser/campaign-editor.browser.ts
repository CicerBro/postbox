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
    testSendFailure: true,
    testSendError: "global sending is disabled",
    testSends: [] as unknown[],
    testSendWait: Promise.resolve(),
    campaignWriteWait: Promise.resolve(),
    targetWriteWait: Promise.resolve(),
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
    else if (path === "/campaigns/7/test-send") {
      state.testSends.push(request.postDataJSON());
      expect(request.headers()["x-csrf-token"]).toBe("session-token");
      expect(new URL(request.url()).searchParams.get("brand_id")).toBe("1");
      await state.testSendWait;
      status = state.testSendFailure ? 422 : 200;
      body = state.testSendFailure ? { error: state.testSendError } : {};
    }
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
        await state.targetWriteWait;
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
      await state.campaignWriteWait;
      state.campaign = { ...state.campaign, ...value };
      body = { id: 7 };
    } else if (path === "/campaigns/7") body = [state.campaign];
    await route.fulfill({ status, json: body });
  });
  return state;
}

test("test-send feedback stays beside the button and recovers on retry", async ({ page, context }) => {
  const state = await fixture(context);
  await page.goto("/campaigns/7");
  const delivery = page.getByRole("group", { name: "Test email delivery" });
  await delivery.getByRole("textbox").fill("one@example.org, two@example.org");
  const button = delivery.getByRole("button", { name: "Send [Test] emails", exact: true });
  await expect(button).toBeEnabled();
  let release!: () => void;
  state.testSendWait = new Promise<void>((resolve) => { release = resolve; });
  await button.click();
  await expect(delivery.getByRole("status")).toHaveText("Sending test emails…");
  await expect(delivery.getByRole("button")).toBeDisabled();
  release();
  await expect(delivery.getByRole("alert")).toHaveText("global sending is disabled");
  await expect(delivery.getByRole("alert")).toHaveAttribute("data-slot", "alert");
  await expect(delivery.getByRole("alert")).toHaveClass(/bg-red-50/);
  await expect(delivery.getByRole("alert")).toBeInViewport();
  await expect(delivery.getByRole("status")).toHaveCount(0);
  await expect(button).toBeEnabled();

  state.testSendFailure = false;
  state.testSendWait = new Promise<void>((resolve) => { release = resolve; });
  await button.click();
  await expect(delivery.getByRole("alert")).toHaveCount(0);
  await expect(delivery.getByRole("status")).toHaveText("Sending test emails…");
  release();
  await expect(delivery.getByRole("status")).toHaveText("[Test] emails sent.");
  await expect(delivery.getByRole("status")).toBeInViewport();
  await expect(button).toBeEnabled();
  expect(state.testSends).toEqual([
    { emails: ["one@example.org", "two@example.org"] },
    { emails: ["one@example.org", "two@example.org"] },
  ]);
});

test("SES access errors appear in a red alert beside test send on mobile", async ({ page, context }, testInfo) => {
  const state = await fixture(context);
  state.testSendError = "operation error SESv2: SendEmail, https response error StatusCode: 403, RequestID: 00000000-0000-4000-8000-000000000000, api error AccessDeniedException: User 'arn:aws:iam::123456789012:user/postbox' is not authorized to perform 'ses:SendRawEmail' on resource 'arn:aws:ses:eu-central-1:123456789012:identity/noreply@example.org'";
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/campaigns/7");
  const delivery = page.getByRole("group", { name: "Test email delivery" });
  await delivery.getByRole("textbox").fill("one@example.org");
  const button = delivery.getByRole("button", { name: "Send [Test] emails", exact: true });
  await button.click();
  const alert = delivery.getByRole("alert");
  await expect(alert).toHaveText(state.testSendError);
  await expect(alert).toHaveClass(/bg-red-50/);
  await expect(page.getByRole("alert")).toHaveCount(1);
  await expect(button).toHaveAccessibleDescription(state.testSendError);
  await delivery.scrollIntoViewIfNeeded();
  await expect(alert).toBeInViewport();
  await expect(button).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await delivery.screenshot({ path: testInfo.outputPath("ses-test-send-error-mobile.png") });
});

test("preview shows merge-tag errors in the pane and recovers after correction", async ({ page, context }) => {
  const state = await fixture(context);
  const valid = '<p>Hi {{ name | "Subscriber" }}</p>';
  state.campaign.html = valid;
  let releasePreview: (() => void) | undefined;
  await context.route("**/api/v1/email-preview?*", async (route) => {
    const input = route.request().postDataJSON();
    const source = `${input.html} ${input.subject}`;
    if (source.includes("n1ame")) {
      await new Promise<void>((resolve) => { releasePreview = resolve; });
      await route.fulfill({ status: 400, json: { error: 'unknown merge field "n1ame"' } });
    } else if (input.html.endsWith("{{ name")) {
      await route.fulfill({ status: 400, json: { error: "unterminated merge tag" } });
    } else {
      await route.fulfill({ json: { subject: input.subject, html: "<p>Hi Subscriber</p>", text: "Hi Subscriber" } });
    }
  });
  await page.goto("/campaigns/7");
  const pane = page.getByRole("region", { name: "Email preview pane", exact: true });
  const raw = page.locator('.cm-content[aria-label="Full HTML source"]');
  const rendered = () => page.frameLocator('iframe[title="Email preview"]').getByText("Hi Subscriber", { exact: true });
  await expect(rendered()).toBeVisible();

  for (const field of ["html", "subject"]) {
    releasePreview = undefined;
    const editor = field === "html" ? raw : page.getByLabel("Subject", { exact: true });
    await editor.fill(valid.replace("name", "n1ame"));
    await expect(pane).toHaveAttribute("aria-busy", "true");
    await expect(pane.getByText("Rendering preview…", { exact: true })).toBeVisible();
    await expect(pane.locator("iframe")).toHaveCount(0);
    await expect.poll(() => !!releasePreview).toBe(true);
    releasePreview!();
    await expect(pane.getByRole("alert")).toHaveText('unknown merge field "n1ame"');
    await expect(pane).toHaveAttribute("aria-busy", "false");
    await expect(pane.locator("iframe")).toHaveCount(0);
    await expect(raw).toContainText(field === "html" ? "n1ame" : "name");
    await editor.fill(field === "html" ? valid : "Saved subject");
    await expect(rendered()).toBeVisible();
    await expect(pane.getByRole("alert")).toHaveCount(0);
  }

  await raw.fill("<p>Hi {{ name");
  await expect(pane.getByRole("alert")).toHaveText("unterminated merge tag");
  await raw.fill(valid);
  await expect(rendered()).toBeVisible();
  expect(state.campaignWrites).toEqual([]);
});

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

for (const pendingRequest of ["campaign", "audience"] as const) {
  test(`first save preserves edits made while the ${pendingRequest} request is pending`, async ({ page, context }) => {
    const state = await fixture(context);
    let release!: () => void;
    const wait = new Promise<void>((resolve) => { release = resolve; });
    if (pendingRequest === "campaign") state.campaignWriteWait = wait;
    else state.targetWriteWait = wait;

    await page.goto("/campaigns/new");
    const subject = page.getByLabel("Subject", { exact: true });
    const html = page.locator('.cm-content[aria-label="Full HTML source"]');
    await subject.fill("Submitted subject");
    await html.fill("<p>Submitted HTML</p>");
    await page.getByText("Plain-text override (optional)", { exact: true }).click();
    await page.getByLabel("Plain text", { exact: true }).fill("Submitted text");
    await page.getByRole("checkbox", { name: "Include Newsletter", exact: true }).check();
    await page.getByRole("button", { name: "Save draft", exact: true }).click();
    await expect.poll(() => pendingRequest === "campaign" ? state.campaignWrites.length : state.targetWrites.length).toBe(1);

    await subject.fill("Newer subject");
    await html.fill("<p>Newer HTML</p>");
    await page.getByLabel("Plain text", { exact: true }).fill("Newer text");
    await page.getByRole("checkbox", { name: "Inline CSS during rendering", exact: true }).uncheck();
    await page.getByRole("checkbox", { name: "Include Newsletter", exact: true }).uncheck();
    await page.getByRole("checkbox", { name: "Include Engaged subscribers", exact: true }).check();
    release();

    await expect(page).toHaveURL(/\/campaigns\/7$/);
    await expect(subject).toHaveValue("Newer subject");
    await expect(html).toHaveText("<p>Newer HTML</p>");
    await page.getByText("Plain-text override (optional)", { exact: true }).click();
    await expect(page.getByLabel("Plain text", { exact: true })).toHaveValue("Newer text");
    await expect(page.getByRole("checkbox", { name: "Inline CSS during rendering", exact: true })).not.toBeChecked();
    await expect(page.getByRole("checkbox", { name: "Include Newsletter", exact: true })).not.toBeChecked();
    await expect(page.getByRole("checkbox", { name: "Include Engaged subscribers", exact: true })).toBeChecked();
    await expect(page.getByText("Unsaved changes", { exact: true })).toBeVisible();
    await expect(page.getByText("Draft saved. Your newer edits are still unsaved.", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Run preflight", exact: true })).toBeDisabled();
    expect(state.campaignWrites).toEqual([{ subject: "Submitted subject", html: "<p>Submitted HTML</p>", text: "Submitted text", inline_css: true }]);
    expect(state.targetWrites).toEqual([[{ target_type: "list", target_id: 11, mode: "include" }]]);

    await page.getByRole("button", { name: "Save draft", exact: true }).click();
    await expect(page.getByText("Saved campaign", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Run preflight", exact: true })).toBeEnabled();
    expect(state.campaignWrites[1]).toEqual({ subject: "Newer subject", html: "<p>Newer HTML</p>", text: "Newer text", inline_css: false });
    expect(state.targetWrites[1]).toEqual([{ target_type: "segment", target_id: 21, mode: "include" }]);
  });
}

test("saved audiences stay visible when their list or segment is unavailable", async ({ page, context }) => {
  const state = await fixture(context);
  state.targets = [
    { target_type: "list", target_id: 10, mode: "include" },
    { target_type: "segment", target_id: 20, mode: "exclude" },
  ];
  await page.goto("/campaigns/7");
  await expect(page.getByText("1 included audience · 1 exclusion", { exact: true })).toBeVisible();
  await expect(page.getByRole("checkbox", { name: "Include Newsletter", exact: true })).not.toBeChecked();
  await expect(page.getByText("Unavailable list (ID 10)", { exact: true })).toBeVisible();
  await expect(page.getByText("Unavailable segment (ID 20)", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Remove unavailable list 10", exact: true }).click();
  await expect(page.getByText("0 included audiences · 1 exclusion", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Remove unavailable segment 20", exact: true }).click();
  await expect(page.getByText("0 included audiences · 0 exclusions", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Save draft", exact: true }).click();
  await expect.poll(() => state.targetWrites).toEqual([[]]);
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

test("campaign action previews open safely with the selected subscriber", async ({ page, context }) => {
  const state = await fixture(context);
  state.targets = [{ target_type: "list", target_id: 11, mode: "include" }];
  const requests: Record<string, unknown>[] = [];
  await context.route("**/api/v1/email-preview/recipients*", (route) => route.fulfill({ json: [{ id: 42, name: "Selected", email: "selected@example.org" }] }));
  await context.route("**/api/v1/email-preview?*", (route) => {
    requests.push(route.request().postDataJSON());
    return route.fulfill({ json: { subject: "Saved subject", text: "", html: `<p>Selected</p><a href="http://127.0.0.1:5174/preview/campaign/opaque">View in browser</a><a href="http://127.0.0.1:5174/preview/unsubscribe/opaque">Unsubscribe</a>` } });
  });
  await context.route("**/preview/campaign/opaque", (route) => route.fulfill({ contentType: "text/html", body: "<p>Saved campaign preview for Selected</p>" }));
  await context.route("**/preview/unsubscribe/opaque", (route) => route.fulfill({ contentType: "text/html", body: "<p>Unsubscribe preview only</p>" }));
  await page.goto("/campaigns/7");
  const frame = page.frameLocator('iframe[title="Email preview"]');
  await expect(frame.getByText("Selected", { exact: true })).toBeVisible();
  expect(requests.some((r) => r.subscriber_id === 42 && r.campaign_id === 7)).toBe(true);
  for (const label of ["View in browser", "Unsubscribe"]) {
    const link = frame.getByRole("link", { name: label });
    await expect(link).toHaveAttribute("target", "_blank");
    await expect(link).toHaveAttribute("rel", "noopener noreferrer");
    const popup = context.waitForEvent("page");
    await link.click();
    const opened = await popup;
    await expect(opened.locator("p")).toBeVisible();
    expect(new URL(opened.url()).pathname).toMatch(/^\/preview\/(campaign|unsubscribe)\/opaque$/);
    await opened.close();
  }
});

test("editor view switches preserve unsaved HTML and undo while merge tags copy without inserting", async ({ page, context }) => {
  const state = await fixture(context);
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await context.route("**/api/v1/custom-fields*", (route) => route.fulfill({ json: [{ id: 1, key: "company", label: "Company" }] }));
  await context.route("**/api/v1/email-preview?*", (route) => route.fulfill({ json: { subject: "Saved subject", html: "<p>Rendered HTML</p>", text: "Rendered HTML" } }));
  await page.setViewportSize({ width: 1800, height: 1200 });
  await page.goto("/campaigns/7");
  const raw = page.locator('.cm-content[aria-label="Full HTML source"]');
  const pane = page.getByRole("region", { name: "Email preview pane", exact: true });
  const frame = page.locator('iframe[title="Email preview"]');
  await expect(raw).toHaveText("<p>Saved HTML</p>");
  await expect(frame).toBeVisible();
  expect((await page.locator(".email-editor .cm-editor").boundingBox())?.height).toBe(960);
  expect((await frame.boundingBox())?.height).toBe(960);
  const sourceBox = (await raw.boundingBox())!;
  expect((await pane.boundingBox())!.x).toBeGreaterThan(sourceBox.x + sourceBox.width);

  await raw.click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.insertText(" unsaved");
  await page.getByRole("button", { name: "Show Preview", exact: true }).click();
  await expect(raw).toBeHidden();
  await expect(frame).toBeVisible();
  await page.getByRole("button", { name: "Show HTML", exact: true }).click();
  await expect(raw).toContainText("unsaved");
  await expect(pane).toBeHidden();
  await raw.click();
  await page.keyboard.press("ControlOrMeta+z");
  await expect(raw).toHaveText("<p>Saved HTML</p>");
  await page.getByRole("button", { name: "Show both side by side", exact: true }).click();
  await expect(raw).toBeVisible();
  await expect(frame).toBeVisible();

  for (const [label, key] of [["name", "name"], ["Company (company)", "company"]]) {
    await page.getByRole("button", { name: "Merge tags", exact: true }).click();
    await page.getByRole("menuitem").filter({ hasText: label }).first().click();
    await expect(page.getByRole("status").filter({ hasText: `Copied {{ ${key} }}` })).toHaveText(`Copied {{ ${key} }}`);
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(`{{ ${key} }}`);
    await expect(raw).toHaveText("<p>Saved HTML</p>");
  }
  expect(state.campaignWrites).toEqual([]);
});

test("preview toolbar stays above the pane and fits desktop and mobile screens", async ({ page, context }) => {
  const state = await fixture(context);
  state.targets = [{ target_type: "list", target_id: 11, mode: "include" }];
  await context.route("**/api/v1/email-preview/recipients*", (route) => {
    const after = route.request().postDataJSON().after;
    return route.fulfill({ json: after ? [{ id: 43, name: "Next", email: "next@example.org" }] : [
      { id: 42, name: "Selected", email: "a-very-long-subscriber-email-address@example.org" },
      { id: 43, name: "Next", email: "next@example.org" },
    ] });
  });
  await context.route("**/api/v1/email-preview?*", (route) => route.fulfill({ json: { subject: "Saved subject", html: "<p>Rendered HTML</p>", text: "Rendered HTML" } }));
  await page.setViewportSize({ width: 1800, height: 1200 });
  await page.goto("/campaigns/7");
  const frame = page.locator('iframe[title="Email preview"]');
  const controls = page.getByRole("group", { name: "Preview controls", exact: true });
  await expect(frame).toBeVisible();
  const pane = page.getByRole("region", { name: "Email preview pane", exact: true });
  const controlsBox = (await controls.boundingBox())!;
  const paneBox = (await pane.boundingBox())!;
  expect(controlsBox.y + controlsBox.height).toBeLessThan(paneBox.y);
  expect(controlsBox.x + controlsBox.width).toBeCloseTo(paneBox.x + paneBox.width, 0);
  const toggle = page.getByRole("button", { name: "Switch to mobile preview", exact: true });
  expect((await toggle.boundingBox())!.x).toBeLessThan((await page.getByRole("button", { name: "Previous subscriber" }).boundingBox())!.x);
  await toggle.click();
  await expect(page.getByRole("button", { name: "Switch to desktop preview" })).toHaveAttribute("aria-pressed", "true");
  expect((await frame.boundingBox())?.width).toBe(402);
  await page.getByRole("button", { name: "Next subscriber" }).click();
  await expect(controls.getByText("next@example.org", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Previous subscriber" }).click();
  await expect(controls.getByText("a-very-long-subscriber-email-address@example.org", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Switch to desktop preview" }).click();
  await page.evaluate(() => document.documentElement.classList.add("dark"));
  await page.locator(".email-editor").screenshot({ path: "test-results/editor-desktop.png" });

  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(controls).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    const editorBox = (await page.locator(".email-editor").boundingBox())!;
    for (const button of await page.locator(".email-editor-button-group button").all()) {
      const buttonBox = (await button.boundingBox())!;
      expect(buttonBox.x).toBeGreaterThanOrEqual(editorBox.x);
      expect(buttonBox.x + buttonBox.width).toBeLessThanOrEqual(editorBox.x + editorBox.width);
    }
    await page.getByRole("button", { name: "Show Preview", exact: true }).click();
    await expect(frame).toBeVisible();
    await expect(page.locator('.cm-content[aria-label="Full HTML source"]')).toBeHidden();
    expect((await frame.boundingBox())!.width).toBeLessThanOrEqual(width);
    await page.getByRole("button", { name: "Merge tags", exact: true }).click();
    const menu = page.getByRole("menu");
    await expect(menu.getByRole("menuitem")).toHaveText([
      "name{{ name }}", 'name (fallback: there){{ name | "there" }}',
      "email{{ email }}", "unsubscribe{{ unsubscribe }}", "webversion{{ webversion }}",
      "currentdaydate{{ currentdaydate }}", "currentmonth{{ currentmonth }}", "currentyear{{ currentyear }}",
    ]);
    const menuBox = (await menu.boundingBox())!;
    expect(menuBox.x).toBeGreaterThanOrEqual(0);
    expect(menuBox.x + menuBox.width).toBeLessThanOrEqual(width);
    await page.keyboard.press("Escape");
  }
  await page.locator(".email-editor").screenshot({ path: "test-results/editor-mobile.png" });
});
