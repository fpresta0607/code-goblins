import type { Page } from "@playwright/test";
import { expect, holdStream, test } from "./site";

// After an update or an install moved the home to goblins' names, the board
// shows one notice from the supervisor's snapshot: what changed, which
// terminals restart and when, and the way back. It can be dismissed only once
// nothing waits to restart, and a dismissal holds for that move alone.
const AT = "2026-11-12T05:00:00Z";
const UNDO = "goblins cutover --undo 'C:\\home\\state\\backups\\cutover-20261112T050000Z'";
const DISMISSED_KEY = "goblins-crossing-dismissed";
const snapshot = (waiting: string[]) => ({
  healthy: true, instance: "crossing", revision: 1, attention: [], afk: { state: "off" },
  ceo_runs: true, ceo_configured: true, ceo_harness: "claude", tasks: [],
  questions: [], reviews: [], runs: [], credentials: [], actions: [],
  crossing: { at: AT, undo: UNDO, waiting },
});

async function openBoard(page: Page, waiting: string[], dismissedAt = "") {
  await holdStream(page, snapshot(waiting));
  if (dismissedAt) await page.addInitScript(({ key, at }) => localStorage.setItem(key, at), { key: DISMISSED_KEY, at: dismissedAt });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
}

const notice = (page: Page) => page.getByRole("status").filter({ hasText: "its command is now goblins" });

test("the notice names each terminal that restarts and the way back, and cannot be dismissed while one waits", async ({ page }) => {
  // Arrange, Act
  await openBoard(page, ["the CEO", "Huey"]);

  // Assert
  await expect(notice(page)).toContainText("each once it is idle with nothing typed on its prompt line, on its own conversation: the CEO, Huey.");
  await expect(notice(page).locator("code")).toHaveText(UNDO);
  await expect(notice(page).getByRole("button", { name: "Dismiss" })).toHaveCount(0);
});

test("once nothing waits the notice is dismissed, and stays dismissed on the next visit", async ({ page }) => {
  // Arrange
  await openBoard(page, []);
  await expect(notice(page)).toContainText("Nothing waits to restart under the new names.");

  // Act
  await notice(page).getByRole("button", { name: "Dismiss" }).click();

  // Assert
  await expect(notice(page)).toHaveCount(0);
  await expect.poll(() => page.evaluate((key) => localStorage.getItem(key), DISMISSED_KEY)).toBe(AT);
  await page.reload();
  await expect(notice(page)).toHaveCount(0);
});

test("a dismissal of an earlier move does not hide a later one", async ({ page }) => {
  // Arrange, Act
  await openBoard(page, [], "2026-11-01T05:00:00Z");

  // Assert
  await expect(notice(page)).toBeVisible();
  await expect(notice(page).getByRole("button", { name: "Dismiss" })).toBeVisible();
});
