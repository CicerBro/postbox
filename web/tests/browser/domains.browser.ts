import { expect, test, type BrowserContext } from "@playwright/test";

async function domainFixture(context: BrowserContext, admin = true) {
  const state = { created: false, verified: false, mailFrom: "", behavior: "USE_DEFAULT_VALUE", failure: 0, writes: 0, secondBrandReads: 0 };
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace("/api/v1", "");
    const brandId = Number(url.searchParams.get("brand_id") || "1");
    let status = 200;
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin, csrf_token: "session-token" };
    else if (path === "/brands") body = [
      { id: 1, name: "Acme", from_email: "sender@example.org" },
      { id: 2, name: "Other", from_email: "sender@other.org" },
    ];
    else if (path === "/brands/1" || path === "/brands/2") body = [{ name: brandId === 1 ? "Acme" : "Other", from_email: brandId === 1 ? "sender@example.org" : "sender@other.org" }];
    else if (path === "/aws-settings") body = { has_credentials: true, region: brandId === 1 ? "eu-west-1" : "us-east-1", updated_at: 1 };
    else if (path === "/aws-settings/identity/mail-from") {
      expect(request.method()).toBe("PATCH");
      expect(request.headers()["x-csrf-token"]).toBe("session-token");
      expect(brandId).toBe(1);
      const input = request.postDataJSON();
      expect(input.identity).toBe("example.org");
      state.mailFrom = input.mail_from_domain;
      state.behavior = input.mail_from_behavior;
      state.writes++;
      body = { configured: !!state.mailFrom };
    } else if (path === "/aws-settings/identity") {
      if (request.method() === "POST") {
        expect(request.headers()["x-csrf-token"]).toBe("session-token");
        expect(request.postDataJSON()).toEqual({ identity: "example.org" });
        state.created = true;
        state.writes++;
      }
      if (brandId === 2) state.secondBrandReads++;
      if (state.failure) {
        status = state.failure;
        body = { error: "SES identity request failed. Check the brand credentials, region, and SES identity permissions." };
      } else if (!state.created || brandId === 2) {
        status = 404;
        body = { error: "no identity" };
      } else {
        body = {
          identity: "example.org", identity_type: "DOMAIN", region: "eu-west-1", custom_endpoint: false,
          sending_enabled: state.verified, verification_status: state.verified ? "SUCCESS" : "PENDING",
          dkim_status: state.verified ? "SUCCESS" : "PENDING", dkim_signing_enabled: true,
          dkim_origin: "AWS_SES", dkim_hosted_zone: "dkim.eu-west-1.amazonses.com",
          mail_from_domain: state.mailFrom, mail_from_status: state.mailFrom ? "PENDING" : "",
          mail_from_behavior: state.behavior, checked_at: Date.now(),
          dns_records: [
            ...["one", "two", "three"].map((token) => ({ type: "CNAME", name: `${token}._domainkey.example.org`, value: `${token}.dkim.eu-west-1.amazonses.com` })),
            ...(state.mailFrom ? [
              { type: "MX", name: state.mailFrom, value: "feedback-smtp.eu-west-1.amazonses.com", priority: 10 },
              { type: "TXT", name: state.mailFrom, value: "v=spf1 include:amazonses.com ~all" },
            ] : []),
          ],
        };
      }
    } else if (path === "/sending") body = { enabled: false };
    await route.fulfill({ status, json: body });
  });
  return state;
}

test("Domains creates the identity, copies AWS records, refreshes verification and configures MAIL FROM", async ({ page, context }, testInfo) => {
  const state = await domainFixture(context);
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/domains");
  await expect(page.getByLabel("Domain to verify")).toHaveValue("example.org");
  await page.getByRole("button", { name: "Verify domain", exact: true }).click();
  await expect(page.getByText("one.dkim.eu-west-1.amazonses.com", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Save MAIL FROM settings", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Copy CNAME value for one._domainkey.example.org", exact: true }).click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe("one.dkim.eu-west-1.amazonses.com");
  state.verified = true;
  await page.getByRole("button", { name: "Refresh status", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save MAIL FROM settings", exact: true })).toBeEnabled();
  await expect(page.getByText("Verified", { exact: true })).toHaveCount(2);
  await page.getByLabel("MAIL FROM subdomain", { exact: true }).fill("bounce.example.org");
  await page.getByRole("combobox", { name: "If the MX record cannot be verified" }).click();
  await page.getByRole("option", { name: "Reject sending", exact: true }).click();
  await page.getByRole("button", { name: "Save MAIL FROM settings", exact: true }).click();
  await expect(page.getByText("feedback-smtp.eu-west-1.amazonses.com", { exact: true })).toBeVisible();
  await expect(page.getByText("v=spf1 include:amazonses.com ~all", { exact: true })).toBeVisible();
  await expect(page.getByText("MAIL FROM · MX · Priority 10", { exact: true })).toBeVisible();
  expect(state.behavior).toBe("REJECT_MESSAGE");
  await page.screenshot({ path: testInfo.outputPath("domains-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: testInfo.outputPath("domains-mobile.png"), fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByLabel("MAIL FROM subdomain", { exact: true }).fill("");
  await page.getByRole("button", { name: "Save MAIL FROM settings", exact: true }).click();
  await expect(page.getByText("Custom MAIL FROM disabled. SES will use its default MAIL FROM domain.", { exact: true })).toBeVisible();
  await expect(page.getByText("feedback-smtp.eu-west-1.amazonses.com", { exact: true })).toHaveCount(0);
  expect(state.writes).toBe(3);
});

test("Domains keeps AWS failures local and permits a status retry", async ({ page, context }) => {
  const state = await domainFixture(context);
  state.failure = 502;
  await page.goto("/domains");
  await expect(page.getByRole("alert")).toContainText("SES identity permissions");
  await expect(page.getByRole("button", { name: "Verify domain", exact: true })).toHaveCount(0);
  state.failure = 0;
  state.created = true;
  state.verified = true;
  await page.getByRole("button", { name: "Refresh status", exact: true }).click();
  await expect(page.getByText("one.dkim.eu-west-1.amazonses.com", { exact: true })).toBeVisible();
  state.failure = 502;
  await page.getByRole("button", { name: "Refresh status", exact: true }).click();
  await expect(page.getByText("The details below are from the last successful check. Refresh status to try again.", { exact: true })).toBeVisible();
  state.failure = 403;
  await page.getByRole("button", { name: "Refresh status", exact: true }).click();
  await expect(page.getByText("one.dkim.eu-west-1.amazonses.com", { exact: true })).toHaveCount(0);
  state.failure = 0;
  state.created = false;
  await page.getByRole("button", { name: "Refresh status", exact: true }).click();
  await expect(page.getByRole("button", { name: "Verify domain", exact: true })).toBeEnabled();
  expect(state.writes).toBe(0);
});

test("brand readers view records without AWS mutation controls", async ({ page, context }) => {
  const state = await domainFixture(context, false);
  state.created = true;
  state.verified = true;
  await page.goto("/domains");
  await expect(page.getByText("one.dkim.eu-west-1.amazonses.com", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Verify domain", exact: true })).toHaveCount(0);
  await expect(page.getByLabel("MAIL FROM subdomain", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Refresh status", exact: true }).click();
  expect(state.writes).toBe(0);
});

test("switching brands clears the previous identity and records on narrow screens", async ({ page, context }, testInfo) => {
  const state = await domainFixture(context);
  state.created = true;
  state.verified = true;
  await page.goto("/domains");
  await expect(page.getByText("one.dkim.eu-west-1.amazonses.com", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Switch brand", exact: true }).click();
  await page.getByRole("menuitem", { name: /Other/ }).click();
  await expect(page.getByLabel("Domain to verify")).toHaveValue("other.org");
  await expect(page.getByText("one.dkim.eu-west-1.amazonses.com", { exact: true })).toHaveCount(0);
  await expect(page.getByText("AWS region: us-east-1.", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Verify domain", exact: true })).toBeVisible();
  expect(state.secondBrandReads).toBeGreaterThan(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: testInfo.outputPath("domains-mobile-missing.png"), fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
