import { expect, holdStream, test, type Page } from "./site";

// The Overlord, 2026-10-08 about 22:25Z, of the terminal button on a live
// goblin's card: "no need to have terminal button on board cards appear
// because clicking the card takes you there". The card itself is the way in,
// by a click or by Enter on it. The supervisor is played here: one snapshot
// on a held stream, and the terminal's socket says it is ready.
const SESSION = "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34";
const snapshot = {
  healthy: true, instance: "fixture", revision: 1, attention: [], cfo_runs: true, afk: { state: "off" },
  tasks: [{ id: "nw-invoice-export", title: "Export invoices as CSV", project: "northwind-api", phase: "working", verified: false, generation: "nw-invoice-export-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", session: SESSION }],
  sessions: [{ id: SESSION, native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "nw-invoice-export", role: "goblin", task_id: "nw-invoice-export", generation: "nw-invoice-export-1", harness: "claude", phase: "working" }],
};

async function open(page: Page) {
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(Buffer.from("READY\r\n"));
  });
  await page.goto("/");
  const board = page.getByRole("button", { name: "Board", exact: true });
  if (await board.isVisible()) await board.click();
  await expect(card(page)).toBeVisible();
}

const shell = (page: Page) => page.locator(".task-board .task-card-shell").filter({ hasText: "Export invoices as CSV" });
const card = (page: Page) => shell(page).locator(".task-card");

async function expectTerminalOpen(page: Page) {
  await expect(page.locator("#panel-title")).toHaveText("Export invoices as CSV");
  await expect(page.locator(".goblin-panel.showing-terminal")).toHaveCount(1);
  await expect(page.locator(".panel-pill").getByRole("button", { name: "Terminal", exact: true })).toHaveAttribute("aria-pressed", "true");
}

for (const [size, viewport] of [["desktop", { width: 1280, height: 900 }], ["phone", { width: 390, height: 844 }]] as const) {
  test.describe(`at ${size} width`, () => {
    test.use({ viewport });

    test("a live goblin's card has no terminal button, at rest, on hover or with the keyboard in it", async ({ page }) => {
      // Arrange
      await open(page);

      // Act
      await card(page).hover();
      await card(page).focus();

      // Assert
      await expect(shell(page).getByRole("button", { name: /terminal/i })).toHaveCount(0);
      await expect(shell(page).locator(".card-terminal")).toHaveCount(0);
    });

    test("a click on a live goblin's card opens its terminal", async ({ page }) => {
      // Arrange
      await open(page);

      // Act
      await card(page).click();

      // Assert
      await expectTerminalOpen(page);
    });

    test("Enter on a live goblin's card opens its terminal", async ({ page }) => {
      // Arrange
      await open(page);
      await card(page).focus();

      // Act
      await page.keyboard.press("Enter");

      // Assert
      await expectTerminalOpen(page);
    });
  });
}
