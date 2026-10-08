import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-08: "also in terminals there should be a right click
// hold +scroll to zoom font and window size?? any terminal?? should correct
// all them as saved size". Holding the right button and turning the wheel
// steps the text size of every open terminal at once, each refits its grid
// to its panel, and the size is kept in this browser.

type Size = { cols: number; rows: number };
interface Relays { resizes: Record<string, Size[]>; typed: Record<string, string> }

// openPair stands in for the board's relay for both terminals: each takes
// every size its view claims, and prints a prompt after the first.
async function openPair(page: Page): Promise<Relays> {
  const relays: Relays = { resizes: { first: [], second: [] }, typed: { first: "", second: "" } };
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    const name = new URL(socket.url()).searchParams.get("run")!;
    let isPrinted = false;
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.onMessage((data) => {
      if (typeof data !== "string") { relays.typed[name] += data.toString("utf8"); return; }
      const control: { type?: string; cols?: number; rows?: number } = JSON.parse(data);
      if (control.type !== "resize") return;
      relays.resizes[name].push({ cols: control.cols!, rows: control.rows! });
      socket.send(JSON.stringify({ type: "size", cols: control.cols, rows: control.rows }));
      if (!isPrinted) { isPrinted = true; socket.send(Buffer.from("PS C:\\> ")); }
    });
  });
  await page.goto("/tests/fixtures/terminal-pair.html");
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  await expect(page.locator(".terminal-view.staged")).toHaveCount(0);
  await settled(relays);
  return relays;
}

// settled waits until neither view claims another size, and returns the last
// size each claimed.
async function settled(relays: Relays): Promise<Record<string, Size>> {
  let seen = "";
  await expect.poll(() => {
    const now = JSON.stringify(relays.resizes);
    const isQuiet = now === seen && relays.resizes.first.length > 0 && relays.resizes.second.length > 0;
    seen = now;
    return isQuiet;
  }, { intervals: [600] }).toBe(true);
  return { first: relays.resizes.first.at(-1)!, second: relays.resizes.second.at(-1)! };
}

// watchMenus records, for each context menu the page is asked to open,
// whether the board kept it from opening.
const watchMenus = (page: Page) => page.evaluate(() => {
  const menus: boolean[] = [];
  Object.assign(window, { menus });
  window.addEventListener("contextmenu", (event) => setTimeout(() => menus.push(event.defaultPrevented)), true);
});
const menus = (page: Page) => page.evaluate(() => (window as unknown as { menus: boolean[] }).menus);
const keptSize = (page: Page) => page.evaluate(() => localStorage.getItem("cfo-terminal-font-size"));
const screenOf = (page: Page, label: string) => page.getByRole("region", { name: label }).locator(".terminal-view > .xterm .xterm-screen");

async function zoomWithRightButton(page: Page, label: string, notches: number) {
  await screenOf(page, label).hover();
  await page.mouse.down({ button: "right" });
  for (let notch = 0; notch < Math.abs(notches); notch++) await page.mouse.wheel(0, notches > 0 ? -100 : 100);
  await page.mouse.up({ button: "right" });
}

test("holding the right button and turning the wheel zooms every open terminal together, opens no menu and types nothing", async ({ page }) => {
  // Arrange
  const relays = await openPair(page);
  const before = await settled(relays);
  await watchMenus(page);

  // Act
  await zoomWithRightButton(page, "First terminal", 2);

  // Assert: the text grew two steps in both terminals, and each refit its
  // grid to its panel, so each program is asked to redraw at fewer columns.
  await expect.poll(() => keptSize(page)).toBe("22");
  const after = await settled(relays);
  expect(after.first.cols).toBeLessThan(before.first.cols);
  expect(after.second.cols).toBeLessThan(before.second.cols);
  expect(after.first.rows).toBeLessThan(before.first.rows);
  await expect.poll(() => menus(page)).toEqual([true]);
  expect(relays.typed).toEqual({ first: "", second: "" });

  // Act: the size outlives a reload.
  relays.resizes.first.length = 0;
  relays.resizes.second.length = 0;
  await page.reload();
  await expect(page.locator(".terminal-view.staged")).toHaveCount(0);

  // Assert
  expect(await settled(relays)).toEqual(after);
  expect(await keptSize(page)).toBe("22");
});

test("a right click that never turns the wheel opens its menu and keeps the size", async ({ page }) => {
  // Arrange
  const relays = await openPair(page);
  const before = await settled(relays);
  await watchMenus(page);

  // Act
  await screenOf(page, "First terminal").click({ button: "right" });

  // Assert
  await expect.poll(() => menus(page)).toEqual([false]);
  await page.waitForTimeout(600);
  expect(await settled(relays)).toEqual(before);
  expect(await keptSize(page)).toBeNull();
});

test("the wheel stops at 12 and 28 px", async ({ page }) => {
  // Arrange
  await openPair(page);

  // Act
  await zoomWithRightButton(page, "First terminal", 12);

  // Assert
  await expect.poll(() => keptSize(page)).toBe("28");

  // Act
  await zoomWithRightButton(page, "Second terminal", -20);

  // Assert
  await expect.poll(() => keptSize(page)).toBe("12");
});

const CHOOSERS = [
  { name: "the right button and the wheel", choose: (page: Page) => zoomWithRightButton(page, "First terminal", -1) },
  { name: "Ctrl+Minus", choose: async (page: Page) => {
    await page.getByRole("region", { name: "First terminal" }).getByRole("textbox", { name: "Terminal input" }).focus();
    await page.keyboard.press("Control+Minus");
  } },
];
for (const { name, choose } of CHOOSERS) {
  test(`a terminal out of sight takes a size chosen with ${name} once it is shown`, async ({ page }) => {
    // Arrange
    const relays = await openPair(page);
    const before = await settled(relays);
    const switcher = page.getByRole("button", { name: "Switch terminal" });
    await switcher.click();
    await settled(relays);

    // Act
    await choose(page);
    await expect.poll(() => keptSize(page)).toBe("19");
    await switcher.click();

    // Assert: shown again at its old panel, it fits more columns at the
    // smaller size.
    await expect.poll(async () => (await settled(relays)).second.cols).toBeGreaterThan(before.second.cols);
  });
}
