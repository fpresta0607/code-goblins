import { expect, holdStream, openItem, test } from "./site";

// The supervisor brings back a CFO whose terminal ended while nobody asked it
// to stop, and stops after it has ended three times in a row soon after it
// started, so it does not start a CFO that keeps ending in a loop. The
// Overlord is told in one plain item whose one action is the board's own
// Reopen the CFO. It runs no command.
const TITLE = "The CFO's terminal ended 3 times in a row, each within 10 minutes of its start, the last time because its terminal ended with exit code 1, so the supervisor stopped bringing it back. Reopen the CFO once you have looked?";
const item = {
  id: "cfo-stopped-0123456789abcdef", identity: "f".repeat(64), title: TITLE, shell: "powershell", admin: false,
  command: "# This item reopens the CFO with the board's Reopen the CFO, never as a command.\n", cwd: "C:\\CodeGoblins",
  state: "ready", created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 86400000).toISOString(), cfo_stopped: true,
};
const snapshot = {
  healthy: true, instance: "cfo-stopped", revision: 1, attention: [], afk: { state: "off" },
  cfo_runs: false, cfo_closed: true, cfo_configured: true, cfo_harness: "", tasks: [],
  questions: [], reviews: [], runs: [item], credentials: [], actions: [],
};

test("a CFO that kept ending reaches the Overlord as one plain item whose action is Reopen the CFO", async ({ page }) => {
  // Arrange
  const posted: string[] = [];
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") posted.push(path);
    if (path === "/api/cfo/reopen") return route.fulfill({ json: { reopened: true } });
    return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");

  // Act
  await openItem(page, TITLE);
  const card = page.getByRole("dialog").locator("article").filter({ hasText: TITLE });

  // Assert: plain words and one action, no command to run.
  await expect(card.getByRole("heading", { name: TITLE })).toBeVisible();
  await expect(card.getByRole("button", { name: /PowerShell|Run/ })).toHaveCount(0);
  await expect(card.locator(".run-command")).toHaveCount(0);

  // Act: he reopens the CFO.
  await card.getByRole("button", { name: "Reopen the CFO" }).click();

  // Assert: the board's own reopen, and no run item started.
  await expect.poll(() => posted).toContain("/api/cfo/reopen");
  expect(posted).not.toContain("/api/actions");
});
