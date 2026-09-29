import { expect, test } from "@playwright/test";
import snapshot from "./fixtures/queued-snapshot.json" with { type: "json" };

for (const shouldFail of [false, true]) {
  test(`CFO restart confirms on Enter and ${shouldFail ? "keeps a failure visible" : "opens the resumed terminal"}`, async ({ page }) => {
    let restarts = 0;
    await page.route("**/api/**", async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === "/api/events") {
        await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify({ ...snapshot, cfo_runs: true })}\n\n` });
      } else if (path === "/api/cfo/resume") {
        await route.fulfill({ json: { harness: "claude", session: "abc01234-5678-9012-abcd-012345678901", terminal: "cfo", identity: "record-1" } });
      } else if (path === "/api/cfo/restart") {
        restarts++;
        expect(route.request().postDataJSON()).toEqual({ identity: "record-1" });
        expect(route.request().headers()["x-cfo-token"]).toBe(snapshot.instance);
        await route.fulfill(shouldFail ? { status: 409, json: { error: "The CFO process ID was reused; nothing stopped" } } : { json: { restarted: true } });
      } else {
        await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
      }
    });
    await page.goto("/");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    const trigger = page.getByRole("button", { name: "Restart CFO", exact: true });
    await expect(trigger).toHaveAttribute("title", "Restart CFO");
    await trigger.click();
    const dialog = page.getByRole("dialog", { name: "Restart your CFO?" });
    await expect(dialog).toContainText("current response will be interrupted");
    await expect(dialog).toContainText("goblins keep running");
    await expect(dialog.getByRole("button", { name: "Restart CFO" })).toBeFocused();
    expect(restarts).toBe(0);
    await page.keyboard.press("Enter");
    if (shouldFail) {
      await expect(dialog.getByRole("alert")).toContainText("nothing stopped");
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Cancel" }).click();
      await expect(trigger).toBeFocused();
    } else {
      await expect(dialog).not.toBeVisible();
    }
    expect(restarts).toBe(1);
  });
}
