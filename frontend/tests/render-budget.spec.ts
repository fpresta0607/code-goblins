import { expect, openTaskList, test, type Page } from "./site";

// The board's render budget (#1599, #1580). The Overlord, 2026-10-27: "every
// UI change must load intelligently ... An update re-renders only what it
// changed, never every line. Animations run only while something is
// happening". Measured on main at fdc490f99 before this budget: an idle board
// redrew 220 components a second for its one-second clock, and a snapshot
// that changed nothing redrew 440. React's own devtools hook counts each
// commit and the function components that did work in it, each named by the
// class of the first element it draws, since the build shortens component
// names: a card is the component that draws .task-card-shell.
const since = "2026-10-27T09:00:00Z";
const memory = { next: 5368709120, floor: 4294967296, total: 34359738368, available: 6442450944, commit_limit: 51539607552, commit_available: 21474836480, paged_pool: 536870912, nonpaged_pool: 322122547 };
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const numbered = (count: number, make: (number: number) => Record<string, unknown>) => Array.from({ length: count }, (_, index) => make(index + 1));
const WORKING = numbered(7, (number) => task("working-" + number, "working", { harness: "claude", pr: "https://github.com/example/code-goblins/pull/" + (200 + number) }));
const RESTING = [
  ...numbered(8, (number) => task("queued-" + number, "queued", { generation: "", brief: true, queue_revision: "q1" })),
  ...numbered(4, (number) => task("paused-" + number, "paused", { at: since })),
  ...numbered(6, (number) => task("finished:done-" + number, "done", { archived: true, merged: true, verified: true, generation: "", branch: "fix/done-" + number, at: since })),
];
const fleet = (tasks: Record<string, unknown>[]) => ({ healthy: true, instance: "fixture", cfo_runs: true, revision: 1, at: since, attention: [], memory, tasks });

interface Counts { commits: number; rendered: Record<string, number>; frames: number }

// open loads the board on a stream the test pushes snapshots into, with the
// devtools hook counting what React draws and the page counting animation
// frames asked for.
async function open(page: Page, snapshot: Record<string, unknown>) {
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  await page.addInitScript((first) => {
    const counts: Counts = { commits: 0, rendered: {}, frames: 0 };
    interface Fiber { tag: number; flags: number; child?: Fiber; sibling?: Fiber; stateNode?: unknown; alternate?: Fiber | null }
    // The class of the first element a component draws, its tag when it has
    // none, and "none" for one that draws no element.
    const drawn = (fiber: Fiber) => {
      let host = fiber.child;
      while (host && host.tag !== 5) host = host.child;
      const element = host?.stateNode instanceof Element ? host.stateNode : null;
      return element ? element.getAttribute("class")?.split(" ")[0] || element.tagName.toLowerCase() : "none";
    };
    Object.assign(window, {
      counts,
      __REACT_DEVTOOLS_GLOBAL_HOOK__: {
        supportsFiber: true, renderers: new Map(), inject() { return 1; }, checkDCE() {}, onCommitFiberUnmount() {}, onPostCommitFiberRoot() {}, onScheduleFiberRoot() {},
        onCommitFiberRoot(_: number, root: { current: Fiber }) {
          counts.commits++;
          const stack = [root.current];
          while (stack.length) {
            const fiber = stack.pop()!;
            if (fiber.sibling) stack.push(fiber.sibling);
            // Children a bailout reused are the ones the commit before drew,
            // with their flags from then, so the walk stops there, as React's
            // own devtools does.
            if (fiber.child && fiber.child !== fiber.alternate?.child) stack.push(fiber.child);
            // A function, forwardRef or memo component that did work.
            if (fiber.flags & 1 && [0, 11, 14, 15].includes(fiber.tag)) {
              const name = drawn(fiber);
              counts.rendered[name] = (counts.rendered[name] || 0) + 1;
            }
          }
        },
      },
    });
    const frame = window.requestAnimationFrame.bind(window);
    window.requestAnimationFrame = (callback) => { counts.frames++; return frame(callback); };
    const streams: EventTarget[] = [];
    Object.assign(window, {
      push: (next: unknown) => streams.forEach((stream) => stream.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(next) }))),
      read: (readings: unknown) => streams.forEach((stream) => stream.dispatchEvent(new MessageEvent("meters", { data: JSON.stringify(readings) }))),
    });
    class HeldStream extends EventTarget {
      onerror: (() => void) | null = null;
      constructor() {
        super();
        streams.push(this);
        setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(first) })));
      }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: HeldStream });
  }, snapshot);
  await page.goto("/");
  await expect(page.locator(".task-card").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

const reset = (page: Page) => page.evaluate(() => Object.assign((window as unknown as { counts: Counts }).counts, { commits: 0, rendered: {}, frames: 0 }));
const counted = (page: Page) => page.evaluate(() => (window as unknown as { counts: Counts }).counts);
const push = (page: Page, snapshot: unknown) => page.evaluate((next) => (window as unknown as { push: (next: unknown) => void }).push(next), snapshot);
const read = (page: Page, readings: unknown) => page.evaluate((next) => (window as unknown as { read: (next: unknown) => void }).read(next), readings);
const total = (counts: Counts) => Object.values(counts.rendered).reduce((sum, count) => sum + count, 0);

test("an idle board draws nothing between its minutes", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  await open(page, fleet([...WORKING, ...RESTING]));
  await reset(page);

  // Act
  await page.clock.runFor(20_000);

  // Assert
  const counts = await counted(page);
  expect(counts.commits, JSON.stringify(counts.rendered)).toBe(0);
});

test("a snapshot that changes nothing draws no card and almost nothing else", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  const snapshot = fleet([...WORKING, ...RESTING]);
  await open(page, snapshot);
  await reset(page);

  // Act
  await push(page, { ...snapshot, revision: 2, at: "2026-10-27T09:00:15Z" });
  await page.waitForTimeout(500);

  // Assert
  const counts = await counted(page);
  expect(counts.rendered["task-card-shell"] ?? 0, JSON.stringify(counts.rendered)).toBe(0);
  expect(total(counts), JSON.stringify(counts.rendered)).toBeLessThanOrEqual(40);
});

test("a snapshot that changes nothing draws no queued card in the CFO's panel", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute, and the CFO's panel
  // shows its queue.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  const snapshot = fleet([...WORKING, ...RESTING]);
  await open(page, snapshot);
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).first().click();
  await openTaskList(page);
  await expect(page.locator(".cfo-queue .task-card-shell").first()).toBeVisible();
  await page.waitForTimeout(1000);
  await reset(page);

  // Act
  await push(page, { ...snapshot, revision: 2, at: "2026-10-27T09:00:15Z" });
  await page.waitForTimeout(500);

  // Assert
  const counts = await counted(page);
  expect(counts.rendered["task-card-shell"] ?? 0, JSON.stringify(counts.rendered)).toBe(0);
});

test("a snapshot that changes nothing draws no message in the CFO box's list again", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute, and the CFO's box
  // lists the five messages he sent last.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  const sent = numbered(5, (number) => ({ id: "sent-" + number, kind: "message", task_id: "", generation: "", status: "succeeded", message: "Taken by the CFO.", awaiting: "", can_send_now: false, send_now_why: "", text: "Message " + number, created_at: since, updated_at: since }));
  const snapshot = { ...fleet([...WORKING, ...RESTING]), actions: sent };
  await open(page, snapshot);
  await expect(page.locator(".queued-message")).toHaveCount(5);
  await reset(page);

  // Act
  await push(page, { ...snapshot, revision: 2, at: "2026-10-27T09:00:15Z" });
  await page.waitForTimeout(500);

  // Assert
  const counts = await counted(page);
  expect(counts.rendered["queued-message"] ?? 0, JSON.stringify(counts.rendered)).toBe(0);
});

test("a snapshot that changes the meters alone draws no card", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  const snapshot = fleet([...WORKING, ...RESTING]);
  await open(page, snapshot);
  await reset(page);

  // Act
  await push(page, { ...snapshot, revision: 2, memory: { ...memory, available: memory.available - 2 ** 20 } });
  await page.waitForTimeout(500);

  // Assert
  const counts = await counted(page);
  expect(counts.rendered["task-card-shell"] ?? 0, JSON.stringify(counts.rendered)).toBe(0);
});

// A meter reading changed the snapshot, so the board parsed the whole fleet
// it holds for it: each build's time, revision and meters now come in an
// event of their own, laid over the snapshot the board shows (#1611).
test("a meter reading of its own moves its meter and draws no card", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  await open(page, fleet([...WORKING, ...RESTING]));
  const line = page.getByRole("group", { name: "Memory" }).locator(":scope > .memory-line");
  await expect(line).toHaveText("Memory free6.0 GB");
  await reset(page);

  // Act
  await read(page, { keys: { at: { value: "2026-10-27T09:00:15Z" }, revision: { value: 2 }, memory: { keys: { available: { value: 3.4 * 2 ** 30 } } } } });

  // Assert
  await expect(line).toHaveText("Memory free3.4 GB");
  const counts = await counted(page);
  expect(counts.rendered["task-card-shell"] ?? 0, JSON.stringify(counts.rendered)).toBe(0);
});

// The meters (memory, disk, processors, GPU and the pool) are read again for
// nearly every snapshot, every three seconds on 2026-10-28, and their bars
// eased each new width over 0.4 s: about 30 frames of layout and paint for
// each snapshot, 1,470 frames in two minutes of the live fleet (#1716). A bar
// takes its new width at once.
test("a meter's new reading moves its bar at once, with no animation", async ({ page }) => {
  // Arrange: the memory bar's animations are read as the board writes its
  // new width, before any frame, since an eased width is over in 0.4 s.
  const snapshot = fleet([...WORKING, ...RESTING]);
  await open(page, snapshot);
  await page.locator(".memory-fill").first().evaluate((bar) => {
    const widthWritten = new Promise<string[]>((resolve) => new MutationObserver(() => resolve(bar.getAnimations().map((animation) => (animation as CSSTransition).transitionProperty || animation.constructor.name))).observe(bar, { attributeFilter: ["style"] }));
    Object.assign(window, { widthWritten });
  });

  // Act
  await push(page, { ...snapshot, revision: 2, memory: { ...memory, available: memory.available - 2 ** 30 } });

  // Assert
  const animated = await page.evaluate(() => (window as unknown as { widthWritten: Promise<string[]> }).widthWritten);
  expect(animated).toEqual([]);
});

test("a snapshot that changes one goblin draws its card alone", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  const snapshot = fleet([...WORKING, ...RESTING]);
  await open(page, snapshot);
  await reset(page);

  // Act
  await push(page, { ...snapshot, revision: 2, tasks: [{ ...WORKING[0], title: "working-1 renamed" }, ...WORKING.slice(1), ...RESTING] });
  await expect(page.getByText("working-1 renamed")).toHaveCount(1);

  // Assert
  const counts = await counted(page);
  expect(counts.rendered["task-card-shell"], JSON.stringify(counts.rendered)).toBe(1);
});

test("a presentation reported since the clock's last minute shows at once", async ({ page }) => {
  // Arrange: the board's clock read the time five seconds into a minute, and
  // a goblin presents a page twenty seconds later.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  const snapshot = fleet([...WORKING, ...RESTING]);
  await open(page, snapshot);
  await page.clock.runFor(20_000);
  const reported = "2026-10-27T12:00:20Z";
  const presenting = { ...WORKING[0], runtime: { state: "working", reason: "", at: reported } };
  const presentation = { id: "p1", kind: "browser", task_id: "working-1", generation: "working-1-1", source: "", target: "", state: "active", url: "https://example.com/walkthrough", at: reported, until: "2026-10-27T12:10:00Z" };

  // Act
  await push(page, { ...snapshot, revision: 2, tasks: [presenting, ...WORKING.slice(1), ...RESTING], activity: [presentation] });

  // Assert
  await expect(page.locator(".task-card", { hasText: "working-1" }).getByText("Browser active")).toBeVisible();
});

test("a board with no goblin at work asks for no animation frame and runs no animation", async ({ page }) => {
  // Arrange
  await open(page, fleet(RESTING));
  await page.waitForTimeout(1500);
  await reset(page);

  // Act
  await page.waitForTimeout(2000);

  // Assert
  const counts = await counted(page);
  const running = await page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running").map((animation) => (animation as CSSAnimation).animationName || animation.constructor.name));
  expect(counts.frames).toBe(0);
  expect(running).toEqual([]);
});
