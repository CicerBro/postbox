import { expect, test } from "@playwright/test";

for (const totp of [false, true]) {
  test(`members can change sign-in email${totp ? " with an authenticator" : ""}`, async ({ page, context }) => {
    const session = { user_id: 2, admin: false, csrf_token: "token", email: "member@example.org", display_name: "", timezone: "UTC", totp_enabled: totp, passkeys_available: false };
    const writes: unknown[] = [];
    let reject = true;
    await context.route("**/api/v1/**", async route => {
      const request = route.request();
      const path = new URL(request.url()).pathname.replace("/api/v1", "");
      if (path === "/auth/email") {
        expect(request.headers()["x-csrf-token"]).toBe("token");
        writes.push(request.postDataJSON());
        if (reject) { await route.fulfill({ status: 400, json: { error: "email address already in use" } }); return; }
        session.email = "new@example.org";
        await route.fulfill({ json: { email: session.email } });
        return;
      }
      await route.fulfill({ json: path === "/auth/session" ? session : [] });
    });
    await page.goto("/account/security");
    await expect(page.getByLabel("Sign-in email", { exact: true })).toHaveValue(session.email);
    const save = page.getByRole("button", { name: "Save email address", exact: true });
    await expect(save).toBeDisabled();
    await page.getByLabel("Sign-in email", { exact: true }).fill("new@example.org");
    await page.getByLabel("Confirm current password", { exact: true }).fill("member-password");
    if (totp) {
      await expect(save).toBeDisabled();
      await page.getByLabel("Confirm authenticator code", { exact: true }).fill("123456");
    } else {
      await expect(page.getByLabel("Confirm authenticator code", { exact: true })).toHaveCount(0);
    }
    await save.click();
    await expect(page.getByText("email address already in use", { exact: true })).toBeVisible();
    reject = false;
    await save.click();
    await expect(page.getByText(/Email updated\. Use your new address/)).toBeVisible();
    expect(writes[1]).toEqual({ email: "new@example.org", password: "member-password", ...(totp ? { code: "123456" } : {}) });
    await expect(page.getByLabel("Confirm current password", { exact: true })).toHaveValue("");
    if (totp) await expect(page.getByLabel("Confirm authenticator code", { exact: true })).toHaveValue("");
    await page.getByRole("link", { name: "Preferences", exact: true }).click();
    await expect(page.getByText("new@example.org", { exact: true }).first()).toBeVisible();
    await page.reload();
    await expect(page.getByText("new@example.org", { exact: true }).first()).toBeVisible();
  });
}
