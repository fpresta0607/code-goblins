import { expect, test, type Page } from "./site";

const LEFT = "The CFO's conversation a1b2c3d4-session could not be resumed when it came back at 2026-10-03 01:47 UTC, so it started on a new one. That conversation is kept: claude --resume a1b2c3d4-session in the CFO's home opens it by hand.";
const UNREGISTERED = "The CFO is not registered; run cfo register in the CFO session";

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
// banner over the board: the CFO's bar says it cannot be reached, with the
// fix in its tip, and the conversation the CFO could not resume is named in
// the CFO's own panel.
test("the board names the conversation the CFO could not resume in the CFO's panel, and its registration on its bar, with no banner", async ({ page }) => {
  // Arrange
  const snapshot = { instance: "conversation-left", revision: 1, healthy: true, example: true, cfo_runs: true, cfo_terminal: "cfo", registration: UNREGISTERED, cfo_conversation_left: LEFT };

  // Act
  await board(page, snapshot);

  // Assert: the fixture's stream ends at once, so the board's own line about
  // its connection may show, and nothing else does.
  await expect(page.locator(".connection-banner").filter({ hasText: /could not be resumed|not registered/ })).toHaveCount(0);
  const bar = page.getByRole("group", { name: "CFO" });
  await expect(bar.locator("p")).toHaveText("The board cannot reach the CFO.");
  await expect(bar.locator("p")).toHaveAttribute("data-tip", UNREGISTERED);
  await bar.getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
  await expect(page.locator(".cfo-conversation-left")).toHaveText(LEFT);
});

test("a board whose CFO came back on its conversation names none it could not resume", async ({ page }) => {
  // Arrange
  const snapshot = { instance: "conversation-resumed", revision: 1, healthy: true, example: true, cfo_runs: true };

  // Act
  await board(page, snapshot);

  // Assert
  await expect(page.getByRole("button", { name: "Board", exact: true })).toBeVisible();
  await expect(page.getByText("could not be resumed")).toHaveCount(0);
});

// The Overlord, 2026-10-07, on an amber box of "axi: lavish-axi end ... exited
// with code 1" over the board: "this massive lump is useless to me as the
// user". A supervisor error goes to the CFO, never onto the board.
test("a supervisor error shows nowhere on the board", async ({ page }) => {
  // Arrange
  const error = String.raw`axi: lavish-axi end C:\dev\code-goblins\.worktrees\gb-cg-board-kill\.lavish\board-lifecycle\index.html exited with code 1`;

  // Act
  await board(page, { instance: "supervisor-error", revision: 1, healthy: true, example: true, cfo_runs: true, error });

  // Assert
  await expect(page.getByRole("button", { name: "Board", exact: true })).toBeVisible();
  await expect(page.getByText("lavish-axi end")).toHaveCount(0);
});
