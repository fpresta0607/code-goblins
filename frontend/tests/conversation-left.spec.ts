import { CEO_DOOR, expect, openTaskList, type Page, test } from "./site";

const LEFT = "The CEO's conversation a1b2c3d4-session could not be resumed when it came back at 2026-10-03 01:47 UTC, so it started on a new one. That conversation is kept: claude --resume a1b2c3d4-session in the CEO's home opens it by hand.";
const UNREGISTERED = "The CEO is not registered; run ceo register in the CEO session";

async function board(page: Page, snapshot: object) {
  await page.route("**/api/**", async (route) => {
    if (new URL(route.request().url()).pathname === "/api/events") {
      await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` });
    } else {
      await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    }
  });
  await page.goto("/");
}

// The Overlord, 2026-10-07: "this massive lump is useless to me". Neither is a
// banner over the board: the CEO's bar says it cannot be reached, with the
// fix in its tip, and the conversation the CEO could not resume is named in
// the CEO's own panel.
test("the board names the conversation the CEO could not resume in the CEO's panel, and its registration on its bar, with no banner", async ({ page }) => {
  // Arrange
  const snapshot = { instance: "conversation-left", revision: 1, healthy: true, example: true, ceo_runs: true, ceo_terminal: "ceo", registration: UNREGISTERED, ceo_conversation_left: LEFT };

  // Act
  await board(page, snapshot);

  // Assert
  const bar = page.getByRole("group", { name: "CEO" });
  await expect(bar).toBeVisible();
  await expect(page.getByText(/could not be resumed|not registered/).filter({ visible: true })).toHaveCount(0);
  await expect(bar.locator("p")).toHaveText("The board cannot reach the CEO.");
  await expect(bar.locator("p")).toHaveAttribute("data-tip", UNREGISTERED);
  await bar.getByRole("button", { name: CEO_DOOR }).last().click();
  await openTaskList(page);
  await expect(page.locator(".ceo-conversation-left")).toHaveText(LEFT);
});

test("a board whose CEO came back on its conversation names none it could not resume", async ({ page }) => {
  // Arrange
  const snapshot = { instance: "conversation-resumed", revision: 1, healthy: true, example: true, ceo_runs: true };

  // Act
  await board(page, snapshot);

  // Assert
  await expect(page.getByRole("button", { name: "Board", exact: true })).toBeVisible();
  await expect(page.getByText("could not be resumed")).toHaveCount(0);
});

// The Overlord, 2026-10-07, on an amber box of "axi: lavish-axi end ... exited
// with code 1" over the board: "this massive lump is useless to me as the
// user". A supervisor error goes to the CEO, never onto the board.
test("a supervisor error shows nowhere on the board", async ({ page }) => {
  // Arrange
  const error = String.raw`axi: lavish-axi end C:\dev\code-goblins\.worktrees\gb-cg-board-kill\.lavish\board-lifecycle\index.html exited with code 1`;

  // Act
  await board(page, { instance: "supervisor-error", revision: 1, healthy: true, example: true, ceo_runs: true, error });

  // Assert
  await expect(page.getByRole("button", { name: "Board", exact: true })).toBeVisible();
  await expect(page.getByText("lavish-axi end")).toHaveCount(0);
});
