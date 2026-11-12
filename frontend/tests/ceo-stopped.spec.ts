import { expect, holdStream, openItem, test } from "./site";

// The supervisor brings back a CEO whose terminal ended while nobody asked it
// to stop, and stops after it has ended three times in a row soon after it
// started, so it does not start a CEO that keeps ending in a loop. The
// Overlord is told in one plain item whose one action is the board's own
// Reopen the CEO. It runs no command.
const TITLE = "The CEO's terminal ended 3 times in a row, each within 10 minutes of its start, with no more than 10 minutes between one end and the next start, the last time because its terminal ended with exit code 1, so the supervisor stopped bringing it back. Reopen the CEO once you have looked?";
const item = {
  id: "ceo-stopped-0123456789abcdef", identity: "f".repeat(64), title: TITLE, shell: "powershell", admin: false,
  command: "# This item reopens the CEO with the board's Reopen the CEO, never as a command.\n", cwd: "C:\\CodeGoblins",
  state: "ready", created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 86400000).toISOString(), ceo_stopped: true,
};
const snapshot = {
  healthy: true, instance: "ceo-stopped", revision: 1, attention: [], afk: { state: "off" },
  ceo_runs: false, ceo_closed: true, ceo_configured: true, ceo_harness: "", tasks: [],
  questions: [], reviews: [], runs: [item], credentials: [], actions: [],
};

test("a CEO that kept ending reaches the Overlord as one plain item whose action is Reopen the CEO", async ({ page }) => {
  // Arrange
  const posted: string[] = [];
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") posted.push(path);
    if (path === "/api/ceo/reopen") return route.fulfill({ json: { reopened: true } });
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

  // Act: he reopens the CEO.
  await card.getByRole("button", { name: "Reopen the CEO" }).click();

  // Assert: the board's own reopen, and no run item started.
  await expect.poll(() => posted).toContain("/api/ceo/reopen");
  expect(posted).not.toContain("/api/actions");
});
