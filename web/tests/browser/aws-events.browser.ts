import { expect, test, type BrowserContext } from "@playwright/test";

async function fixture(context: BrowserContext, admin = true) {
  const state = { setup: 0, reads: 0, confirmed: false, denied: false, updated: 1, hasToken: false, patches: [] as Record<string, unknown>[] };
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1", "");
    let status = 200;
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin, csrf_token: "session-token" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "sender@example.org" }];
    else if (path === "/auth/security") body = {};
    else if (path === "/aws-settings") {
      if (request.method() === "PATCH") {
        state.updated++;
        const patch = request.postDataJSON();
        state.patches.push(patch);
        if (patch.clear_session_token) state.hasToken = false;
        else if (patch.session_token) state.hasToken = true;
      }
      body = { mode: "static", region: "eu-west-1", has_credentials: true, configured: true, has_session_token: state.hasToken, sns_topic_arn: state.setup ? "arn:aws:sns:eu-west-1:123456789012:postbox-test-events" : "", updated_at: state.updated };
    } else if (path === "/aws-settings/setup-events" || path === "/aws-settings/event-status") {
      if (path.endsWith("setup-events")) {
        expect(request.method()).toBe("POST");
        expect(request.headers()["x-csrf-token"]).toBe("session-token");
        expect(new URL(request.url()).searchParams.get("brand_id")).toBe("1");
        state.setup++;
      } else { expect(request.method()).toBe("GET"); state.reads++; }
      if (state.denied) { status = 502; body = { error: "SNS subscriptions could not be read. Grant sns:ListSubscriptionsByTopic on this topic." }; }
      else body = { configured: true, configuration_set: "postbox-test-brand-1", sns_topic_arn: "arn:aws:sns:eu-west-1:123456789012:postbox-test-events", endpoint: "https://postbox.example.org/events/sns", subscription_status: state.confirmed ? "confirmed" : "pending_confirmation", checked_at: Date.now() };
    }
    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  });
  return state;
}

test("admin provisions SES and SNS and refreshes confirmation", async ({ page, context }, testInfo) => {
  const state = await fixture(context);
  await page.goto("/settings?brand_id=1");
  await page.getByRole("button", { name: "Amazon SES", exact: true }).click();
  const setup = page.getByRole("button", { name: "Set up SES and SNS", exact: true });
  await expect(page.getByRole("combobox", { name: "AWS region", exact: true })).toContainText("eu-west-1");
  await expect(setup).toBeEnabled();
  await setup.click();
  await expect(page.getByText("Waiting for SNS subscription confirmation.", { exact: false })).toBeVisible();
  await expect(page.getByText("https://postbox.example.org/events/sns", { exact: true })).toBeVisible();
  await expect(page.getByLabel("SNS topic ARN", { exact: true })).toHaveValue("arn:aws:sns:eu-west-1:123456789012:postbox-test-events");
  expect(state.setup).toBe(1);
  state.confirmed = true;
  await page.getByRole("button", { name: "Refresh event status" }).click();
  await expect(page.getByText("Event delivery is ready. SNS subscription confirmed.")).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("sns-ready.png"), fullPage: true });
  state.denied = true;
  await page.getByRole("button", { name: "Refresh event status" }).click();
  await expect(page.getByRole("alert")).toContainText("sns:ListSubscriptionsByTopic");
  await expect(page.getByText("Event delivery is ready. SNS subscription confirmed.")).toHaveCount(0);
  await page.getByLabel("SNS topic ARN", { exact: true }).fill("arn:aws:sns:eu-west-1:123456789012:other");
  await expect(setup).toBeDisabled();
  await expect(page.getByRole("button", { name: "Refresh event status" })).toBeDisabled();
});

test("saved credentials show masked placeholders without submitting them and keep the topic visible", async ({ page, context }) => {
  const state = await fixture(context);
  state.hasToken = true;
  state.setup = 1;
  await page.goto("/settings?brand_id=1");
  await page.getByRole("button", { name: "Amazon SES", exact: true }).click();
  const access = page.getByLabel("Access key ID", { exact: true });
  const secret = page.getByLabel("Secret access key", { exact: true });
  const token = page.getByLabel("Session token (optional)", { exact: true });
  const topic = "arn:aws:sns:eu-west-1:123456789012:postbox-test-events";
  for (const input of [access, secret, token]) {
    await expect(input).toHaveValue("");
    await expect(input).toHaveAttribute("placeholder", "••••••••");
  }
  await expect(page.getByLabel("SNS topic ARN", { exact: true })).toHaveValue(topic);
  const save = page.getByRole("button", { name: "Save Amazon SES settings", exact: true });
  await save.click();
  await expect(save).toBeEnabled();
  expect(state.patches).toEqual([{ mode: "static", region: "eu-west-1", sns_topic_arn: topic }]);

  await access.fill("replacement-access-key");
  await secret.fill("replacement-secret-key");
  await save.click();
  await expect(access).toHaveValue("");
  await expect(secret).toHaveValue("");
  await expect(secret).toHaveAttribute("placeholder", "••••••••");
  expect(state.patches[1]).toEqual({ mode: "static", region: "eu-west-1", sns_topic_arn: topic,
    access_key_id: "replacement-access-key", secret_access_key: "replacement-secret-key" });

  await page.getByLabel("Remove saved session token", { exact: true }).check();
  await expect(token).not.toHaveAttribute("placeholder", "••••••••");
  await save.click();
  await expect(save).toBeEnabled();
  await expect(page.getByLabel("Remove saved session token", { exact: true })).toHaveCount(0);
  await expect(token).not.toHaveAttribute("placeholder", "••••••••");
  expect(state.patches[2]).toEqual({ mode: "static", region: "eu-west-1", sns_topic_arn: topic, clear_session_token: true });
});

test("brand readers refresh status without provisioning controls", async ({ page, context }) => {
  const state = await fixture(context, false);
  state.confirmed = true;
  await page.goto("/settings?brand_id=1");
  await page.getByRole("button", { name: "Amazon SES", exact: true }).click();
  await expect(page.getByRole("button", { name: "Set up SES and SNS" })).toHaveCount(0);
  await page.getByRole("button", { name: "Refresh event status" }).click();
  await expect(page.getByText("Event delivery is ready. SNS subscription confirmed.")).toBeVisible();
  expect(state.setup).toBe(0);
  expect(state.reads).toBe(1);
});
