import { expect, holdStream, openTaskList, type Page, test } from "./site";

// The Overlord, 2026-10-09 about 12:00Z, with a screenshot of Bernie's panel
// in its Terminal view, 685 px wide at 150 percent: "fix header of terminal:
// this should not be skewed and so tall. Place buttons under when super
// long." The buttons kept their one row, so the name and the title were
// pressed into a column about 110 px wide, words broke in the middle and the
// header was about 400 px tall. Here is his goblin as it was: its name, its
// title and every button his header showed. The supervisor is played by one
// held snapshot.
const REPO = "https://github.com/fpresta0607/PrecisionDocs-AI";
const at = "2026-10-09T02:11:00Z";
const BERNIE = {
  id: "pd-whats-new-connectors", title: "What's new in PrecisionDocs for the connector changes; Claude Code", project: "PrecisionDocs-AI",
  goblin_name: "Bernie", goblin_title: "Merge Maestro", harness: "claude", model: "claude-opus-5-5", backend: "native",
  phase: "paused", verified: false, generation: "s-pd-whats-new-connectors", since: "2026-10-08T22:00:00Z", at,
  reason: "Paused by the Overlord; resumes on Resume",
  lifecycle: { phase: "paused", action: "pause", at, kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason: "overlord", at } },
  ticket: { number: 1516, url: REPO + "/issues/1516", state: "pr open" },
  pr: REPO + "/pull/1523",
  hosted_checks: { head: "5f52309400", state: "passed", checks: 9, at },
};
const BUTTONS = ["Open in VS Code", "Open folder", "Open Ticket #1516: pr open", "Open pull request PrecisionDocs-AI #1523", "Checks passed: All 9 checks passed"];
// A phone has no VS Code or folder button, since both open on the PC.
const PHONE_BUTTONS = BUTTONS.slice(2);

// Opens Bernie's panel at a panel width, or maximized for the full width.
async function open(page: Page, pane: number | "maximized", goblin = BERNIE) {
  await page.addInitScript((width) => {
    localStorage.setItem("cfo-pane-width", String(width === "maximized" ? 800 : width));
    localStorage.setItem("cfo-terminal-maximized", String(width === "maximized"));
    localStorage.setItem("cfo-pane-maximized", String(width === "maximized"));
  }, pane);
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [], tasks: [goblin] });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await page.locator(".task-card").filter({ hasText: "Bernie" }).click();
  await expect(page.locator("#panel-title")).toHaveText("Bernie - " + goblin.goblin_title);
  await page.evaluate(() => document.fonts.ready);
}

// How the header lays out its name, its title and its buttons.
function headerReport() {
  const header = document.querySelector<HTMLElement>(".panel-header")!;
  const name = header.querySelector<HTMLElement>("#panel-title")!;
  const title = header.querySelector<HTMLElement>(".panel-goblin-task")!;
  const actions = [...header.querySelectorAll<HTMLElement>(".panel-actions > a, .panel-actions > button")].filter((action) => action.checkVisibility());
  // The tops of the lines an element's words are drawn on, inside its box.
  const lines = (element: HTMLElement) => {
    const box = element.getBoundingClientRect(), tops = new Set<number>();
    const range = document.createRange();
    range.selectNodeContents(element);
    for (const rect of range.getClientRects()) if (rect.width > 0 && rect.top < box.bottom - 1) tops.add(Math.round(rect.top));
    return tops.size;
  };
  // The words of an element drawn over more than one line. A task id's parts,
  // joined by hyphens, wrap at a hyphen as words wrap at a space.
  const broken = (element: HTMLElement) => {
    const split: string[] = [];
    const words = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    for (let text = words.nextNode(); text; text = words.nextNode()) {
      for (const word of text.textContent!.matchAll(/[^\s-]+-*/g)) {
        const range = document.createRange();
        range.setStart(text, word.index);
        range.setEnd(text, word.index + word[0].length);
        const tops = new Set([...range.getClientRects()].filter((rect) => rect.width > 0).map((rect) => Math.round(rect.top)));
        if (tops.size > 1) split.push(word[0]);
      }
    }
    return split;
  };
  const titleBox = title.getBoundingClientRect(), nameBox = name.getBoundingClientRect();
  const boxes = actions.map((action) => action.getBoundingClientRect());
  return {
    buttons: actions.map((action) => action.getAttribute("aria-label") || action.textContent!.trim()),
    nameLines: lines(name),
    titleLines: lines(title),
    broken: [...broken(name), ...broken(title)],
    // The name or the title drawn cut short, past its box.
    cut: [name, title].filter((element) => element.scrollWidth > element.clientWidth + 1 || element.scrollHeight > element.clientHeight + 1).map((element) => element.id || element.className),
    textWidth: Math.round(Math.max(nameBox.width, titleBox.width)),
    headerWidth: Math.round(header.clientWidth),
    headerHeight: Math.round(header.getBoundingClientRect().height),
    // Every button lies beside the title's lines or wholly under them.
    besideOrUnder: boxes.every((box) => box.left >= titleBox.right - 1 || box.top >= titleBox.bottom - 1),
    // The first button under the title starts where the title starts.
    underAt: (() => {
      const under = boxes.filter((box) => box.top >= titleBox.bottom - 1);
      return under.length ? Math.round(Math.min(...under.map((box) => box.left)) - titleBox.left) : 0;
    })(),
    pageScroll: document.documentElement.scrollWidth - document.documentElement.clientWidth,
  };
}

const VIEWS = [["his window", { width: 1707, height: 1067 }, 1.5], ["a phone", { width: 390, height: 844 }, 2]] as const;

for (const [where, viewport, deviceScaleFactor] of VIEWS) {
  test.describe(`in ${where}`, () => {
    test.use({ viewport, deviceScaleFactor });

    // 360 px is the narrowest the divider lets the panel go, and 685 px the
    // width of his screenshot, taken at 150 percent. A phone shows the panel
    // across its screen.
    const widths = where === "a phone" ? ["maximized"] as const : [360, 685, 1000, "maximized"] as const;
    for (const pane of widths) {
      test(`at ${pane === "maximized" ? "full" : pane + " px"} width the panel keeps a long name and title readable, with the buttons beside or under the title`, async ({ page }, testInfo) => {
        // Arrange
        await open(page, pane);

        // Act
        const report = await page.evaluate(headerReport);
        await page.locator(".goblin-panel").screenshot({ path: testInfo.outputPath(`panel-${pane}-${viewport.width}.png`) });

        // Assert: the header over the messages and the terminal carries no
        // buttons since 2026-10-17, the task list beside or under it does.
        expect(report.buttons).toEqual([]);
        const detailsHeader = await openTaskList(page);
        const listed = await detailsHeader.evaluate((details) => [...details.querySelectorAll<HTMLElement>(".panel-actions > a, .panel-actions > button")].filter((action) => action.checkVisibility()).map((action) => action.getAttribute("aria-label") || action.textContent!.trim()));
        expect(listed).toEqual(where === "a phone" ? PHONE_BUTTONS : BUTTONS);
        expect(report.broken).toEqual([]);
        expect(report.nameLines).toBeLessThanOrEqual(2);
        expect(report.titleLines).toBeLessThanOrEqual(2);
        expect(report.cut).toEqual([]);
        expect(report.besideOrUnder).toBe(true);
        expect(report.underAt).toBe(0);
        expect(report.pageScroll).toBe(0);
      });
    }
  });
}

// A name or task id too long for its line wraps whole onto the page, with no
// tip to read it in (decision 1013298: "No panel tips, those texts move onto
// the page").
test("a long name and task id show whole on the page, with no tip", async ({ page }) => {
  // Arrange
  const name = "Merge Maestro of the Connector Release Notes";
  const id = "pd-whats-new-connectors-release-notes-for-every-connector-shipped-this-week";
  await open(page, 360, { ...BERNIE, goblin_title: name, id });
  const header = page.locator(".panel-header");

  // Act
  const report = await page.evaluate(headerReport);
  await header.locator("#panel-title").hover();
  await header.locator(".panel-goblin-task").hover();

  // Assert: both wrap rather than fit one line, and neither is cut.
  await expect(header.locator(".panel-goblin-task")).toHaveText(id);
  expect(report.nameLines).toBeGreaterThan(1);
  expect(report.titleLines).toBeGreaterThan(1);
  expect(report.cut).toEqual([]);
  await expect(page.getByRole("tooltip")).toHaveCount(0);
});
