import { expect, holdStream, test, type Page } from "./site";

// The panel's top row at any width the panel can take: no control lies on
// another or leaves the row, the Task and Terminal switch and Close or Back
// always show, and what the row cannot hold goes into More and works from
// there. The Overlord's picture of 2026-10-02 showed the word Terminal under
// two icons in the CFO's panel dragged narrow.
const since = "2026-10-02T09:00:00Z";
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const BOARD = [
  task("queued-one", "queued", { generation: "", brief: true, queue_revision: "q1" }),
  task("working-one", "working", { harness: "codex", backend: "native" }),
];

interface Posted { path: string }

// Opens the board with the panel as wide as the Overlord dragged it, and the
// terminal view beside the board, as his was, not over it.
async function open(page: Page, width: number, posted: Posted[] = []) {
  await page.addInitScript((pane) => {
    if (localStorage.getItem("cfo-pane-width")) return;
    localStorage.setItem("cfo-pane-width", String(pane));
    localStorage.setItem("cfo-terminal-maximized", "false");
  }, width);
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [], tasks: BOARD });
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") {
      posted.push({ path });
      await route.fulfill({ json: {} });
    } else if (path === "/api/workspace") await route.fulfill({ json: { repository: "code-goblins", root: "C:/work/code-goblins", harness: "codex", notes: [] } });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

// What is wrong with the row as it is drawn, and the controls it shows.
function rowReport(): { shown: string[]; problems: string[] } {
  const row = document.querySelector(".context-pane .panel-top")!;
  const frame = row.getBoundingClientRect();
  const controls = [...row.querySelectorAll<HTMLElement>("button, summary, a")].filter((control) => {
    const style = getComputedStyle(control);
    return control.getClientRects().length > 0 && style.visibility !== "hidden" && !control.closest("[inert], [role=menu]");
  });
  const name = (control: HTMLElement) => control.getAttribute("aria-label") || control.textContent!.trim();
  const problems: string[] = [];
  controls.forEach((first, at) => {
    const a = first.getBoundingClientRect();
    if (a.left < frame.left - .5 || a.right > frame.right + .5 || a.top < frame.top - .5 || a.bottom > frame.bottom + .5) problems.push(name(first) + " leaves the row");
    if (a.width < 32 || a.height < 32) problems.push(name(first) + " is " + Math.round(a.width) + " by " + Math.round(a.height) + " px");
    if (first.scrollWidth > first.clientWidth + 1) problems.push(name(first) + " is cut short");
    for (const second of controls.slice(at + 1)) {
      const b = second.getBoundingClientRect();
      if (Math.min(a.right, b.right) - Math.max(a.left, b.left) > .5 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > .5) problems.push(name(first) + " lies on " + name(second));
    }
  });
  // One line: the row is no taller than its tallest control and its padding.
  if (frame.height > 72) problems.push("the row is " + Math.round(frame.height) + " px tall");
  return { shown: controls.map(name), problems };
}

// Presses a control of the row by its name, in the row when it is there and
// from More when it is not; label is its name as an icon, and name its name
// in the menu.
async function press(page: Page, label: string, name: string) {
  const row = page.locator(".context-pane .panel-top");
  const inRow = row.getByRole("button", { name: label, exact: true });
  if (await inRow.count()) return inRow.click();
  await row.getByRole("button", { name: "More", exact: true }).click();
  await row.getByRole("menuitem", { name, exact: true }).click();
}

const pane = (page: Page) => page.locator(".context-pane");

for (const [where, viewport, deviceScaleFactor] of [["his window", { width: 1707, height: 1067 }, 1.5], ["a browser tab", { width: 1440, height: 900 }, 1]] as const) {
  test.describe(`in ${where}`, () => {
    test.use({ viewport, deviceScaleFactor });

    // 360 px is the narrowest the divider lets the panel go.
    for (const width of [360, 430, 520, 700]) {
      test(`at ${width} px the CFO's terminal panel shows every control apart, and the rest in More`, async ({ page }) => {
        const posted: Posted[] = [];
        await open(page, width, posted);
        await page.keyboard.press("Control+Alt+1");
        await expect(page.locator("#panel-title")).toHaveText("CFO");
        await expect(page.locator(".canvas-region")).toBeVisible();
        expect(Math.round((await pane(page).boundingBox())!.width)).toBe(width);
        const report = await page.evaluate(rowReport);
        expect(report.problems).toEqual([]);
        for (const essential of ["Task", "Terminal", "Close panel"]) expect(report.shown).toContain(essential);

        await press(page, "Open in Windows Terminal", "Open in terminal");
        await expect.poll(() => posted.map((request) => request.path)).toEqual(["/api/terminal/open"]);
        await press(page, "Maximize the panel", "Maximize");
        await expect(page.locator(".canvas-region")).toBeHidden();
        expect((await page.evaluate(rowReport)).problems).toEqual([]);
        await press(page, "Restore the panel", "Restore");
        await expect(page.locator(".canvas-region")).toBeVisible();
      });

      test(`at ${width} px a goblin's panel keeps Back and its switch, each view`, async ({ page }) => {
        await open(page, width);
        await page.locator(".task-board .task-card").filter({ hasText: "working-one" }).click();
        await expect(page.locator("#panel-title")).toHaveText("working-one");
        for (const view of ["Task", "Terminal"]) {
          await pane(page).locator(".panel-pill").getByRole("button", { name: view, exact: true }).click();
          await expect(page.locator(".canvas-region")).toBeVisible();
          const report = await page.evaluate(rowReport);
          expect(report.problems, view).toEqual([]);
          for (const essential of ["Task", "Terminal", "Back to the CFO"]) expect(report.shown, view).toContain(essential);
        }
        // A queued task has no terminal, so no switch: Back and what fits.
        await page.locator(".task-board .task-card").filter({ hasText: "queued-one" }).click();
        await expect(page.locator("#panel-title")).toHaveText("queued-one");
        const report = await page.evaluate(rowReport);
        expect(report.problems).toEqual([]);
        expect(report.shown).toContain("Back to the CFO");
      });
    }

    // At 360 px the switch cannot keep its words beside Back, so it shows its
    // icons, and with them the row holds every control again.
    test("at the narrowest width the switch shows its icons, each named", async ({ page }) => {
      await open(page, 360);
      await page.locator(".task-board .task-card").filter({ hasText: "working-one" }).click();
      const pill = pane(page).locator(".panel-pill");
      await pill.getByRole("button", { name: "Terminal", exact: true }).click();
      await expect(page.locator(".canvas-region")).toBeVisible();
      for (const [name, pressed] of [["Task", "false"], ["Terminal", "true"]]) {
        const button = pill.getByRole("button", { name, exact: true });
        await expect(button).toHaveAttribute("data-tip", name);
        await expect(button).toHaveAttribute("aria-pressed", pressed);
        await expect(button).toHaveText("");
      }
      expect((await page.evaluate(rowReport)).problems).toEqual([]);
    });

    // At 430 px the CFO's row holds its switch with its words and Close, and
    // no room for both other controls, so both are in More.
    test("More holds what the row cannot, by keyboard", async ({ page }) => {
      await open(page, 430);
      await page.keyboard.press("Control+Alt+1");
      const row = pane(page).locator(".panel-top");
      await expect(row.locator(".panel-pill").getByRole("button", { name: "Terminal", exact: true })).toHaveText("Terminal");
      const more = row.getByRole("button", { name: "More", exact: true });
      await more.focus();
      await page.keyboard.press("Enter");
      const items = row.getByRole("menuitem");
      await expect(items).toHaveText(["Maximize", "Open in terminal"]);
      await expect(items.first()).toBeFocused();
      await page.keyboard.press("ArrowDown");
      await expect(items.nth(1)).toBeFocused();
      // Escape closes More and leaves the panel open.
      await page.keyboard.press("Escape");
      await expect(items).toHaveCount(0);
      await expect(more).toBeFocused();
      await expect(page.locator("#panel-title")).toHaveText("CFO");
      await expect(pane(page)).toBeVisible();
    });

    test("with room to spare the switch keeps its words and there is no More", async ({ page }) => {
      await open(page, 700);
      await page.keyboard.press("Control+Alt+1");
      const row = pane(page).locator(".panel-top");
      await expect(row.locator(".panel-pill").getByRole("button", { name: "Terminal", exact: true })).toHaveText("Terminal");
      await expect(row.getByRole("button", { name: "More", exact: true })).toHaveCount(0);
      await expect(row.getByRole("button", { name: "Maximize the panel", exact: true })).toBeVisible();
      await expect(row.getByRole("button", { name: "Open in Windows Terminal", exact: true })).toBeVisible();
    });
  });
}

// The dictation card over a terminal was sized by the window, so in a panel
// narrower than the card its left side was cut off.
test("the dictation card fits a terminal 360 px wide", async ({ page }) => {
  await page.setViewportSize({ width: 1707, height: 1067 });
  await page.goto("/tests/fixtures/voice-bubble.html");
  await page.locator("main").evaluate((terminal) => { terminal.style.width = "360px"; });
  await page.locator(".voice-bubble").click();
  const card = (await page.getByRole("dialog", { name: "Recent messages" }).boundingBox())!;
  const terminal = (await page.locator("main").boundingBox())!;
  expect(card.x).toBeGreaterThanOrEqual(terminal.x);
  expect(card.x + card.width).toBeLessThanOrEqual(terminal.x + terminal.width);
});
