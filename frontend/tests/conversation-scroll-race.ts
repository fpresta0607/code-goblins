import { expect, type Locator, type Page } from "./site";
import { at, GOBLIN, NAME, open, openGoblin, snapshot } from "./session-board";

const conversation = "fixture-conversation";
const reply = (seq: number, text: string) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text }] });
const messages = Array.from({ length: 1000 }, (_, seq) => reply(seq, "Answer " + seq + "\n\nMore to read."));
const action = { id: "q1", kind: "message", task_id: GOBLIN.id, generation: GOBLIN.generation, status: "queued", text: "Waiting for delivery", created_at: at, updated_at: at };

async function settled(talk: Locator) {
  await talk.evaluate(async () => { for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame); });
}

// delayedScroll scrolls with the scroll's own event held back while an update
// arrives. With drawsRowAbove the scroll goes past two rows at their estimate,
// so the window draws a row above him wherever the list rested, as 80 px does
// only from some places.
export async function delayedScroll(page: Page, owner: "CEO" | "goblin", movement: "keyboard" | "wheel", update: "stream" | "board", drawsRowAbove = false) {
  const queued = { ...action, task_id: owner === "CEO" ? "" : GOBLIN.id };
  await open(page, snapshot({ actions: [queued] }), "", { messages, conversation, more: false });
  if (owner === "CEO") await page.keyboard.press("Control+Alt+1"); else await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + (owner === "CEO" ? "CEO" : NAME) });
  await expect(talk.locator('[data-message="m999"]')).toBeVisible();
  await expect(talk.locator('[data-queued="q1"]')).toBeVisible();
  await settled(talk);
  await expect.poll(() => distanceToEnd(talk)).toBeLessThanOrEqual(1);
  if (movement === "keyboard") {
    await page.addStyleTag({ content: ".conversation-items > [data-message] { height: 160px; min-height: 160px; box-sizing: border-box; } .conversation-items { row-gap: 18px; } .conversation-room { margin-top: -18px; }" });
    await settled(talk);
    for (let step = 0; step < 2; step++) {
      await talk.evaluate((element) => element.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", shiftKey: true, bubbles: true })));
      await settled(talk);
    }
    await page.getByRole("button", { name: "Jump to latest" }).click();
    await settled(talk);
  }
  const firstDrawn = () => talk.locator("[data-message]").first().getAttribute("data-message");
  const drawnBefore = await firstDrawn();
  const before = await talk.evaluate((element, { movement, update, queued, board, reach }) => {
    const guard = (event: Event) => { if (event.target === element) event.stopImmediatePropagation(); };
    window.addEventListener("scroll", guard, true);
    Object.assign(window, { releaseScroll: () => window.removeEventListener("scroll", guard, true) });
    const end = element.scrollTop;
    if (movement === "keyboard") element.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", shiftKey: true, bubbles: true }));
    else element.scrollTop -= reach;
    const top = element.getBoundingClientRect().top;
    const row = [...element.querySelectorAll<HTMLElement>("[data-message]")].find((item) => item.getBoundingClientRect().bottom > top)!;
    Object.assign(window, { raceRow: row });
    const place = { end, top: element.scrollTop, height: element.scrollHeight, id: row.dataset.message!, text: row.textContent, offset: row.getBoundingClientRect().top - top };
    if (update === "stream") window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [
      { id: place.id, seq: Number(place.id.slice(1)), at: "2026-10-22T09:00:00Z", kind: "reply", blocks: [{ kind: "text", text: "Changed during scroll" }] },
      { id: "m1000", seq: 1000, at: "2026-10-22T09:00:00Z", kind: "reply", blocks: [{ kind: "text", text: "Newest during scroll" }] },
    ] } } }));
    else window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: { ...board, revision: 2, actions: [{ ...queued, status: "succeeded" }] } }));
    return place;
  }, { movement, update, queued, board: snapshot(), reach: drawsRowAbove ? 240 : 80 });
  expect(before.top).toBeLessThan(before.end - 24);
  await settled(talk);
  if (drawsRowAbove) expect(await firstDrawn()).not.toBe(drawnBefore);
  const after = await talk.evaluate((element, id) => {
    const row = element.querySelector<HTMLElement>(`[data-message="${id}"]`);
    return { top: element.scrollTop, height: element.scrollHeight, same: row === (window as unknown as { raceRow: Element }).raceRow,
      text: row?.textContent, offset: row && row.getBoundingClientRect().top - element.getBoundingClientRect().top };
  }, before.id);
  // A row drawn above him taller than its estimate moves the list's top and its
  // height together at rest, and what he reads stays put.
  expect({ fromEnd: after.height - after.top, same: after.same, text: after.text, offset: after.offset }).toEqual({ fromEnd: before.height - before.top, same: true, text: before.text, offset: before.offset });
  await expect(talk.locator('[data-queued="q1"]')).toHaveCount(1);
  const jump = page.getByRole("button", { name: "Jump to latest" });
  await expect(jump).toBeVisible();
  await jump.evaluate((button: HTMLButtonElement) => button.click());
  await talk.evaluate((element) => {
    (window as unknown as { releaseScroll: () => void }).releaseScroll();
    element.dispatchEvent(new Event("scroll"));
  });
  if (update === "stream") await expect(talk.locator('[data-message="m1000"]')).toHaveText(/Newest during scroll/);
  else await expect(talk.locator('[data-queued="q1"]')).toHaveCount(0);
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
  await settled(talk);
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
}

// A long history whose rows lie far from the window's estimate, each sized
// only once it is drawn, and the reply that goes on streaming at its end.
const question = (seq: number) => ({ id: "m" + seq, seq, at, kind: "prompt", source: "overlord", text: "Question " + seq });
const answer = (seq: number) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text: Array.from({ length: 1 + seq % 5 }, (_, line) =>
  `Paragraph ${line} of answer ${seq}: the gate ran, the tests passed and the board kept its place while he read.`).join("\n\n") }] });
const HISTORY = Array.from({ length: 300 }, (_, seq) => seq % 2 ? answer(seq) : question(seq));
const WORKING = { type: "screen", asking: false, open: false, working: true, submission: "", doing: "Cogitating" };

interface Frame { t: number; top: number; end: number; rows: { id: string; offset: number }[] }
interface Written { t: number; frame: number; kind: string; delta: number; sinceScroll: number }
interface Watched { frames: Frame[]; writes: Written[]; rowsDrawn: number[] }
interface Watcher { watch: () => void; watched: () => Watched; quiet: () => number; flick: (distance: number) => Promise<void>; land: (distance: number) => void; startStream: (every?: number) => void; stopStream: () => void; newest: () => string }
const watcher = (page: Page) => page.evaluate(() => (window as unknown as Watcher).watch());
const watched = (page: Page) => page.evaluate(() => (window as unknown as Watcher).watched());

// streamingTalk opens the goblin's messages at their end while its reply
// streams through the board's message path: the reply that grows and the
// usage it spends every tenth of a second, the terminal's working line and
// a board snapshot every third. Each frame notes the scroll top and where
// the rows in view lie, and each scroll position the page writes to the
// list is noted, apart from the reader's own: how far it moved the list and
// how long after the list last scrolled. With isStreaming false no reply is
// in progress until start begins it, and owner CEO opens the CEO's messages.
async function streamingTalk(page: Page, { isStreaming = true, owner = "goblin" }: { isStreaming?: boolean; owner?: "CEO" | "goblin" } = {}) {
  await page.addInitScript(() => {
    const frames: Frame[] = [], writes: Written[] = [], rowsDrawn: number[] = [];
    let isWatching = false, scrolled = -Infinity;
    const isList = (element: Element) => element.classList.contains("conversation");
    addEventListener("scroll", (event) => { if (event.target instanceof Element && isList(event.target)) scrolled = performance.now(); }, { capture: true, passive: true });
    const scrollTop = Object.getOwnPropertyDescriptor(Element.prototype, "scrollTop")!;
    const written = (box: Element, kind: string, write: () => void) => {
      if (!isWatching || !isList(box)) {
        write();
        return;
      }
      const before = scrollTop.get!.call(box), at = performance.now();
      write();
      writes.push({ t: at, frame: frames.length, kind, delta: scrollTop.get!.call(box) - before, sinceScroll: at - scrolled });
    };
    Object.defineProperty(Element.prototype, "scrollTop", { configurable: true, get(this: Element) { return scrollTop.get!.call(this); },
      set(this: Element, value: number) { written(this, "scrollTop", () => scrollTop.set!.call(this, value)); } });
    for (const name of ["scrollTo", "scrollBy"] as const) {
      const native = Element.prototype[name] as (this: Element, ...values: unknown[]) => void;
      Element.prototype[name] = function (this: Element, ...values: unknown[]) {
        written(this, name, () => native.apply(this, values));
      } as typeof Element.prototype.scrollTo;
    }
    const talk = () => document.querySelector<HTMLElement>(".messages-view:not([hidden]) .conversation");
    const rows = new MutationObserver((records) => { rowsDrawn.push(...records.map(() => performance.now())); });
    const note = () => {
      const box = talk();
      if (isWatching && box) {
        const edge = box.getBoundingClientRect(), top = scrollTop.get!.call(box);
        frames.push({ t: performance.now(), top, end: box.scrollHeight - top - box.clientHeight, rows: [...box.querySelectorAll<HTMLElement>(":scope > .conversation-items > [data-message]")].flatMap((row) => {
          const place = row.getBoundingClientRect();
          return place.bottom > edge.top && place.top < edge.bottom ? [{ id: row.dataset.message!, offset: place.top - edge.top }] : [];
        }) });
      }
      requestAnimationFrame(note);
    };
    requestAnimationFrame(note);
    const watcher: Omit<Watcher, "startStream" | "stopStream" | "newest"> = {
      watch: () => {
        frames.length = 0; writes.length = 0; rowsDrawn.length = 0; isWatching = true;
        rows.disconnect();
        rows.observe(talk()!.querySelector(".conversation-items")!, { childList: true });
      },
      watched: () => ({ frames, writes, rowsDrawn }),
      quiet: () => performance.now() - scrolled,
      // His own scroll, as a finger's flick glides the list on a frame at a
      // time, which the page does not count among its writes.
      flick: async (distance) => {
        const box = talk()!;
        for (let step = distance / 5; Math.abs(step) > 1; step *= .8) {
          scrollTop.set!.call(box, scrollTop.get!.call(box) + step);
          await new Promise(requestAnimationFrame);
        }
      },
      // His own scroll this far in one frame, with no frame after it.
      land: (distance) => {
        const box = talk()!;
        scrollTop.set!.call(box, scrollTop.get!.call(box) + distance);
      },
    };
    Object.assign(window, watcher);
  });
  const relay = await open(page, snapshot(), "", { conversation: "fixture-conversation", messages: HISTORY, more: false });
  if (owner === "CEO") await page.keyboard.press("Control+Alt+1"); else await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + (owner === "CEO" ? "CEO" : NAME) });
  await expect(talk.locator('[data-message="m299"]')).toBeVisible();
  await settled(talk);
  if (isStreaming) relay.say(WORKING);
  await page.evaluate(({ board, isStreaming, last }) => {
    let tick = 0, timer: ReturnType<typeof setInterval> | undefined;
    let reply = { id: "m300", seq: 300, at: "2026-10-22T09:00:00Z", kind: "reply", blocks: [] as object[] };
    const send = (type: string, data: object) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type, data: { conversation: "fixture-conversation", ...data } } }));
    const stream: Pick<Watcher, "startStream" | "stopStream" | "newest"> = {
      startStream: (every = 100) => {
        timer = setInterval(() => {
          tick++;
          const tokens = 1000 + tick * 731;
          // A reply ends after forty lines and the next one starts, as a
          // session's do, so a slow machine's long run never grows one
          // message the page redraws on every line without end.
          if (reply.blocks.length === 40) reply = { ...reply, id: "m" + (reply.seq + 1), seq: reply.seq + 1, blocks: [] };
          reply.blocks = [...reply.blocks, { kind: "text", text: "Streamed line " + tick + " of the reply that goes on while he reads." }];
          send("messages", { messages: [{ ...reply }] });
          send("usage", { usage: { input_tokens: tokens, output_tokens: tokens / 10, cache_read_tokens: tokens * 3, cost_usd: tokens / 100000, context_tokens: tokens, context_window: 200000, model: "claude-opus-5-5", effort: "xhigh" } });
          if (tick % 3 === 0) window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: { ...board, revision: 2 + tick } }));
        }, every);
      },
      stopStream: () => clearInterval(timer),
      // The message the stream sent last, or the history's last before it
      // sends any.
      newest: () => tick ? reply.id : last,
    };
    Object.assign(window, stream);
    if (isStreaming) stream.startStream();
  }, { board: snapshot(), isStreaming, last: HISTORY[HISTORY.length - 1].id });
  const saying = () => setInterval(() => relay.say({ ...WORKING, doing: ["Cogitating", "Brewing", "Pondering"][Math.floor(Date.now() / 300) % 3] }), 300);
  let says = isStreaming ? saying() : undefined;
  await page.waitForTimeout(400);
  const start = async (every?: number) => {
    relay.say(WORKING);
    await page.evaluate((every) => (window as unknown as Watcher).startStream(every), every);
    says = saying();
    await page.waitForTimeout(400);
  };
  return { talk, start, stop: () => { clearInterval(says); return page.evaluate(() => (window as unknown as Watcher).stopStream()); } };
}

// writtenBefore is how far the page's writes moved the list since the frame
// before this one. Each write names the frame it came before, as the clock is
// too coarse to order them: WebKit's ticks in whole milliseconds.
const writtenBefore = (writes: Written[], at: number) => writes.filter((write) => write.frame === at).reduce((sum, write) => sum + write.delta, 0);

// moved is the most any row in view jumped from one frame to the next: how
// far it moved within the list's content, less how far the page's own scroll
// writes moved the list meanwhile. His own scroll moves the scroll top alone,
// never a row's place in the content, and a row the page moves by as much as
// it scrolls the list stays where he sees it.
export function moved({ frames, writes }: Watched) {
  let most = 0;
  for (let at = 1; at < frames.length; at++) {
    const written = writtenBefore(writes, at);
    for (const row of frames[at].rows) {
      const prior = frames[at - 1].rows.find((each) => each.id === row.id);
      if (prior) most = Math.max(most, Math.abs(row.offset + frames[at].top - prior.offset - frames[at - 1].top - written));
    }
  }
  return most;
}

// fellBack is the most the list fell back in one frame as he read down, past
// what the page wrote: a browser that pulls the scroll position up by itself
// writes nothing, so no count of the page's writes shows it.
function fellBack({ frames, writes }: Watched) {
  let most = 0;
  for (let at = 1; at < frames.length; at++) most = Math.max(most, frames[at - 1].top + writtenBefore(writes, at) - frames[at].top);
  return most;
}

// whileScrolling keeps the scroll positions the page wrote while his scroll
// still moved the list: a browser that animates his scroll drops such a
// write and shows the shift it paid for, where one written once his scroll
// rests lands with it in the same frame.
const whileScrolling = (writes: Written[]) => writes.filter((write) => write.sinceScroll < 200);

// quiet waits until his scroll, and whatever the list writes once it rests,
// has been still for a while.
async function quiet(page: Page) {
  await page.waitForTimeout(400);
  await expect.poll(() => page.evaluate(() => (window as unknown as Watcher).quiet())).toBeGreaterThan(600);
}

// watchStill watches the list while he reads still for this long and at
// least twenty frames, as a loaded machine's WebKit can draw none for a while.
async function watchStill(page: Page, ms: number) {
  await watcher(page);
  await page.waitForTimeout(ms);
  await expect.poll(() => page.evaluate(() => (window as unknown as Watcher).watched().frames.length), { timeout: 30_000 }).toBeGreaterThanOrEqual(20);
  return watched(page);
}

// stillness is the most the row under the list's top edge in the first frame
// moved on screen in any frame after.
function stillness(frames: Frame[]) {
  const first = frames[0].rows[0];
  return Math.max(...frames.map((frame) => Math.abs((frame.rows.find((row) => row.id === first.id)?.offset ?? Infinity) - first.offset)));
}

// fingerDown sets a finger on the list's middle as WebKit and Chromium
// deliver it: its pointer cancelled once the browser takes the pan, its touch
// held until fingerUp lifts it, which says when it did. WebKit makes a touch
// only by its older document.createTouch, and Chromium only by new Touch.
const fingerDown = (talk: Locator) => talk.evaluate((element) => {
  const edge = element.getBoundingClientRect();
  const [clientX, clientY] = [edge.left + edge.width / 2, edge.top + edge.height / 2];
  const target = document.elementFromPoint(clientX, clientY)!;
  const older = document as unknown as { createTouch?: (view: Window, target: EventTarget, identifier: number, pageX: number, pageY: number, screenX: number, screenY: number) => Touch; createTouchList?: (...touches: Touch[]) => Touch[] };
  const finger = older.createTouch && older.createTouchList ? older.createTouchList(older.createTouch(window, target, 1, clientX, clientY, clientX, clientY)) : [new Touch({ identifier: 1, target, clientX, clientY })];
  const pointer = { bubbles: true, composed: true, pointerId: 2, pointerType: "touch", isPrimary: true, clientX, clientY };
  target.dispatchEvent(new PointerEvent("pointerdown", { ...pointer, cancelable: true, buttons: 1 }));
  target.dispatchEvent(new TouchEvent("touchstart", { bubbles: true, cancelable: true, composed: true, touches: finger, targetTouches: finger, changedTouches: finger }));
  target.dispatchEvent(new PointerEvent("pointercancel", pointer));
  Object.assign(window, { finger });
});
const fingerUp = (page: Page) => page.evaluate(() => {
  const at = performance.now();
  window.dispatchEvent(new TouchEvent("touchend", { changedTouches: (window as unknown as { finger: Touch[] }).finger }));
  return at;
});
const glide = (page: Page, distance: number) => page.evaluate((distance) => (window as unknown as Watcher).flick(distance), distance);
const distanceToEnd = (talk: Locator) => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight);
const newest = (page: Page) => page.evaluate(() => (window as unknown as Watcher).newest());

export type Reading = "wheel" | "keyboard" | "scrollbar" | "flick";

// readUpWhileStreaming scrolls up through rows the window has not drawn yet
// as the reply streams, then reads still: the rows in view move only as his
// scroll moves them, and once he rests the stream moves nothing, writes no
// scroll position and draws no row (the Overlord, 2026-11-03: "As I'm
// scrolling up, it jitters... Fix that. No jitter.").
export async function readUpWhileStreaming(page: Page, reading: Reading) {
  const { talk, stop } = await streamingTalk(page);
  const box = (await talk.boundingBox())!;
  // At the end the scrollbar's thumb sits right above its down arrow.
  const gutter = await talk.evaluate((element: HTMLElement) => element.offsetWidth - element.clientWidth);
  const thumb = { x: box.x + box.width - gutter / 2, y: box.y + box.height - gutter - 8 };
  if (reading === "wheel") await talk.hover();
  if (reading === "keyboard") await talk.focus();
  if (reading === "scrollbar") {
    await page.mouse.move(thumb.x, thumb.y);
    await page.mouse.down();
  }
  const steps = { wheel: 24, keyboard: 8, scrollbar: 24, flick: 4 }[reading];
  for (let step = 0; step < steps; step++) {
    // Once his first step has left the end, which the list followed until
    // then, the list moves only as he does.
    if (step === 1) await watcher(page);
    if (reading === "wheel") await page.mouse.wheel(0, -160);
    if (reading === "keyboard") await page.keyboard.press("PageUp");
    if (reading === "scrollbar") await page.mouse.move(thumb.x, thumb.y - (step + 1) * 4);
    if (reading === "flick") await page.evaluate(() => (window as unknown as Watcher).flick(-1200));
    await page.waitForTimeout(reading === "flick" ? 0 : 50);
  }
  if (reading === "scrollbar") {
    await page.waitForTimeout(400);
    await page.mouse.up();
  }
  const scrolled = await watched(page);
  // His own scroll is how far the scroll top moved, less what the page wrote
  // to it once his scroll rested between steps.
  const [first, last] = [scrolled.frames[0], scrolled.frames.at(-1)!];
  const written = scrolled.writes.filter((write) => write.t > first.t && write.t <= last.t).reduce((sum, write) => sum + write.delta, 0);
  expect(last.top - first.top - written).toBeLessThan(-2000);
  expect(scrolled.rowsDrawn.length).toBeGreaterThan(0);
  expect({ writes: whileScrolling(scrolled.writes), isStill: moved(scrolled) <= 1 }).toEqual({ writes: [], isStill: true });
  await quiet(page);
  const read = await watchStill(page, 1200);
  expect({ writes: read.writes, rowsDrawn: read.rowsDrawn, isStill: stillness(read.frames) <= 1 }).toEqual({ writes: [], rowsDrawn: [], isStill: true });
  await page.getByRole("button", { name: "Jump to latest" }).click();
  await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
  await page.waitForTimeout(300);
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
  await stop();
}

// leaveEndWhileStreaming moves a few pixels up from the end between two lines
// of a streaming reply, as a trackpad's slow drag or a flick's first frames
// do: the list stops following at once, so the next streamed line never
// pulls him back.
export async function leaveEndWhileStreaming(page: Page, leave: (talk: Locator) => Promise<void>) {
  const { talk, stop } = await streamingTalk(page);
  await page.evaluate(() => (window as unknown as Watcher).stopStream());
  await settled(talk);
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
  await leave(talk);
  await quiet(page);
  await page.evaluate(() => (window as unknown as Watcher).startStream());
  const read = await watchStill(page, 800);
  expect({ writes: read.writes, isStill: stillness(read.frames) <= 1 }).toEqual({ writes: [], isStill: true });
  await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
  await stop();
}

// rowGrowsAboveHim rests a way up from the end while a reply streams, and a
// row above him grows as a picture in it loads. What the list writes to keep
// his place is no scroll of his down: what streamed stays held back, and from
// a few pixels off the end he does not follow it again.
export async function rowGrowsAboveHim(page: Page, reach: number) {
  const { talk, stop } = await streamingTalk(page);
  const reply = talk.locator('[data-message="m300"]');
  await talk.hover();
  await page.mouse.wheel(0, -reach);
  const jump = page.getByRole("button", { name: "Jump to latest" });
  await expect(jump).toBeVisible();
  await quiet(page);
  const held = await reply.textContent();
  await watcher(page);
  await talk.evaluate((element) => {
    const top = element.getBoundingClientRect().top;
    const above = [...element.querySelectorAll<HTMLElement>("[data-message]")].filter((row) => row.getBoundingClientRect().bottom < top).at(-1)!;
    above.style.height = above.offsetHeight + 200 + "px";
  });
  await quiet(page);
  const { writes } = await watched(page);
  expect(writes.reduce((sum, write) => sum + write.delta, 0)).toBeGreaterThan(190);
  expect(await reply.textContent()).toBe(held);
  await expect(jump).toBeVisible();
  await stop();
}

// flingsOverHeldReplies reads far up, where long replies arrive held back, and
// lands on the end he left in one frame, past every row drawn, as a fling does
// when the page falls behind it. There those replies still count at their
// estimates, within a screen, though they reach further: he stays where he
// landed with them drawn below him, and neither the list, by moving itself to
// their end, nor WebKit, by laying the list out without the rows it swaps,
// moves him from there.
export async function flingsOverHeldReplies(page: Page) {
  const { talk, stop } = await streamingTalk(page, { isStreaming: false });
  const screen = await talk.evaluate((element) => element.clientHeight);
  await glide(page, -6000);
  await quiet(page);
  await page.evaluate(() => {
    const blocks = Array.from({ length: 40 }, (_, line) => ({ kind: "text", text: "Held line " + line + " of a reply that arrived while he read." }));
    const messages = [300, 301, 302].map((seq) => ({ id: "m" + seq, seq, at: "2026-10-22T09:00:00Z", kind: "reply", blocks }));
    window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages } } }));
  });
  const height = () => talk.evaluate((element) => element.scrollHeight);
  const top = () => talk.evaluate((element) => element.scrollTop);
  const heldHeight = await height();
  const end = await distanceToEnd(talk);
  const landed = await top() + end;
  await watcher(page);
  await page.evaluate((end) => (window as unknown as Watcher).land(end), end);
  await quiet(page);
  const { writes } = await watched(page);
  expect(await height() - heldHeight).toBeGreaterThan(screen);
  expect(writes.filter((write) => Math.abs(write.delta) >= screen)).toEqual([]);
  expect(Math.abs(await top() - landed - writes.reduce((sum, write) => sum + write.delta, 0))).toBeLessThanOrEqual(1);
  await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
  await stop();
}

// landsOverHeldRepliesAfterShortRows reads up with rests through rows shorter
// than their estimate, which leaves the room above him holding far more than
// the window's heights say, and lands within a screen of the end over replies
// held back. The rows he lands among are drawn where he is, not where his raw
// scroll top falls in the window's heights, so no frame shows him a blank.
export async function landsOverHeldRepliesAfterShortRows(page: Page) {
  const { talk, stop } = await streamingTalk(page, { isStreaming: false });
  await page.addStyleTag({ content: ".conversation-items > [data-message] { height: 60px; overflow: hidden; }" });
  await settled(talk);
  const shift = () => talk.evaluate((element) => parseFloat(element.querySelector<HTMLElement>(".conversation-items")!.style.getPropertyValue("--room-shift")) || 0);
  for (let round = 0; round < 30 && await shift() < 2500; round++) {
    await glide(page, -640);
    await quiet(page);
  }
  expect(await shift()).toBeGreaterThanOrEqual(2500);
  await page.evaluate(() => {
    const messages = Array.from({ length: 20 }, (_, at) => ({ id: "m" + (300 + at), seq: 300 + at, at: "2026-10-22T09:00:00Z", kind: "reply", blocks: [{ kind: "text", text: "A reply held back while he read." }] }));
    window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages } } }));
  });
  const screen = await talk.evaluate((element) => element.clientHeight);
  const end = await distanceToEnd(talk);
  await watcher(page);
  await page.evaluate((distance) => (window as unknown as Watcher).land(distance), end - screen + 100);
  await quiet(page);
  const { frames } = await watched(page);
  expect(frames.length).toBeGreaterThan(0);
  expect(frames.filter((frame) => !frame.rows.length)).toEqual([]);
  await stop();
}

// startReplyAtEnd sends a reply's first line and the usage it brings to the
// box at once, as the session's stream does when a reply starts, while the
// list follows its end: the list stays at its end, with no Jump to latest.
export async function startReplyAtEnd(page: Page) {
  await open(page, snapshot(), "", { conversation, messages: HISTORY, more: false });
  await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk.locator('[data-message="m299"]')).toBeVisible();
  await settled(talk);
  await page.evaluate(() => {
    const send = (type: string, data: object) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type, data: { conversation: "fixture-conversation", ...data } } }));
    send("messages", { messages: [{ id: "m300", seq: 300, at: "2026-10-22T09:00:00Z", kind: "reply", blocks: [{ kind: "text", text: "The first streamed line of the reply." }] }] });
    send("usage", { usage: { input_tokens: 1731, output_tokens: 173, cache_read_tokens: 5193, cost_usd: .01731, context_tokens: 1731, context_window: 200000, model: "claude-opus-5-5", effort: "xhigh" } });
  });
  await settled(talk);
  expect(await talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
  await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
}

// readUpUnderHeldFinger reads up from the end by a finger as WebKit and
// Chromium deliver it, its pointer cancelled once the browser takes the pan
// while its touch goes on, and then holds the finger down and still as the
// reply streams: the list writes no scroll position under it, and once it
// lifts and 250 ms pass the room gives back what it lent the rows drawn into
// it, the scroll position taking that as the rows in view hold still.
export async function readUpUnderHeldFinger(page: Page) {
  const { talk, stop } = await streamingTalk(page);
  await fingerDown(talk);
  await glide(page, -1200);
  await watcher(page);
  await glide(page, -2400);
  await page.waitForTimeout(600);
  const held = await watched(page);
  expect({ writes: held.writes, isStill: moved(held) <= 1 }).toEqual({ writes: [], isStill: true });
  const lifted = await fingerUp(page);
  await expect.poll(async () => (await watched(page)).writes.length).toBeGreaterThan(0);
  const rested = await watched(page);
  const [given] = rested.writes;
  expect({ isAfterRest: given.t - lifted >= 240, isGivenBack: Math.abs(given.delta) > 1, isStill: moved(rested) <= 1 }).toEqual({ isAfterRest: true, isGivenBack: true, isStill: true });
  await stop();
}

// readOlderPastLostRelease presses the list in a way whose release never
// reaches it, and then scrolls to its top: by the mouse's right button, whose
// release a context menu that opens on press takes, by a Mac's Ctrl+click,
// whose menu opens on the press of the main button, or by a press the window
// loses its focus under. None of them holds anything, so the older page his
// scroll asked for is read in above him once his scroll rests.
export async function readOlderPastLostRelease(page: Page, loss: "right button" | "menu" | "blur") {
  await open(page, snapshot(), "", { conversation, messages: HISTORY.slice(150), more: true });
  await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("before"), (route) => route.fulfill({ json: { conversation, messages: HISTORY.slice(0, 150), more: false } }));
  await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk.locator('[data-message="m299"]')).toBeVisible();
  await settled(talk);
  const older = page.waitForResponse((response) => new URL(response.url()).searchParams.get("before") === "150");
  await talk.evaluate((element, loss) => {
    const press = { bubbles: true, cancelable: true, composed: true, pointerType: "mouse", isPrimary: true };
    if (loss === "right button") element.dispatchEvent(new PointerEvent("pointerdown", { ...press, button: 2, buttons: 2 }));
    else element.dispatchEvent(new PointerEvent("pointerdown", { ...press, button: 0, buttons: 1, ctrlKey: loss === "menu" }));
    if (loss === "menu") element.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, composed: true, button: 0, buttons: 1, ctrlKey: true }));
    if (loss === "blur") window.dispatchEvent(new FocusEvent("blur"));
    element.scrollTop = 0;
  }, loss);
  await (await older).finished();
  await expect(talk.locator('[data-message="m149"]')).toBeAttached();
}

// phoneTalk opens the CEO's long history at its end, as the Overlord reads it
// on his phone, with no reply in progress or with one streaming through the
// board's message path. That reply streams on from a box that already shows
// what the session spent, as a reply's first line that brings its usage
// takes WebKit off the end by itself, which startReplyAtEnd checks on its own.
async function phoneTalk(page: Page, isStreaming: boolean) {
  const opened = await streamingTalk(page, { isStreaming: false, owner: "CEO" });
  await expect.poll(() => distanceToEnd(opened.talk)).toBeLessThanOrEqual(1);
  if (isStreaming) {
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "usage", data: { conversation: "fixture-conversation",
      usage: { input_tokens: 1000, output_tokens: 100, cache_read_tokens: 3000, cost_usd: .01, context_tokens: 1000, context_window: 200000, model: "claude-opus-5-5", effort: "xhigh" } } } })));
    await settled(opened.talk);
    await opened.start(400);
    await expect.poll(() => distanceToEnd(opened.talk)).toBeLessThanOrEqual(1);
  }
  return opened;
}

// readUpByFinger reads up from the end by a finger through rows the window
// has not drawn yet: it drags the list up, lifts, and the list glides on. It
// says what the list did from the drag's first frame off the end.
async function readUpByFinger(page: Page, talk: Locator) {
  await fingerDown(talk);
  await glide(page, -600);
  await watcher(page);
  for (let drag = 0; drag < 2; drag++) await glide(page, -1200);
  await fingerUp(page);
  await glide(page, -2400);
  return watched(page);
}

// restsStill watches the list from the end of his glide until well after it
// rests: what the list writes once it rests moves no row in view, and after
// that nothing is written, no row is drawn and nothing moves.
async function restsStill(page: Page) {
  await watcher(page);
  await quiet(page);
  const rested = await watched(page);
  const read = await watchStill(page, 1200);
  expect({ isRestStill: moved(rested) <= 1, writes: read.writes, rowsDrawn: read.rowsDrawn, isStill: stillness(read.frames) <= 1 })
    .toEqual({ isRestStill: true, writes: [], rowsDrawn: [], isStill: true });
}

// phoneReadsUp reads up by a finger, idle or while the reply streams: the
// rows in view move only by his finger and its glide, and nothing shakes once
// the list rests (the Overlord, 2026-11-04, on his phone: "Scroll up on
// mobile in cfo chat jitters worse!").
export async function phoneReadsUp(page: Page, isStreaming: boolean) {
  const { talk, stop } = await phoneTalk(page, isStreaming);
  const scrolled = await readUpByFinger(page, talk);
  const [first, last] = [scrolled.frames[0], scrolled.frames.at(-1)!];
  const written = scrolled.writes.reduce((sum, write) => sum + write.delta, 0);
  expect(last.top - first.top - written).toBeLessThan(-2000);
  expect(scrolled.rowsDrawn.length).toBeGreaterThan(0);
  expect({ writes: whileScrolling(scrolled.writes), isStill: moved(scrolled) <= 1 }).toEqual({ writes: [], isStill: true });
  await restsStill(page);
  await stop();
}

// phoneReadsDown reads up by a finger, rests, and reads back down by a finger
// and its glide to the end: the rows in view move only as he does all the
// way down, the list never moves itself by a screen, as it did leaping to an
// end that a reply carried on without him, he follows the end once he reaches
// the newest line, and nothing shakes once it rests (2026-11-04: "Scroll down
// and up and jump to latest all wonky"). Each glide reaches far, as all that
// streamed while he read waits below the end he left.
export async function phoneReadsDown(page: Page, isStreaming: boolean) {
  const { talk, stop } = await phoneTalk(page, isStreaming);
  await readUpByFinger(page, talk);
  await quiet(page);
  const screen = await talk.evaluate((element) => element.clientHeight);
  await fingerDown(talk);
  await watcher(page);
  for (let drag = 0; drag < 2; drag++) await glide(page, 1200);
  await fingerUp(page);
  for (let glides = 0; glides < 30 && await distanceToEnd(talk) > 1; glides++) await glide(page, 9600);
  await page.waitForTimeout(300);
  const scrolled = await watched(page);
  const arrived = scrolled.frames.findIndex((frame) => frame.end <= 1);
  expect(arrived).toBeGreaterThan(0);
  const reaching = { ...scrolled, frames: scrolled.frames.slice(0, arrived), writes: scrolled.writes.filter((write) => write.t <= scrolled.frames[arrived - 1].t) };
  expect({ writes: whileScrolling(reaching.writes), isStill: moved(reaching) <= 1, isHeld: fellBack(reaching) <= 1, leaps: scrolled.writes.filter((write) => Math.abs(write.delta) >= screen) })
    .toEqual({ writes: [], isStill: true, isHeld: true, leaps: [] });
  await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
  await expect(talk.locator(`[data-message="${await newest(page)}"]`)).toBeInViewport();
  await stop();
  await restsStill(page);
}

// phoneJumps reads up by a finger, rests, and taps Jump to latest: the first
// frame the list moves in shows the newest line at the end, and every frame
// after stays at the end, as the reply streams or not, and once it is done
// nothing shakes.
export async function phoneJumps(page: Page, isStreaming: boolean) {
  const { talk, stop } = await phoneTalk(page, isStreaming);
  await readUpByFinger(page, talk);
  await quiet(page);
  const jump = page.getByRole("button", { name: "Jump to latest" });
  const place = (await jump.boundingBox())!;
  const from = await talk.evaluate((element) => element.scrollTop);
  await watcher(page);
  const tapped = await newest(page);
  await page.touchscreen.tap(place.x + place.width / 2, place.y + place.height / 2);
  await expect(jump).toHaveCount(0);
  await page.waitForTimeout(1000);
  await expect.poll(async () => (await watched(page)).frames.length, { timeout: 30_000 }).toBeGreaterThan(0);
  const landed = await watched(page);
  const moving = landed.frames.findIndex((frame) => frame.top !== from);
  expect(moving).toBeGreaterThanOrEqual(0);
  const seq = (id = "") => Number(id.slice(1));
  expect({ isNewestShown: seq(landed.frames[moving].rows.at(-1)?.id) >= seq(tapped), offEnd: landed.frames.slice(moving).filter((frame) => frame.end > 1).map((frame) => frame.end) })
    .toEqual({ isNewestShown: true, offEnd: [] });
  await expect(talk.locator("[data-message]").last()).toBeInViewport();
  await stop();
  await restsStill(page);
}
