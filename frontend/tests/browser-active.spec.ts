import { expect, test, type Locator, type Page } from "./site";
import { GOBLIN, NAME, open, snapshot } from "./session-board";

// The Overlord, 2026-11-13: "if there's browser that's active I should always
// have be able to click into it on the car" (#2559). A card that shows
// Browser active opens what is active in a new tab: the Screen view of the
// goblin's own browser while it runs, else the page it presents, on the
// board, on the Orchestration canvas and in the list the canvas becomes at
// phone width, the CEO's card included.

const PAGE_ADDRESS = "https://pages.example.com/walkthrough";
const CEO = { id: "ceo-1", native_id: "n-ceo", harness: "claude", role: "ceo", task_id: "", generation: "ceo-g1", phase: "working" };

// presenting is the board while the goblin, and the CEO when asked, each
// present the page, live since five seconds ago.
function presenting(goblin: Record<string, unknown> = {}, isCeoPresenting = false) {
  const at = new Date(Date.now() - 5_000).toISOString(), until = new Date(Date.now() + 600_000).toISOString();
  const shown = { kind: "review", source: "", state: "active", url: PAGE_ADDRESS, at, until };
  const activity = [{ ...shown, id: "p-goblin", task_id: GOBLIN.id, generation: GOBLIN.generation, target: "" },
    ...(isCeoPresenting ? [{ ...shown, id: "p-ceo", task_id: "", generation: CEO.generation, target: CEO.id, ceo_identity: "ceo", live: true }] : [])];
  return snapshot({ sessions: [CEO], ceo_session: CEO.id, activity }, { runtime: { state: "working", reason: "", at }, ...goblin });
}

const pageOpener = (within: Locator | Page, name: string) => within.getByRole("button", { name: "Browser active: open the page " + name + " presents in a new tab", exact: true });

// opensInTab presses Browser active the way asked and says the address of the
// tab it opened, the presented page served here.
async function opensInTab(page: Page, opener: Locator, press: () => Promise<void> = () => opener.click()): Promise<string> {
  await page.context().route(PAGE_ADDRESS, (route) => route.fulfill({ contentType: "text/html", body: "<!doctype html><title>Walkthrough</title>" }));
  const opened = page.context().waitForEvent("page");
  await press();
  const tab = await opened;
  await tab.waitForURL((address) => address.href !== "about:blank");
  const address = new URL(tab.url());
  await tab.close();
  return address.origin === new URL(page.url()).origin ? address.pathname + address.search : address.href;
}

test("a goblin that presents a page and runs no browser of its own has a Browser active a keyboard opens it from", async ({ page }) => {
  // Arrange
  await open(page, presenting());
  const shell = page.locator(".task-board .task-card-shell").filter({ hasText: NAME });
  const opener = pageOpener(shell, NAME);

  // Assert: the card shows Browser active once, as a button of its own, not
  // words inside the card's button.
  await expect(shell.getByText("Browser active", { exact: true })).toHaveCount(1);
  await expect(opener).toBeVisible();
  expect(await opener.evaluate((button) => !!button.parentElement?.closest("button"))).toBe(false);

  // Act
  const address = await opensInTab(page, opener, async () => { await opener.focus(); await page.keyboard.press("Enter"); });

  // Assert
  expect(address).toBe(PAGE_ADDRESS);
});

test("a goblin using its own browser opens its screen from Browser active, even while it presents a page", async ({ page }) => {
  // Arrange
  await open(page, presenting({ browser: true, browser_in_use: true }));
  const shell = page.locator(".task-board .task-card-shell").filter({ hasText: NAME });

  // Assert
  await expect(shell.getByText("Browser active", { exact: true })).toHaveCount(1);
  await expect(pageOpener(shell, NAME)).toHaveCount(0);

  // Act
  const address = await opensInTab(page, shell.getByRole("button", { name: "Browser active: open " + NAME + "'s screen in a new tab", exact: true }));

  // Assert
  expect(address).toBe("/?screen=" + GOBLIN.id);
});

for (const { view, width } of [{ view: "canvas", width: 1440 }, { view: "list", width: 390 }]) {
  test(`on the Orchestration ${view} the goblin's card and the CEO's each open the page they present`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height: 900 });
    await open(page, presenting({}, true));
    await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    const cards = page.locator(view === "canvas" ? ".flow-node" : ".workflow-node");
    const goblin = cards.filter({ hasText: NAME });
    const ceo = cards.filter({ has: pageOpener(page, "CEO") });

    // Assert: each card says Browser active once.
    await expect(goblin.getByText("Browser active", { exact: true })).toHaveCount(1);
    await expect(ceo.getByText("Browser active", { exact: true })).toHaveCount(1);

    // Act
    const fromGoblin = await opensInTab(page, pageOpener(goblin, NAME));
    const fromCeo = await opensInTab(page, pageOpener(ceo, "CEO"));

    // Assert
    expect([fromGoblin, fromCeo]).toEqual([PAGE_ADDRESS, PAGE_ADDRESS]);
  });
}
