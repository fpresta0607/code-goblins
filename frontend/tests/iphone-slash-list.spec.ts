import { devices, expect, holdStream, servePages, test } from "./site";

const PRIVATE = "https://pc.example.ts.net:4311";
// KEYBOARD_PX is how much of an iPhone 15's screen its keyboard covers.
const KEYBOARD_PX = 336;

const COMMANDS = [
  { name: "alpha-notes", description: "Keep running notes for the task at hand", source: "skill", sigil: "$" },
  { name: "kit:find-notes", description: "Find the notes a project keeps about a change, by what they say and when they were written, across every folder of the project", source: "skill", sigil: "$" },
  { name: "release-notes", description: "Write the release notes for the branch", source: "skill", sigil: "$" },
  { name: "compact", description: "summarize conversation to prevent hitting the context limit", source: "builtin", sigil: "/" },
  { name: "diff", description: "show git diff (including untracked files)", source: "builtin", sigil: "/" },
  { name: "model", description: "choose what model and reasoning effort to use", source: "builtin", sigil: "/" },
  { name: "review", description: "review my current changes and find issues", source: "builtin", sigil: "/" },
  { name: "status", description: "show current session configuration and token usage", source: "builtin", sigil: "/" },
];

test.use({ ...devices["iPhone 15"], browserName: "webkit" });

// With the keyboard up, the slash list stands between the top of what the
// screen shows and the box, its first option in sight, and a tap takes the
// option it lands on.
test("iPhone: with the keyboard up, a slash opens the list on screen above the box, and a tap takes a skill", async ({ page }) => {
  // Arrange: the keyboard covers the bottom of the screen, as Safari reports
  // it through the visual viewport.
  const snapshot = {
    healthy: true, instance: "fixture", revision: 1, attention: [], cfo_runs: true, cfo_harness: "claude", cfo_terminal: "cfo-1", afk: { state: "off" },
    tasks: [{ id: "nw-invoice-export", title: "Export invoices as CSV", project: "northwind-api", goblin_name: "Kip", goblin_title: "Ledger Scout", phase: "working", verified: false, generation: "nw-invoice-export-1", harness: "codex", model: "m", effort: "high", backend: "native" }],
  };
  await page.addInitScript((keyboard) => {
    localStorage.setItem("cfo-chat-style", "modern");
    localStorage.setItem("cfo-voice-hint-v1", "dismissed");
    const real = window.visualViewport!;
    const viewport = new EventTarget();
    Object.defineProperties(viewport, {
      width: { get: () => real.width }, height: { get: () => real.height - keyboard },
      offsetTop: { get: () => 0 }, offsetLeft: { get: () => 0 }, scale: { get: () => 1 },
    });
    Object.defineProperty(window, "visualViewport", { value: viewport, configurable: true });
  }, KEYBOARD_PX);
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: COMMANDS } }));
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => { socket.send(JSON.stringify({ type: "history", bytes: 0 })); });
  await servePages(page.context(), PRIVATE);
  await page.goto(PRIVATE + "/");
  const board = page.getByRole("button", { name: "Board", exact: true });
  if (await board.isVisible()) await board.click();
  await page.locator(".task-board .task-card-shell").filter({ hasText: "Kip - Ledger Scout" }).locator(".task-card").click();
  const field = page.getByRole("textbox", { name: "Message Kip - Ledger Scout" });
  await field.tap();

  // Act
  await page.keyboard.type("/");

  // Assert
  const list = page.getByRole("listbox", { name: "Commands", exact: true });
  await expect(list.locator(".popup-option-label")).toHaveText(COMMANDS.map((each) => each.sigil + each.name));
  const shown = await page.evaluate(() => window.visualViewport!.height);
  const card = (await list.boundingBox())!;
  const input = (await page.locator(".composer-input").boundingBox())!;
  expect(card.y).toBeGreaterThanOrEqual(0);
  expect(card.y + card.height).toBeLessThanOrEqual(input.y);
  expect(input.y + input.height).toBeLessThanOrEqual(shown);
  const first = (await list.getByRole("option").first().boundingBox())!;
  expect(first.y).toBeGreaterThanOrEqual(card.y);

  // Act
  await list.getByRole("option", { name: "$kit:find-notes" }).tap();

  // Assert
  await expect(field).toHaveValue("$kit:find-notes ");
  await expect(list).toHaveCount(0);
});
