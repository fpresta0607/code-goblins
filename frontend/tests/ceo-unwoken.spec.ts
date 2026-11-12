import { expect, holdStream, openItem, test } from "./site";

// From 01:17Z to 01:33Z on 2026-10-13 six wakes waited for an idle Codex CEO
// and nobody knew. The CEO cannot be told why its wakes wait, so the
// Overlord is: one item in plain words whose one action is the board's own
// Restart the CEO, which the supervisor carries out.
// It runs no command: a CEO a run terminal started is not proven to outlive
// that terminal.
const TITLE = "The CEO has not been told of 2 fleet updates for 5 minutes, because its screen shows work it started still running. Restart the CEO so it reads them?";
const item = {
  id: "ceo-unwoken-0123456789abcdef", identity: "e".repeat(64), title: TITLE, shell: "powershell", admin: false,
  command: "# This item restarts the CEO with the board's Restart the CEO, never as a command.\n", cwd: "C:\\CodeGoblins",
  state: "ready", created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 86400000).toISOString(), ceo_unwoken: 1001228,
};
const snapshot = {
  healthy: true, instance: "ceo-unwoken", revision: 1, attention: [], afk: { state: "off" },
  ceo_runs: true, ceo_terminal: "ceo", ceo_harness: "codex", tasks: [],
  questions: [], reviews: [], runs: [item], credentials: [], actions: [],
};

test("an untold CEO reaches the Overlord as one plain item whose action is Restart the CEO", async ({ page }) => {
  // Arrange
  const posted: string[] = [];
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") posted.push(path);
    if (path === "/api/ceo/restart") return route.fulfill({ json: { resumed: true } });
    return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => { socket.send(JSON.stringify({ type: "history", bytes: 0 })); });
  await page.goto("/");

  // Act
  await openItem(page, TITLE);
  const card = page.getByRole("dialog").locator("article").filter({ hasText: TITLE });

  // Assert: plain words and one action, no command to run.
  await expect(card.getByRole("heading", { name: TITLE })).toBeVisible();
  await expect(card.getByRole("button", { name: /PowerShell|Run/ })).toHaveCount(0);
  await expect(card.locator(".run-command")).toHaveCount(0);

  // Act: he restarts the CEO, which asks nothing more.
  await card.getByRole("button", { name: "Restart the CEO" }).click();

  // Assert: the board's own restart, and no run item started.
  await expect.poll(() => posted).toContain("/api/ceo/restart");
  expect(posted).not.toContain("/api/actions");
});
