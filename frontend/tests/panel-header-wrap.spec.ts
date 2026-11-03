import { expect, holdStream, openTaskList, type Page, test } from "./site";
import { NAME, open as openBoard, OTHER } from "./session-board";

// The Overlord, 2026-10-09 about 12:00Z, with a screenshot of Bernie's panel
// in its Terminal view, 685 px wide at 150 percent: "fix header of terminal:
// this should not be skewed and so tall. Place buttons under when super
// long." The buttons kept their one row, so the name and the title were
// pressed into a column about 110 px wide, words broke in the middle and the
// header was about 400 px tall. Here is his goblin as it was: its name, its
// title and every button his header showed. Since 2026-10-29 the header is
// one short row ("Minimize height and maximize the space"), so a name or
// title too long for it ends in an ellipsis there and shows whole in the
// panel's Task view, and since #1987 the row holds no task id: the Task view
// has it beside the whole name. The supervisor is played by one held
// snapshot.
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

// How the header lays out its name and its buttons.
function headerReport() {
  const header = document.querySelector<HTMLElement>(".panel-header")!;
  const name = header.querySelector<HTMLElement>("#panel-title")!;
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
  const nameBox = name.getBoundingClientRect();
  const isCut = (element: HTMLElement) => element.scrollWidth > element.clientWidth + 1 || element.scrollHeight > element.clientHeight + 1;
  const isEllipsis = (element: HTMLElement) => getComputedStyle(element).textOverflow === "ellipsis";
  // The picture's button, the selector's buttons and the row's controls, each
  // a target of its own, but the CFO's AFK switch, a labelled pill whose size
  // #1987 leaves as it was.
  const targets = [...header.querySelectorAll<HTMLElement>(".panel-style-button, .panel-view-switch button, .panel-top button:not(.afk-toggle)")].filter((target) => target.checkVisibility());
  const label = (target: HTMLElement) => target.getAttribute("aria-label") || target.textContent!.trim();
  const middle = nameBox.top + nameBox.height / 2;
  return {
    buttons: actions.map((action) => action.getAttribute("aria-label") || action.textContent!.trim()),
    nameLines: lines(name),
    broken: broken(name),
    // The name drawn cut short past its box with no ellipsis, or ended by one.
    cut: [name].filter((element) => isCut(element) && !isEllipsis(element)).map((element) => element.id),
    shortened: [name].filter((element) => isCut(element) && isEllipsis(element)).map((element) => element.id),
    // The name's middle within the height of every target, so all are on one row.
    isOneRow: targets.every((target) => { const box = target.getBoundingClientRect(); return middle > box.top && middle < box.bottom; }),
    small: targets.filter((target) => { const box = target.getBoundingClientRect(); return box.width < 44 || box.height < 44; }).map(label),
    unnamed: targets.filter((target) => !label(target)).length,
    tips: [...header.querySelectorAll<HTMLElement>("[title]")].map((element) => element.title),
    textWidth: Math.round(nameBox.width),
    headerWidth: Math.round(header.clientWidth),
    headerHeight: Math.round(header.getBoundingClientRect().height),
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
      test(`at ${pane === "maximized" ? "full" : pane + " px"} width the panel keeps its name and title on its header's one row and its buttons in its task list`, async ({ page }, testInfo) => {
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
        expect(report.nameLines).toBe(1);
        expect(report.isOneRow).toBe(true);
        expect(report.cut).toEqual([]);
        expect(report.small).toEqual([]);
        expect(report.tips).toEqual([]);
        await expect(page.locator(".panel-header")).not.toContainText(BERNIE.id);
        await expect(detailsHeader.locator(".panel-whole-name")).toHaveText("Bernie - Merge Maestro · pd-whats-new-connectors");
        expect(report.pageScroll).toBe(0);
      });
    }
  });
}

// A name too long for the header's row ends in an ellipsis there, and shows
// whole with its task id in the Task view, with no tip to read it in
// (decision 1013298: "No panel tips, those texts move onto the page").
test("a long name is cut short in the header and shows whole with its task id in the Task view, with no tip", async ({ page }) => {
  // Arrange
  const name = "Merge Maestro of the Connector Release Notes";
  const id = "pd-whats-new-connectors-release-notes-for-every-connector-shipped-this-week";
  await open(page, 360, { ...BERNIE, goblin_title: name, id });
  const header = page.locator(".panel-header");

  // Act
  const report = await page.evaluate(headerReport);
  await header.locator("#panel-title").hover();

  // Assert: the name keeps to the row, an ellipsis ends it, the row has no
  // task id, and the Task view has both whole.
  await expect(header).not.toContainText(id);
  expect(report.nameLines).toBe(1);
  expect(report.cut).toEqual([]);
  expect(report.shortened).toEqual(["panel-title"]);
  await expect(page.getByRole("tooltip")).toHaveCount(0);
  await expect((await openTaskList(page)).locator(".panel-whole-name")).toHaveText("Bernie - " + name + " · " + id);
});

// rowOffers answers the drawing of a control of the header's row, its icon
// button or its item in the row's More, which is closed again, or "" where
// neither shows.
async function rowOffers(page: Page, label: string, name: string): Promise<string> {
  const row = page.locator(".context-pane .panel-top");
  const button = row.getByRole("button", { name: label, exact: true });
  if (await button.count()) return await button.locator("path").first().getAttribute("d") ?? "";
  const more = row.getByRole("button", { name: "More", exact: true });
  if (!await more.count()) return "";
  await more.click();
  const item = row.getByRole("menuitem", { name, exact: true });
  const drawing = await item.count() ? await item.locator("path").first().getAttribute("d") ?? "" : "";
  await more.click();
  await expect(row.getByRole("menuitem")).toHaveCount(0);
  return drawing;
}

// styleOffers answers the drawings of Modern chat and Terminal in the menu
// the session's picture opens, and the name of the one it checks, and closes
// it again.
async function styleOffers(page: Page): Promise<{ modern: string; terminal: string; checked: string }> {
  const header = page.locator(".context-pane .panel-header");
  await header.getByRole("button", { name: "Chat style", exact: true }).click();
  const drawing = async (name: string) => await header.getByRole("menuitemradio", { name, exact: true }).locator("path").first().getAttribute("d") ?? "";
  const offers = { modern: await drawing("Modern chat"), terminal: await drawing("Terminal"), checked: await header.getByRole("menuitemradio", { checked: true }).innerText() };
  await page.keyboard.press("Escape");
  await expect(header.getByRole("menuitemradio")).toHaveCount(0);
  return offers;
}

// The CFO and two goblins in both styles, on a phone, a narrow panel and a
// wide one (#1987): the name keeps the header's one row, every control there
// is a named 44 px target with no tip, the picture offers both styles with
// the one shown checked, Open in Windows Terminal is in the row or its More,
// and the Chat icon is neither style's.
const IVY = OTHER.goblin_name + " - " + OTHER.goblin_title;
const LAYOUTS = [["a phone", { width: 390, height: 844 }, 0], ["a narrow panel", { width: 1440, height: 900 }, 360], ["a wide panel", { width: 1707, height: 1067 }, 1000]] as const;
for (const style of ["modern", "classic"] as const) {
  test.describe(`in ${style}`, () => {
    test.use({ chatStyle: style });
    for (const [where, viewport, pane] of LAYOUTS) {
      test(`in ${where} the CFO and both goblins keep their name on the header's one row, with both styles on the picture and Open in Windows Terminal at hand`, async ({ page }) => {
        // Arrange
        await page.setViewportSize(viewport);
        if (pane) await page.addInitScript((width) => localStorage.setItem("cfo-pane-width", String(width)), pane);
        await openBoard(page);

        for (const [key, name] of [["1", "CFO"], ["2", NAME], ["3", IVY], ["1", "CFO"]] as const) {
          // Act
          await page.keyboard.press("Control+Alt+" + key);
          await expect(page.locator("#panel-title")).toHaveText(name);
          // In Terminal style the session's terminal takes the keyboard once
          // it is whole, so the picture's menu opens after that.
          if (style === "classic") await expect(page.locator(".xterm-helper-textarea:focus")).toHaveCount(1);
          await page.evaluate(() => document.fonts.ready);
          const report = await page.evaluate(headerReport);
          const styles = await styleOffers(page);
          const windowIcon = await rowOffers(page, "Open in Windows Terminal", "Open in terminal");
          const chatIcon = await page.locator(".panel-header").getByRole("button", { name: "Chat", exact: true }).locator("path").first().getAttribute("d");

          // Assert
          expect(report.nameLines, name).toBe(1);
          expect(report.isOneRow, name).toBe(true);
          expect(report.cut, name).toEqual([]);
          expect(report.small, name).toEqual([]);
          expect(report.unnamed, name).toBe(0);
          expect(report.tips, name).toEqual([]);
          expect(report.pageScroll, name).toBe(0);
          expect(styles.checked, name + "'s picture checks the style shown").toBe(style === "modern" ? "Modern chat" : "Terminal");
          expect(styles.modern, name + "'s styles are drawn apart").not.toBe(styles.terminal);
          expect([styles.modern, styles.terminal], name + "'s Chat is neither style's icon").not.toContain(chatIcon);
          // A phone offers Windows Terminal only while the box shows, as the
          // box's own menu did before.
          expect(windowIcon !== "", name + " offers Open in Windows Terminal").toBe(style === "modern" || where !== "a phone");
        }
      });
    }
  });
}
