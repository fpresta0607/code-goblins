import { expect, test, type Page } from "./site";
import { holdMessages, open, openGoblin, releaseMessages, snapshot, typed } from "./session-board";

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
  await expect(page.locator(".composer-dock")).not.toContainText(/Sent|Replied/);
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
  await expect(firstWord(page)).toHaveText("Cogitating");
  await expect(working(page).getByRole("status", { name: "Cogitating" })).toBeVisible();

  // Act: the harness names another.
  relay.say(screen(true, "Sautéing"));

  // Assert
  await expect(firstWord(page)).toHaveText("Sautéing");

  // Act: a turn row with no words of its own.
  relay.say(screen(true));

  // Assert
  await expect(firstWord(page)).toHaveText("Working");

  // Act: the turn ends.
  relay.say(screen(false));

  // Assert
  await expect(working(page)).toHaveCount(0);
  await expect(page.getByRole("log")).not.toContainText(/Cogitating|Sautéing/);
});

// The Overlord, 2026-10-30: "make twitsting and teh verbs rotate in a type
// writere hroiztonal shimmer". Each word shows in its turn by its own
// animations, which come round again while the turn runs. shownAt holds them
// ms into that rotation and reads the word whose window is open then: its
// letters as drawn, the room the words take, the steps of its turn its window
// slides through, from unseen to whole a letter at a time, and the properties
// its typing and its shimmer move, which the compositor draws.
const shownAt = (page: Page, ms: number) => page.evaluate((ms) => {
  const doing = document.querySelector(".reply-doing");
  if (!doing) throw new Error("no words beside the dither");
  const effects = document.getAnimations().filter((animation) => animation.effect instanceof KeyframeEffect && !!animation.effect.target?.closest(".reply-doing"));
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
}, ms);

test("the harness's words type out a letter at a time, a shimmer crosses them, and whimsical verbs follow in turn", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);

  // Act
  relay.say(screen(true, "Twisting"));
  await expect(firstWord(page)).toHaveText("Twisting");
  const typing = await shownAt(page, 1_500);

  // Assert: Twisting, typed in eight steps that each show one more letter,
  // and crossed by a shimmer, both by transform alone.
  expect(typing.word).toBe("Twisting");
  expect(typing.shifts).toHaveLength("Twisting".length + 1);
  expect(typing.shifts.every((shift, at) => at === 0 || shift < typing.shifts[at - 1]), JSON.stringify(typing.shifts)).toBe(true);
  expect(typing.shifts.at(-1)).toBe(0);
  expect(typing.typingMoves).toEqual(["transform"]);
  expect(typing.shimmerMoves).toEqual(["transform"]);

  // Act
  const next = await shownAt(page, 4_500);

  // Assert: the next verb, typed the same way.
  expect(next.word).toBe("Conjuring");
  expect(next.shifts).toHaveLength("Conjuring".length + 1);

  // Act and Assert
  expect((await shownAt(page, 7_500)).word).toBe("Tinkering");
  await expect(working(page).getByRole("status", { name: "Twisting" })).toBeVisible();
});

// The CFO, 2026-10-30, of a conversation that changed in the page every
// time a verb came: "nothing moves when nothing runs". The words rotate by
// their own animations, so as the next verb shows nothing in the page
// changes.
test("the next verb shows in its turn with nothing in the page changed", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);
  relay.say(screen(true, "Twisting"));
  await expect(firstWord(page)).toHaveText("Twisting");
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
  await expect.poll(shown, { timeout: 10_000 }).toBe("Conjuring");

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
const shimmerAt = (page: Page, share: number) => page.evaluate((share) => {
  const words = document.getAnimations().filter((animation) => animation.effect instanceof KeyframeEffect && !!animation.effect.target?.closest(".reply-doing"));
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
}, share);
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
  await expect(firstWord(page)).toHaveText("Twisting");

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
  await expect(firstWord(page)).toHaveText("Working");
  const first = await shownAt(page, 1_500);

  // Assert
  expect(first.word).toBe("Working");
  expect(first.shifts).toHaveLength("Working".length + 1);
  expect((await shownAt(page, 4_500)).word).toBe("Conjuring");
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
  await expect(firstWord(page)).toHaveText("Working");

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
    await expect(firstWord(page)).toHaveText("Twisting");
    await page.clock.runFor(10_000);

    // Assert: Twisting shows whole, and no other word.
    expect((await shownAt(page, 4_500)).word).toBe("Twisting");
    expect(await page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running" && animation.effect instanceof KeyframeEffect && !!animation.effect.target?.closest(".context-pane")).length)).toBe(0);
  });
});

// The Overlord, 2026-10-28, from his phone: "dither stream on mobile stop icon
// is automatically displayed however on mobile put the button on the end of
// it". On a touch screen Stop shows at the end of the dither's row, after its
// words, and a tap stops the turn.
test.describe("on a phone", () => {
  test.use({ viewport: { width: 393, height: 852 }, hasTouch: true, isMobile: true });

  test("Stop sits at the end of the dither's row, after its words, and a tap stops the turn", async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({}, { phase: "settled" }), "", TALK);
    await openGoblin(page);

    // Act
    relay.say(screen(true, "Cogitating"));

    // Assert
    const stop = working(page).getByRole("button", { name: "Stop", exact: true });
    await expect(stop).toBeVisible();
    await expect(stop.locator(".icon")).toHaveCSS("opacity", "1");
    const [dither, words, button] = await Promise.all([".working-indicator", ".reply-doing", ".reply-stop"].map((selector) => working(page).locator(selector).boundingBox()));
    expect(dither && words && button, "all three drawn").toBeTruthy();
    if (!dither || !words || !button) return;
    expect(words.x).toBeGreaterThanOrEqual(dither.x + dither.width);
    expect(button.x, "Stop after the words").toBeGreaterThanOrEqual(words.x + words.width);
    for (const part of [dither, words]) expect(Math.abs(part.y + part.height / 2 - (button.y + button.height / 2)), "one row").toBeLessThan(6);

    // Act
    await stop.tap();

    // Assert
    await expect.poll(() => typed(relay)).toBe("\x1b");
  });

  // A heading Codex names its step by, which the relay cuts at 48 letters,
  // is wider than a phone's row: it types out to the row's end and ends
  // there in an ellipsis, before Stop.
  test("a heading wider than the row types to the row's end and ends in an ellipsis before Stop", async ({ page }) => {
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
    const [words, stop] = await Promise.all([".reply-doing", ".reply-stop"].map((selector) => working(page).locator(selector).boundingBox()));
    expect(words && stop, "both drawn").toBeTruthy();
    if (!words || !stop) return;
    expect(words.x + words.width, "the words end before Stop").toBeLessThanOrEqual(stop.x);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
  });
});

// The Overlord, 2026-10-28: "connecting screens don't need to display the
// dither or usage unavailable it's just messy when clicking into a goblin
// task". While the view connects no dither shows, and once it is live it does.
test("a working goblin's dither shows only once its view has connected", async ({ page }) => {
  // Arrange
  await holdMessages(page);
  await open(page, snapshot({}, { runtime: { state: "working" } }), "", TALK);

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
