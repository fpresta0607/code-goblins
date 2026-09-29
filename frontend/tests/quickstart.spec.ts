import { expect, test } from "@playwright/test";
import snapshot from "./fixtures/queued-snapshot.json" with { type: "json" };

for (const shouldFail of [false, true]) {
  test(`first run uses the remembered agent in the home and ${shouldFail ? "shows launch failures" : "accepts Enter"}`, async ({ page }) => {
    let starts = 0;
    await page.route("**/api/**", async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === "/api/events") {
        await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify({ ...snapshot, example: false, cfo_runs: false })}\n\n` });
      } else if (path === "/api/setup") {
        await route.fulfill({ json: { home: "C:\\Scratch\\CodeGoblins", default_agent: "codex", cfo_runs: false, agents: [
          { id: "claude", name: "Claude Code", installed: false, signed_in: false, reason: "Not installed" },
          { id: "codex", name: "Codex", installed: true, signed_in: true },
          { id: "pi", name: "pi", installed: true, signed_in: false, reason: "Sign-in needed" },
        ] } });
      } else if (path === "/api/setup/start") {
        starts++;
        expect(route.request().postDataJSON()).toEqual({ agent: "codex" });
        expect(route.request().headers()["x-cfo-token"]).toBe(snapshot.instance);
        await route.fulfill(shouldFail ? { status: 409, json: { error: "Waiting for memory" } } : { json: { started: true } });
      } else {
        await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
      }
    });
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Start your CFO" })).toBeVisible();
    await expect(page.getByText("C:\\Scratch\\CodeGoblins")).toBeVisible();
    await expect(page.getByRole("radio", { name: /Codex/ })).toBeChecked();
    await expect(page.getByRole("textbox")).toHaveCount(0);
    await expect(page.getByRole("button", { name: /without a CFO/ })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Board", exact: true })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Start CFO (default)" })).toBeFocused();
    await page.keyboard.press("Enter");
    if (shouldFail) {
      await expect(page.locator(".first-run").getByRole("alert")).toHaveText("Waiting for memory");
      await page.getByRole("radio", { name: /pi/ }).check();
      await expect(page.getByRole("button", { name: "Start CFO (default)" })).toBeDisabled();
      await expect(page.locator(".first-run-actions")).toContainText("Run goblins setup");
    } else {
      await expect(page.getByRole("heading", { name: "Start your CFO" })).not.toBeVisible();
    }
    expect(starts).toBe(1);
  });
}
