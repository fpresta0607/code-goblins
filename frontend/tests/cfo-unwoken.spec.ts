import { expect, holdStream, openItem, test } from "./site";

// From 01:17Z to 01:33Z on 2026-10-13 six wakes waited for an idle Codex CFO
// and nobody knew. The CFO cannot be told why its wakes wait, so the
// Overlord is: one item in plain words whose one action is the board's own
// Restart the CFO, with its confirmation, which the supervisor carries out.
// It runs no command: a CFO a run terminal started is not proven to outlive
// that terminal.
const TITLE = "The CFO has not been told of 2 fleet updates for 5 minutes, because its screen shows work it started still running. Restart the CFO so it reads them?";
const item = {
  id: "cfo-unwoken-0123456789abcdef", identity: "e".repeat(64), title: TITLE, shell: "powershell", admin: false,
  command: "# This item restarts the CFO with the board's Restart the CFO, never as a command.\n", cwd: "C:\\CodeGoblins",
  state: "ready", created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 86400000).toISOString(), cfo_unwoken: 1001228,
};
const snapshot = {
  healthy: true, instance: "cfo-unwoken", revision: 1, attention: [], afk: { state: "off" },
  cfo_runs: true, cfo_terminal: "cfo", cfo_harness: "codex", tasks: [],
  questions: [], reviews: [], runs: [item], credentials: [], actions: [],
};

test("an untold CFO reaches the Overlord as one plain item whose action is Restart the CFO", async ({ page }) => {
  // Arrange
  const posted: string[] = [];
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") posted.push(path);
    if (path === "/api/cfo/restart") return route.fulfill({ json: { resumed: true } });
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

  // Act: he restarts the CFO, and confirms.
  await card.getByRole("button", { name: "Restart the CFO" }).click();
  await page.getByRole("dialog", { name: "Restart the CFO?" }).getByRole("button", { name: "Restart the CFO" }).click();

  // Assert: the board's own restart, and no run item started.
  await expect.poll(() => posted).toContain("/api/cfo/restart");
  expect(posted).not.toContain("/api/actions");
});
