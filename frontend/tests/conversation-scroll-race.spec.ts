import { expect, test, type Locator, type Page } from "./site";
import { at, GOBLIN, NAME, open, openGoblin, snapshot } from "./session-board";
import { delayedScroll, flingsOverHeldReplies, landsOverHeldRepliesAfterShortRows, leaveEndWhileStreaming, moved, readOlderPastLostRelease, readUpUnderHeldFinger, readUpWhileStreaming, rowGrowsAboveHim } from "./conversation-scroll-race";

for (const owner of ["CFO", "goblin"] as const) {
  for (const movement of ["keyboard", "wheel"] as const) {
    for (const update of ["stream", "board"] as const) {
      test(`${owner} keeps its place after ${movement} before scroll delivery during a ${update} update`, async ({ page }) => {
        await delayedScroll(page, owner, movement, update);
      });
    }
  }
}

// Reading up through rows not drawn yet while a reply streams, at a wide and
// a narrow desktop's width, by each way a desktop scrolls.
for (const [reading, width] of [["wheel", 1440], ["keyboard", 820], ["scrollbar", 1440], ["wheel", 820]] as const) {
  test(`reading up by ${reading} at ${width} px moves nothing in view while a reply streams`, async ({ page }) => {
    test.slow();
    await page.setViewportSize({ width, height: 900 });
    await readUpWhileStreaming(page, reading);
  });
}

test("a few pixels up from the end leave it before the next streamed line", async ({ page }) => {
  test.slow();
  await leaveEndWhileStreaming(page, async (talk) => {
    await talk.hover();
    await page.mouse.wheel(0, -6);
  });
});

// The list's own write as a row grows above him is no scroll of his down, from
// a screen up as from a few pixels off the end.
for (const reach of [6, 300]) {
  test(`a row growing above him ${reach} px up from the end leaves what streams held back`, async ({ page }) => {
    test.slow();
    await rowGrowsAboveHim(page, reach);
  });
}

test("a fling over long replies held back leaves him to read on down to them", async ({ page }) => {
  test.slow();
  await flingsOverHeldReplies(page);
});

test("a landing over replies held back after reading up through short rows shows him rows in every frame", async ({ page }) => {
  test.slow();
  await landsOverHeldRepliesAfterShortRows(page);
});

// A finger held still on the list keeps his scroll from rest until it lifts,
// though the browser cancels its pointer once it takes the pan, and a press
// whose release never reaches the list holds nothing: a right-button press or
// a Mac's Ctrl+click, whose release a context menu that opens on press takes,
// or a press the window loses its focus under.
test("a finger held still after reading up writes no scroll position until it lifts", async ({ page }) => {
  test.slow();
  await readUpUnderHeldFinger(page);
});

for (const [loss, press] of [["right button", "a right-button press a context menu keeps"], ["menu", "a Ctrl+click whose context menu opens on the press"], ["blur", "a press the window loses its focus under"]] as const) {
  test(`${press} lets the older page his scroll reached read in`, async ({ page }) => {
    await readOlderPastLostRelease(page, loss);
  });
}

// Short prompts, each drawn well under the window's estimate, leave the room
// above the drawn rows more than they take; once he reaches the top and rests,
// the first message sits at the top, with no blank above it.
test("reading up through short rows with rests and on to the top leaves no blank above the first message", async ({ page }) => {
  test.slow();
  const prompts = Array.from({ length: 240 }, (_, seq) => ({ id: "m" + seq, seq, at, kind: "prompt", source: "overlord", text: "Question " + seq }));
  await open(page, snapshot(), "", { conversation: "fixture-conversation", messages: prompts, more: false });
  await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk.locator('[data-message="m239"]')).toBeVisible();
  await talk.hover();
  for (let round = 0; round < 12; round++) {
    for (let tick = 0; tick < 4; tick++) {
      await page.mouse.wheel(0, -160);
      await page.waitForTimeout(40);
    }
    await page.waitForTimeout(500);
  }
  for (let tick = 0; tick < 200 && await talk.evaluate((element) => element.scrollTop > 0); tick++) {
    await page.mouse.wheel(0, -400);
    await page.waitForTimeout(16);
  }
  await expect.poll(() => talk.evaluate((element) => {
    const first = element.querySelector('[data-message="m0"]');
    return { top: element.scrollTop, isFirstAtTop: !!first && first.getBoundingClientRect().top - element.getBoundingClientRect().top <= 40 };
  })).toEqual({ top: 0, isFirstAtTop: true });
});

test("older history keeps the reading anchor when its response precedes scroll delivery", async ({ page }) => {
  const reply = (seq: number) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text: "Answer " + seq + "\n\nMore to read." }] });
  const conversation = "fixture-conversation";
  await open(page, snapshot(), "", { conversation, messages: Array.from({ length: 50 }, (_, index) => reply(index + 50)), more: true });
  let release = () => {};
  const waiting = new Promise<void>((resolve) => { release = resolve; });
  const requests: string[] = [];
  await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("before"), async (route) => {
    requests.push(route.request().url());
    await waiting;
    await route.fulfill({ json: { conversation, messages: Array.from({ length: 50 }, (_, index) => reply(index)), more: false } });
  });
  await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk.locator('[data-message="m99"]')).toBeVisible();
  await talk.evaluate((element) => { element.scrollTop = 0; });
  await expect.poll(() => requests.length).toBe(1);
  await talk.evaluate(async (element) => {
    element.scrollTop = element.scrollHeight;
    for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame);
  });
  const before = await talk.evaluate((element) => {
    const guard = (event: Event) => { if (event.target === element) event.stopImmediatePropagation(); };
    window.addEventListener("scroll", guard, true);
    Object.assign(window, { releaseScroll: () => window.removeEventListener("scroll", guard, true) });
    element.scrollTop -= 80;
    const top = element.getBoundingClientRect().top;
    const row = [...element.querySelectorAll<HTMLElement>("[data-message]")].find((item) => item.getBoundingClientRect().bottom > top)!;
    Object.assign(window, { raceRow: row });
    return { id: row.dataset.message!, offset: row.getBoundingClientRect().top - top, text: row.textContent, height: element.scrollHeight };
  });
  const response = page.waitForResponse((value) => new URL(value.url()).searchParams.has("before"));
  release();
  await (await response).finished();
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight)).toBeGreaterThan(before.height + 1000);
  const after = await talk.evaluate((element, id) => {
    const row = element.querySelector<HTMLElement>(`[data-message="${id}"]`);
    return { same: row === (window as unknown as { raceRow: Element }).raceRow, text: row?.textContent,
      offset: row && row.getBoundingClientRect().top - element.getBoundingClientRect().top };
  }, before.id);
  expect(after).toEqual({ same: true, text: before.text, offset: before.offset });
});

// Read-in leaves the view on the message he reads, not on a scroll top an older
// page shifts: a step that crosses no row draws no view of its own after it.
test("an older page after a read-in below him leaves the message he reads where it was", async ({ page }) => {
  test.slow();
  const reply = (seq: number) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text: "Answer " + seq + "\n\nMore to read." }] });
  const conversation = "fixture-conversation";
  await open(page, snapshot(), "", { conversation, messages: Array.from({ length: 50 }, (_, index) => reply(index + 50)), more: true });
  let release = () => {};
  const waiting = new Promise<void>((resolve) => { release = resolve; });
  const requests: string[] = [];
  await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("before"), async (route) => {
    requests.push(route.request().url());
    await waiting;
    await route.fulfill({ json: { conversation, messages: Array.from({ length: 50 }, (_, index) => reply(index)), more: false } });
  });
  await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk.locator('[data-message="m99"]')).toBeVisible();
  await talk.evaluate((element) => { element.scrollTop = 0; });
  await expect.poll(() => requests.length).toBe(1);
  await page.evaluate((messages) => {
    window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages } } }));
  }, Array.from({ length: 6 }, (_, index) => reply(index + 100)));
  // Landing measures the rows, so the second lands true: a pixel outside the
  // screen from the end where read-in begins, a step that crosses no row away.
  for (let landing = 0; landing < 2; landing++) {
    await talk.evaluate((element) => { element.scrollTop = element.scrollHeight - element.clientHeight * 2 - 1; });
    await page.waitForTimeout(600);
  }
  await talk.evaluate((element) => { element.scrollTop += 2; });
  await expect(talk.locator('[data-message="m100"]')).toBeAttached();
  await page.waitForTimeout(600);
  const before = await talk.evaluate((element) => {
    const top = element.getBoundingClientRect().top;
    const row = [...element.querySelectorAll<HTMLElement>("[data-message]")].find((item) => item.getBoundingClientRect().bottom > top)!;
    return { id: row.dataset.message!, offset: row.getBoundingClientRect().top - top, height: element.scrollHeight };
  });
  const response = page.waitForResponse((value) => new URL(value.url()).searchParams.has("before"));
  release();
  await (await response).finished();
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight)).toBeGreaterThan(before.height + 1000);
  await page.waitForTimeout(600);
  const after = await talk.evaluate((element, id) => {
    const row = element.querySelector<HTMLElement>(`[data-message="${id}"]`);
    return row && row.getBoundingClientRect().top - element.getBoundingClientRect().top;
  }, before.id);
  expect(after).not.toBeNull();
  expect(Math.abs(after! - before.offset)).toBeLessThanOrEqual(1);
});

// moved reads a write against the frames around it, and the clock cannot order
// them: WebKit's ticks in whole milliseconds, so a write just after a frame can
// carry that frame's time and read as a jump of all it wrote.
test("moved reads a write on the tick of the frame before it as moving nothing, and a row's own jump as a jump", () => {
  const rows = [{ id: "m1", offset: 5 }];
  const frames = [{ t: 10, top: 100, end: 0, rows }, { t: 20, top: 100, end: 0, rows }, { t: 30, top: 300, end: 0, rows }];
  expect(moved({ frames, writes: [{ t: 20, frame: 2, kind: "scrollTop", delta: 200, sinceScroll: 300 }], rowsDrawn: [] })).toBe(0);
  expect(moved({ frames: [frames[0], { ...frames[1], rows: [{ id: "m1", offset: 47 }] }], writes: [], rowsDrawn: [] })).toBe(42);
});

async function raceTalk(page: Page, actions: object[] = [], setup?: () => Promise<void>) {
  const messages = Array.from({ length: 1000 }, (_, seq) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text: "Answer " + seq + "\n\nMore to read." }] }));
  await open(page, snapshot({ actions }), "", { conversation: "fixture-conversation", messages, more: false });
  await setup?.();
  await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk.locator('[data-message="m999"]')).toBeVisible();
  await talk.evaluate(async () => { for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame); });
  return talk;
}

async function holdMovement(talk: Locator, distance: number) {
  return talk.evaluate((element, distance) => {
    const guard = (event: Event) => { if (event.target === element) event.stopImmediatePropagation(); };
    window.addEventListener("scroll", guard, true);
    element.scrollTop -= distance;
    const top = element.getBoundingClientRect().top;
    const row = [...element.querySelectorAll<HTMLElement>("[data-message]")].find((item) => item.getBoundingClientRect().bottom > top)!;
    Object.assign(window, { raceRow: row });
    return { id: row.dataset.message!, offset: row.getBoundingClientRect().top - top, text: row.textContent, top: element.scrollTop };
  }, distance);
}

async function expectPlace(talk: Locator, before: { id: string; offset: number; text: string | null; top: number }) {
  const after = await talk.evaluate((element, id) => {
    const row = element.querySelector<HTMLElement>(`[data-message="${id}"]`);
    return { same: row === (window as unknown as { raceRow: Element }).raceRow, text: row?.textContent,
      offset: row && row.getBoundingClientRect().top - element.getBoundingClientRect().top, top: element.scrollTop };
  }, before.id);
  expect(after).toEqual({ same: true, text: before.text, offset: before.offset, top: before.top });
}

test("a queued picture loading before scroll delivery keeps the reading position", async ({ page }) => {
  const action = { id: "q-image", kind: "message", task_id: GOBLIN.id, generation: GOBLIN.generation, status: "queued", text: "Picture\nC:\\goblin\\composer-files\\pasted-1.png", created_at: at, updated_at: at };
  let release = () => {};
  const waiting = new Promise<void>((resolve) => { release = resolve; });
  const requests: string[] = [];
  const talk = await raceTalk(page, [action], async () => {
    await page.route("**/api/composer/file?**", async (route) => {
      requests.push(route.request().url());
      await waiting;
      await route.fulfill({ contentType: "image/svg+xml", body: '<svg xmlns="http://www.w3.org/2000/svg" width="120" height="160"><rect width="120" height="160" fill="blue"/></svg>' });
    });
  });
  const picture = talk.locator('[data-queued="q-image"] img');
  await expect(picture).toHaveCount(1);
  await expect.poll(() => requests.length).toBe(1);
  const before = await holdMovement(talk, 160);
  release();
  await expect.poll(() => picture.evaluate((image: HTMLImageElement) => image.naturalHeight)).toBe(160);
  await talk.evaluate(async () => { for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame); });
  await expectPlace(talk, before);
});

for (const alreadyReading of [false, true]) {
  test(`${alreadyReading ? "a second reading movement" : "movement from the end"} survives a viewport resize before scroll delivery`, async ({ page }) => {
    const talk = await raceTalk(page);
    await talk.evaluate(async (element) => {
      element.scrollTop -= 2 * element.clientHeight;
      for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame);
    });
    await page.getByRole("button", { name: "Jump to latest" }).click();
    await talk.evaluate(async () => { for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame); });
    if (alreadyReading) {
      await talk.evaluate(async (element) => {
        element.scrollTop -= 240;
        for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame);
      });
      await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
    }
    const before = await holdMovement(talk, 160);
    const viewport = page.viewportSize()!;
    const height = await talk.evaluate((element) => element.clientHeight);
    await page.setViewportSize({ width: viewport.width, height: viewport.height - 100 });
    await expect.poll(() => talk.evaluate((element) => element.clientHeight)).toBeLessThan(height);
    await talk.evaluate(async () => { for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame); });
    await expectPlace(talk, before);
  });
}
