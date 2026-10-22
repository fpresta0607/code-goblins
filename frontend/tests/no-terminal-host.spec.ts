import { expect, holdStream, test, type Page, type WebSocketRoute } from "./site";

// Issue 1218, 2026-10-20: a goblin whose terminal's host did not come back
// after a PC restart showed a black pane with a cursor in its Terminal tab,
// under its red "Did not come back after the restart", and the Overlord read
// it as a goblin at work: "if its runnign thatn baord is broke". A goblin with
// no terminal says so in words and draws no terminal and no cursor, and one
// whose host runs still shows its terminal. The supervisor is played here:
// one snapshot on a held stream, and each view of the goblin's terminal is a
// socket the test answers as the relay does.
const SESSION = "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34";
const TASK = { id: "nw-invoice-export", title: "Export invoices as CSV", goblin_name: "Zora", goblin_title: "Invoice Imp", project: "northwind-api", phase: "working", verified: false, generation: "nw-invoice-export-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", session: SESSION };
const snapshot = (changes: object) => ({
  healthy: true, instance: "fixture", revision: 1, attention: [], cfo_runs: true, afk: { state: "off" },
  tasks: [{ ...TASK, ...changes }],
  sessions: [{ id: SESSION, native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: TASK.id, role: "goblin", task_id: TASK.id, generation: TASK.generation, harness: "claude", phase: "working" }],
});
const DID_NOT_COME_BACK = { comeback: { id: TASK.id, state: "stopped", reason: "claude is not on PATH" } };
const NO_TERMINAL = "No terminal: this goblin did not come back after the restart. On its Task view, Restart under Harness brings it back and Stop retires it.";
const ENGINES = { harnesses: [{ id: "claude", name: "Claude Code", reason: "", models: [{ id: "claude-opus-5-5", name: "Opus 5.5", efforts: ["xhigh"], default_effort: "xhigh" }] }] };
// A view waits this long for its first screen before it is shown all the
// same (READY_FALLBACK_MS in terminalView.ts); a look after it sees what
// stays.
const PAST_THE_WAIT = 1500;

// closes answers a view as the relay answers one of a terminal with no host,
// once the view has asked for its size: the relay accepts every view, then
// closes it with the reason.
const closes = (code: number, reason: string) => (socket: WebSocketRoute) => socket.onMessage(() => void socket.close({ code, reason }));
// runs answers a view as a running host's relay does: the history, then the
// screen.
const runs = (socket: WebSocketRoute) => {
  socket.send(JSON.stringify({ type: "history", bytes: 0 }));
  socket.send(Buffer.from("\x1b[2J\x1b[HZora is at work\r\n"));
};

// open opens the goblin's Terminal tab from its card, as he does, and
// returns the views of its terminal the board opened.
async function open(page: Page, changes: object, answer: (socket: WebSocketRoute) => void): Promise<string[]> {
  const views: string[] = [];
  await holdStream(page, snapshot(changes));
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    views.push(socket.url());
    answer(socket);
  });
  await page.goto("/");
  const board = page.getByRole("button", { name: "Board", exact: true });
  if (await board.isVisible()) await board.click();
  await page.locator(".task-board .task-card-shell").filter({ hasText: "Zora" }).locator(".task-card").click();
  await expect(page.locator(".goblin-panel.showing-terminal")).toHaveCount(1);
  return views;
}

// The shown terminal slot, and the terminal drawn in it, if any.
const slot = (page: Page) => page.locator(".terminal-deck .deck-slot:not([hidden])");
// Open in terminal, in the panel's row or, where the row has no room for it
// beside a Claude Code session's three views, in its More menu.
async function openInTerminal(page: Page) {
  const more = page.locator(".panel-top").getByRole("button", { name: "More", exact: true });
  if (await more.count()) await more.click();
  return page.getByRole("button", { name: "Open in Windows Terminal", exact: true }).or(page.getByRole("menuitem", { name: "Open in terminal", exact: true }));
}
const drawnTerminal = (page: Page) => slot(page).locator(".terminal-view:not(.staged) .xterm");

test.use({ viewport: { width: 1280, height: 800 } });

test("a goblin that did not come back after the restart says it has no terminal, draws none and no cursor, and names its Task view's actions", async ({ page }, testInfo) => {
  // Arrange and act: its relay closes a view as it closes one of a host the
  // restart ended, whose record was left behind.
  const views = await open(page, DID_NOT_COME_BACK, closes(1008, "The terminal of this task did not answer."));
  await page.waitForTimeout(PAST_THE_WAIT);

  // Assert: words, no terminal, no cursor, and no view of a terminal that
  // is not there.
  await expect(slot(page).getByText(NO_TERMINAL, { exact: true })).toBeVisible();
  await expect(slot(page).locator(".xterm")).toHaveCount(0);
  await expect(slot(page).locator(".xterm-cursor, .xterm-cursor-layer")).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toHaveCount(0);
  await expect(await openInTerminal(page)).toHaveCount(0);
  expect(views).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("no-terminal-host.png") });

  // Act: he turns to its Task view and opens Harness, as the words say.
  await page.route("**/api/engines", (route) => route.fulfill({ json: ENGINES }));
  await page.getByRole("group", { name: "Panel view" }).getByRole("button", { name: "Task", exact: true }).click();
  await page.locator(".goblin-panel summary").filter({ hasText: "Harness" }).click();

  // Assert: Restart, which brings it back, and Stop, which retires it.
  await expect(page.getByRole("button", { name: "Restart Zora - Invoice Imp", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Stop Export invoices as CSV", exact: true })).toBeVisible();
});

test("a goblin whose relay says no terminal runs keeps saying so, and never shows an empty terminal", async ({ page }) => {
  // Arrange and act: no comeback says the host is gone, and the relay
  // closes the view as it closes one whose host left no record.
  const views = await open(page, {}, closes(1000, "No terminal is running for this task."));
  await page.waitForTimeout(PAST_THE_WAIT);

  // Assert
  expect(views).toHaveLength(1);
  await expect(slot(page).getByText("No terminal is running for this task.", { exact: true })).toBeVisible();
  await expect(drawnTerminal(page)).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).not.toBeVisible();
});

test("a goblin whose host runs still shows its terminal", async ({ page }, testInfo) => {
  // Arrange and act
  const views = await open(page, {}, runs);
  await page.waitForTimeout(PAST_THE_WAIT);

  // Assert
  expect(views).toHaveLength(1);
  await expect(drawnTerminal(page)).toBeVisible();
  await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
  await expect(slot(page).getByText(/No terminal/)).toHaveCount(0);
  await expect(await openInTerminal(page)).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("live-terminal-host.png") });
});
