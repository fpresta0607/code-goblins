import { expect, type Locator, type Page } from "./site";
import { at, GOBLIN, NAME, open, openGoblin, snapshot } from "./session-board";

const conversation = "fixture-conversation";
const reply = (seq: number, text: string) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text }] });
const messages = Array.from({ length: 1000 }, (_, seq) => reply(seq, "Answer " + seq + "\n\nMore to read."));
const action = { id: "q1", kind: "message", task_id: GOBLIN.id, generation: GOBLIN.generation, status: "queued", text: "Waiting for delivery", created_at: at, updated_at: at };

async function settled(talk: Locator) {
  await talk.evaluate(async () => { for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame); });
}

export async function delayedScroll(page: Page, owner: "CFO" | "goblin", movement: "keyboard" | "wheel", update: "stream" | "board") {
  const queued = { ...action, task_id: owner === "CFO" ? "" : GOBLIN.id };
  await open(page, snapshot({ actions: [queued] }), "", { messages, conversation, more: false });
  if (owner === "CFO") await page.keyboard.press("Control+Alt+1"); else await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + (owner === "CFO" ? "CFO" : NAME) });
  await expect(talk.locator('[data-message="m999"]')).toBeVisible();
  await expect(talk.locator('[data-queued="q1"]')).toBeVisible();
  await settled(talk);
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
  const before = await talk.evaluate((element, { movement, update, queued, board }) => {
    const guard = (event: Event) => { if (event.target === element) event.stopImmediatePropagation(); };
    window.addEventListener("scroll", guard, true);
    Object.assign(window, { releaseScroll: () => window.removeEventListener("scroll", guard, true) });
    const end = element.scrollTop;
    if (movement === "keyboard") element.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", shiftKey: true, bubbles: true }));
    else element.scrollTop -= 80;
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
  }, { movement, update, queued, board: snapshot() });
  expect(before.top).toBeLessThan(before.end - 24);
  await settled(talk);
  const after = await talk.evaluate((element, id) => {
    const row = element.querySelector<HTMLElement>(`[data-message="${id}"]`);
    return { top: element.scrollTop, height: element.scrollHeight, same: row === (window as unknown as { raceRow: Element }).raceRow,
      text: row?.textContent, offset: row && row.getBoundingClientRect().top - element.getBoundingClientRect().top };
  }, before.id);
  expect(after).toEqual({ top: before.top, height: before.height, same: true, text: before.text, offset: before.offset });
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

interface Frame { t: number; top: number; rows: { id: string; offset: number }[] }
interface Written { t: number; kind: string; delta: number; sinceScroll: number }
interface Watched { frames: Frame[]; writes: Written[]; rowsDrawn: number[] }
interface Watcher { watch: () => void; watched: () => Watched; quiet: () => number; flick: (distance: number) => Promise<void>; startStream: () => void; stopStream: () => void }
const watcher = (page: Page) => page.evaluate(() => (window as unknown as Watcher).watch());
const watched = (page: Page) => page.evaluate(() => (window as unknown as Watcher).watched());

// streamingTalk opens the goblin's messages at their end while its reply
// streams through the board's message path: the reply that grows and the
// usage it spends every tenth of a second, the terminal's working line and
// a board snapshot every third. Each frame notes the scroll top and where
// the rows in view lie, and each scroll position the page writes to the
// list is noted, apart from the reader's own: how far it moved the list and
// how long after the list last scrolled.
async function streamingTalk(page: Page) {
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
      writes.push({ t: at, kind, delta: scrollTop.get!.call(box) - before, sinceScroll: at - scrolled });
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
        const edge = box.getBoundingClientRect();
        frames.push({ t: performance.now(), top: scrollTop.get!.call(box), rows: [...box.querySelectorAll<HTMLElement>(":scope > .conversation-items > [data-message]")].flatMap((row) => {
          const place = row.getBoundingClientRect();
          return place.bottom > edge.top && place.top < edge.bottom ? [{ id: row.dataset.message!, offset: place.top - edge.top }] : [];
        }) });
      }
      requestAnimationFrame(note);
    };
    requestAnimationFrame(note);
    const watcher: Omit<Watcher, "startStream" | "stopStream"> = {
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
    };
    Object.assign(window, watcher);
  });
  const relay = await open(page, snapshot(), "", { conversation: "fixture-conversation", messages: HISTORY, more: false });
  await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk.locator('[data-message="m299"]')).toBeVisible();
  await settled(talk);
  relay.say(WORKING);
  await page.evaluate((board) => {
    let tick = 0, timer: ReturnType<typeof setInterval> | undefined;
    let reply = { id: "m300", seq: 300, at: "2026-10-22T09:00:00Z", kind: "reply", blocks: [] as object[] };
    const send = (type: string, data: object) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type, data: { conversation: "fixture-conversation", ...data } } }));
    const stream: Pick<Watcher, "startStream" | "stopStream"> = {
      startStream: () => {
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
        }, 100);
      },
      stopStream: () => clearInterval(timer),
    };
    Object.assign(window, stream);
    stream.startStream();
  }, snapshot());
  const says = setInterval(() => relay.say({ ...WORKING, doing: ["Cogitating", "Brewing", "Pondering"][Math.floor(Date.now() / 300) % 3] }), 300);
  await page.waitForTimeout(400);
  return { talk, stop: () => { clearInterval(says); return page.evaluate(() => (window as unknown as Watcher).stopStream()); } };
}

// moved is the most any row in view jumped from one frame to the next: how
// far it moved within the list's content, less how far the page's own scroll
// writes moved the list meanwhile. His own scroll moves the scroll top alone,
// never a row's place in the content, and a row the page moves by as much as
// it scrolls the list stays where he sees it.
function moved({ frames, writes }: Watched) {
  let most = 0;
  for (let at = 1; at < frames.length; at++) {
    const written = writes.filter((write) => write.t > frames[at - 1].t && write.t <= frames[at].t).reduce((sum, write) => sum + write.delta, 0);
    for (const row of frames[at].rows) {
      const prior = frames[at - 1].rows.find((each) => each.id === row.id);
      if (prior) most = Math.max(most, Math.abs(row.offset + frames[at].top - prior.offset - frames[at - 1].top - written));
    }
  }
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
  await expect.poll(() => page.evaluate(() => (window as unknown as Watcher).watched().frames.length)).toBeGreaterThanOrEqual(20);
  return watched(page);
}

// stillness is the most the row under the list's top edge in the first frame
// moved on screen in any frame after.
function stillness(frames: Frame[]) {
  const first = frames[0].rows[0];
  return Math.max(...frames.map((frame) => Math.abs((frame.rows.find((row) => row.id === first.id)?.offset ?? Infinity) - first.offset)));
}

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
