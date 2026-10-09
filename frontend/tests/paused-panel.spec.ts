import { expect, holdStream, test, type Locator, type Page } from "./site";

// The Overlord, 2026-10-09 about 13:50Z, with a screenshot of Bernie's paused
// panel in its Task view: "in paused goblin panels the highlight line, it's
// not needed to be presented, and then the stopped resources dropdown, make
// that an actual caret that matches the caret below it". Under Paused by you
// the panel said "It stays paused until you resume it.", which its status
// already says, and Stopped resources opened on the browser's own triangle
// while Workspace under it turns the board's chevron. Here is his goblin as
// its lifecycle record kept it, with the buttons his header showed. The
// supervisor is played by one held snapshot.
const REPO = "https://github.com/fpresta0607/PrecisionDocs-AI";
const at = "2026-10-09T07:11:36Z";
const HEAD = "@" + "a".repeat(40);
const BERNIE = {
  id: "pd-whats-new", title: "What's new in PrecisionDocs for the connector changes; Claude Code", project: "PrecisionDocs-AI",
  goblin_name: "Bernie", goblin_title: "Merge Maestro", harness: "claude", model: "claude-opus-5-5", backend: "native",
  phase: "paused", verified: false, generation: "s1791515797213332900", since: "2026-10-08T22:00:00Z", at,
  reason: "Paused by the Overlord; resumes on Resume",
  lifecycle: { phase: "paused", action: "pause", at, handoff_saved: false, validation_restarts: true,
    kept: ["worktree C:\\dev\\code-goblins\\worktrees\\PrecisionDocs-AI\\pd-whats-new", "task session and branch"],
    stopped: ["terminal host pid 27236"],
    problems: ["Stopping-point deadline reached or request failed; no new handoff was saved", "gate commits are pinned locally but could not merge into the task branch; worktree and run are kept for conflict resolution"],
    pause: { reason: "overlord", at } },
  ticket: { number: 1516, url: REPO + "/issues/1516", state: "pr open" },
  pr: REPO + "/pull/1523",
  hosted_checks: { head: "5f52309400", state: "passed", checks: 9, at },
};

// A goblin paused for one reason, or before pauses had reasons.
const paused = (name: string, pause?: { reason: string; until?: string }) => ({
  id: "paused-" + name.toLowerCase(), title: name + "'s fixture task", project: "code-goblins", goblin_name: name, goblin_title: "Fixture",
  harness: "claude", phase: "paused", verified: false, generation: "s-" + name.toLowerCase(), since: at, at,
  lifecycle: { phase: "paused", action: "pause", at, kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, ...(pause && { pause: { until: "", at, ...pause } }) },
});

async function open(page: Page, goblins: object[], name: string) {
  await page.clock.setFixedTime(Date.parse("2026-10-09T13:50:00Z"));
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [], tasks: goblins });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await select(page, name);
  await page.evaluate(() => document.fonts.ready);
}

async function select(page: Page, name: string) {
  await page.locator(".task-card").filter({ hasText: name }).first().click();
  await expect(page.locator("#panel-title")).toContainText(name);
  const pill = page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true });
  if (await pill.count()) await pill.click();
}

const panel = (page: Page) => page.locator(".goblin-panel");

// Each pause reason the supervisor records, by the status the panel reads
// for it. Every one says what resumes the goblin, so nothing under it does.
const PAUSES: [string, { reason: string; until?: string } | undefined, string | RegExp][] = [
  ["Mabel", { reason: "memory" }, "Memory: resumes at 5 GB free"],
  ["Nettle", { reason: "allowance", until: "2026-10-10T07:00:00Z" }, /^Allowance: resumes /],
  ["Quill", { reason: "question", until: "q-7" }, "Waiting for your answer"],
  ["Cogsworth", { reason: "ci", until: "run:https://github.com/o/r/actions/runs/9" + HEAD }, "Waiting on CI"],
  ["Dross", { reason: "deploy", until: "run:https://github.com/o/r/actions/runs/9" + HEAD }, "Waiting on deploy"],
  ["Pip", { reason: "dependency", until: "pr:https://github.com/o/r/pull/331" }, "Waiting on PR #331 to merge"],
  ["Datura", { reason: "dependency", until: "date:2026-10-10T09:00:00Z" }, /^Resumes /],
  ["Tansy", { reason: "dependency", until: "task:pd-whats-new" }, "Waiting on Bernie - Merge Maestro"],
];

// The caret a summary draws as a person sees it: its shape, its size, its
// color, its weight and how far it is turned.
const caret = (summary: Locator) => summary.evaluate((element) => {
  const icon = element.querySelector(":scope > svg");
  if (!icon) return null;
  const style = getComputedStyle(icon);
  return { shape: icon.innerHTML, width: style.width, height: style.height, color: style.color, weight: style.strokeWidth, turn: style.transform };
});

// What Chromium tells a screen reader about the element a selector finds.
async function announced(page: Page, selector: string) {
  const cdp = await page.context().newCDPSession(page);
  const { result } = await cdp.send("Runtime.evaluate", { expression: `document.querySelector(${JSON.stringify(selector)})` });
  const { nodes: [node] } = await cdp.send("Accessibility.getPartialAXTree", { objectId: result.objectId, fetchRelatives: false });
  await cdp.detach();
  return { role: node.role?.value, name: node.name?.value, expanded: node.properties?.find((property) => property.name === "expanded")?.value.value ?? false };
}

for (const [size, viewport, scale] of [["the Overlord's window", { width: 1707, height: 960 }, 1.5], ["a phone", { width: 390, height: 844 }, 2]] as const) {
  test.describe(`at ${size}`, () => {
    test.use({ viewport, deviceScaleFactor: scale });

    test("a paused panel has no line under its status that only repeats it", async ({ page }) => {
      // Arrange
      await open(page, [BERNIE, ...PAUSES.map(([name, pause]) => paused(name, pause))], "Bernie");
      const header = panel(page).locator(".panel-header");

      for (const [name, status] of [["Bernie", "Paused by you"], ...PAUSES.map(([name, , status]) => [name, status])] as [string, string | RegExp][]) {
        // Act
        await select(page, name);

        // Assert: the status says why it waits and what resumes it, and no
        // line under it says so again.
        await expect(header.locator(".panel-status"), name).toHaveText(status);
        await expect(header.locator(".panel-activity"), name).toHaveCount(0);
        await expect(header, name).not.toContainText(/It (stays paused|resumes by itself)/);
      }

      // Assert: Bernie's Details still hold what his pause could not do.
      await select(page, "Bernie");
      await header.locator(".raw-details > summary").click();
      await expect(header.locator(".raw-details-text")).toContainText("no new handoff was saved");
    });

    test("a goblin paused before pauses had reasons keeps the line its bare Paused leaves out", async ({ page }) => {
      // Arrange
      await open(page, [paused("Old")], "Old");
      const header = panel(page).locator(".panel-header");

      // Assert
      await expect(header.locator(".panel-status")).toHaveText("Paused");
      await expect(header.locator(".panel-activity")).toHaveText("It stays paused until you resume it.");
    });

    test("Stopped resources starts closed and opens with the panel's caret, never the browser's marker", async ({ page }) => {
      // Arrange
      await open(page, [BERNIE], "Bernie");
      const stopped = panel(page).locator(".lifecycle-details details").filter({ hasText: "Stopped resources (1)" });
      const summary = stopped.locator("> summary");
      const workspace = panel(page).locator(".panel-content > details").filter({ has: page.locator("> summary", { hasText: "Workspace" }) }).locator("> summary");

      // Assert: no dropdown in the panel draws the browser's marker, and
      // Stopped resources draws Workspace's caret, closed.
      expect(await panel(page).locator("summary").evaluateAll((summaries) => summaries.filter((element) => {
        const style = getComputedStyle(element);
        return style.display === "list-item" && style.listStyleType !== "none";
      }).map((element) => element.textContent))).toEqual([]);
      await expect(stopped).not.toHaveAttribute("open");
      await expect(panel(page).getByText("terminal host pid 27236")).toBeHidden();
      const closed = await caret(workspace);
      expect(closed).not.toBeNull();
      expect(await caret(summary)).toEqual(closed);

      // Act: open both, Stopped resources from the keyboard.
      await workspace.click();
      await summary.focus();
      await page.keyboard.press("Enter");

      // Assert: it opens, and its caret turns as Workspace's does.
      await expect(stopped).toHaveAttribute("open");
      await expect(panel(page).getByText("terminal host pid 27236")).toBeVisible();
      await expect.poll(() => caret(workspace)).not.toEqual(closed);
      const opened = await caret(workspace);
      await expect.poll(() => caret(summary)).toEqual(opened);

      // Act: close it again from the keyboard.
      await page.keyboard.press("Space");

      // Assert
      await expect(stopped).not.toHaveAttribute("open");
      await expect.poll(() => caret(summary)).toEqual(closed);
    });
  });
}

test("a screen reader hears Stopped resources as a closed disclosure that opens", async ({ page }) => {
  // Arrange
  await open(page, [BERNIE], "Bernie");
  const selector = ".lifecycle-details details > summary";

  // Assert
  expect(await announced(page, selector)).toEqual({ role: "DisclosureTriangle", name: "Stopped resources (1)", expanded: false });

  // Act
  await page.locator(selector).focus();
  await page.keyboard.press("Enter");

  // Assert
  await expect.poll(() => announced(page, selector)).toEqual({ role: "DisclosureTriangle", name: "Stopped resources (1)", expanded: true });
});
