import { expect, test } from "@playwright/test";

for (const mobile of [false, true]) {
  test(`campaign report keeps compact activity and stops outcome queries on ${mobile ? "mobile" : "desktop"}`, async ({ page, context }, testInfo) => {
    await page.setViewportSize({ width: mobile ? 390 : 1920, height: mobile ? 844 : 1080 });
    await page.clock.install();
    const recipientRequests: string[] = [];
    await context.route("**/api/v1/**", async route => {
      const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
      if (path === "/campaigns/7/recipients") recipientRequests.push(route.request().url());
      const campaign = { id: 7, subject: "October newsletter", status: "sent", created_at: 1700000000000, scheduled_at: null };
      const activity = [
        { id: 1, name: "Alex Example", email: "alex@example.org", at: 1788254101000 },
        { id: 2, name: null, email: "unnamed@example.org", at: 1788254100000 },
      ];
      let body: unknown = [];
      if (path === "/auth/session") body = { user_id: 1, admin: true, csrf_token: "report-token" };
      else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "sender@example.org" }];
      else if (path === "/sending") body = { enabled: true };
      else if (path === "/campaigns/7") body = [campaign];
      else if (path === "/campaigns/7/preview") body = { subject: campaign.subject, html: "<p>Newsletter</p>", text: "" };
      else if (path === "/campaigns/7/report") body = {
        stats: [{ recipients: 42, sent: 42, delivered: 40, opens_unique: 8, clicks_unique: 2, unsubscribes: 1, bounces_hard: 1, bounces_soft: 0, complaints: 0 }],
        links: [], countries: [], hourly: [],
        recent: { opened: activity, unsubscribed: activity, bounced: activity, complained: activity },
        delivery: { first_sent_at: 1788254101000, last_sent_at: 1788254101000, from_name: "Sender", from_email: "sender@example.org", sender_snapshotted: true, historical: false, delivered_estimated: false },
      };
      else if (path === "/campaigns/7/events") {
        await route.fulfill({ contentType: "text/event-stream", body: "" });
        return;
      }
      await route.fulfill({ json: body });
    });
    await page.goto("/campaigns/7/report");
    await expect(page.getByRole("heading", { name: "October newsletter", exact: true })).toBeVisible();
    const subtext = page.getByText("2 recorded of unique opens", { exact: true });
    await expect(subtext).toBeVisible();
    const dimensions = await subtext.evaluate(element => ({ height: element.getBoundingClientRect().height, lineHeight: parseFloat(getComputedStyle(element).lineHeight) }));
    if (mobile) expect(dimensions.height).toBeGreaterThan(dimensions.lineHeight);
    else expect(dimensions.height).toBeLessThanOrEqual(dimensions.lineHeight + 1);
    for (const title of ["Recently opened", "Recent unsubscribes", "Recent bounces", "Recent spam complaints"]) {
      const card = page.locator(".card").filter({ has: page.getByRole("heading", { name: title, exact: true }) });
      await expect(card.getByRole("columnheader")).toHaveText(["Name", "Email", "When"]);
      await expect(card.locator("tbody tr:first-child td")).toHaveText(["Alex Example", "alex@example.org", /.+/]);
      await expect(card.locator("tbody tr:nth-child(2) td:first-child")).toHaveText("Unnamed");
      const lineCounts = await card.locator("tbody tr:first-child td").evaluateAll(cells => cells.map(cell => cell.getBoundingClientRect().height - parseFloat(getComputedStyle(cell).paddingTop) - parseFloat(getComputedStyle(cell).paddingBottom) <= parseFloat(getComputedStyle(cell).lineHeight) + 1));
      expect(lineCounts).toEqual([true, true, true]);
    }
    await expect(page.getByText("Imported Sendy report.", { exact: false })).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Recipient outcomes" })).toHaveCount(0);
    await page.clock.fastForward(30000);
    expect(recipientRequests).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath("campaign-report.png"), fullPage: true });
  });
}

test("links a recovery send and navigates between combined and individual reports", async ({ page, context }) => {
  let linked = false;
  const original = { id: 1, subject: "Black Friday", status: "sent", created_at: 1700000000000 };
  const recovery = { ...original, id: 21, subject: "Authentication recovery" };
  await context.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    let body: unknown = [];
    if (path === "/auth/session") body = { user_id: 1, admin: true, csrf_token: "report-token" };
    else if (path === "/brands") body = [{ id: 1, name: "Acme", from_email: "sender@example.org" }];
    else if (path === "/sending") body = { enabled: true };
    else if (path === "/campaigns") body = linked ? [{ ...original, recovery_count: 1 }] : [original, recovery];
    else if (path === "/campaigns/1") body = [original];
    else if (path === "/campaigns/21") body = [recovery];
    else if (path === "/campaigns/21/recovery") {
      expect(route.request().method()).toBe("PUT");
      expect(route.request().postDataJSON()).toEqual({ original_campaign_id: 1 });
      linked = true;
      body = { original_campaign_id: 1 };
    } else if (path === "/campaigns/1/report" || path === "/campaigns/21/report") {
      body = {
        stats: [{ recipients: 1, sent: 1, delivered: 1 }],
        links: [], countries: [], hourly: [],
        recovery: linked ? {
          original_campaign_id: 1,
          ...(path === "/campaigns/1/report" ? {
            sends: [{ ...original, sent: 1, delivered: 0 }, { ...recovery, sent: 1, delivered: 1 }],
            stats: { recipients: 85, sent: 80, delivered: 70, bounced: 6, recovered: 10, awaiting_outcome: 4, opens_unique: 20, clicks_unique: 4, unsubscribed: 2, complained: 1 },
          } : {}),
        } : null,
      };
    } else if (path.endsWith("/events")) {
      await route.fulfill({ contentType: "text/event-stream", body: "" });
      return;
    }
    await route.fulfill({ json: body });
  });

  await page.goto("/campaigns/21/report");
  await page.getByText("Link as a recovery send", { exact: true }).click();
  await page.getByLabel("Original campaign ID").fill("1");
  await page.getByRole("button", { name: "Link campaigns" }).click();
  await expect(page.getByText("Combined report · 2 sends")).toBeVisible();
  for (const [label, rate, detail] of [
    ["Unique opens", "25.00%", "20 recorded of sent"],
    ["Unique clicks", "5.00%", "4 recorded of sent"],
    ["Click-to-open rate", "20.00%", "4 recorded of unique opens"],
    ["Unsubscribed", "2.50%", "2 recorded of sent"],
    ["Bounced", "7.50%", "6 recorded of sent"],
    ["Spam complaints", "1.25%", "1 recorded of sent"],
    ["Delivered", "87.50%", "70 recorded of sent"],
    ["Awaiting outcome", "5.00%", "4 recorded of sent"],
    ["Recovered after bounce", "12.50%", "10 recorded of sent"],
  ]) {
    const card = page.locator(".metric-card").filter({ has: page.getByText(label, { exact: true }) });
    await expect(card.getByText(rate, { exact: true })).toBeVisible();
    await expect(card.getByText(detail, { exact: true })).toBeVisible();
  }
  await page.getByRole("link", { name: "Original send" }).click();
  await expect(page.getByRole("heading", { name: "Newsletter preview" })).toBeVisible();
  await page.getByRole("link", { name: "Combined report" }).click();
  await page.getByRole("link", { name: "Recovery send #21" }).click();
  await expect(page.getByRole("heading", { name: "Authentication recovery" })).toBeVisible();
  await page.getByRole("link", { name: "All campaigns" }).click();
  await expect(page.getByRole("row").filter({ hasText: "Black Friday" })).toContainText("2 sends");
  await expect(page.getByRole("link", { name: "Authentication recovery" })).toHaveCount(0);
});
