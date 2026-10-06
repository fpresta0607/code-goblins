import { expect, test, type Page } from "./site";
import { events } from "./panel-states";

// The task panel says each thing once, in plain words: one true status in its
// header with one sentence under it, the words that sentence leaves out
// behind Details, and Changes, Workspace and Connections closed until opened,
// with no diff read before a file is opened. The tasks are the Overlord's
// 2026-10-05 screenshots, with the words their goblins really wrote.
const PATCH = "@@ -1,3 +1,4 @@\n package lifecycle\n+// resume\n \n func Resume() {}\n";

async function open(page: Page, calls: string[] = []) {
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== "/api/events") calls.push(url.pathname + url.search);
    if (url.pathname === "/api/events") return route.fulfill({ contentType: "text/event-stream", body: events() });
    if (url.pathname === "/api/workspace") return route.fulfill({ json: { project: "code-goblins", repository: "code-goblins", branch: "feat/resume-and-closed-cfo", root: "C:\\dev\\code-goblins\\.worktrees\\gb-cg-goblins-quickstart", harness: "claude", model: "Reported: claude-opus-5-5", notes: [] } });
    if (url.pathname.endsWith("/files")) return route.fulfill({ json: [{ path: "internal/lifecycle/service.go", status: "modified" }, { path: "frontend/src/workflow.ts", status: "modified" }] });
    if (url.pathname.endsWith("/diff")) return route.fulfill({ json: { path: url.searchParams.get("path"), patch: PATCH, code: "", head: "h", revision: "", fingerprint: "f", binary: false, code_omitted: false } });
    if (url.pathname.endsWith("/activity")) return route.fulfill({ json: ["2026-10-05T16:30:06Z lifecycle-failed: Requested by the operator"] });
    return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

const panel = (page: Page) => page.locator(".goblin-panel");

// A card is found by the start of its title, so a test fails on what it
// checks rather than on finding its card.
async function select(page: Page, title: string) {
  await page.locator(".task-card").filter({ has: page.locator(".card-title").filter({ hasText: title }) }).first().click();
  await expect(page.locator("#panel-title")).toContainText(title);
  const pill = page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true });
  if (await pill.count()) await pill.click();
}

// What a person reads in the panel below its title: the hidden terminal and
// every closed Details are left out, as a browser leaves them out.
const shown = (page: Page) => panel(page).evaluate((region: HTMLElement) => {
  const title = region.querySelector<HTMLElement>(".panel-identity h2")!.innerText;
  return region.innerText.replace(title, "");
});

const STATES = [
  { state: "working", title: "An OpenClaw-style quick start in the goblins command", status: "Working",
    sentence: "334's red run was not a broken merge: every red job except the aggregate was cancelled after 15 minutes without ever getting a runner. It is now on main with build and vet passing and pushed once. 289 and 300 are next, waiting on their local Go runs.",
    raw: "on main 04188dad", isFailure: false, isQuiet: true },
  { state: "waiting on you", title: "SIQstack colors and a clean browser tab for the board", status: "Waiting on the CFO",
    sentence: "The three mockups are on the Scrawl page. Reply build or say what to change.", raw: "waiting on overlord: the three mockups", isFailure: false, isQuiet: false },
  { state: "paused", title: "Memory and subscription dials in one header", status: "Paused",
    sentence: "It stays paused until you resume it. The goblin's last saved notes are kept.", raw: "Stopping-point deadline reached or request failed; no new handoff was saved", isFailure: false, isQuiet: false },
  { state: "a pause that did not finish", title: "Paused goblins resume by themselves when the reason for the pause clears", status: "Pause failed",
    sentence: "The pause did not finish, so the goblin is not paused. Its work is kept. Try Pause again.", raw: "context deadline exceeded", isFailure: true, isQuiet: true },
  { state: "failed", title: "PrecisionDocs-AI uses far fewer GitHub Actions minutes", status: "Failed",
    sentence: "Go test billing failed: TestMeteredUsage timed out after 10m0s. Log at report.log.", raw: "failed: go test ./internal/billing failed at 9f3c2a1e", isFailure: true, isQuiet: false },
];

for (const [size, viewport, scale] of [["the Overlord's window", { width: 1707, height: 960 }, 1.5], ["a phone", { width: 390, height: 844 }, 2]] as const) {
  test.describe(`at ${size}`, () => {
    test.use({ viewport, deviceScaleFactor: scale });

    // Under Working and Pause failed the Overlord wants no line (2026-10-05):
    // what it would say is the first thing behind Details.
    test("each panel says its state once, as its one status, with one plain sentence or none, and the raw words behind Details", async ({ page }) => {
      // Arrange
      await open(page);
      for (const { state, title, status, sentence, raw, isFailure, isQuiet } of STATES) {
        // Act
        await select(page, title);
        const header = panel(page).locator(".panel-header");
        const text = await shown(page);

        // Assert
        await expect(header.locator("h2"), state).toHaveText(title);
        await expect(header.locator(".panel-status"), state).toHaveText(status);
        if (isQuiet) await expect(header.locator(".panel-activity"), state).toHaveCount(0);
        else await expect(header.locator(".panel-activity"), state).toHaveText(sentence);
        expect(text.split(status).length - 1, state + ": the status is said once").toBe(1);
        expect(text, state + ": no raw words outside Details").not.toMatch(/;|\b(?=[0-9a-f]*\d)(?=[0-9a-f]*[a-f])[0-9a-f]{7,40}\b|[A-Za-z]:\\|\/tasktmp\/|deadline|^\s*(working|waiting on|blocked|failed):/im);
        await expect(header.getByRole("button", { name: "Open the log" }), state).toHaveCount(isFailure ? 1 : 0);
        await header.locator(".raw-details > summary").filter({ hasText: "Details" }).click();
        await expect(header.locator(".raw-details-text"), state).toContainText(raw);
        if (isQuiet) await expect(header.locator(".raw-details-text p").first(), state).toHaveText(sentence);
        await header.locator(".raw-details > summary").filter({ hasText: "Details" }).click();
        expect((await panel(page).locator(".lifecycle-details").allInnerTexts()).join(" "), state).not.toMatch(/Failed|Needs attention|Paused at/);
      }
    });

    test("the Tasks list adds no wait line, keeps its chip on one line, and shows its titles without the harness", async ({ page }) => {
      // Arrange
      await open(page);
      const tasks = page.getByRole("region", { name: "Tasks", exact: true });
      const card = (title: string) => tasks.locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText(title, { exact: true }) });
      const blocked = "A very quick tour for new users: how Code Goblins works and how to use the board, on first open";

      // Act
      const next = card("Updates arrive as their own special Overlord command in the Command Center, with one Update button (checksum-verified, safe swap and rollback, goblins untouched, the desktop window too)").locator(".next-chip");

      // Assert: the Overlord, 2026-10-05: "waits for a task doesnt make sens
      // dont ened any addiioantional text". A waiting card says nothing about
      // its wait, and the CFO's note on it stays in its panel behind More.
      await expect(tasks.locator(".card-title")).toHaveText([
        blocked,
        "Updates arrive as their own special Overlord command in the Command Center, with one Update button (checksum-verified, safe swap and rollback, goblins untouched, the desktop window too)",
        "The already-pushed skip design for tests-kept and the other open hardening items",
      ]);
      await expect(page.locator(".task-board")).not.toContainText(/Waits (for|until)/);
      await expect(tasks).not.toContainText("cg-hardening retired");
      await expect(tasks).not.toContainText("default first-open layout");
      await expect(next).toHaveText("Next at 5 GB");
      expect(await next.evaluate((chip) => chip.scrollWidth <= chip.clientWidth && chip.getClientRects().length === 1 && chip.getBoundingClientRect().height < 2 * parseFloat(getComputedStyle(chip).lineHeight))).toBe(true);
      await expect(page.locator(".task-board")).not.toContainText("; Claude Code");

      // Act: open the waiting task.
      await select(page, blocked);
      const header = panel(page).locator(".panel-header");

      // Assert: its status alone, and its note behind More.
      await expect(header.locator(".panel-status")).toHaveText("Not started");
      await expect(header.locator(".panel-activity")).toHaveCount(0);
      await expect(header).not.toContainText(/Waits (for|until)/);
      await header.locator(".raw-details > summary").filter({ hasText: "More" }).click();
      await expect(header.locator(".raw-details-text")).toHaveText("default first-open layout must land and the generated mockup must be approved");
    });
  });
}

test("opening a task panel reads no diff; Changes reads its files when opened, and a file's diff when that file is opened", async ({ page }) => {
  // Arrange
  const calls: string[] = [];
  await open(page, calls);
  const reads = (kind: string) => calls.filter((call) => call.includes("/" + kind + "?"));

  // Act
  await select(page, "An OpenClaw-style quick start in the goblins command");
  await expect(panel(page).getByRole("group", { name: /^Controls for / })).toBeVisible();
  await page.waitForTimeout(500);

  // Assert: nothing about the change set is read while Changes is closed.
  expect(reads("files")).toEqual([]);
  expect(reads("diff")).toEqual([]);
  const changes = panel(page).locator("details.changes-section");
  await expect(changes).not.toHaveAttribute("open");

  // Act: open Changes.
  await changes.locator("> summary").click();

  // Assert: its summary comes first, with the pull request's files on
  // GitHub, and no file is open or read.
  await expect(changes.locator(".section-toolbar p")).toHaveText("Full task changes · 2 files");
  await expect(changes.getByRole("link", { name: "Files on GitHub" })).toHaveAttribute("href", "https://github.com/fpresta0607/code-goblins/pull/334/files");
  await expect(changes.locator("details.file-review[open]")).toHaveCount(0);
  expect(reads("files")).toHaveLength(1);
  expect(reads("diff")).toEqual([]);

  // Act: open one file.
  await changes.locator("details.file-review > summary").filter({ hasText: "frontend/src/workflow.ts" }).click();

  // Assert: only that file's diff is read.
  await expect(changes.locator(".diff-view")).toHaveCount(1);
  expect(reads("diff")).toEqual(["/api/tasks/cg-goblins-quickstart/diff?revision=&path=frontend%2Fsrc%2Fworkflow.ts"]);
});

test("Workspace and Connections are closed sections like those below them, and Workspace opens on what it showed", async ({ page }) => {
  // Arrange
  await open(page);
  await select(page, "An OpenClaw-style quick start in the goblins command");
  const sections = panel(page).locator(".panel-content > details.disclosure > summary");

  // Assert
  await expect(sections).toHaveText(["Workspace", "Connections", "Changes", "Activity", "History"]);
  await expect(panel(page).locator(".panel-content > details.disclosure[open]")).toHaveCount(0);

  // Act
  await sections.filter({ hasText: "Workspace" }).click();

  // Assert
  const workspace = panel(page).locator("details.workspace-details");
  await expect(workspace.locator("dt")).toHaveText(["Repository", "Branch", "Working folder"]);
  await expect(workspace.locator("dd")).toHaveText(["code-goblins", "feat/resume-and-closed-cfo", "C:\\dev\\code-goblins\\.worktrees\\gb-cg-goblins-quickstart"]);
});

test("a failure's Open the log opens Activity and reads the log", async ({ page }) => {
  // Arrange
  const calls: string[] = [];
  await open(page, calls);
  await select(page, "Paused goblins resume by themselves when the reason for the pause clears");

  // Act
  await panel(page).getByRole("button", { name: "Open the log" }).click();

  // Assert
  await expect(panel(page).locator("#task-activity")).toHaveAttribute("open");
  await expect(panel(page).locator(".activity-list")).toContainText("lifecycle-failed");
  expect(calls).toContain("/api/tasks/cg-fleet-auto-resume/activity");
});
