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
