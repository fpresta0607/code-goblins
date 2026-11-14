import { expect, test, type Locator, type Page } from "./site";
import { box, field, GOBLIN, holdMessages, open, openGoblin, pickStyle, releaseMessages, reportSession, snapshot, typed } from "./session-board";

// The Overlord, 2026-10-27: "We don't need to print the date after every
// response. We only need to print it at the end of a message, and the dither
// will take that place as it's running", and of "Sent just now / Replied
// just now": "this goes under my messages, not above the chat bar". A turn's
// replies show one time, at the turn's end; while the session works the
// dither takes that place, with no Stop beside it since 2026-11-03 ("The stop
// button next to the When I'm the verb icon bothers me. Remove it."; #1987):
// the box's Stop and Escape stop the turn. Since
// 2026-10-28 nothing shows under his message until he points at it ("It
// shouldn't be static ... Only when you hover in that position where it says
// 'Sent just now', that's where the timestamp and copy icon come"), and since
// 2026-11-04 each text of a reply has a Copy of its own, the last of a
// finished turn's between its time and the harness's mark (#2048). The
// supervisor is played by tests/session-board.ts.

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
], conversation: "fixture-conversation", more: false };

const row = (page: Page, id: string) => page.locator(`[data-message="${id}"]`);
const times = (page: Page) => page.getByRole("log").locator(".message-reply time");
const working = (page: Page) => page.locator(".reply-working");
// screen tells the board's view of the terminal whether the harness works,
// and what its turn row says it is doing.
const screen = (isWorking: boolean, doing = "") => ({ type: "screen", asking: false, open: false, working: isWorking, ...doing ? { doing } : {} });
// Every word beside the dither is in the page from the start, laid unseen in
// the one place; firstWord is the one shown first.
const firstWord = (page: Page) => working(page).locator(".working-word").first();
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
  await expect(page.locator(".composer-dock")).not.toContainText(/Sent|Replied/);
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

test("each text of a turn in progress has its own Copy shown with no pointer over it, and once the turn ends its last text's Copy sits between the time and the mark", async ({ page }) => {
  // Arrange: the second turn goes on with a short text between two calls.
  const going = { messages: [...TALK.messages,
    reply(6, [{ kind: "call", tool: "Bash", subject: "npx playwright test phone", state: "done" }, { kind: "text", text: "One more." }, { kind: "call", tool: "Read", subject: "tests/phone.spec.ts", state: "done" }]),
    reply(7, [{ kind: "text", text: "The phone spec passes." }]),
  ], conversation: "fixture-conversation", more: false };
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", going);
  await openGoblin(page);
  const copies = page.getByRole("log").locator(".message-reply .message-copy");

  // Act
  relay.say(screen(true));

  // Assert: every text has one Copy, and none of the calls or the dither has one.
  await expect(working(page)).toHaveCount(1);
  await expect(copies).toHaveCount(5);
  for (const id of ["r1", "r3", "r5", "r6", "r7"]) await expect(row(page, id).locator(".message-copy")).toHaveCount(1);
  await expect(row(page, "r2").locator(".message-copy")).toHaveCount(0);
  await expect(working(page).locator(".message-copy")).toHaveCount(0);
  for (const opacity of await copies.evaluateAll((elements) => elements.map((element) => getComputedStyle(element).opacity))) expect(opacity, "shown without hover").toBe("1");
  await expect(row(page, "r7").locator("time")).toHaveCount(0);

  // Act: the turn ends.
  relay.say(screen(false));

  // Assert: still one Copy per text, the last now in its turn's end row.
  await expect(working(page)).toHaveCount(0);
  await expect(copies).toHaveCount(5);
  const parts = await row(page, "r7").locator(".message-reply-actions > *").evaluateAll((elements) => elements.map((element) => element.tagName === "TIME" ? "time" : element.classList.contains("message-copy") ? "copy" : element.classList.contains("mark") ? "mark" : element.className));
  expect(parts).toEqual(["time", "copy", "mark"]);
});

// #2048 and the Overlord, 2026-11-04: a text section of a reply had no Copy
// on a phone, and a centered note had one that copied only part of it. Into
// a turn that still runs stream a text, the note of a Stop hook's feedback,
// another text and a code block: each text's Copy and the code block's copy
// their whole Markdown source the moment they show, tapped on a phone with no
// hover, and the hook's note, only a label whose feedback the transcript keeps
// from the board, has no Copy; a background command's note copies all of it.
const FIRST = "Reading **the handlers** first:\n\n- `src/touch.ts`\n- `src/swipe.ts`";
const SECOND = "The hook asked for the phone spec, so it runs next.";
const CODE = "expect(swipe).toBe(true);\nexpect(scroll).toBe(false);";
const THIRD = "Then the check:\n\n```ts\n" + CODE + "\n```";
const BACKGROUND = 'Background command "Reattach to the gate run during the final re-review" completed (exit code 0)';
for (const width of [390, 1440]) {
  test.describe(`at ${width} px`, () => {
    const isPhone = width === 390;
    test.use({ viewport: { width, height: 900 }, hasTouch: isPhone, isMobile: isPhone });

    test("each text and code block streamed into a running turn copies its whole source, and the Stop hook's note has no Copy", async ({ page, context }) => {
      // Arrange
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      const relay = await open(page, snapshot({}, { phase: "settled" }), "", { messages: [TALK.messages[0]], conversation: "fixture-conversation", more: false });
      await openGoblin(page);
      relay.say(screen(true));
      await expect(working(page)).toHaveCount(1);
      const stream = (message: object) => page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [data] } } })), message);
      const press = (target: Locator) => isPhone ? target.tap() : target.click();
      const copied = async () => (await page.evaluate(() => navigator.clipboard.readText())).replaceAll("\r\n", "\n");

      // Act
      await stream(reply(1, [{ kind: "text", text: FIRST }]));
      await stream({ id: "n2", seq: 2, at: minute(2), kind: "prompt", source: "notice", text: "Stop hook feedback" });
      await stream(reply(3, [{ kind: "text", text: SECOND }, { kind: "call", tool: "Bash", subject: "npx playwright test phone", state: "running" }]));
      await stream(reply(4, [{ kind: "text", text: THIRD }]));
      await stream({ id: "n5", seq: 5, at: minute(5), kind: "prompt", source: "notice", text: BACKGROUND });

      // Assert: the turn still runs, and each text's Copy copies it as written.
      await expect(working(page)).toHaveCount(1);
      for (const [id, text] of [["r1", FIRST], ["r3", SECOND], ["r4", THIRD]]) {
        const copy = row(page, id).locator(".message-reply-actions .message-copy");
        await expect(copy).toHaveCount(1);
        expect(await copy.evaluate((element) => getComputedStyle(element).opacity), id + "'s Copy shows without hover").toBe("1");
        await press(copy);
        await expect.poll(copied, { message: id + "'s Copy copies its whole source" }).toBe(text);
      }
      await press(row(page, "r4").locator(".code-copy"));
      await expect.poll(copied, { message: "the code block's Copy copies its code" }).toBe(CODE);

      // The hook's note shows no Copy, and the background command's copies all its words.
      await expect(row(page, "n2")).toHaveText("Stop hook feedback");
      await expect(row(page, "n2").locator(".message-copy")).toHaveCount(0);
      const noted = row(page, "n5").locator(".message-copy");
      if (isPhone) expect(await noted.evaluate((element) => getComputedStyle(element).opacity), "the note's Copy shows on a phone").toBe("1");
      await press(noted);
      await expect.poll(copied).toBe(BACKGROUND);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
    });
  });
}

test("while the session works the dither takes the time's place at the reply in progress with no Stop, and the box's Stop and Escape stop the turn with no terminal shown", async ({ page }) => {
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
  await expect(page.locator(".composer-dock")).not.toContainText(/Sent|Replied/);

  // Act: he points at the dither.
  await working(page).hover();

  // Assert: the row holds the dither and its words alone.
  await expect(working(page).getByRole("button")).toHaveCount(0);
  await expect(working(page).locator(".working-indicator")).toBeVisible();
  await expect(working(page).locator(".reply-doing")).toBeVisible();

  // Act: the box's Stop.
  await box(page).getByRole("button", { name: "Stop", exact: true }).click();

  // Assert: Escape went to the harness, and no terminal was shown.
  await expect.poll(() => typed(relay)).toBe("\x1b");
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();

  // Act: Escape in the box stops it too.
  await field(page).press("Escape");

  // Assert
  await expect.poll(() => typed(relay)).toBe("\x1b\x1b");

  // Act: the turn ends.
  relay.say(screen(false));

  // Assert: the dither gives its place to the reply's time.
  await expect(working(page)).toHaveCount(0);
  await expect(row(page, "r5").locator("time")).toHaveCount(1);
});

// The dither and the box's Stop are drawn from the fleet's stream alone: a
// terminal's screen and the board's snapshot can each still say a turn works
// after it ended, and neither draws either (#2292).
test("a turn the stream says ended draws no dither and no Stop while the terminal's screen and the board's snapshot say the session works", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "working" }), "", TALK);
  await openGoblin(page);
  await reportSession(page, { state: "ended" });

  // Act
  relay.say(screen(true, "Cogitating"));

  // Assert: the box heard the screen say its harness works, since it holds a
  // Send for the queue, and it offers Send, not Stop, while the conversation
  // draws the turn's end where the dither would be.
  await expect(box(page)).toHaveAttribute("data-queues", "");
  await expect(box(page).locator(".composer-send")).toHaveAccessibleName("Send");
  await expect(box(page).getByRole("button", { name: "Stop", exact: true })).toHaveCount(0);
  await expect(working(page)).toHaveCount(0);
  await expect(row(page, "r5").locator("time")).toHaveCount(1);
  await expect(page.getByRole("log")).not.toContainText("Cogitating");
});

test("with no reply yet the dither sits at the reply's place under his message without Stop, held typing or not", async ({ page }) => {
  // Arrange
  const asked = { messages: [...TALK.messages, { id: "p6", seq: 6, at: minute(6), kind: "prompt", source: "overlord", text: "And the iPhone one" }], conversation: "fixture-conversation", more: false };
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", asked);
  await openGoblin(page);

  // Act
  relay.say(screen(true));

  // Assert
  await expect(working(page)).toHaveCount(1);
  expect(await follows(page, '[data-message="p6"]', ".reply-working"), "the dither follows his message").toBe(true);
  await expect(working(page).getByRole("button")).toHaveCount(0);

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
    // The press on his card leaves the pointer over the panel that opened on
    // it, where it could hover his message and show that message's own actions.
    await page.mouse.move(0, 0);
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
  await expect(page.locator(".composer-dock .usage-bar .mark")).toHaveCount(0);
});

// The Overlord, 2026-10-28: "also have words that appear next to dither all
// the whimsical verbs claude whilst working uses". Beside the dither are the
// words the harness's own turn row says, or Working when it says none, and an
// idle session shows neither.
test("a working session's dither carries the harness's own words for what it does, and an idle session shows neither", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);

  // Act
  relay.say(screen(true, "Cogitating"));

  // Assert
  await expect(working(page)).toHaveCount(1);
  await expect(firstWord(page)).toHaveText("Cogitating…");
  await expect(working(page).getByRole("status", { name: "Cogitating" })).toBeVisible();

  // Act: the harness names another.
  relay.say(screen(true, "Sautéing"));

  // Assert
  await expect(firstWord(page)).toHaveText("Sautéing…");

  // Act: a turn row with no words of its own.
  relay.say(screen(true));

  // Assert
  await expect(firstWord(page)).toHaveText("Working…");

  // Act: the turn ends.
  relay.say(screen(false));

  // Assert
  await expect(working(page)).toHaveCount(0);
  await expect(page.getByRole("log")).not.toContainText(/Cogitating|Sautéing/);
});

// The words' animations start only once their room is measured, a frame or
// more after the words are in the page (working-word.tsx). A check that holds
// them waits in the page, a frame at a time, until they are there, and holds
// them in the task that found them, so nothing comes between the two (#1886).
// A hold with no wait read no word at all about once in twelve runs, and a
// wait made in a round trip of its own left a gap before the hold (#1914).
// Under reduced motion there is none. STARTED_MS is how long a check waits.
const STARTED_MS = 5_000;

// The Overlord, 2026-10-30: "make twitsting and teh verbs rotate in a type
// writere hroiztonal shimmer". Each word shows in its turn by its own
// animations, which come round again while the turn runs. shownAt holds them
// ms into that rotation and reads the word whose window is open then: its
// letters as drawn, the room the words take, the steps of its turn its window
// slides through, from unseen to whole a letter at a time, and the properties
// its typing and its shimmer move, which the compositor draws.
const shownAt = (page: Page, ms: number, isAnimated = true) => page.evaluate(async ({ ms, isAnimated, wait }) => {
  const wordEffects = () => document.getAnimations().filter((animation) => animation.effect instanceof KeyframeEffect && !!animation.effect.target?.closest(".reply-doing"));
  for (const began = performance.now(); isAnimated && !wordEffects().length; await new Promise(requestAnimationFrame)) {
    if (performance.now() - began > wait) throw new Error("the words' animations never started");
  }
  const doing = document.querySelector(".reply-doing");
  if (!doing) throw new Error("no words beside the dither");
  const effects = wordEffects();
  for (const animation of effects) {
    animation.pause();
    animation.currentTime = ms;
  }
  const room = doing.getBoundingClientRect();
  const frame = [...doing.querySelectorAll(":scope > .working-word")].find((each) => {
    const { left, right } = each.getBoundingClientRect();
    return Math.min(right, room.right) - Math.max(left, room.left) > 0.5;
  });
  const letters = frame?.querySelector(":scope > span");
  const effectOn = (target?: Element | null) => effects.map((animation) => animation.effect).find((effect): effect is KeyframeEffect => effect instanceof KeyframeEffect && !!target && effect.target === target);
  const typing = effectOn(letters), shimmer = effectOn(frame?.nextElementSibling);
  const moved = (effect?: KeyframeEffect) => [...new Set((effect?.getKeyframes() ?? []).flatMap((frame) => Object.keys(frame)))].filter((key) => !["offset", "computedOffset", "easing", "composite"].includes(key));
  const range = document.createRange();
  if (letters) range.selectNodeContents(letters);
  return {
    word: letters?.textContent ?? "",
    drawn: letters ? range.getBoundingClientRect().width : 0,
    room: room.width,
    // Its last two keyframes take it away at its turn's end.
    shifts: (typing?.getKeyframes() ?? []).slice(0, -2).map((frame) => Number(/translateX\((-?[\d.]+)px\)/.exec(String(frame.transform))?.[1])),
    typingMoves: moved(typing),
    shimmerMoves: moved(shimmer),
  };
}, { ms, isAnimated, wait: STARTED_MS });

test("the harness's words type out a letter at a time, a shimmer crosses them, and whimsical verbs follow in turn", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);

  // Act
  relay.say(screen(true, "Twisting"));
  await expect(firstWord(page)).toHaveText("Twisting…");
  const typing = await shownAt(page, 1_500);

  // Assert: Twisting and its ellipsis, typed in nine steps that each show one more letter,
  // and crossed by a shimmer, both by transform alone.
  expect(typing.word).toBe("Twisting…");
  expect(typing.shifts).toHaveLength("Twisting…".length + 1);
  expect(typing.shifts.every((shift, at) => at === 0 || shift < typing.shifts[at - 1]), JSON.stringify(typing.shifts)).toBe(true);
  expect(typing.shifts.at(-1)).toBe(0);
  expect(typing.typingMoves).toEqual(["transform"]);
  expect(typing.shimmerMoves).toEqual(["transform"]);

  // Act
  const next = await shownAt(page, 4_500);

  // Assert: the next verb, typed the same way.
  expect(next.word).toBe("Conjuring…");
  expect(next.shifts).toHaveLength("Conjuring…".length + 1);

  // Act and Assert
  expect((await shownAt(page, 7_500)).word).toBe("Tinkering…");
  await expect(working(page).getByRole("status", { name: "Twisting" })).toBeVisible();
});

// The CEO, 2026-10-30, of a conversation that changed in the page every
// time a verb came: "nothing moves when nothing runs". The words rotate by
// their own animations, so as the next verb shows nothing in the page
// changes.
test("the next verb shows in its turn with nothing in the page changed", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);
  relay.say(screen(true, "Twisting"));
  await expect(firstWord(page)).toHaveText("Twisting…");
  await working(page).evaluate((spot) => {
    const changes: string[] = [];
    new MutationObserver((records) => changes.push(...records.map((record) => record.type + ":" + (record.attributeName ?? "")))).observe(spot, { subtree: true, childList: true, characterData: true, attributes: true });
    Object.assign(window, { changes });
  });
  const shown = () => working(page).locator(".reply-doing").evaluate((doing) => {
    const room = doing.getBoundingClientRect();
    return [...doing.querySelectorAll(":scope > .working-word")].filter((frame) => {
      const { left, right } = frame.getBoundingClientRect();
      return Math.min(right, room.right) - Math.max(left, room.left) > 0.5;
    }).map((frame) => frame.textContent).join();
  });

  // Act
  await expect.poll(shown, { timeout: 10_000 }).toBe("Conjuring…");

  // Assert
  expect(await page.evaluate(() => (window as unknown as { changes: string[] }).changes)).toEqual([]);
});

// The shimmer is light on the letters alone: the live check of 2026-10-30
// found it greyed the ground about them, and past a word narrower than its
// room drew a hard-edged grey bar before Stop. shimmerAt holds the words'
// typing and shimmer at a share of the shimmer's crossing; lightOf reads how
// much the shimmer lightens the letters there, and the ground, against the
// same words with no shimmer and the ground with neither. The ground is what
// lies a pixel or more from the letters' ink, past their smoothed edges.
const shimmerAt = (page: Page, share: number) => page.evaluate(async ({ share, wait }) => {
  const wordEffects = () => document.getAnimations().filter((animation) => animation.effect instanceof KeyframeEffect && !!animation.effect.target?.closest(".reply-doing"));
  for (const began = performance.now(); !wordEffects().length; await new Promise(requestAnimationFrame)) {
    if (performance.now() - began > wait) throw new Error("the words' animations never started");
  }
  const words = wordEffects();
  const shimmer = words.map((animation) => animation.effect).find((effect): effect is KeyframeEffect => effect instanceof KeyframeEffect && effect.target === document.querySelector(".working-shine"));
  if (!shimmer) throw new Error("no shimmer crosses the words");
  // It crosses in the one stretch of its rotation that eases.
  const frames = shimmer.getKeyframes();
  const crossing = frames.findIndex((frame) => frame.easing === "ease-in-out");
  const [from, to] = [frames[crossing], frames[crossing + 1]].map((frame) => Number(frame.computedOffset));
  const rotation = Number(shimmer.getComputedTiming().duration);
  for (const animation of words) {
    animation.pause();
    animation.currentTime = rotation * (from + (to - from) * share);
  }
}, { share, wait: STARTED_MS });
const lightOf = async (page: Page) => {
  const doing = working(page).locator(".reply-doing");
  const hide = (selectors: string[]) => doing.evaluate((element, selectors) => {
    for (const each of element.querySelectorAll<HTMLElement>(".working-shine, .working-word")) each.style.visibility = selectors.some((selector) => each.matches(selector)) ? "hidden" : "";
  }, selectors);
  const shown = await doing.screenshot();
  await hide([".working-shine"]);
  const unlit = await doing.screenshot();
  await hide([".working-shine", ".working-word"]);
  const ground = await doing.screenshot();
  await hide([]);
  return page.evaluate(async (shots) => {
    const [shown, unlit, ground] = await Promise.all(shots.map(async (base64) => {
      const bitmap = await createImageBitmap(new Blob([Uint8Array.from(atob(base64), (letter) => letter.charCodeAt(0))], { type: "image/png" }));
      const context = new OffscreenCanvas(bitmap.width, bitmap.height).getContext("2d")!;
      context.drawImage(bitmap, 0, 0);
      return context.getImageData(0, 0, bitmap.width, bitmap.height);
    }));
    const { width, height } = shown;
    const channelsAt = (x: number, y: number) => [0, 1, 2].map((channel) => (y * width + x) * 4 + channel);
    const isInk = (x: number, y: number) => x >= 0 && y >= 0 && x < width && y < height && channelsAt(x, y).some((channel) => Math.abs(unlit.data[channel] - ground.data[channel]) > 2);
    const light = { letters: 0, ground: 0 };
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        const lifts = channelsAt(x, y).map((channel) => shown.data[channel] - unlit.data[channel]);
        if (isInk(x, y)) light.letters = Math.max(light.letters, ...lifts);
        else if (![-1, 0, 1].some((across) => [-1, 0, 1].some((down) => isInk(x + across, y + down)))) light.ground = Math.max(light.ground, ...lifts.map(Math.abs));
      }
    }
    return light;
  }, [shown, unlit, ground].map((shot) => shot.toString("base64")));
};

test("the shimmer lightens the letters and leaves the ground about them as it was, past the word too", async ({ page }) => {
  // Arrange: Twisting, narrower than its room.
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);
  relay.say(screen(true, "Twisting"));
  await expect(firstWord(page)).toHaveText("Twisting…");

  // Act: halfway across.
  await shimmerAt(page, .5);
  const halfway = await lightOf(page);

  // Assert
  expect(halfway.letters, "the letters are lit").toBeGreaterThan(30);
  expect(halfway.ground, "the ground stays as it was").toBeLessThanOrEqual(2);

  // Act: most of the way, where a light the width of the room lay past
  // the word.
  await shimmerAt(page, .7);

  // Assert
  expect((await lightOf(page)).ground, "the ground past the word stays as it was").toBeLessThanOrEqual(2);
});

test("with no words of its harness's own, Working types out first and gives way to the whimsical verbs", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);

  // Act
  relay.say(screen(true));
  await expect(firstWord(page)).toHaveText("Working…");
  const first = await shownAt(page, 1_500);

  // Assert
  expect(first.word).toBe("Working…");
  expect(first.shifts).toHaveLength("Working…".length + 1);
  expect((await shownAt(page, 4_500)).word).toBe("Conjuring…");
});

// The words keep the room of the widest as drawn, which is not always the
// one of most letters: Rummaging's nine letters are drawn wider than
// Scavenging's ten in most faces. So as they rotate the row keeps its width,
// and nothing after it moves.
test("the words keep the room of the widest as drawn while they rotate, so nothing after them moves", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);
  relay.say(screen(true));
  await expect(firstWord(page)).toHaveText("Working…");

  // Act: Working, then each of the eleven verbs in turn.
  const seen: { word: string; drawn: number; room: number }[] = [];
  for (let turn = 0; turn < 12; turn++) {
    const { word, drawn, room } = await shownAt(page, turn * 3_000 + 1_500);
    seen.push({ word, drawn, room });
  }

  // Assert
  expect(new Set(seen.map((each) => each.word)).size).toBe(12);
  expect(new Set(seen.map((each) => each.room)).size, JSON.stringify(seen)).toBe(1);
  expect(seen.filter((each) => each.drawn > each.room + 0.5), "every word fits its room").toEqual([]);
});

test.describe("under reduced motion", () => {
  test.use({ reducedMotion: "reduce" });

  test("the harness's words show whole and stay, and nothing beside them moves", async ({ page }) => {
    // Arrange
    await page.clock.install({ time: new Date("2026-10-22T09:10:00Z") });
    const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
    await openGoblin(page);

    // Act
    relay.say(screen(true, "Twisting"));
    await expect(firstWord(page)).toHaveText("Twisting…");
    await page.clock.runFor(10_000);

    // Assert: Twisting shows whole, and no other word.
    expect((await shownAt(page, 4_500, false)).word).toBe("Twisting…");
    expect(await page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running" && animation.effect instanceof KeyframeEffect && !!animation.effect.target?.closest(".context-pane")).length)).toBe(0);
  });
});

// On a touch screen too the dither's row is the dither and its words alone
// (the Overlord, 2026-11-03: "The stop button next to the When I'm the verb
// icon bothers me. Remove it."; #1987), and a tap on the box's Stop stops the
// turn.
test.describe("on a phone", () => {
  test.use({ viewport: { width: 393, height: 852 }, hasTouch: true, isMobile: true });

  test("the dither's row ends with its words and holds no Stop, and a tap on the box's Stop stops the turn", async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
    await openGoblin(page);

    // Act
    relay.say(screen(true, "Cogitating"));

    // Assert
    await expect(firstWord(page).locator(":scope > span")).toHaveText("Cogitating…");
    await expect(working(page).getByRole("button")).toHaveCount(0);
    const [dither, words] = await Promise.all([".working-indicator", ".reply-doing"].map((selector) => working(page).locator(selector).boundingBox()));
    expect(dither && words, "both drawn").toBeTruthy();
    if (!dither || !words) return;
    expect(words.x).toBeGreaterThanOrEqual(dither.x + dither.width);
    expect(Math.abs(dither.y + dither.height / 2 - (words.y + words.height / 2)), "one row").toBeLessThan(6);

    // Act
    await box(page).getByRole("button", { name: "Stop", exact: true }).tap();

    // Assert
    await expect.poll(() => typed(relay)).toBe("\x1b");
  });

  // A heading Codex names its step by, which the relay cuts at 48 letters,
  // is wider than a phone's row: it types out to the row's end and ends
  // there in an ellipsis.
  test("a heading wider than the row types to the row's end and ends in an ellipsis there", async ({ page }) => {
    // Arrange
    const heading = "Tracing how the phone terminal turns each swipe…";
    const relay = await open(page, snapshot({}, { phase: "settled", harness: "codex" }), "", TALK);
    await openGoblin(page);

    // Act
    relay.say(screen(true, heading));

    // Assert
    const letters = firstWord(page).locator(":scope > span");
    await expect(letters).toHaveText(heading);
    const drawn = await letters.evaluate((element) => ({ isCut: element.scrollWidth > element.clientWidth, overflow: getComputedStyle(element).textOverflow, width: element.getBoundingClientRect().width }));
    expect(drawn.isCut, "the heading is wider than the row").toBe(true);
    expect(drawn.overflow).toBe("ellipsis");
    const typing = await shownAt(page, 2_900);
    expect(typing.word).toBe(heading);
    expect(typing.shifts).toHaveLength(heading.length + 1);
    expect(typing.shifts[0], "the window starts at the row's end, not the heading's").toBeLessThanOrEqual(drawn.width + 0.5);
    expect(typing.shifts.every((shift, at) => at === 0 || shift <= typing.shifts[at - 1]), JSON.stringify(typing.shifts)).toBe(true);
    expect(typing.shifts.at(-1)).toBe(0);
    const [words, row] = await Promise.all([working(page).locator(".reply-doing").boundingBox(), working(page).boundingBox()]);
    expect(words && row, "both drawn").toBeTruthy();
    if (!words || !row) return;
    expect(words.x + words.width, "the words end at the row's end").toBeLessThanOrEqual(row.x + row.width + 0.5);
    await expect(working(page).getByRole("button")).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
  });
});

// The Overlord, 2026-10-28: "connecting screens don't need to display the
// dither or usage unavailable it's just messy when clicking into a goblin
// task". While the view connects no dither shows, and once it is live it does.
// A goblin the board's snapshot says works, and one whose own hooks say it
// compacts, which draws the dither whatever its turn.
for (const [what, first] of [
  ["working", snapshot({}, { runtime: { state: "working" } })],
  ["compacting", snapshot({ sessions: [{ id: "claude/goblin-session", native_id: "goblin-session", harness: "claude", role: "goblin", task_id: GOBLIN.id, generation: GOBLIN.generation, phase: "settled", updated_at: "2026-10-22T09:05:00Z", is_compacting: true }] }, { phase: "settled", session: "claude/goblin-session" })],
] as const) {
  test(`a ${what} goblin's dither shows only once its view has connected`, async ({ page }) => {
    // Arrange
    await holdMessages(page);
    await open(page, first, "", TALK);

    // Act
    await openGoblin(page);

    // Assert: connecting, and no dither.
    await expect(page.locator(".terminal-connecting")).toBeVisible();
    await expect(working(page)).toHaveCount(0);

    // Act
    await releaseMessages(page);

    // Assert
    await expect(row(page, "r5")).toBeVisible();
    await expect(working(page)).toHaveCount(1);
  });
}

// The Overlord, 2026-11-14, during a compaction: "it renders not confirmed old
// stale messages as if its queued or being sent" (#2541). While the session
// compacts, a message of his whose delivery ended with no take seen stays
// where he sent it, above Compacting, and only what still waits for the
// session is drawn under it.
test("while the session compacts a message never seen taken stays above Compacting, apart from what waits", async ({ page }) => {
  // Arrange
  const session = "claude/goblin-session";
  const compacting = (revision: number, actions: object[]) => snapshot({ revision, actions, sessions: [{ id: session, native_id: "goblin-session", harness: "claude", role: "goblin", task_id: GOBLIN.id, generation: GOBLIN.generation, phase: "settled", updated_at: minute(5), is_compacting: true }] }, { phase: "settled", harness: "claude", session });
  const relay = await open(page, compacting(1, []), "", TALK);
  await openGoblin(page);
  const ended = { id: "a1", kind: "message", task_id: GOBLIN.id, text: "check the phone spec", status: "uncertain", message: "The terminal closed before it took it.", awaiting: "", created_at: minute(6), updated_at: minute(6) };
  const waits = { id: "a2", kind: "message", task_id: GOBLIN.id, text: "then the tablet", status: "queued", message: "Queued.", created_at: minute(7), updated_at: minute(7) };

  // Act
  relay.say(screen(true, "Tinkering"));
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), compacting(2, [ended, waits]));

  // Assert
  await expect(working(page).locator(".working-word")).toHaveText(["Compacting…"]);
  await expect(page.getByRole("log").locator('[data-queued="a1"]')).toContainText("Not confirmed");
  await expect(page.getByRole("log").locator('[data-queued="a2"]')).toContainText("Queued");
  expect(await follows(page, '[data-message="r5"]', '[data-queued="a1"]'), "it comes after the rows before it").toBe(true);
  expect(await follows(page, '[data-queued="a1"]', ".reply-working"), "it stays above Compacting").toBe(true);
  expect(await follows(page, ".reply-working", '[data-queued="a2"]'), "what waits stays under Compacting").toBe(true);
});

// The Overlord, 2026-11-04: a compaction showed whimsical verbs as if the
// session were at work on his ask. While the session's own hooks say it
// compacts, in a turn or between turns, the dither's words say Compacting…
// alone; his draft and the file added to it stay in the box, the message he
// sent meanwhile stays Queued, never Sent, until the board hands it over, it
// ends on the same session, the compaction replays nothing he already read,
// and Terminal shows the harness's own screen with no queue pane and no
// second scroller.
for (const [harness, isTurnRunning] of [["claude", true], ["codex", false]] as const) {
  test(`while ${harness} compacts ${isTurnRunning ? "in a turn" : "between turns"} the dither says Compacting… alone and his draft, file and queued message stay`, async ({ page }) => {
    // Arrange
    const session = harness + "/goblin-session";
    const compacting = (isCompacting: boolean, revision: number, actions: object[] = []) => snapshot({ revision, actions, sessions: [{ id: session, native_id: "goblin-session", harness, role: "goblin", task_id: GOBLIN.id, generation: GOBLIN.generation, phase: "settled", updated_at: "2026-10-22T09:05:00Z", is_compacting: isCompacting }] }, { phase: "settled", harness, session });
    const publish = (detail: object) => page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), detail);
    const relay = await open(page, compacting(false, 1), "", TALK);
    const posted: { id: string; kind: string; task_id: string; text: string }[] = [];
    await page.route("**/api/actions", (route) => { posted.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: {} }); });
    await page.route("**/api/composer/file**", (route) => route.fulfill({ status: 201, json: { path: "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-1.md" } }));
    await openGoblin(page);

    // Act
    relay.say(screen(isTurnRunning, isTurnRunning ? "Tinkering" : ""));
    await publish(compacting(true, 2));
    await expect(working(page)).toHaveCount(1);
    await field(page).fill("Queue this while it compacts");
    await field(page).press("Enter");
    await expect.poll(() => posted.length).toBe(1);
    const queued = { ...posted[0], status: "queued" };
    await publish(compacting(true, 3, [queued]));
    await field(page).fill("keep this draft");
    await box(page).locator('input[type="file"]').setInputFiles({ name: "notes.md", mimeType: "text/markdown", buffer: Buffer.from("# notes") });
    await expect(box(page).getByRole("list", { name: "Attached files" })).toContainText("pasted-1.md");

    // Assert
    const shown = working(page).locator(".working-word");
    await expect(shown).toHaveText(["Compacting…"]);
    await expect(working(page).getByRole("status", { name: "Compacting" })).toBeVisible();
    await expect(working(page).locator(".reply-doing")).toHaveCSS("color", "rgb(178, 154, 233)");
    await expect(working(page).getByRole("status", { name: "Compacting" })).toHaveCSS("color", "rgb(178, 154, 233)");
    expect(await working(page).locator(".reply-doing").evaluate((element) => element.getAnimations({ subtree: true }).length), "Compacting stays visible instead of retyping every cycle").toBe(0);
    const bubble = page.getByRole("log").locator(`[data-queued="${queued.id}"]`);
    await expect(bubble).toContainText("Queued");
    await expect(bubble).not.toContainText("Sent");
    expect(typed(relay), "nothing is typed into a compacting harness").toBe("");

    // Act: the compaction ends with its summary row.
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [
      { id: "c6", seq: 6, at: "2026-10-22T09:06:00Z", kind: "compaction", trigger: "auto", text: "The swipe is fixed and the phone spec runs." },
    ] } } })));
    relay.say(screen(false));
    await publish(compacting(false, 4, [queued]));

    // Assert
    await expect(row(page, "c6")).toBeVisible();
    await expect(working(page), "the state ends with the compaction").toHaveCount(0);
    for (const id of ["p0", "r1", "r3", "p4", "r5"]) await expect(row(page, id), id + " shows once").toHaveCount(1);
    await expect(page.getByRole("log").getByText("Fixed the swipe.", { exact: true })).toHaveCount(1);
    await expect(bubble).toContainText("Queued");
    await expect(bubble).not.toContainText("Sent");
    await expect(field(page)).toHaveValue("keep this draft");
    await expect(box(page).getByRole("list", { name: "Attached files" })).toContainText("pasted-1.md");
    expect(posted, "the compaction sent nothing again").toHaveLength(1);

    // Act
    await publish(compacting(true, 5, [queued]));
    await pickStyle(page, "Terminal");

    // Assert
    await expect(page.locator(".xterm").first()).toBeVisible();
    await expect(page.locator(".classic-queue:visible"), "no queue pane").toHaveCount(0);
    await expect(page.locator(".conversation:visible"), "no second scroller").toHaveCount(0);
  });
}
