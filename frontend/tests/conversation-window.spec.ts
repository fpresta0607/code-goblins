import { expect, test, type Locator, type Page } from "./site";
import { at, GOBLIN, NAME, open, openGoblin, snapshot } from "./session-board";

// Chromium on a Windows machine whose own animations are off, as a CI
// runner's may be, scrolls at once where a page asks it to scroll smoothly, so
// the browser here scrolls smoothly whatever the machine says, as it does on
// his PC: a Jump can then land while a smooth scroll is still under way.
test.use({ launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--enable-smooth-scrolling"] } });

// A conversation loads only what it shows (the Overlord, 2026-10-27, 12:40Z:
// "every UI change must load intelligently ... An update re-renders only what
// it changed, never every line"): the page holds the rows within reach of
// where he reads however long the session is, a scroll draws the view again
// only when it changes which rows are drawn, an idle conversation changes
// nothing in the page while the board's snapshot keeps arriving, and a reply
// that grows as it streams changes its own message and at most the rows its
// growth pushes off the window's far edge, never every line. The supervisor
// is played by tests/session-board.ts; every conversation is made up.

const reply = (seq: number, text: string) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text }] });
const prompt = (seq: number) => ({ id: "m" + seq, seq, at, kind: "prompt", source: "overlord", text: "Question " + seq });
const LONG = { messages: Array.from({ length: 1000 }, (_, seq) => seq % 2 ? reply(seq, `Answer ${seq}\n\nIt runs over a few lines so the page has height.`) : prompt(seq)), conversation: "fixture-conversation", more: false };
const log = (page: Page) => page.getByRole("log", { name: "Messages of " + NAME });

// watch records every change made in the conversation's list from now on: the
// message a change was inside, each row that left or joined the list, a
// change to the room the list keeps above and below its rows, a change in the
// working row, and anything else outside a message.
const watch = (page: Page) => page.evaluate(() => {
  const changes: string[] = [];
  const list = document.querySelector(".messages-view:not([hidden]) .conversation-items")!;
  new MutationObserver((records) => {
    for (const record of records) {
      const target = record.target instanceof Element ? record.target : record.target.parentElement;
      const message = target?.closest("[data-message]")?.getAttribute("data-message");
      if (message) changes.push(message);
      else if (target?.closest(".reply-working")) changes.push("working");
      else if (record.target === list && record.type === "childList") {
        for (const row of record.removedNodes) changes.push("left:" + (row instanceof Element ? row.getAttribute("data-message") : row.nodeName));
        for (const row of record.addedNodes) changes.push("joined:" + (row instanceof Element ? row.getAttribute("data-message") : row.nodeName));
      } else if (record.attributeName === "style" && (record.target === list || record.target === list.querySelector(":scope > .conversation-room"))) changes.push("room");
      else changes.push("outside a message:" + record.type + ":" + (record.attributeName || ""));
    }
  }).observe(list, { subtree: true, childList: true, characterData: true, attributes: true });
  (window as unknown as { changes: string[] }).changes = changes;
});
const changes = (page: Page) => page.evaluate(() => (window as unknown as { changes: string[] }).changes);

const reading = (talk: Locator) => talk.evaluate(async (element) => {
  let prior = "", quiet = 0;
  for (let frame = 0; frame < 120; frame++) {
    await new Promise(requestAnimationFrame);
    const top = element.getBoundingClientRect().top;
    const row = [...element.querySelectorAll<HTMLElement>("[data-message]")].find((row) => row.getBoundingClientRect().bottom > top);
    const place = { id: row?.dataset.message ?? "", offset: (row?.getBoundingClientRect().top ?? top) - top, top: element.scrollTop, height: element.scrollHeight, text: row?.textContent };
    const next = JSON.stringify(place);
    quiet = next === prior ? quiet + 1 : 0;
    if (quiet === 3 && place.id) { Object.assign(window, { keptRow: row }); return place; }
    prior = next;
  }
  throw new Error("The reading position did not settle within 120 frames");
});

for (const reset of ["before request", "during old response", "during new response"]) {
  test(`reset pagination keeps conversations separate: ${reset}`, async ({ page }) => {
    const newest = { messages: LONG.messages.slice(50, 100), conversation: "conversation-a", more: true };
    const resetPage = { messages: newest.messages.map((message) => ({ ...message, id: "new-" + message.id })), conversation: "conversation-b", more: true };
    await open(page, snapshot(), "", newest);
    let release = () => {};
    const held = new Promise<void>((resolve) => { release = resolve; });
    const requests: string[] = [];
    let isReset = false;
    await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("before"), async (route) => {
      requests.push(route.request().url());
      const isOldResponse = reset === "during old response" && requests.length === 1;
      await held;
      const messages = LONG.messages.slice(0, 50).map((message) => ({ ...message, id: isReset && !isOldResponse ? "new-" + message.id : message.id }));
      await route.fulfill({ json: { messages, conversation: isReset && !isOldResponse ? "conversation-b" : "conversation-a", more: false } });
    });
    await openGoblin(page);
    const talk = log(page);
    await expect(talk).toContainText("Answer 99");
    await reading(talk);
    await talk.evaluate((element) => { element.scrollTop -= 1000; });
    await reading(talk);
    if (reset !== "before request") {
      await talk.evaluate((element) => { element.scrollTop = 0; });
      await expect.poll(() => requests.length).toBe(1);
      await reading(talk);
    }
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })), resetPage);
    isReset = true;
    await reading(talk);
    if (reset === "before request") {
      await talk.evaluate((element) => { element.scrollTop = 0; });
      await reading(talk);
      expect(requests).toEqual([]);
      release();
    } else {
      const place = await reading(talk);
      await watch(page);
      const response = page.waitForResponse((response) => new URL(response.url()).searchParams.has("before"));
      release();
      await response;
      expect(await reading(talk)).toEqual(place);
      expect(await changes(page)).toEqual([]);
    }
    await expect(talk.locator('[data-message="m50"]')).toBeVisible();
    await expect(talk.locator('[data-message^="new-"]')).toHaveCount(0);
    await page.getByRole("button", { name: "Jump to latest" }).click();
    await expect(talk.locator('[data-message="new-m99"]')).toBeInViewport();
    const response = page.waitForResponse((response) => new URL(response.url()).searchParams.has("before"));
    await talk.evaluate((element) => { element.scrollTop = 0; });
    await response;
    // The older page his scroll reached is read in above him once it rests.
    await expect(talk.locator('[data-message="new-m49"]')).toBeAttached();
    await reading(talk);
    await expect(talk.locator('[data-message="new-m50"]')).toBeInViewport();
    await talk.evaluate((element) => { element.scrollTop = 0; });
    await expect(talk.locator('[data-message="new-m0"]')).toBeVisible();
    await expect(talk.locator('[data-message="m0"]')).toHaveCount(0);
    expect(requests).toHaveLength(reset === "before request" ? 1 : 2);
  });
}

for (const owner of ["CEO", "goblin"]) {
  for (const isWorking of [true, false]) {
    test(`${owner}: working metadata stays frozen near the end when ${isWorking ? "finishing, but for its indicator" : "starting"}`, async ({ page }) => {
      const action = { id: "working-tail", kind: "message", task_id: owner === "CEO" ? "" : GOBLIN.id, text: "Waiting for this turn", status: "running", awaiting: { task: owner === "CEO" ? "" : GOBLIN.id }, can_send_now: true, at };
      const board = (isBusy: boolean, revision: number) => snapshot({ revision, actions: [action], ceo_session: "ceo-1", sessions: [{ id: "ceo-1", role: "ceo", harness: "claude", phase: isBusy ? "working" : "idle" }] }, { phase: isBusy ? "working" : "idle" });
      await open(page, board(isWorking, 1), "", LONG);
      if (owner === "CEO") await page.keyboard.press("Control+Alt+1");
      else await openGoblin(page);
      const talk = page.getByRole("log", { name: "Messages of " + (owner === "CEO" ? "CEO" : NAME) });
      await expect(talk).toContainText("Answer 999");
      await expect(talk.locator(".reply-working")).toHaveCount(isWorking ? 1 : 0);
      await reading(talk);
      await talk.evaluate((element) => { element.scrollTop -= 30; });
      const place = await reading(talk);
      await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
      await watch(page);

      await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), board(!isWorking, 2));
      // The rows he reads keep still, the working row's room included, but a
      // turn that ended shows no indicator in it.
      expect(await reading(talk)).toEqual(place);
      expect((await changes(page)).filter((change) => change !== "working")).toEqual([]);
      await expect(talk.locator(".reply-working")).toHaveCount(isWorking ? 1 : 0);
      await expect(talk.locator(".reply-working:visible")).toHaveCount(0);
      await expect(talk.locator(".reply-working").getByRole("button")).toHaveCount(0);
      await expect(talk.locator('[data-message="m999"] time')).toHaveCount(isWorking ? 0 : 1);
      await expect(talk.locator('[data-queued="working-tail"] .queued-state')).toHaveText("Sent");
      await expect(talk.locator('[data-queued="working-tail"]').getByRole("button", { name: "Send now" })).toHaveCount(0);

      await page.getByRole("button", { name: "Jump to latest" }).click();
      await expect(talk.locator(".reply-working")).toHaveCount(isWorking ? 0 : 1);
      await expect(talk.locator('[data-message="m999"] time')).toHaveCount(isWorking ? 1 : 0);
      await expect(talk.locator('[data-queued="working-tail"] .queued-state')).toHaveText("Sent");
      await expect(talk.locator('[data-queued="working-tail"]').getByRole("button", { name: "Send now" })).toHaveCount(0);
      await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
    });
  }
}

test("following settles queued rows, delayed pictures and working-indicator geometry", async ({ page }) => {
  await open(page, snapshot({}, { phase: "idle" }), "", LONG);
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/composer/file?*", async (route) => {
    await held;
    await route.fulfill({ contentType: "image/svg+xml", body: '<svg xmlns="http://www.w3.org/2000/svg" width="300" height="240"><rect width="300" height="240" fill="blue"/></svg>' });
  });
  await openGoblin(page);
  const talk = log(page);
  await expect(talk).toContainText("Answer 999");
  await reading(talk);
  const distance = () => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight);
  const action = { id: "tail", kind: "message", task_id: GOBLIN.id, text: "Queued picture\n/fixture/composer-files/pasted-picture.png", status: "queued", at };

  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, actions: [action] }, { phase: "idle" }));
  const queued = talk.locator('[data-queued="tail"]');
  await expect(queued).toBeInViewport();
  await expect.poll(distance).toBeLessThanOrEqual(1);
  const height = await queued.evaluate((element) => element.getBoundingClientRect().height);
  release();
  await expect.poll(() => queued.locator("img").evaluate((image: HTMLImageElement) => image.naturalHeight)).toBe(240);
  await expect.poll(() => queued.evaluate((element) => element.getBoundingClientRect().height)).toBeGreaterThan(height + 200);
  await expect.poll(distance).toBeLessThanOrEqual(1);

  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 3, actions: [action] }));
  await expect(talk.locator(".reply-working")).toHaveCount(1);
  await expect.poll(distance).toBeLessThanOrEqual(1);
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 4 }, { phase: "idle" }));
  await expect(queued).toHaveCount(0);
  await expect(talk.locator(".reply-working")).toHaveCount(0);
  await expect.poll(distance).toBeLessThanOrEqual(1);
  await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
});

for (const owner of ["CEO", "goblin"]) {
  for (const resume of ["jump", "scroll"]) {
    test(`${owner}: streaming leaves the history untouched after a fast scroll, then coalesces on ${resume}`, async ({ page }, info) => {
      // Arrange
      await open(page, snapshot(), "", LONG);
      if (owner === "CEO") await page.keyboard.press("Control+Alt+1");
      else await openGoblin(page);
      const talk = page.getByRole("log", { name: "Messages of " + (owner === "CEO" ? "CEO" : NAME) });
      await expect(talk).toContainText("Answer 999");
      await talk.hover();
      for (let step = 0; step < 4; step++) await page.mouse.wheel(0, -700);
      const place = await reading(talk);
      await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
      await watch(page);

      // Act: separate stream deliveries revise a visible row and the same
      // pending reply repeatedly, including an overlapping reconnect page.
      for (let update = 1; update <= 12; update++) {
        await page.evaluate(async ({ changed, latest, next, update }) => {
          window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [next, changed, latest] } } }));
          if (update === 6) window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data: { messages: [latest, next], conversation: "fixture-conversation", more: true } } }));
          await new Promise(requestAnimationFrame);
        }, { changed: reply(Number(place.id.slice(1)), "Changed history " + update), latest: reply(1000, "Pending answer " + update), next: reply(1001, "Second pending answer " + update), update });
      }
      await talk.evaluate(() => new Promise(requestAnimationFrame));

      // Assert: no message or spacer mutation, node replacement or motion.
      const evidence = await talk.evaluate((element, place) => {
        const row = element.querySelector(`[data-message="${place.id}"]`)!;
        return { mutations: (window as unknown as { changes: string[] }).changes, isSameNode: row === (window as unknown as { keptRow: Element }).keptRow,
          text: row.textContent, offset: row.getBoundingClientRect().top - element.getBoundingClientRect().top, top: element.scrollTop, height: element.scrollHeight };
      }, place);
      await info.attach("stream-while-reading", { body: JSON.stringify({ place, evidence }), contentType: "application/json" });
      expect(evidence).toEqual({ mutations: [], isSameNode: true, text: place.text, offset: place.offset, top: place.top, height: place.height });

      if (resume === "jump") await page.getByRole("button", { name: "Jump to latest" }).click();
      else await talk.evaluate((element) => { element.scrollTop = element.scrollHeight; });
      await expect(talk.locator('[data-message="m1000"]')).toHaveCount(1);
      await expect(talk.locator('[data-message="m1000"]')).toContainText("Pending answer 12");
      await expect(talk.locator('[data-message="m1001"]')).toHaveCount(1);
      await expect(talk.locator('[data-message="m1001"]')).toContainText("Second pending answer 12");
      expect(await talk.locator("[data-message]").evaluateAll((rows) => rows.slice(-2).map((row) => row.getAttribute("data-message")))).toEqual(["m1000", "m1001"]);
      await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
      await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
      await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [data] } } })), reply(1002, "Following again"));
      await expect(talk.locator('[data-message="m1002"]')).toBeInViewport();
      expect(await talk.locator("[data-message]").count()).toBeLessThan(60);
    });
  }
}

test("Jump to latest cancels an active smooth scroll and settles after pending tall replies are measured", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", LONG);
  await openGoblin(page);
  const talk = log(page);
  await expect(talk).toContainText("Answer 999");
  await talk.evaluate((element) => { element.scrollTop -= 2500; });
  await reading(talk);
  await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: data } } })),
    [reply(1000, Array.from({ length: 30 }, (_, line) => "Pending tall line " + line).join("\n\n")), reply(1001, "Actual latest reply")]);

  // Act: press Jump while the browser still has smooth scrolling to do.
  expect(await talk.evaluate(async (element) => {
    const before = element.scrollTop;
    element.scrollTo({ top: 0, behavior: "smooth" });
    for (let frame = 0; frame < 120; frame++) {
      await new Promise(requestAnimationFrame);
      if (element.scrollTop < before - 1 && element.scrollTop > 0) return true;
    }
    return false;
  })).toBe(true);
  await page.getByRole("button", { name: "Jump to latest" }).click();

  // Assert: after measurement every subsequent frame stays at the live end.
  await expect(talk.locator('[data-message="m1001"]')).toBeInViewport();
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
  const distances = await talk.evaluate(async (element) => {
    const distances: number[] = [];
    for (let frame = 0; frame < 40; frame++) {
      await new Promise(requestAnimationFrame);
      distances.push(element.scrollHeight - element.scrollTop - element.clientHeight);
    }
    return distances;
  });
  expect(Math.max(...distances)).toBeLessThanOrEqual(1);
  await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
});

test("fast scrolling measures new rows without painting a displaced reading anchor", async ({ page }, info) => {
  // Arrange
  await open(page, snapshot(), "", LONG);
  await openGoblin(page);
  const talk = log(page);
  await expect(talk).toContainText("Answer 999");
  await reading(talk);

  // Act: every step crosses a window boundary into unmeasured history. Read
  // the actual anchor at the scroll event and each of the next four paints.
  const steps = [];
  for (let step = 0; step < 12; step++) {
    steps.push(await talk.evaluate((element) => new Promise<{ id: string; offsets: (number | null)[] }>((done) => {
      element.addEventListener("scroll", async () => {
        const top = element.getBoundingClientRect().top;
        const row = [...element.querySelectorAll<HTMLElement>("[data-message]")].find((row) => row.getBoundingClientRect().bottom > top);
        const id = row?.dataset.message ?? "";
        const offsets: (number | null)[] = [row ? row.getBoundingClientRect().top - top : null];
        for (let frame = 0; frame < 4; frame++) {
          await new Promise(requestAnimationFrame);
          const anchor = element.querySelector(`[data-message="${id}"]`);
          offsets.push(anchor ? anchor.getBoundingClientRect().top - element.getBoundingClientRect().top : null);
        }
        done({ id, offsets });
      }, { once: true });
      element.scrollTop -= 450;
    })));
  }

  // Assert
  await info.attach("fast-scroll-anchors", { body: JSON.stringify(steps), contentType: "application/json" });
  for (const step of steps) {
    expect(step.offsets, "no blank frame or removed reading anchor at " + step.id).not.toContain(null);
    const offsets = step.offsets.filter((value): value is number => value !== null);
    expect(Math.max(...offsets) - Math.min(...offsets), "anchor movement at " + step.id).toBeLessThanOrEqual(1);
  }
});

test("a thousand-message conversation holds only the rows near where he reads, and still reads to its first message", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");

  // Act and Assert: at the end only a screenful and its reach is drawn.
  const drawn = () => log(page).locator("[data-message]").count();
  expect(await drawn()).toBeLessThan(60);

  // Reading back to the top draws the first messages, still a few at a time.
  for (let tries = 0; tries < 40 && !(await log(page).getByText("Question 0", { exact: true }).isVisible()); tries++) {
    await log(page).evaluate((element) => { element.scrollTop = 0; });
    await page.waitForTimeout(50);
  }
  await expect(log(page).getByText("Question 0", { exact: true })).toBeVisible();
  expect(await drawn()).toBeLessThan(60);
});

test("an idle conversation changes nothing in the page while the board's snapshot keeps arriving", async ({ page }) => {
  // Arrange
  await open(page, snapshot({ actions: [{ id: "q1", kind: "message", task_id: GOBLIN.id, text: "queued while busy", status: "queued", at }] }));
  await openGoblin(page);
  await expect(log(page).locator(".queued-message")).toContainText("queued while busy");
  await page.waitForTimeout(500);
  await watch(page);

  // Act: eight seconds of snapshots, one every half second, each a new
  // revision with nothing in the conversation changed.
  for (let revision = 2; revision < 18; revision++) {
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision, actions: [{ id: "q1", kind: "message", task_id: GOBLIN.id, text: "queued while busy", status: "queued", at }] }));
    await page.waitForTimeout(500);
  }

  // Assert
  expect(await changes(page)).toEqual([]);
});

test("a reply that grows as it streams changes its own message and at most the rows leaving the window's far edge", async ({ page }) => {
  // Arrange
  const page50 = { messages: Array.from({ length: 50 }, (_, seq) => seq % 2 ? reply(seq, "Answer " + seq) : prompt(seq)), conversation: "fixture-conversation", more: false };
  await open(page, snapshot(), "", page50);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 49");
  const newest = log(page).locator('[data-message="m49"]');
  const height = await newest.evaluate((row) => row.getBoundingClientRect().height);
  const drawn = await log(page).evaluate((element) => [...element.querySelectorAll<HTMLElement>(":scope > .conversation-items > [data-message]")].map((row) => row.dataset.message || ""));
  await watch(page);

  // Act: the newest reply streams on in three updates, each three lines longer.
  for (const count of [3, 6, 9]) {
    const text = ["Answer 49", ...Array.from({ length: count }, (_, line) => "Streamed line " + (line + 1))].join("\n\n");
    await page.evaluate((changed) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [changed] } } })), reply(49, text));
    await expect(newest).toContainText("Streamed line " + count);
  }
  await page.waitForTimeout(300);

  // Assert: the reply grew by several lines, and every change in the list was
  // inside it, in the room kept above the rows drawn, or a row its growth
  // pushed off the window's far edge, which are the oldest rows drawn.
  expect(await newest.evaluate((row) => row.getBoundingClientRect().height)).toBeGreaterThan(height + 100);
  const seen = await changes(page);
  expect(seen).toContain("m49");
  expect(seen.filter((each) => each !== "m49" && each !== "room" && !each.startsWith("left:"))).toEqual([]);
  const left = seen.filter((each) => each.startsWith("left:")).map((each) => each.slice("left:".length));
  expect([...left].sort()).toEqual(drawn.slice(0, left.length).sort());
});

// countDraws counts each commit in which the conversation's view drew, by
// React's own devtools hook, as tests/render-budget.spec.ts counts what a
// commit drew: the build shortens component names, so the view is the
// component whose first element is .messages-view.
const countDraws = (page: Page) => page.addInitScript(() => {
  interface Fiber { tag: number; flags: number; child?: Fiber; sibling?: Fiber; stateNode?: unknown; alternate?: Fiber | null }
  const counted = { draws: 0 };
  Object.assign(window, {
    counted,
    __REACT_DEVTOOLS_GLOBAL_HOOK__: {
      supportsFiber: true, renderers: new Map(), inject() { return 1; }, checkDCE() {}, onCommitFiberUnmount() {}, onPostCommitFiberRoot() {}, onScheduleFiberRoot() {},
      onCommitFiberRoot(_: number, root: { current: Fiber }) {
        const stack = [root.current];
        while (stack.length) {
          const fiber = stack.pop()!;
          if (fiber.sibling) stack.push(fiber.sibling);
          // Children a bailout reused are the ones the commit before drew,
          // with their flags from then, so the walk stops there.
          if (fiber.child && fiber.child !== fiber.alternate?.child) stack.push(fiber.child);
          // A function, forwardRef or memo component that did work.
          if (!(fiber.flags & 1) || ![0, 11, 14, 15].includes(fiber.tag)) continue;
          let host = fiber.child;
          while (host && host.tag !== 5) host = host.child;
          if (host?.stateNode instanceof Element && host.stateNode.classList.contains("messages-view")) { counted.draws++; return; }
        }
      },
    },
  });
});

test("scrolling a few pixels at a time draws the view again only when it changes which rows are drawn", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute, so the board's
  // minute clock draws nothing while he scrolls, and he reads a thousand
  // pixels up from the end of a long conversation, every row below him drawn.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  await countDraws(page);
  await open(page, snapshot(), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");
  await log(page).evaluate((element) => { element.scrollTop -= 1000; });
  await page.waitForTimeout(1000);
  const sample = () => page.evaluate(() => ({
    rows: [...document.querySelectorAll<HTMLElement>(".messages-view:not([hidden]) .conversation-items > [data-message]")].map((row) => row.dataset.message).join(),
    draws: (window as unknown as { counted: { draws: number } }).counted.draws,
  }));

  // Act: sixty steps of five pixels down, each smaller than the list's top
  // padding, across the edges of the rows leaving the window above.
  const steps = [await sample()];
  for (let step = 0; step < 60; step++) {
    await log(page).evaluate((element) => { element.scrollTop += 5; });
    await page.waitForTimeout(50);
    steps.push(await sample());
  }

  // Assert: the rows drawn changed along the way, and each step that left
  // them as they were drew nothing.
  const moved = steps.slice(1).map((each, step) => each.rows !== steps[step].rows);
  expect(moved).toContain(true);
  expect(steps.slice(1).map((each, step) => moved[step] ? 0 : each.draws - steps[step].draws)).toEqual(moved.map(() => 0));
});

test("a snapshot that changes nothing in the conversation draws its view once, with the panel, and never again by itself", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute, so the board's
  // minute clock draws nothing, and the view follows the end of a long
  // conversation with one of his messages queued.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  await countDraws(page);
  const actions = [{ id: "q1", kind: "message", task_id: GOBLIN.id, text: "queued while busy", status: "queued", at }];
  await open(page, snapshot({ actions }), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");
  await expect(log(page).locator(".queued-message")).toContainText("queued while busy");
  const draws = () => page.evaluate(() => (window as unknown as { counted: { draws: number } }).counted.draws);
  // settled waits until the view has drawn nothing for three frames in a row
  // and says how often it has drawn.
  const settled = () => page.evaluate(async () => {
    const counted = (window as unknown as { counted: { draws: number } }).counted;
    let prior = -1, quiet = 0;
    for (let frame = 0; frame < 120; frame++) {
      await new Promise(requestAnimationFrame);
      quiet = counted.draws === prior ? quiet + 1 : 0;
      prior = counted.draws;
      if (quiet === 3) return prior;
    }
    throw new Error("The view kept drawing for 120 frames");
  });

  // Act: five snapshots, each a new revision with nothing in the
  // conversation changed.
  const drawn: number[] = [];
  for (let revision = 2; revision < 7; revision++) {
    const before = await settled();
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision, actions }));
    await expect.poll(draws).toBeGreaterThan(before);
    drawn.push(await settled() - before);
  }

  // Assert: each drew the view once, as the panel drew it again.
  expect(drawn).toEqual([1, 1, 1, 1, 1]);
});

// A conversation whose last reply ran a command, whose output folds under
// its row.
const WITH_CALL = { messages: [...LONG.messages.slice(0, 999), { id: "m999", seq: 999, at, kind: "reply", blocks: [
  { kind: "text", text: "Answer 999" },
  { kind: "call", tool: "Bash", subject: "go test ./...", state: "done", result: "ok  github.com/example/repo  1.2s" },
] }], conversation: "fixture-conversation", more: false };
// jumpTo scrolls the list to top, repeating while the rows it reaches are drawn.
const jumpTo = async (page: Page, top: number) => {
  for (let tries = 0; tries < 20; tries++) {
    await log(page).evaluate((element, top) => { element.scrollTop = top < 0 ? element.scrollHeight : top; }, top);
    await page.waitForTimeout(60);
  }
};

test("a small scroll up from the end keeps the newest messages drawn", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");

  // Act: one small step up, just past where the list counts as at its end.
  await log(page).evaluate((element) => { element.scrollTop -= 30; });
  await page.waitForTimeout(300);

  // Assert
  await expect(log(page).getByText("Answer 999", { exact: true })).toBeVisible();
  await expect(log(page).getByText("Question 998", { exact: true })).toBeVisible();
});

test("a fold he opened stays open after its row leaves the window and comes back", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", WITH_CALL);
  await openGoblin(page);
  const call = log(page).locator('[data-message="m999"] .disclosure.message-call');
  await call.locator("summary").click();
  await expect(call.locator(".call-result")).toHaveText("ok  github.com/example/repo  1.2s");

  // Act: read far up, so the row leaves the page, then come back.
  await jumpTo(page, 0);
  await expect(log(page).locator('[data-message="m999"]')).toHaveCount(0);
  await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [data] } } })), reply(1000, "Arrived while the fold was off screen"));
  await jumpTo(page, -1);

  // Assert
  await expect(call.locator(".call-result")).toHaveText("ok  github.com/example/repo  1.2s");
});

test("the keyboard on a row that leaves the window goes to the list, whose keys keep working", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", WITH_CALL);
  await openGoblin(page);
  await log(page).locator('[data-message="m999"] summary').focus();

  // Act
  await jumpTo(page, 0);
  await expect(log(page).locator('[data-message="m999"]')).toHaveCount(0);

  // Assert: the list holds the keyboard, and Shift+PageDown scrolls it.
  await expect(log(page)).toBeFocused();
  const before = await log(page).evaluate((element) => element.scrollTop);
  await page.keyboard.press("Shift+PageDown");
  await expect.poll(() => log(page).evaluate((element) => element.scrollTop)).toBeGreaterThan(before);
});

// The Overlord, 2026-10-29: "One scroll only, the chat's."
test("a queued message waits at the end of the conversation's one scroll while he reads far up a long one", async ({ page }) => {
  // Arrange: a message waits in the queue, and he reads about 3,000 px up
  // from the end of a long conversation.
  await open(page, snapshot({ actions: [{ id: "q1", kind: "message", task_id: GOBLIN.id, text: "queued while busy", status: "queued", at }] }), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");
  await log(page).evaluate((element) => { element.scrollTop = element.scrollHeight - element.clientHeight - 3000; });
  await page.waitForTimeout(500);
  const queued = page.locator('.queued-message[data-queued="q1"]');

  // Assert
  await expect(log(page).locator(".queued-message")).toHaveCount(1);
  await expect(queued).not.toBeInViewport();
  expect(await queued.evaluate((element) => element === [...element.closest("[role=log]")!.querySelectorAll("[data-message], .queued-message")].at(-1))).toBe(true);
});

test("a selection from an older message to the newest copies every message between, however far it reaches", async ({ page }) => {
  // Arrange: a caret at the start of an older message, as a click leaves it.
  await open(page, snapshot(), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");
  const older = log(page).locator('[data-message="m960"]');
  for (let tries = 0; tries < 40 && await older.count() === 0; tries++) {
    await log(page).evaluate((element) => { element.scrollTop -= 400; });
    await page.waitForTimeout(60);
  }
  await expect(older).toHaveCount(1);
  await older.evaluate((row) => { document.getSelection()?.collapse(row.querySelector(".message-bubble")?.firstChild ?? row, 0); });
  await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [data] } } })), reply(1000, "Arrived while the selection was held"));

  // Act: read down to the end, about 3,000 px, and extend the selection there,
  // as Shift+click does.
  await jumpTo(page, -1);
  await expect(log(page).locator('[data-message="m999"]')).toHaveCount(1);
  await expect(older, "the row the caret is in stays in the page").toHaveCount(1);
  await log(page).locator('[data-message="m999"]').evaluate((row) => {
    const text = row.querySelector("p")?.firstChild ?? row;
    document.getSelection()?.extend(text, (text.textContent ?? "").length);
  });

  // Assert: every message from the older one to the newest is selected.
  await expect.poll(() => page.evaluate(() => document.getSelection()?.toString() ?? "")).toContain("Answer 979");
  const selected = await page.evaluate(() => document.getSelection()?.toString() ?? "");
  for (const seq of [960, 961, 979, 998, 999]) expect(selected, "message " + seq).toContain((seq % 2 ? "Answer " : "Question ") + seq);
});
