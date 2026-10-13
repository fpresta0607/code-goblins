import { expect, holdStream, test, type Page } from "./site";

// From 2026-10-12 23:20Z to 2026-10-13 04:10Z the CFO was told four times
// that "a goblin's terminal" ended or "The terminal of this task did not
// answer", and could not tell which: two were its own terminal as it was
// restarted. A terminal the board reports to the CFO is named, the CFO's as
// its own and a goblin's by its name and task.
const SESSION = "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34";
const snapshot = {
  healthy: true, instance: "fixture", revision: 1, attention: [], afk: { state: "off" },
  cfo_runs: true, cfo_terminal: "cfo", cfo_harness: "codex",
  tasks: [{ id: "nw-invoice-export", title: "Export invoices as CSV", goblin_name: "Zora", goblin_title: "Invoice Imp", project: "northwind-api", phase: "working", verified: false, generation: "nw-invoice-export-1", harness: "codex", model: "gpt-6-astra", effort: "xhigh", backend: "native", session: SESSION }],
  sessions: [{ id: SESSION, native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "nw-invoice-export", role: "goblin", task_id: "nw-invoice-export", generation: "nw-invoice-export-1", harness: "codex", phase: "working" }],
};

// open plays the supervisor: its stream, a terminal view of each that closes
// as the supervisor closes one that did not answer, and the report the board
// gives the CFO.
async function open(page: Page) {
  const reports: { where: string; text: string }[] = [];
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.route("**/api/cfo/report", (route) => { reports.push(route.request().postDataJSON() as { where: string; text: string }); return route.fulfill({ status: 202, json: { reported: true } }); });
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    const query = new URL(socket.url()).searchParams;
    const name = query.get("cfo") ? "the CFO" : "this task";
    socket.close({ code: 1008, reason: `The terminal of ${name} did not answer.` });
  });
  await page.goto("/");
  return reports;
}

test("a terminal that did not answer is named to the CFO, its own and a goblin's", async ({ page }) => {
  // Arrange: the board opens on the CFO's terminal.
  const reports = await open(page);
  await expect.poll(() => reports.map((report) => report.where)).toContain("the CFO's terminal");

  // Act: he opens the goblin's terminal.
  const boardTab = page.getByRole("button", { name: "Board", exact: true });
  if (await boardTab.isVisible()) await boardTab.click();
  await page.locator(".task-board .task-card-shell").filter({ hasText: "Export invoices as CSV" }).locator(".task-card").click();

  // Assert
  await expect.poll(() => reports).toContainEqual({ where: "the terminal of Zora - Invoice Imp (nw-invoice-export)", text: "The terminal of this task did not answer." });
  expect(reports.map((report) => report.where)).not.toContain("a goblin's terminal");
});
