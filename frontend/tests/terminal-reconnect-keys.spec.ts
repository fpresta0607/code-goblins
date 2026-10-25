import { expect, holdStream, test, type Page } from "./site";
import { RUNS, stepIn } from "./step-in";

// The Overlord, 2026-10-13 about 22:32Z, on Claude Code's permissions screen
// in the CFO's terminal: "Have to navigate and turn navigate enter. It doesn't
// actually work." A view whose relay closed it, as one that fell behind a busy
// program's output is, reconnects by itself, and the keys he presses then and
// after must either reach the program or be said not sent. The supervisor is
// played here: one snapshot on a held stream, and each connection of the
// terminal's view is a socket the test answers. Each test runs under
// Terminal and stepped in (tests/step-in.ts).
const SESSION = "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34";
const snapshot = (harness: string) => ({
  healthy: true, instance: "fixture", revision: 1, attention: [], cfo_runs: true, afk: { state: "off" },
  tasks: [{ id: "nw-invoice-export", title: "Export invoices as CSV", project: "northwind-api", phase: "working", verified: false, generation: "nw-invoice-export-1", harness, model: "claude-opus-5-5", effort: "xhigh", backend: "native", session: SESSION }],
  sessions: [{ id: SESSION, native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "nw-invoice-export", role: "goblin", task_id: "nw-invoice-export", generation: "nw-invoice-export-1", harness, phase: "working" }],
});

const ESC = "\x1b";
// The keys his screen named, in the order he pressed them, and what each sends.
const KEYS: [key: string, sent: string][] = [["ArrowUp", ESC + "[A"], ["ArrowDown", ESC + "[B"], ["Enter", "\r"], ["Escape", ESC], ["Tab", "\t"], ["r", "r"]];

const FELL_BEHIND = { code: 1013, reason: "The view fell behind the terminal's output." };

// A connection is one socket of the view: what it was sent, a way to say the
// screen is whole, and a way to close it as the relay does.
interface Connection {
  inputs: Buffer[];
  ready: () => void;
  close: (code: number, reason: string) => void;
  say: (message: object) => void;
}

// open opens the goblin's terminal from its card, stepping it in where the
// run does, and clicks into it, as he does, once its first connection is
// whole. A connection after the first is whole only once the test says so.
async function open(page: Page, run: typeof RUNS[number]): Promise<Connection[]> {
  const connections: Connection[] = [];
  await holdStream(page, snapshot(run.harness));
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    const connection: Connection = {
      inputs: [],
      ready: () => {
        socket.send(JSON.stringify({ type: "history", bytes: 0 }));
        socket.send(Buffer.from(ESC + "[2J" + ESC + "[HREADY\r\n"));
      },
      close: (code, reason) => void socket.close({ code, reason }),
      say: (message) => socket.send(JSON.stringify(message)),
    };
    socket.onMessage((data) => { if (typeof data !== "string") connection.inputs.push(data); });
    connections.push(connection);
    if (connections.length === 1) connection.ready();
  });
  await page.goto("/");
  const board = page.getByRole("button", { name: "Board", exact: true });
  if (await board.isVisible()) await board.click();
  await page.locator(".task-board .task-card").filter({ hasText: "Export invoices as CSV" }).click();
  await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
  if (run.isSteppedIn) await stepIn(page, connections[0].say);
  await page.locator(".host-terminal .terminal-view:not(.staged)").click();
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeFocused();
  return connections;
}

const sent = (connection: Connection) => Buffer.concat(connection.inputs).toString("latin1");

// typed is what a connection was sent other than the focus reports xterm
// sends as the view gains and loses the keyboard.
const typed = (connection: Connection) => sent(connection).replaceAll(ESC + "[I", "").replaceAll(ESC + "[O", "");

for (const run of RUNS) {
  test(`every key he presses after the view reconnects reaches the program, ${run.name}`, async ({ page }) => {
    // Arrange
    const connections = await open(page, run);
    connections[0].close(FELL_BEHIND.code, FELL_BEHIND.reason);
    await expect.poll(() => connections.length).toBe(2);
    connections[1].ready();
    await expect(page.getByRole("status").filter({ hasText: "Reconnecting" })).toHaveCount(0);

    // Act
    for (const [key] of KEYS) await page.keyboard.press(key);

    // Assert
    await expect.poll(() => typed(connections[1])).toBe(KEYS.map(([, bytes]) => bytes).join(""));
    await expect(page.locator(".goblin-panel.showing-terminal")).toHaveCount(1);
  });

  test(`a key he presses while the view reconnects is said not sent, and none is sent late, ${run.name}`, async ({ page }) => {
    // Arrange
    const connections = await open(page, run);
    connections[0].close(FELL_BEHIND.code, FELL_BEHIND.reason);
    await expect(page.getByRole("status").filter({ hasText: "Reconnecting" })).toBeVisible();

    // Act
    await page.keyboard.press("Enter");

    // Assert
    const notice = page.getByRole("status").filter({ hasText: "Not sent. The terminal is reconnecting." });
    await expect(notice).toBeVisible();
    await expect.poll(() => connections.length).toBe(2);
    connections[1].ready();
    await expect(notice).toHaveCount(0);
    await page.keyboard.press("r");
    await expect.poll(() => typed(connections[1])).toBe("r");
    expect(typed(connections[0])).toBe("");
  });

  test(`a key he presses in a terminal that refuses typing is said not sent, ${run.name}`, async ({ page }) => {
    // Arrange
    const connections = await open(page, run);
    connections[0].close(1008, "The review gate owns this goblin's work right now, so typing is paused. Reconnect to watch the screen.");
    await expect(page.getByRole("button", { name: "Reconnect", exact: true })).toBeVisible();
    await page.locator(".host-terminal .terminal-view").click();

    // Act
    await page.keyboard.press("ArrowDown");

    // Assert
    await expect(page.getByRole("status").filter({ hasText: "Not sent. The terminal is closed." })).toBeVisible();
    expect(typed(connections[0])).toBe("");
  });
}
