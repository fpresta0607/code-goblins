import { expect, holdStream, openTaskList, type Page, test } from "./site";

// The Overlord, 2026-10-17: "minimal cards are better like an imessage
// history with name status of whether working time working or last worked
// tickets pr icons etc and harness icon". A goblin's card is one row: its
// face, its name and when it last worked on top, its status under them in at
// most two lines and its project under that, and its ticket, pull request, checks and harness mark as
// icons whose tips say what each one is. A finished goblin's row leads with
// its name too, its task in the name's tip, and a pause on a goblin names the
// goblin alone (the CFO's decision 1008011).
const REPO = "https://github.com/fpresta0607/code-goblins";
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
const goblin = (id: string, name: string, title: string, task: string, fields: Record<string, unknown> = {}) => ({
  id, title: task, goblin_name: name, goblin_title: title, project: "code-goblins", phase: "working", report: "working", verified: false,
  generation: id + "-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", since: ago(210), ...fields,
});
const pause = (until: string, minutes: number) => ({ phase: "paused", action: "pause", at: ago(minutes), kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason: "dependency", until, at: ago(minutes) } });
const LONG = "Teach the board to read every harness's own session record instead of its screen, for the waiting and working status";
const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  projects: [{ name: "code-goblins", repository: "fpresta0607/code-goblins", contributors: [] }],
  tasks: [
    goblin("cg-benny", "Benny", "AFK Ace", "Show what needs you while AFK", { pr: REPO + "/pull/1031", ticket: { number: 1013, url: REPO + "/issues/1013", state: "open" } }),
    goblin("cg-kip", "Kip", "Helper", "Help Benny with the AFK page", { parent: "cg-benny", since: ago(12), project: "local-tools" }),
    goblin("cg-otis", "Otis", "Ledger Clerk", "Sync the ledger", { phase: "paused", at: ago(48), lifecycle: pause("task:cg-benny", 48) }),
    goblin("cg-wade", "Wade", "Waiter", "Wait on a long task", { phase: "paused", at: ago(40), lifecycle: pause("task:cg-long", 40) }),
    { id: "cg-long", title: LONG, project: "code-goblins", phase: "queued", verified: false, generation: "", brief: true, since: ago(30) },
    goblin("cg-bart", "Bartholomew", "Distinguished Infrastructure Archivist", "Archive the old gate logs", { since: ago(5) }),
    goblin("finished:cg-ivy", "Ivy", "Merge Maven", "Read the CFO's status from the live CFO", { phase: "done", archived: true, merged: true, verified: true, at: ago(35), pr: REPO + "/pull/1036", branch: "fix/cfo-status-from-registration", generation: "" }),
  ],
};

async function open(page: Page) {
  await page.setViewportSize({ width: 1707, height: 1000 });
  await page.clock.setFixedTime(now);
  await holdStream(page, SNAPSHOT);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}
const card = (page: Page, name: string) => page.locator(".task-board .task-card-shell").filter({ has: page.locator(".card-title").getByText(name, { exact: true }) });
const box = async (part: ReturnType<Page["locator"]>) => (await part.boundingBox())!;

test("a working goblin's card is one row: face, name, time and status, with its parts as icons", async ({ page }) => {
  // Arrange: the board without the CFO's panel beside it.
  await open(page);
  await page.getByRole("button", { name: "Close panel" }).click();
  const row = card(page, "Benny - AFK Ace");

  // Act
  await row.locator(".card-pr").hover();
  const prTip = await page.getByRole("tooltip").textContent();
  await row.locator(".card-ticket").hover();
  const ticketTip = await page.getByRole("tooltip").textContent();

  // Assert
  await expect(row.locator(".card-status-text")).toHaveText("Working");
  await expect(row.locator(".card-clock")).toContainText("3h 30m");
  await expect(row).not.toContainText("#1031");
  await expect(row).not.toContainText("#1013");
  expect(prTip).toContain("code-goblins #1031");
  expect(ticketTip).toContain("#1013");
  const [face, name, clock, status, mark] = await Promise.all([".goblin-avatar", ".card-title", ".card-clock", ".card-status-text", ".card-harness"].map((selector) => box(row.locator(selector))));
  expect(Math.abs((clock.y + clock.height / 2) - (name.y + name.height / 2))).toBeLessThan(6);
  expect(clock.x).toBeGreaterThan(name.x + name.width);
  expect(status.y).toBeGreaterThanOrEqual(name.y + name.height - 1);
  expect(mark.x).toBeGreaterThan(status.x + status.width);
  expect((await box(row)).height).toBeLessThan(face.height + 62);
});

test("a finished goblin's row leads with its name, with its task in the name's tip", async ({ page }) => {
  // Arrange
  await open(page);
  const row = card(page, "Ivy - Merge Maven");

  // Act
  await row.locator(".task-card").hover();

  // Assert
  await expect(row).toBeVisible();
  await expect(page.getByRole("tooltip")).toHaveText("Read the CFO's status from the live CFO");
  await expect(row.locator(".card-status-text")).toHaveText("Merged");
});

test("a pause on a goblin names the goblin alone", async ({ page }) => {
  // Arrange and act
  await open(page);

  // Assert
  await expect(card(page, "Otis - Ledger Clerk").locator(".card-status-text")).toHaveText("Paused until Benny delivers");
});

test("a status takes at most two lines", async ({ page }) => {
  // Arrange and act
  await open(page);
  const status = card(page, "Wade - Waiter").locator(".card-status-text");

  // Assert
  await expect(status).toContainText("Paused until " + LONG.slice(0, 20));
  const lines = await status.evaluate((element) => Math.round(element.getBoundingClientRect().height / parseFloat(getComputedStyle(element).lineHeight)));
  expect(lines).toBeLessThanOrEqual(2);
  expect(await status.evaluate((element) => element.scrollHeight > element.clientHeight + 1)).toBe(true);
});

test("a helper's row names no parent, and its panel names the goblin it helps", async ({ page }) => {
  // Arrange
  await open(page);
  const row = card(page, "Kip - Helper");

  // Act
  await row.locator(".task-card").click();

  // Assert
  await expect(row).not.toContainText("Helper of");
  await expect(page.locator("#panel-title")).toHaveText("Kip - Helper");
  const header = await openTaskList(page);
  await expect(header).toContainText("Helper of Benny - AFK Ace");
});

test("in a column beside the CFO's panel a card's time and icons take a line under its status", async ({ page }) => {
  // Arrange and act: his window, with the CFO's panel open beside the board.
  await open(page);
  const row = card(page, "Benny - AFK Ace");

  // Assert: the name and status keep the card's width, and the time and
  // icons share a line under them, the time at the left.
  const [name, clock, status, icons, shell] = await Promise.all([".card-title", ".card-clock", ".card-status-text", ".card-icons", ".task-card-shell"].map((selector) => box(selector === ".task-card-shell" ? row : row.locator(selector))));
  expect(shell.width).toBeLessThan(360);
  await expect(row.locator(".card-title")).toHaveText("Benny - AFK Ace");
  expect(await row.locator(".card-title").evaluate((title) => title.scrollWidth <= title.clientWidth)).toBe(true);
  expect(status.height).toBeLessThan(1.5 * 22.4);
  expect(clock.y).toBeGreaterThanOrEqual(status.y + status.height - 1);
  expect(Math.abs((clock.y + clock.height / 2) - (icons.y + icons.height / 2))).toBeLessThan(6);
  expect(clock.x).toBeLessThan(name.x);
  expect(icons.x + icons.width).toBeLessThanOrEqual(shell.x + shell.width);
});

// The Overlord, 2026-10-17: "by the time, put the little clock icon". Every
// goblin's time has its clock icon at its left, centred on it, on the board
// and the canvas.
const isClockBesideItsTime = (clock: Element) => {
  const text = document.createRange();
  text.selectNode(clock.lastChild!);
  const icon = clock.querySelector(".icon")!.getBoundingClientRect(), time = text.getBoundingClientRect();
  const isWhole = (box: DOMRect) => box.width > 0 && box.height > 0;
  return isWhole(icon) && isWhole(time) && icon.right <= time.left && Math.abs((icon.top + icon.height / 2) - (time.top + time.height / 2)) < 3;
};
for (const colorScheme of ["dark", "light"] as const) {
  test(`every goblin's time has its clock icon beside it on the board and the canvas in ${colorScheme}`, async ({ page }, testInfo) => {
    // Arrange
    await page.emulateMedia({ colorScheme });
    await open(page);
    await page.getByRole("button", { name: "Close panel" }).click();
    const rows = { working: card(page, "Benny - AFK Ace"), waiting: page.locator(".task-board .task-card-shell").filter({ has: page.locator(".card-title", { hasText: LONG.slice(0, 20) }) }), paused: card(page, "Otis - Ledger Clerk"), done: card(page, "Ivy - Merge Maven") };

    // Act and assert: the board.
    for (const [kind, row] of Object.entries(rows)) {
      const clock = row.locator(".card-clock");
      await expect(clock.locator(".icon"), kind).toBeVisible();
      expect(await clock.evaluate(isClockBesideItsTime), kind).toBe(true);
      await row.screenshot({ path: testInfo.outputPath(`board-${kind}-${colorScheme}.png`) });
    }

    // Act and assert: the canvas, which draws only goblins in progress.
    await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    const node = (name: string) => page.locator(".flow-node").filter({ has: page.locator("strong").getByText(name, { exact: true }) });
    const clock = node("Benny - AFK Ace").locator(".card-clock");
    await expect(clock.locator(".icon")).toBeVisible();
    expect(await clock.evaluate(isClockBesideItsTime)).toBe(true);
    await node("Benny - AFK Ace").screenshot({ path: testInfo.outputPath(`canvas-working-${colorScheme}.png`) });
    await expect(node("Otis - Ledger Clerk")).toHaveCount(0);
  });
}

// The Overlord, 2026-10-17: "please include project repo below status line in
// board, I liked having that, and please flex wrap goblin name and title". A
// goblin's project is a quiet line of its own under its status, and its name
// and title wrap onto another line rather than end in an ellipsis.
test("a goblin's project is on its own line under its status", async ({ page }, testInfo) => {
  // Arrange and act: his window, with the CFO's panel open beside the board.
  await open(page);

  // Assert
  for (const name of ["Benny - AFK Ace", "Otis - Ledger Clerk", "Ivy - Merge Maven"]) {
    const row = card(page, name), project = row.locator(".card-repo");
    await expect(project, name).toHaveText("code-goblins");
    const [status, line] = await Promise.all([box(row.locator(".card-status-text")), box(project)]);
    expect(line.y, name).toBeGreaterThanOrEqual(status.y + status.height - 1);
    expect(Math.abs(line.x - status.x), name).toBeLessThan(20);
    expect(line.height, name).toBeLessThan(1.5 * 22.4);
    await row.screenshot({ path: testInfo.outputPath(`project-${name.split(" ")[0].toLowerCase()}.png`) });
  }
});

test("a goblin's long name and title wrap onto another line instead of being cut", async ({ page }, testInfo) => {
  // Arrange and act: a column beside the CFO's panel, too narrow for the
  // name and title on one line.
  await open(page);
  const row = card(page, "Bartholomew - Distinguished Infrastructure Archivist"), title = row.locator(".card-title");

  // Assert
  await expect(title).toBeVisible();
  const lines = await title.evaluate((element) => Math.round(element.getBoundingClientRect().height / parseFloat(getComputedStyle(element).lineHeight)));
  expect(lines).toBeGreaterThan(1);
  expect(await title.evaluate((element) => element.scrollWidth <= element.clientWidth && element.scrollHeight <= element.clientHeight)).toBe(true);
  await row.screenshot({ path: testInfo.outputPath("long-name.png") });
});

// The Overlord, 2026-10-17: a GitHub icon beside the project line that opens
// that repository's root on GitHub. A project whose origin is not on GitHub
// has none, and the rest of the line still opens the card's panel.
test("a goblin's project line opens its repository on GitHub, and a project with no GitHub remote has no mark", async ({ page }, testInfo) => {
  // Arrange
  await open(page);
  await page.getByRole("button", { name: "Close panel" }).click();
  const row = card(page, "Benny - AFK Ace"), link = row.locator(".card-repo a");

  // Act
  await link.hover();
  const tip = await page.getByRole("tooltip").textContent();
  await row.screenshot({ path: testInfo.outputPath("project-link.png") });
  const line = (await box(row.locator(".card-repo")))!;
  await page.mouse.click(line.x + 4, line.y + line.height / 2);

  // Assert
  await expect(link).toHaveAttribute("href", "https://github.com/fpresta0607/code-goblins");
  await expect(link).toHaveAttribute("target", "_blank");
  await expect(link).toHaveAttribute("aria-label", "Open fpresta0607/code-goblins on GitHub");
  expect(tip).toBe("fpresta0607/code-goblins");
  await expect(card(page, "Kip - Helper").locator(".card-repo")).toHaveText("local-tools");
  await expect(card(page, "Kip - Helper").locator(".card-repo a")).toHaveCount(0);
  await expect(page.locator("#panel-title")).toHaveText("Benny - AFK Ace");
});
