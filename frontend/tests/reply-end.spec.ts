import { expect, test, type Page } from "./site";
import { open, openGoblin, snapshot, typed } from "./session-board";

// The Overlord, 2026-10-27: "We don't need to print the date after every
// response. We only need to print it at the end of a message, and the dither
// will take that place as it's running. And then the stop would be something
// like an icon when you're hovering the dither, that you can then stop the
// agent", and of "Sent just now / Replied just now": "this goes under my
// messages, not above the chat bar". A turn's replies show one time, at the
// turn's end; while the session works the dither takes that place, with Stop
// on it that stops the turn through Modern Chat's own interrupt. Since
// 2026-10-28 nothing shows under his message until he points at it ("It
// shouldn't be static ... Only when you hover in that position where it says
// 'Sent just now', that's where the timestamp and copy icon come"), and Copy
// shows only under a finished turn's last reply ("why the stray copy button in
// our chat?"). The supervisor is played by tests/session-board.ts.

const minute = (count: number) => new Date(Date.parse("2026-10-22T09:00:00Z") + count * 60_000).toISOString();
const reply = (seq: number, blocks: object[]) => ({ id: "r" + seq, seq, at: minute(seq), kind: "reply", blocks });
// Two turns: the first of three replies, its tool rows among them, and the
// second of one.
const TALK = { messages: [
  { id: "p0", seq: 0, at: minute(0), kind: "prompt", source: "overlord", text: "Fix the swipe" },
  reply(1, [{ kind: "text", text: "Reading the handlers." }, { kind: "call", tool: "Read", subject: "src/touch.ts", state: "done" }]),
  reply(2, [{ kind: "call", tool: "Edit", subject: "src/touch.ts", state: "done" }]),
  reply(3, [{ kind: "text", text: "Fixed the swipe." }]),
  { id: "p4", seq: 4, at: minute(4), kind: "prompt", source: "overlord", text: "Now run the phone spec" },
  reply(5, [{ kind: "text", text: "Running it." }]),
], more: false };

const row = (page: Page, id: string) => page.locator(`[data-message="${id}"]`);
const times = (page: Page) => page.getByRole("log").locator(".message-reply time");
const working = (page: Page) => page.locator(".reply-working");
// screen tells the board's view of the terminal whether the harness works.
const screen = (isWorking: boolean) => ({ type: "screen", asking: false, open: false, working: isWorking });
// follows says whether what after matches comes after what before matches,
// in the page's order.
const follows = (page: Page, before: string, after: string) => page.evaluate(([before, after]) => {
  const first = document.querySelector(before), second = document.querySelector(after);
  return !!first && !!second && !!(first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING);
}, [before, after]);

test("a finished turn of many replies shows its time once, at its end, and nothing shows under his message", async ({ page }) => {
  // Arrange and Act
  await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);

  // Assert: one time per turn, on each turn's last reply.
  await expect(times(page)).toHaveCount(2);
  await expect(row(page, "r3").locator("time")).toHaveCount(1);
  await expect(row(page, "r5").locator("time")).toHaveCount(1);
  for (const id of ["r1", "r2"]) await expect(row(page, id).locator("time")).toHaveCount(0);
  await expect(working(page)).toHaveCount(0);

  // Nothing under his messages, and the line above the box carries none of it.
  for (const id of ["p0", "p4"]) await expect(row(page, id).locator(".message-actions")).toBeHidden();
  await expect(page.getByRole("log")).not.toContainText(/Sent|Replied/);
  await expect(page.locator(".session-footer")).not.toContainText(/Sent|Replied|Working/);
});

test("pointing at his message shows when he sent it and Copy in the spot under it, and nothing moves", async ({ page }) => {
  // Arrange
  await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);
  const sent = row(page, "p4");
  const actions = sent.locator(".message-actions");
  await expect(actions).toBeHidden();
  const before = await sent.boundingBox();

  // Act
  await sent.hover();

  // Assert: the real time he sent it, as the transcript holds it, then Copy.
  await expect(actions).toBeVisible();
  await expect(actions.locator("time")).toHaveAttribute("datetime", minute(4));
  await expect(actions.locator("time")).not.toHaveText(/ago|just now/);
  await expect(actions.getByRole("button", { name: "Copy", exact: true })).toBeVisible();
  const parts = await actions.locator("> *").evaluateAll((elements) => elements.map((element) => element.tagName === "TIME" ? "time" : element.classList.contains("message-copy") ? "copy" : element.className));
  expect(parts).toEqual(["time", "copy"]);
  expect(await sent.boundingBox(), "the spot is kept, so nothing moves").toEqual(before);
});

test("a turn in progress shows no Copy, not even beside a short text between its tool calls, and once it ends exactly one under its last reply", async ({ page }) => {
  // Arrange: the second turn goes on with a short text between two calls.
  const going = { messages: [...TALK.messages,
    reply(6, [{ kind: "call", tool: "Bash", subject: "npx playwright test phone", state: "done" }, { kind: "text", text: "One more." }, { kind: "call", tool: "Read", subject: "tests/phone.spec.ts", state: "done" }]),
    reply(7, [{ kind: "text", text: "The phone spec passes." }]),
  ], more: false };
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", going);
  await openGoblin(page);
  const copies = page.getByRole("log").locator(".message-reply .message-copy");

  // Act
  relay.say(screen(true));

  // Assert: only the first, finished turn has its Copy.
  await expect(working(page)).toHaveCount(1);
  await expect(copies).toHaveCount(1);
  await expect(row(page, "r3").locator(".message-copy")).toHaveCount(1);
  for (const id of ["r5", "r6", "r7"]) await expect(row(page, id).locator(".message-copy")).toHaveCount(0);

  // Act: the turn ends.
  relay.say(screen(false));

  // Assert: one Copy per finished turn, under its last reply.
  await expect(working(page)).toHaveCount(0);
  await expect(copies).toHaveCount(2);
  await expect(row(page, "r7").locator(".message-copy")).toHaveCount(1);
  for (const id of ["r1", "r2", "r5", "r6"]) await expect(row(page, id).locator(".message-copy")).toHaveCount(0);
});

test("while the session works the dither takes the time's place at the reply in progress, and Stop on it stops the turn with no terminal shown", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);
  relay.say(screen(true));

  // Assert: the reply in progress shows no time, the dither follows it, and
  // the line above the box says nothing of it.
  await expect(working(page)).toHaveCount(1);
  await expect(times(page)).toHaveCount(1);
  await expect(row(page, "r5").locator("time")).toHaveCount(0);
  expect(await follows(page, '[data-message="r5"]', ".reply-working"), "the dither follows the reply in progress").toBe(true);
  await expect(page.locator(".session-footer")).not.toContainText(/Sent|Replied|Working/);
  const stop = working(page).getByRole("button", { name: "Stop", exact: true });
  const mark = stop.locator(".icon");
  await expect(mark).toHaveCSS("opacity", "0");

  // Act: he points at the dither.
  await stop.hover();

  // Assert: Stop shows on it.
  await expect(mark).toHaveCSS("opacity", "1");

  // Act
  await stop.click();

  // Assert: Escape went to the harness, and no terminal was shown.
  await expect.poll(() => typed(relay)).toBe("\x1b");
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Back to messages" })).toHaveCount(0);

  // Act: the keyboard reaches it too.
  await stop.focus();
  await page.keyboard.press("Enter");

  // Assert
  await expect.poll(() => typed(relay)).toBe("\x1b\x1b");

  // Act: the turn ends.
  relay.say(screen(false));

  // Assert: the dither gives its place to the reply's time.
  await expect(working(page)).toHaveCount(0);
  await expect(row(page, "r5").locator("time")).toHaveCount(1);
});

test("with no reply yet the dither sits at the reply's place under his message, and held typing shows it without Stop", async ({ page }) => {
  // Arrange
  const asked = { messages: [...TALK.messages, { id: "p6", seq: 6, at: minute(6), kind: "prompt", source: "overlord", text: "And the iPhone one" }], more: false };
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", asked);
  await openGoblin(page);

  // Act
  relay.say(screen(true));

  // Assert
  await expect(working(page)).toHaveCount(1);
  expect(await follows(page, '[data-message="p6"]', ".reply-working"), "the dither follows his message").toBe(true);
  await expect(working(page).getByRole("button", { name: "Stop", exact: true })).toBeVisible();

  // Act: a review holds his typing.
  relay.say({ type: "held", reason: "pipeline owns this task" });

  // Assert
  await expect(working(page).getByRole("button")).toHaveCount(0);
  await expect(working(page).locator(".working-indicator")).toBeVisible();
});

// The Overlord, 2026-10-28, from his phone: "please clean up all the time
// stamps". At phone widths too, a turn shows one time, nothing shows under
// his message, and no time is on a line above the box.
for (const width of [360, 393, 430]) {
  test(`at ${width} px the times keep their places and nothing shows under his message, finished and working`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height: 852 });
    const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
    await openGoblin(page);
    const dock = page.locator(".composer-dock");

    // Assert: finished.
    await expect(times(page)).toHaveCount(2);
    await expect(row(page, "p4").locator(".message-actions")).toBeHidden();
    await expect(dock).not.toContainText(/Sent|Replied|Working|\d:\d\d/);

    // Act
    relay.say(screen(true));

    // Assert: working, the dither is in the conversation, not over the box.
    await expect(working(page)).toBeVisible();
    await expect(times(page)).toHaveCount(1);
    await expect(dock.locator(".reply-working, .working-indicator")).toHaveCount(0);
    await expect(dock).not.toContainText(/Sent|Replied|Working|\d:\d\d/);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
  });
}

// The Overlord, 2026-10-28: "Copy should come after the timestamp. The icon
// should come after the timestamp", and "Don't need the Claude icon by the
// dither". Under the last reply of a turn the row reads its time, Copy, then
// the harness's mark, and the line above the box carries no mark.
test("under a turn's last reply the row reads its time, then Copy, then the harness's mark, and no mark is above the box", async ({ page }) => {
  // Arrange and Act
  await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);

  // Assert
  const parts = await row(page, "r5").locator(".message-reply-actions > *").evaluateAll((elements) => elements.map((element) => element.tagName === "TIME" ? "time" : element.classList.contains("message-copy") ? "copy" : element.classList.contains("mark") ? "mark" : element.className));
  expect(parts).toEqual(["time", "copy", "mark"]);
  await expect(row(page, "r2").locator(".message-reply-actions .mark")).toHaveCount(0);
  await expect(page.locator(".session-footer .mark")).toHaveCount(0);
});

