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

test("the board names the conversation the CFO could not resume, apart from its registration", async ({ page }) => {
  // Arrange
  const snapshot = { instance: "conversation-left", revision: 1, healthy: true, example: true, cfo_runs: true, registration: UNREGISTERED, cfo_conversation_left: LEFT };

  // Act
  await board(page, snapshot);

  // Assert
  await expect(page.getByRole("status").filter({ hasText: "a1b2c3d4-session could not be resumed" })).toHaveText(LEFT);
  await expect(page.getByRole("alert").filter({ hasText: UNREGISTERED })).toBeVisible();
  await expect(page.getByRole("alert").filter({ hasText: "a1b2c3d4-session" })).toHaveCount(0);
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
