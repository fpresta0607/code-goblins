import { expect, holdStream, openTaskList, type Page, test } from "./site";
import { NAME, open as openBoard, OTHER } from "./session-board";

// The Overlord, 2026-10-09 about 12:00Z, with a screenshot of Bernie's panel
// in its Terminal view, 685 px wide at 150 percent: "fix header of terminal:
// this should not be skewed and so tall. Place buttons under when super
// long." The buttons kept their one row, so the name and the title were
// pressed into a column about 110 px wide, words broke in the middle and the
// header was about 400 px tall. Here is his goblin as it was: its name, its
// title and every button his header showed. Since 2026-10-29 the header is
// one short row ("Minimize height and maximize the space"), since #1987 the
// row holds no task id, which the Task view has beside the whole name, and
// since 2026-11-04 a name or title too long for the row wraps onto more lines
// in a taller header, never cut. The supervisor is played by one held
// snapshot.
const REPO = "https://github.com/fpresta0607/PrecisionDocs-AI";
const at = "2026-10-09T02:11:00Z";
const BERNIE = {
  id: "pd-whats-new-connectors", title: "What's new in PrecisionDocs for the connector changes; Claude Code", project: "PrecisionDocs-AI",
  goblin_name: "Bernie", goblin_title: "Merge Maestro", harness: "claude", model: "claude-opus-5-5", backend: "native",
  phase: "paused", verified: false, generation: "s-pd-whats-new-connectors", since: "2026-10-08T22:00:00Z", at,
  reason: "Paused by the Overlord; resumes on Resume",
  lifecycle: { phase: "paused", action: "pause", at, kept: [], stopped: [], problems: [], handoff_saved: true, validation_kept: false, pause: { reason: "overlord", at } },
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
    localStorage.setItem("ceo-pane-width", String(width === "maximized" ? 800 : width));
    localStorage.setItem("ceo-terminal-maximized", String(width === "maximized"));
    localStorage.setItem("ceo-pane-maximized", String(width === "maximized"));
  }, pane);
  await holdStream(page, { healthy: true, instance: "fixture", ceo_runs: true, ceo_harness: "claude", revision: 1, attention: [], tasks: [goblin] });
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
  // The words of an element drawn over more than one line that one line of it
  // could hold whole: a word wider than its line has nowhere else to go. A task
  // id's parts, joined by hyphens, wrap at a hyphen as words wrap at a space.
  const broken = (element: HTMLElement) => {
    const split: string[] = [];
    const words = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    for (let text = words.nextNode(); text; text = words.nextNode()) {
      for (const word of text.textContent!.matchAll(/[^\s-]+-*/g)) {
        const range = document.createRange();
        range.setStart(text, word.index);
        range.setEnd(text, word.index + word[0].length);
        const drawn = [...range.getClientRects()].filter((rect) => rect.width > 0);
        const isWhole = new Set(drawn.map((rect) => Math.round(rect.top))).size === 1;
        const width = drawn.reduce((sum, rect) => sum + rect.width, 0);
        if (!isWhole && width <= element.getBoundingClientRect().width) split.push(word[0]);
      }
    }
    return split;
  };
  const nameBox = name.getBoundingClientRect();
  const isCut = (element: HTMLElement) => element.scrollWidth > element.clientWidth + 1 || element.scrollHeight > element.clientHeight + 1;
  const isEllipsis = (element: HTMLElement) => getComputedStyle(element).textOverflow === "ellipsis";
  // The picture's button, the selector's buttons and the row's controls, each
  // a target of its own, but the CEO's AFK switch, a labelled pill whose size
  // #1987 leaves as it was.
  const targets = [...header.querySelectorAll<HTMLElement>(".panel-style-button, .panel-view-switch button, .panel-top button")].filter((target) => target.checkVisibility());
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
        if (pane === 360 || where === "a phone") expect(report.nameLines, "the row has too little room for it").toBeGreaterThan(1);
        else expect(report.nameLines, "the row has room for it").toBe(1);
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

// A name too long for the header's row wraps there, never cut, and shows
// whole with its task id in the Task view, with no tip to read it in
// (decision 1013298: "No panel tips, those texts move onto the page").
test("a long name wraps whole in the header and shows with its task id in the Task view, with no tip", async ({ page }) => {
  // Arrange
  const name = "Merge Maestro of the Connector Release Notes";
  const id = "pd-whats-new-connectors-release-notes-for-every-connector-shipped-this-week";
  await open(page, 360, { ...BERNIE, goblin_title: name, id });
  const header = page.locator(".panel-header");

  // Act
  const report = await page.evaluate(headerReport);
  await header.locator("#panel-title").hover();

  // Assert: the name wraps, never cut or ended by an ellipsis, the row has no
  // task id, and the Task view has both whole.
  await expect(header).not.toContainText(id);
  expect(report.nameLines).toBeGreaterThan(1);
  expect(report.cut).toEqual([]);
  expect(report.shortened).toEqual([]);
  expect(report.broken).toEqual([]);
  await expect(page.getByRole("tooltip")).toHaveCount(0);
  await expect((await openTaskList(page)).locator(".panel-whole-name")).toHaveText("Bernie - " + name + " · " + id);
});

// The Overlord, 2026-11-04: a long goblin name was cut in its header. The
// longest a goblin is called, by the catalog's longest first name and title
// (internal/goblinname/catalog.go) or by its whole task title where it has no
// name, wraps in a taller header at a phone's width and at the desktop's,
// never cut and never under the selector or the controls, which keep their
// 44 px targets beside it.
const LONGEST = [
  ["the longest name and title", { goblin_name: "Wendell", goblin_title: "Variable Wrangler" }, "Wendell - Variable Wrangler"],
  ["the longest task title", { goblin_name: "", goblin_title: "", title: "Announce the PrecisionDocs connector changes inside the app with a What's new that looks great" },
    "Announce the PrecisionDocs connector changes inside the app with a What's new that looks great"],
] as const;
for (const width of [390, 1440]) {
  for (const [what, fields, shown] of LONGEST) {
    test(`at ${width} px ${what} wraps whole in a taller header beside its controls`, async ({ page }) => {
      // Arrange
      await page.setViewportSize({ width, height: 900 });
      await holdStream(page, { healthy: true, instance: "fixture", ceo_runs: true, ceo_harness: "claude", revision: 1, attention: [], tasks: [{ ...BERNIE, ...fields }] });
      await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
      await page.goto("/");

      // Act
      await page.locator(".task-card").filter({ hasText: shown.split(" - ")[0].slice(0, 20) }).first().click();
      await expect(page.locator("#panel-title")).toHaveText(shown);
      await page.evaluate(() => document.fonts.ready);
      const report = await page.evaluate(headerReport);
      const overlaps = await page.evaluate(() => {
        const name = document.querySelector("#panel-title")!.getBoundingClientRect();
        return [...document.querySelectorAll<HTMLElement>(".panel-header .panel-view-switch button, .panel-header .panel-top button")].filter((target) => target.checkVisibility())
          .filter((target) => { const box = target.getBoundingClientRect(); return box.left < name.right && name.left < box.right && box.top < name.bottom && name.top < box.bottom; })
          .map((target) => target.getAttribute("aria-label") || target.textContent!.trim());
      });

      // Assert
      expect(report.cut, shown).toEqual([]);
      expect(report.shortened, shown).toEqual([]);
      expect(report.broken, shown).toEqual([]);
      expect(overlaps, "nothing is drawn over the name").toEqual([]);
      expect(report.small, shown).toEqual([]);
      expect(report.pageScroll, shown).toBe(0);
      if (report.nameLines > 1) expect(report.headerHeight, "the header grows with the name").toBeGreaterThan(52);
      if (width === 390) expect(report.nameLines, "a phone wraps it").toBeGreaterThan(1);
    });
  }
}

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
// the session's picture opens, the name of the one it checks and the colour
// of that one's words and of the other's, and closes it again.
async function styleOffers(page: Page): Promise<{ modern: string; terminal: string; checked: string; colours: { checked: string; other: string } }> {
  const header = page.locator(".context-pane .panel-header");
  await header.getByRole("button", { name: "Chat style", exact: true }).click();
  const drawing = async (name: string) => await header.getByRole("menuitemradio", { name, exact: true }).locator("path").first().getAttribute("d") ?? "";
  const colour = async (checked: boolean) => await header.getByRole("menuitemradio", { checked }).evaluate((item) => getComputedStyle(item).color);
  const offers = {
    modern: await drawing("Modern chat"), terminal: await drawing("Terminal"), checked: await header.getByRole("menuitemradio", { checked: true }).innerText(),
    colours: { checked: await colour(true), other: await colour(false) },
  };
  await page.keyboard.press("Escape");
  await expect(header.getByRole("menuitemradio")).toHaveCount(0);
  return offers;
}

// The CEO and two goblins in both styles, on a phone, a narrow panel and a
// wide one (#1987): the name keeps the header's one row, every control there
// is a named 44 px target with no tip, the picture offers both styles with
// the one shown checked, Open in Windows Terminal is in the row or its More
// off a phone and nowhere on one (#2222), the supervisor is named CEO, and
// the Chat icon is Modern chat's message bubble (2026-11-04).
const IVY = OTHER.goblin_name + " - " + OTHER.goblin_title;
const LAYOUTS = [["a phone", { width: 390, height: 844 }, 0], ["a narrow panel", { width: 1440, height: 900 }, 360], ["a wide panel", { width: 1707, height: 1067 }, 1000]] as const;
for (const style of ["modern", "classic"] as const) {
  test.describe(`in ${style}`, () => {
    test.use({ chatStyle: style });
    for (const [where, viewport, pane] of LAYOUTS) {
      test(`in ${where} the CEO and both goblins keep their name on the header's one row, with both styles on the picture and Open in Windows Terminal at hand off a phone`, async ({ page }) => {
        // Arrange
        await page.setViewportSize(viewport);
        if (pane) await page.addInitScript((width) => localStorage.setItem("ceo-pane-width", String(width)), pane);
        await openBoard(page);

        for (const [key, name] of [["1", "CEO"], ["2", NAME], ["3", IVY], ["1", "CEO"]] as const) {
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
          if (name === "CEO") expect(report.nameLines, name).toBe(1);
          expect(report.broken, name).toEqual([]);
          expect(report.isOneRow, name).toBe(true);
          expect(report.cut, name).toEqual([]);
          expect(report.small, name).toEqual([]);
          expect(report.unnamed, name).toBe(0);
          expect(report.tips, name).toEqual([]);
          expect(report.pageScroll, name).toBe(0);
          expect(styles.checked, name + "'s picture checks the style shown").toBe(style === "modern" ? "Modern chat" : "Terminal");
          expect(styles.colours.checked, name + "'s checked style is drawn apart from the other").not.toBe(styles.colours.other);
          expect(styles.modern, name + "'s styles are drawn apart").not.toBe(styles.terminal);
          expect(chatIcon, name + "'s Chat is Modern chat's message bubble").toBe(styles.modern);
          // A phone has no Windows Terminal to open the session in.
          expect(windowIcon !== "", name + " offers Open in Windows Terminal").toBe(where !== "a phone");
        }
      });
    }
  });
}
