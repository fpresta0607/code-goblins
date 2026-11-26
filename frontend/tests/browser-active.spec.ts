import { expect, test, type Locator, type Page } from "./site";
import { GOBLIN, NAME, open, snapshot } from "./session-board";

// The Overlord, 2026-11-13: "if there's browser that's active I should always
// have be able to click into it on the car" (#2559). A card that shows
// Browser active opens what is active: the goblin's panel on its Screen view
// while its own browser runs, else the page it presents in a new tab, on the
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

// The Overlord, 2026-11-26: "browser active button in kanban and goblin cards
// takes you to a different browser control window than the toggle in banner"
// (#2930). While the goblin uses its own browser, Browser active selects its
// card and shows its panel on the Screen view, the view the panel header's
// switch and the chat's browser row show, in every layout that draws the
// card, even while the goblin presents a page too.
const LAYOUTS = [
  { layout: "kanban board", width: 1440, cards: ".task-board .task-card-shell" },
  { layout: "stacked board", width: 1440, cards: ".task-board .task-card-shell" },
  { layout: "Orchestration canvas", width: 1440, cards: ".flow-node" },
  { layout: "Orchestration list", width: 390, cards: ".workflow-node" },
];
for (const { layout, width, cards } of LAYOUTS) {
  test(`on the ${layout} a goblin using its own browser shows it from Browser active in its panel's Screen view, as the panel's switch does`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height: 900 });
    await page.addInitScript((stacked) => localStorage.setItem("ceo-board-layout", stacked ? "stacked" : "kanban"), layout === "stacked board");
    await page.routeWebSocket("**/api/screen?*", (socket) => socket.send(JSON.stringify({ type: "state", is_driven: false, can_drive: true, title: "Sign in", host: "shop.example.com", has_page: true })));
    await open(page, presenting({ browser: true, browser_in_use: true }));
    if (layout.startsWith("Orchestration")) await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    else await expect(page.locator(".workspace.kanban")).toHaveCount(layout === "kanban board" ? 1 : 0);
    const shell = page.locator(cards).filter({ hasText: NAME });
    const opener = shell.getByRole("button", { name: /^Browser active/ });
    const tabs: string[] = [];
    page.context().on("page", (tab) => tabs.push(tab.url()));

    // Assert: the card says Browser active once and offers no presented page.
    await expect(shell.getByText("Browser active", { exact: true })).toHaveCount(1);
    await expect(pageOpener(shell, NAME)).toHaveCount(0);

    // Act
    await opener.click();

    // Assert: the goblin's card is selected and its panel shows its browser
    // on the Screen view, in this tab.
    await expect(shell.locator(".task-card")).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByRole("group", { name: "Panel view", exact: true }).getByRole("button", { name: "Screen", exact: true })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByRole("region", { name: NAME + "'s browser", exact: true })).toBeVisible();
    expect(tabs).toEqual([]);
  });
}

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
