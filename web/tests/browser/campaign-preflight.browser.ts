import { test, expect, type BrowserContext } from "@playwright/test";
import type { Checklist } from "../../src/pages/campaign-checklist";

function checklist(): Checklist {
  const links = ["", "/premium", "/faq", "/privacy", "/terms"].map((path) => ({
    url: `https://example.org${path}`,
    status: "broken",
    http_status: 403,
    message: "HTTP 403",
    personalized: false,
  }));
  return {
    recipients: 273793,
    message_bytes: 16077,
    upload_bytes: 4401690061,
    duration_seconds: 1608,
    quota: { MaxSendRate: 170, Max24HourSend: 350000, SentLast24Hours: 0 },
    fits_daily_quota: true,
    checks: {
      ready: true,
      errors: [],
      warnings: links.map((link) => ({
        code: "link",
        message: `${link.url}: HTTP 403`,
      })),
      links,
      html_bytes: 12288,
      html_clipping: false,
      suppressed: 32,
      inactive: 0,
      skipped: 32,
      remaining_daily_quota: 350000,
    },
  };
}

async function fixture(context: BrowserContext, result: Checklist) {
  await context.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    let body: unknown = [];
    if (path === "/auth/session")
      body = { user_id: 1, admin: true, csrf_token: "csrf" };
    else if (path === "/brands")
      body = [{ id: 1, name: "Acme", from_email: "hello@example.org" }];
    else if (path === "/sending") body = { enabled: true };
    else if (path === "/campaigns/7")
      body = [
        {
          id: 7,
          subject: "Your latest newsletter",
          html: "<p>Hello</p>",
          text: "",
          inline_css: true,
          status: "draft",
          created_by: 1,
        },
      ];
    else if (path === "/campaigns/7/checklist") body = result;
    else if (path === "/email-preview")
      body = {
        html: "<p>Hello</p>",
        subject: "Your latest newsletter",
        text: "Hello",
      };
    await route.fulfill({ json: body });
  });
}

for (const mobile of [false, true]) {
  test(`preflight explains repeated 403 failures and allows sending on ${mobile ? "mobile" : "desktop"}`, async ({
    page,
    context,
  }, testInfo) => {
    await page.setViewportSize(
      mobile ? { width: 390, height: 844 } : { width: 1440, height: 1100 },
    );
    await fixture(context, checklist());
    await page.goto("/campaigns/7");
    await page
      .getByRole("button", { name: "Run preflight", exact: true })
      .click();
    const results = page.getByRole("region", { name: "Preflight results" });
    await expect(
      results.getByRole("heading", { name: "Ready to send" }),
    ).toBeVisible();
    await expect(results).not.toContainText("Sending blocked");
    await expect(results).toContainText("do not block sending or scheduling");
    await expect(results).toContainText("Cloudflare or other bot protection");
    await expect(results).toContainText("5 links could not be verified");
    await expect(results).toContainText(
      "The website refused the automated check (HTTP 403).",
    );
    await expect(results).toContainText(
      "32 contacts excluded: 32 suppressed and 0 inactive.",
    );
    await expect(results).toContainText("27 minutes");
    await expect(results).toContainText("Within SES daily quota");
    await expect(
      results.getByText("https://example.org/terms", { exact: true }),
    ).not.toBeVisible();
    await results.scrollIntoViewIfNeeded();
    await results.screenshot({
      path: testInfo.outputPath(
        `preflight-${mobile ? "mobile" : "desktop"}.png`,
      ),
    });
    await results.getByText("Link results", { exact: false }).click();
    await expect(
      results.getByText("https://example.org/terms", { exact: true }),
    ).toBeVisible();
    await expect(
      results.getByText("Unverified · HTTP 403", { exact: true }),
    ).toHaveCount(5);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page
      .getByRole("button", { name: "Start sending", exact: true })
      .click();
    await expect(page.getByRole("dialog", { name: "Start campaign?" })).toBeVisible();
    await page.getByRole("button", { name: "Keep reviewing", exact: true }).click();
    await page.locator('input[type="datetime-local"]').fill("2099-01-01T12:00");
    await page.getByRole("button", { name: "Schedule", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "Schedule campaign?" })).toBeVisible();
  });
}

test("ready preflight keeps nonblocking notes and permits confirmation", async ({
  page,
  context,
}) => {
  const result = checklist();
  result.checks.ready = true;
  result.checks.errors = [];
  result.checks.links = [
    {
      url: "https://example.org",
      status: "warning",
      message: "Redirect was not followed; verify the destination",
      http_status: 302,
      personalized: true,
    },
  ];
  result.checks.warnings = [
    {
      code: "link",
      message:
        "https://example.org: Redirect was not followed; verify the destination",
    },
    {
      code: "personalized_link",
      message:
        "https://example.org: checked with sample values; verify with a real recipient",
    },
    {
      code: "missing_merge_value",
      message: "12 recipients have no first name",
      tag: "first_name",
    },
  ];
  result.checks.user_remaining_daily_quota = 300000;
  await fixture(context, result);
  await page.goto("/campaigns/7");
  await page
    .getByRole("button", { name: "Run preflight", exact: true })
    .click();
  const results = page.getByRole("region", { name: "Preflight results" });
  await expect(
    results.getByRole("heading", { name: "Ready to send" }),
  ).toBeVisible();
  await expect(results).toContainText("Worth reviewing");
  await expect(results).toContainText("12 recipients have no first name");
  await expect(results).toContainText("1 link needs a manual review");
  await expect(results).toContainText("Campaign owner’s daily budget:");
  await page
    .getByRole("button", { name: "Start sending", exact: true })
    .click();
  await expect(
    page.getByRole("dialog", { name: "Start campaign?" }),
  ).toBeVisible();
});

test("quota failures and clipping stay distinct from passing checks", async ({
  page,
  context,
}) => {
  const result = checklist();
  result.checks.ready = false;
  result.checks.warnings = [];
  result.fits_daily_quota = false;
  result.checks.links = [];
  result.checks.errors = [
    {
      code: "daily_quota",
      message: "Campaign exceeds the remaining SES daily quota",
    },
  ];
  result.checks.remaining_daily_quota = 12000;
  result.checks.html_bytes = 110000;
  result.checks.html_clipping = true;
  await fixture(context, result);
  await page.goto("/campaigns/7");
  await page
    .getByRole("button", { name: "Run preflight", exact: true })
    .click();
  const results = page.getByRole("region", { name: "Preflight results" });
  await expect(results).toContainText("Exceeds SES daily quota");
  await expect(results).toContainText("Gmail may clip this email");
  await expect(results).not.toContainText("Within SES daily quota");
  await expect(results).not.toContainText("Below Gmail’s clipping threshold");
  await expect(results.locator("summary")).toHaveCount(0);
});

test("template errors do not present unavailable estimates as passing checks", async ({
  page,
  context,
}) => {
  const result = checklist();
  result.checks.ready = false;
  result.checks.warnings = [];
  result.checks.errors = [
    {
      code: "merge_tag",
      message: 'unknown merge field "first_nmae"',
      tag: "first_nmae",
    },
  ];
  result.checks.links = [];
  result.recipients = 0;
  await fixture(context, result);
  await page.goto("/campaigns/7");
  await page
    .getByRole("button", { name: "Run preflight", exact: true })
    .click();
  const results = page.getByRole("region", { name: "Preflight results" });
  await expect(results).toContainText('unknown merge field "first_nmae"');
  await expect(results).toContainText("estimates will be available after");
  await expect(results).not.toContainText("Within SES daily quota");
  await expect(results).not.toContainText("Below Gmail’s clipping threshold");
});
