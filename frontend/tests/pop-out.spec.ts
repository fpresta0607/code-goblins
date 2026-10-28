import { expect, test, type Page } from "./site";
import { box, boxMenu, field, GOBLIN, NAME, open, openGoblin, typed } from "./session-board";

// The Overlord, 2026-10-27: "the terminal button should be like an open link.
// So it should be something that you can open in its own window. And that can
// be either the terminal or it can be the modern chat." Then, 2026-10-28:
// "maybe terminal button is better by expand up top as the open icon". The
// open icon in the panel's top row opens the session in a window of its own, in the view the Modern/Classic
// switch chose, on the same session: a new window of the goblins-window app
// inside it, a new browser window elsewhere. The supervisor is played by
// tests/session-board.ts.

const popOut = (page: Page) => page.locator(".panel-top").getByRole("button", { name: "Open in its own window", exact: true });

// recordWindows keeps each window the page asks the browser for, and each
// message it sends the goblins-window app when it is inside one, instead of
// opening anything.
async function recordWindows(page: Page, isInApp: boolean) {
  await page.addInitScript((isInApp) => {
    const seen = window as unknown as { opened: string[]; posted: string[] };
    seen.opened = [];
    seen.posted = [];
    window.open = (address?: string | URL) => { seen.opened.push(String(address)); return null; };
    if (isInApp) Object.assign(window, { chrome: { webview: { postMessage: (message: string) => seen.posted.push(message) } } });
  }, isInApp);
}
const asked = (page: Page) => page.evaluate(() => {
  const { opened, posted } = window as unknown as { opened: string[]; posted: string[] };
  return { opened, posted };
});

for (const style of ["modern", "classic"] as const) {
  test(`the box pops the session out into a window of its own, which shows it ${style === "modern" ? "as Modern Chat" : "in its terminal"} on the same session`, async ({ page, context }) => {
    // Arrange
    await recordWindows(page, false);
    const relay = await open(page);
    await openGoblin(page);
    if (style === "classic") {
      await boxMenu(page);
      await box(page).getByRole("button", { name: "Classic", exact: true }).click();
      await expect(page.getByRole("region", { name: "Goblin terminal", exact: true })).toBeVisible();
    }

    // Act
    await popOut(page).click();

    // Assert: a new browser window for this session's own page, and the box
    // holds no window of its own.
    await boxMenu(page);
    await expect(box(page).getByRole("button", { name: /^Open in / })).toHaveCount(0);
    const { opened, posted } = await asked(page);
    expect(posted).toEqual([]);
    expect(opened).toHaveLength(1);
    const address = new URL(opened[0]);
    expect(address.origin + address.pathname).toBe(new URL(page.url()).origin + "/");
    expect(address.searchParams.get("session")).toBe(GOBLIN.id);

    // Act: that page opens in the window.
    const popped = await context.newPage();
    const poppedRelay = await open(popped);
    await popped.goto(opened[0]);

    // Assert: it opens on the session's panel, maximized, in the chosen
    // view, connected to the same session, with no pop-out of its own.
    await expect(popped.locator("#panel-title")).toHaveText(NAME);
    await expect(popped.locator(".canvas-region")).toBeHidden();
    if (style === "modern") await expect(popped.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
    else await expect(popped.getByRole("region", { name: "Goblin terminal", exact: true })).toBeVisible();
    await expect.poll(() => poppedRelay.addresses.some((each) => new URL(each).searchParams.get("task") === GOBLIN.id)).toBe(true);
    await expect(popOut(popped)).toHaveCount(0);
    if (style === "classic") await expect(box(popped), "Classic shows the terminal alone until he uses a box").toHaveCount(0);

    // Act: he writes from the popped window, in its box or its terminal.
    if (style === "modern") {
      await field(popped).fill("from the other window");
      await field(popped).press("Enter");
    } else {
      await popped.getByRole("region", { name: "Goblin terminal", exact: true }).getByRole("textbox", { name: "Terminal input", exact: true }).pressSequentially("from the other window");
    }

    // Assert: it reaches the same session, and the board's own window keeps
    // its panel where it was.
    await expect.poll(() => typed(poppedRelay)).toContain("from the other window");
    expect(typed(relay)).toBe("");
    await expect(page.locator(".canvas-region")).toBeVisible();
  });
}

test("inside the goblins-window app the pop-out asks the app for a window of its own, not the browser", async ({ page }) => {
  // Arrange
  await recordWindows(page, true);
  await open(page);
  await openGoblin(page);

  // Act
  await popOut(page).click();

  // Assert
  const { opened, posted } = await asked(page);
  expect(opened).toEqual([]);
  expect(posted).toEqual(["window:" + new URL("/?session=" + GOBLIN.id, page.url()).href]);
});
