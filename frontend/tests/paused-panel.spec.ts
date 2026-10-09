import { expect, holdStream, test, type Page } from "./site";
import { caret, nativeMarkers } from "./caret";

// The Overlord, 2026-10-09 about 13:50Z, with a screenshot of Bernie's paused
// panel in its Task view: "in paused goblin panels the highlight line, it's
// not needed to be presented, and then the stopped resources dropdown, make
// that an actual caret that matches the caret below it". Under Paused by you
// the panel said "It stays paused until you resume it.", which its status
// already says, and Stopped resources opened on the browser's own triangle
// while Workspace under it turns the board's chevron. Later that day, on a
// panel whose Details read what its pause could not do: "every time I look at
// the details it is the same text ... that line that says it resumes by
// itself, that should be part of the details. It should just be a well
// written, proper case, human readable little description about what that
// agent did or is doing. Not what you're seeing currently in this terminal
// window." Here is his goblin as its lifecycle record kept it, with the
// buttons his header showed and the last thing it reported. The supervisor is
// played by one held snapshot.
const REPO = "https://github.com/fpresta0607/PrecisionDocs-AI";
const at = "2026-10-09T07:11:36Z";
const HEAD = "@" + "a".repeat(40);
// What a pause that missed its handoff and its teardown leaves in its record,
// which the CFO hears and the panel never shows.
const PROBLEMS = ["Stopping-point deadline reached or request failed; no new handoff was saved", "its terminal ended, but the rest of its stop did not finish: context deadline exceeded"];
const BERNIE = {
  id: "pd-whats-new", title: "What's new in PrecisionDocs for the connector changes; Claude Code", project: "PrecisionDocs-AI",
  goblin_name: "Bernie", goblin_title: "Merge Maestro", harness: "claude", model: "claude-opus-5-5", backend: "native",
  phase: "paused", verified: false, generation: "s1791515797213332900", since: "2026-10-08T22:00:00Z", at,
  reason: "Paused by the Overlord; resumes on Resume", activity: "lifecycle-paused: overlord; worktree C:\\dev\\code-goblins\\worktrees\\PrecisionDocs-AI\\pd-whats-new; task session and branch",
  last_report: "working: PR 1523 is green at 5f52309400; answering the two review notes on the connector page",
  lifecycle: { phase: "paused", action: "pause", at, handoff_saved: false, validation_restarts: true,
    kept: ["worktree C:\\dev\\code-goblins\\worktrees\\PrecisionDocs-AI\\pd-whats-new", "task session and branch"],
    stopped: ["terminal host pid 27236"],
    problems: [...PROBLEMS, "gate commits are pinned locally but could not merge into the task branch; worktree and run are kept for conflict resolution"],
    pause: { reason: "overlord", at } },
  ticket: { number: 1516, url: REPO + "/issues/1516", state: "pr open" },
  pr: REPO + "/pull/1523",
  hosted_checks: { head: "5f52309400", state: "passed", checks: 9, at },
};

// A goblin paused for one reason, or before pauses had reasons, whose pause
// left the same record as every other and who last reported work of its own.
const paused = (name: string, pause?: { reason: string; until?: string }) => ({
  id: "paused-" + name.toLowerCase(), title: name + "'s fixture task", project: "code-goblins", goblin_name: name, goblin_title: "Fixture",
  harness: "claude", phase: "paused", verified: false, generation: "s-" + name.toLowerCase(), since: at, at,
  activity: "lifecycle-paused: " + (pause?.reason || "paused") + "; worktree C:\\dev\\code-goblins\\worktrees\\code-goblins\\" + name.toLowerCase() + "; task session and branch",
  last_report: `working: ${name} wrote the tests at frontend/tests/${name.toLowerCase()}.spec.ts; the fixes come next`,
  lifecycle: { phase: "paused", action: "pause", at, kept: [], stopped: [], problems: PROBLEMS, handoff_saved: false, validation_restarts: false, ...(pause && { pause: { until: "", at, ...pause } }) },
});
// What that goblin's Details says it did.
const did = (name: string) => `${name} wrote the tests at ${name.toLowerCase()}.spec.ts. The fixes come next.`;

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
    });

    test("a paused goblin's Details is one plain description of what it did last, then what it waits for", async ({ page }) => {
      // Arrange: a goblin paused for memory, for a task, for a time, for CI
      // and by him, and one paused before pauses had reasons.
      await open(page, [BERNIE, ...PAUSES.map(([name, pause]) => paused(name, pause)), paused("Old")], "Bernie");
      const header = panel(page).locator(".panel-header");
      const details = header.locator(".raw-details-text");
      const cases: [string, string, string, RegExp][] = [
        ["Mabel", "Memory: resumes at 5 GB free", did("Mabel"), /^It resumes by itself once 5 GB of memory is free\.$/],
        ["Tansy", "Waiting on Bernie - Merge Maestro", did("Tansy"), /^It resumes by itself when Bernie - Merge Maestro finishes\.$/],
        ["Datura", "Resumes ", did("Datura"), /^It resumes by itself on Oct \d+, \d+:00\s[AP]M\.$/],
        ["Cogsworth", "Waiting on CI", did("Cogsworth"), /^It resumes by itself when its CI run finishes\.$/],
        ["Bernie", "Paused by you", "PR 1523 is green. Answering the two review notes on the connector page.", /^It stays paused until you resume it\.$/],
        ["Old", "Paused", did("Old"), /^It stays paused until you resume it\.$/],
      ];
      const read: string[] = [];

      for (const [name, status, work, waits] of cases) {
        // Act
        await select(page, name);
        await header.locator(".raw-details > summary").click();
        const text = await details.innerText();

        // Assert: what the goblin last reported, in sentences, then what it
        // waits for, with nothing of the pause's own record, as the panel's
        // prose and not as a terminal's lines.
        await expect(header.locator(".panel-status"), name).toContainText(status);
        await expect(header.locator(".panel-activity"), name).toHaveCount(0);
        expect(text.startsWith(work + " "), name + ": " + text).toBe(true);
        expect(text.slice(work.length + 1), name).toMatch(waits);
        expect(text, name).not.toMatch(/;|deadline|handoff|did not finish|lifecycle|pid \d|[A-Za-z]:\\/);
        await expect(details.locator("p"), name).toHaveCount(1);
        expect(await details.evaluate((element) => getComputedStyle(element).fontFamily === getComputedStyle(document.body).fontFamily), name).toBe(true);
        read.push(text);
        await header.locator(".raw-details > summary").click();
      }

      // Assert: no two goblins' Details read the same.
      expect(new Set(read).size).toBe(cases.length);
    });

    test("Stopped resources starts closed and opens with the panel's caret, never the browser's marker", async ({ page }) => {
      // Arrange
      await open(page, [BERNIE], "Bernie");
      const stopped = panel(page).locator(".lifecycle-details details").filter({ hasText: "Stopped resources (1)" });
      const summary = stopped.locator("> summary");
      const workspace = panel(page).locator(".panel-content > details").filter({ has: page.locator("> summary", { hasText: "Workspace" }) }).locator("> summary");

      const details = panel(page).locator(".panel-header .raw-details > summary");

      // Assert: no dropdown in the panel draws the browser's marker, and
      // Stopped resources and Details draw Workspace's caret, closed.
      expect(await nativeMarkers(panel(page))).toEqual([]);
      await expect(stopped).not.toHaveAttribute("open");
      await expect(panel(page).getByText("terminal host pid 27236")).toBeHidden();
      const closed = await caret(workspace);
      expect(closed).not.toBeNull();
      expect(await caret(summary)).toEqual(closed);
      await expect(details).toHaveText("Details");
      expect(await caret(details)).toEqual(closed);

      // Act: open all three, Stopped resources from the keyboard.
      await workspace.click();
      await details.click();
      await summary.focus();
      await page.keyboard.press("Enter");

      // Assert: it opens, and each caret turns as Workspace's does.
      await expect(stopped).toHaveAttribute("open");
      await expect(panel(page).getByText("terminal host pid 27236")).toBeVisible();
      const opened = await caret(workspace);
      expect(opened).not.toEqual(closed);
      expect(await caret(summary)).toEqual(opened);
      expect(await caret(details)).toEqual(opened);

      // Act: close it again from the keyboard.
      await page.keyboard.press("Space");

      // Assert
      await expect(stopped).not.toHaveAttribute("open");
      expect(await caret(summary)).toEqual(closed);
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
