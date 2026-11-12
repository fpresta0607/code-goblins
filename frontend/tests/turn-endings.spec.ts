import { expect, test, type Page } from "./site";
import { at, box, NAME, open, openGoblin, PAGE, reportSession, snapshot, typed, type Relay } from "./session-board";

// #2292 and #2293 (the Overlord, 2026-11-10): the CEO's chat kept its Stop
// button and its working word after the harness had ended its turn on a
// usage-limit reply, and Stop changed nothing. Claude Code runs no hook where
// a usage limit, a failed request or his own Stop ends a turn, so the board's
// snapshot goes on saying the session works. The fleet's stream is what says
// whether a turn runs, to the box's Stop as to the working word: here the
// snapshot says the session works from start to end, and only the stream
// says the turn ended.

const CEO_SESSION = { ceo_session: "ceo-1", sessions: [{ id: "ceo-1", role: "ceo", harness: "claude", phase: "active" }] };
const reply = (seq: number, text: string) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text }] });
const tell = (page: Page, type: "page" | "messages", data: object) => page.evaluate((detail) => { window.dispatchEvent(new CustomEvent("fixture-messages", { detail })); }, { type, data });
const added = (page: Page, row: object) => tell(page, "messages", { conversation: PAGE.conversation, messages: [row] });
const stop = (page: Page, name: string) => box(page, name).getByRole("button", { name: "Stop", exact: true });
const working = (page: Page) => page.locator(".reply-working");
// The working word's own animations that still run.
const wordAnimations = (page: Page) => page.evaluate(() => document.getAnimations().filter((animation) => animation.id.startsWith("working-") && animation.playState === "running").length);

// expectTurn asserts what a running turn shows, and expectIdle what an ended
// one leaves: the send button, no Stop, no working word and none of its
// animations.
async function expectTurn(page: Page, name: string) {
  await expect(stop(page, name)).toBeVisible();
  await expect(working(page)).toBeVisible();
  await expect.poll(() => wordAnimations(page)).toBeGreaterThan(0);
}
async function expectIdle(page: Page, name: string) {
  await expect(stop(page, name)).toHaveCount(0);
  await expect(box(page, name).locator(".composer-send")).toHaveAccessibleName("Send");
  await expect(working(page)).toHaveCount(0);
  expect(await wordAnimations(page), "the working word stops animating when it hides").toBe(0);
}

for (const owner of ["CEO", "goblin"]) {
  test(`${owner}: Stop in a live turn presses Escape in the harness, and the box returns to Send once the turn's record holds the interrupt`, async ({ page }) => {
    // Arrange: a turn runs.
    const name = owner === "CEO" ? "CEO" : NAME;
    const relay = await open(page, snapshot(CEO_SESSION));
    if (owner === "CEO") await page.keyboard.press("Control+Alt+1");
    else await openGoblin(page);
    await reportSession(page, { state: "running" });
    await expectTurn(page, name);

    // Act: he presses Stop, and the harness records where it was stopped.
    await stop(page, name).click();
    await expect.poll(() => typed(relay)).toBe("\x1b");
    await added(page, { id: "m2", seq: 2, at, kind: "interrupt", text: "Request interrupted by user" });
    await reportSession(page, { state: "ended" });

    // Assert
    await expectIdle(page, name);
  });
}

const ENDINGS: { name: string; end: (page: Page, relay: Relay) => Promise<void> }[] = [
  { name: "a normal reply", end: (page) => added(page, reply(2, "The swipe is fixed.")) },
  { name: "a usage-limit reply", end: (page) => added(page, reply(2, "You've hit your weekly limit · resets 9am (America/Chicago)")) },
  { name: "a failed request's reply", end: (page) => added(page, reply(2, "API Error: 529 Overloaded")) },
  { name: "Stop", end: (page) => added(page, { id: "m2", seq: 2, at, kind: "interrupt", text: "Request interrupted by user" }) },
  // A harness started again comes back on a terminal that reconnects, and its
  // stream begins again from the session's page.
  { name: "a restart", end: async (page, relay) => { relay.drop(); await tell(page, "page", PAGE); } },
];

for (const ending of ENDINGS) {
  test(`a turn that ends on ${ending.name} leaves no Stop and no working word while the board's snapshot still says the session works`, async ({ page }) => {
    // Arrange
    const relay = await open(page);
    await openGoblin(page);
    await reportSession(page, { state: "running" });
    await expectTurn(page, NAME);

    // Act
    await ending.end(page, relay);
    await reportSession(page, { state: "ended" });

    // Assert
    await expectIdle(page, NAME);
  });
}
