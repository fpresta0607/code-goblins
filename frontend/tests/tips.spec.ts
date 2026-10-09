import { expect, holdStream, openItem, test, type Locator, type Page } from "./site";

// Every tip on the board touches its own part, holds its whole text on a
// solid surface, stays inside the window and is cut off by nothing, wherever
// its part is: a card, the CFO's bar, the top bar, a task's panel, the
// Command Center or the Orchestration canvas, on a wide window and on a phone.
const GB = 2 ** 30;
const since = "2026-10-02T09:00:00Z";
// Commit is the tighter of memory and commit, so Start and Resume carry the
// longest tips they have.
const memory = { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, paged_pool: 0.6 * GB, nonpaged_pool: 0.4 * GB, available: 3.4 * GB, commit_available: 2.5 * GB };
const LONG = "Paused goblins resume by themselves when the reason for the pause clears, and the board says which goblin each one was waiting on and for how long it has waited";
// The longest title the queue held on 2026-10-02.
const LONGEST = "An OpenClaw-style quick start in the goblins command: detect and install Claude Code, Codex and pi, walk through sign-in, pick the CFO's agent, clear Enter-to-continue steps, and a final screen with the board link or Enter for the CFO terminal";
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const TASKS = [
  task("queued-one", "queued", { title: LONG, generation: "", brief: true, queue_revision: "q1" }),
  task("queued-two", "queued", { generation: "", brief: true, queue_revision: "q1" }),
  task("working-one", "working", { harness: "codex", model: "gpt-6-astra", effort: "xhigh", pr: "https://github.com/example/code-goblins/pull/205" }),
  task("working-two", "waiting", { harness: "claude", model: "claude-opus-5-5", effort: "xhigh", waiting_on: "working-one" }),
  task("paused-one", "paused", { at: since }),
  task("finished:done-one", "done", { archived: true, merged: true, verified: true, generation: "", branch: "fix/done-one", at: since, pr: "https://github.com/example/code-goblins/pull/198" }),
];
const QUESTION = { id: "q1", identity: "q1", text: "Which layout should the board open in when the window is too narrow for three columns beside the panel?", options: ["Kanban", "Stacked"], recommended: "Kanban", status: "pending", task: "", created_at: since };

// A question waiting on the Overlord counts on the board's bar and badge, so
// only the Command Center's own walk has one.
async function open(page: Page, tasks: Record<string, unknown>[] = TASKS, questions: Record<string, unknown>[] = [], subscriptions: Record<string, unknown>[] = []) {
  const snapshot = { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [], memory, tasks, questions, subscriptions };
  // The walks point at one part after another, so the board must hold still.
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

// What is wrong with the tip a part shows now, which side of the part it
// stands on, and how many tips it shows. A tip is the board's floating one,
// or one a stylesheet draws from the part's ::after, which is measured the
// same way so that such a tip coming back is held to the same rules. The
// floating tip touches its part: the edge it turns to the part stands 8 px
// from it, the room its arrow spans, wherever the part has moved to since the
// pointer came, it lies across the part along that edge, and its arrow sits on
// that edge, over the part, pointing at it. off is how far the tip's middle
// is from its part's along that edge.
function tipFaults(part: Element): { shown: number; faults: string[]; side: string; off: number } {
  const want = part.getAttribute("data-tip") || "";
  const tips: { text: string; box: DOMRect; from: Element; opacity: number; fixed: boolean; style: CSSStyleDeclaration; node?: HTMLElement }[] = [];
  for (const node of document.querySelectorAll<HTMLElement>("[role=tooltip]")) {
    const style = getComputedStyle(node);
    if (style.visibility !== "hidden") tips.push({ text: node.textContent || "", box: node.getBoundingClientRect(), from: node, opacity: 1, fixed: style.position === "fixed", style, node });
  }
  const after = getComputedStyle(part, "::after");
  if (after.content.includes("attr(") || after.content === JSON.stringify(want)) {
    let block = part;
    while (getComputedStyle(block).position === "static") block = block.parentElement!;
    const frame = block.getBoundingClientRect(), shift = new DOMMatrixReadOnly(after.transform === "none" ? undefined : after.transform);
    const size = (length: string, edges: string[]) => parseFloat(length) + (after.boxSizing === "border-box" ? 0 : edges.reduce((sum, edge) => sum + parseFloat(after.getPropertyValue(edge)), 0));
    const width = size(after.width, ["padding-left", "padding-right", "border-left-width", "border-right-width"]);
    const height = size(after.height, ["padding-top", "padding-bottom", "border-top-width", "border-bottom-width"]);
    const left = after.left === "auto" ? frame.right - block.clientLeft - parseFloat(after.right) - width : frame.left + block.clientLeft + parseFloat(after.left);
    const top = after.top === "auto" ? frame.bottom - block.clientTop - parseFloat(after.bottom) - height : frame.top + block.clientTop + parseFloat(after.top);
    if (parseFloat(after.opacity) > .01) tips.push({ text: want, box: new DOMRect(left + shift.e, top + shift.f, width, height), from: part, opacity: parseFloat(after.opacity), fixed: false, style: after });
  }
  const name = (element: Element) => element.tagName.toLowerCase() + "." + [...element.classList].join(".");
  const faults: string[] = [];
  let side = "", off = 0;
  for (const tip of tips) {
    const say = (what: string) => faults.push("'" + want + "' " + what);
    if (tip.text !== want) say("shows '" + tip.text + "'");
    const { box } = tip;
    if (box.left < 0 || box.top < 0 || box.right > document.documentElement.clientWidth || box.bottom > document.documentElement.clientHeight) say("leaves the window");
    if (tip.node) {
      const text = document.createRange();
      text.selectNodeContents(tip.node);
      const words = text.getBoundingClientRect();
      if (words.left < box.left - 1 || words.top < box.top - 1 || words.right > box.right + 1 || words.bottom > box.bottom + 1) say("does not hold its whole text");
      const own = part.getBoundingClientRect();
      const gaps = { above: own.top - box.bottom, below: box.top - own.bottom, right: box.left - own.right, left: own.left - box.right };
      const isAlong = (at: string) => at === "above" || at === "below";
      const across = (at: string) => isAlong(at) ? Math.min(box.right, own.right) - Math.max(box.left, own.left) : Math.min(box.bottom, own.bottom) - Math.max(box.top, own.top);
      const touched = (["above", "below", "right", "left"] as const).find((at) => Math.abs(gaps[at] - 8) < 1 && across(at) > 0);
      side = touched ?? "";
      if (touched) off = Math.abs(isAlong(touched) ? box.left + box.width / 2 - own.left - own.width / 2 : box.top + box.height / 2 - own.top - own.height / 2);
      const arrow = getComputedStyle(tip.node, "::before");
      if (!touched) say("does not touch its part");
      else if (arrow.content === "none" || arrow.content === "normal") say("has no arrow");
      else {
        const x = box.left + tip.node.clientLeft + parseFloat(arrow.left) + parseFloat(arrow.width) / 2;
        const y = box.top + tip.node.clientTop + parseFloat(arrow.top) + parseFloat(arrow.height) / 2;
        const offEdge = { above: Math.abs(y - box.bottom), below: Math.abs(y - box.top), right: Math.abs(x - box.left), left: Math.abs(x - box.right) }[touched];
        const isOverPart = isAlong(touched) ? x >= own.left - 1 && x <= own.right + 1 : y >= own.top - 1 && y <= own.bottom + 1;
        // The arrow shows the lower right corner of its square, which its
        // turn points from the tip at the part.
        const turn = new DOMMatrixReadOnly(arrow.transform === "none" ? undefined : arrow.transform);
        const [toX, toY] = { above: [0, 1], below: [0, -1], right: [-1, 0], left: [1, 0] }[touched];
        const isTurnedToPart = (turn.a + turn.c) * toX + (turn.b + turn.d) * toY > 1;
        if (offEdge > 1.5 || !isOverPart || !isTurnedToPart) say("points its arrow away from its part");
      }
    }
    if (tip.style.textOverflow === "ellipsis" || tip.style.whiteSpace === "nowrap" && box.width >= parseFloat(tip.style.maxWidth)) say("cuts its text short");
    let opacity = tip.opacity;
    for (let at: Element | null = tip.from; at; at = at.parentElement) {
      const style = getComputedStyle(at);
      opacity *= parseFloat(style.opacity);
      // A box that scrolls or hides what leaves it cuts off a tip drawn inside it.
      if (!tip.fixed && at !== tip.node && at !== document.body && at !== document.documentElement && (style.overflowX !== "visible" || style.overflowY !== "visible")) {
        const clip = at.getBoundingClientRect();
        if (box.left < clip.left - 1 || box.top < clip.top - 1 || box.right > clip.right + 1 || box.bottom > clip.bottom + 1) say("is cut off by " + name(at));
      }
    }
    if (opacity < .99) say("is see-through, at " + opacity.toFixed(2) + " of full");
    const alpha = /^rgba\(.*,\s*([\d.]+)\)$/.exec(tip.style.backgroundColor);
    if (alpha && parseFloat(alpha[1]) < 1 || tip.style.backgroundColor === "transparent") say("has a see-through background");
    if (parseFloat(tip.style.fontSize) < 15) say("is " + tip.style.fontSize);
  }
  return { shown: tips.length, faults, side, off };
}

// Points at every part in scope that has a tip, one at a time, and returns
// what is wrong with each tip and how many were looked at. A card carries the
// tip of its shortened title only once the pointer is on it.
async function walk(scope: Locator) {
  const faults: string[] = [];
  let shown = 0;
  for (const part of await scope.locator("[data-tip]:visible, .task-card:visible").all()) {
    if (await part.isDisabled()) continue;
    await part.hover();
    if (!await part.getAttribute("data-tip")) continue;
    let seen = { shown: 0, faults: [] as string[], side: "", off: 0 };
    await expect.poll(async () => (seen = await part.evaluate(tipFaults)).shown, { message: "a tip for " + await part.getAttribute("data-tip") }).toBe(1);
    // A tip that slides or fades in is judged once it is still.
    await expect.poll(async () => { const before = JSON.stringify(seen); seen = await part.evaluate(tipFaults); return JSON.stringify(seen) === before; }).toBe(true);
    shown++;
    faults.push(...seen.faults);
  }
  return { shown, faults };
}

for (const [size, viewport] of [["wide", { width: 1440, height: 900 }], ["phone", { width: 390, height: 844 }]] as const) {
  test.describe(`on a ${size} window`, () => {
    test.use({ viewport });

    test("every tip on the board, the CFO's bar and the top bar is whole, solid and inside the window", async ({ page }) => {
      test.slow();
      await open(page);
      const { shown, faults } = await walk(page.locator(".app-shell"));
      expect(shown).toBeGreaterThanOrEqual(15);
      expect(faults).toEqual([]);
    });

    test("every tip in a task's panel is whole, solid and inside the window", async ({ page }) => {
      await open(page);
      await page.locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText("working-two", { exact: true }) }).locator(".task-card").click();
      await expect(page.locator("#panel-title")).toHaveText("working-two");
      await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
      const { shown, faults } = await walk(page.locator(".context-pane"));
      expect(shown).toBeGreaterThanOrEqual(3);
      expect(faults).toEqual([]);
    });

    test("every tip in the Command Center is whole, solid and inside the window", async ({ page }) => {
      await open(page, TASKS, [QUESTION]);
      const dialog = page.locator("dialog.question-modal");
      await openItem(page, "Which layout should the board open in");
      await expect(dialog).toBeVisible();
      const card = await walk(dialog);
      await dialog.getByRole("button", { name: "Close the Command Center", exact: true }).click();
      await expect(dialog).toBeHidden();
      const menu = page.locator(".command-center-menu");
      await menu.locator("summary").click();
      const list = await walk(menu);
      expect(list.shown + card.shown).toBeGreaterThanOrEqual(3);
      expect([...list.faults, ...card.faults]).toEqual([]);
    });
  });
}

test("every tip on the Orchestration canvas is whole, solid and inside the window", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await expect(page.locator(".canvas-controls")).toBeVisible();
  const { shown, faults } = await walk(page.locator(".canvas-region"));
  expect(shown).toBeGreaterThanOrEqual(3);
  expect(faults).toEqual([]);
});

// The one rule every tip keeps, on a page holding one part: it touches its
// own part, above it, else below it, else to its right, else to its left,
// taking the first side where it fits on the screen whole, centered on the
// part, with its arrow on the part. A part as tall as the screen leaves no
// room above or below it.
const SCREEN = { width: 640, height: 480 };
async function pointAtPartAt(page: Page, at: { left: number; top: number; width: number; height: number }) {
  await page.setViewportSize(SCREEN);
  await page.goto("/tests/fixtures/tip-sides.html");
  await page.evaluate(() => document.fonts.ready);
  const part = page.locator("#part");
  await part.evaluate((element, at) => Object.assign((element as HTMLElement).style, { left: at.left + "px", top: at.top + "px", width: at.width + "px", height: at.height + "px" }), at);
  await page.mouse.move(at.left + at.width / 2, at.top + at.height / 2);
  let seen = { shown: 0, faults: [] as string[], side: "", off: 0 };
  await expect.poll(async () => (seen = await part.evaluate(tipFaults)).shown).toBe(1);
  // The tip's font can arrive after its text, so it is judged once it is still.
  await expect.poll(async () => { const before = JSON.stringify(seen); seen = await part.evaluate(tipFaults); return JSON.stringify(seen) === before; }).toBe(true);
  return seen;
}

for (const [side, room, at] of [
  ["above", "room everywhere", { left: 300, top: 220, width: 40, height: 40 }],
  ["below", "no room above it", { left: 300, top: 8, width: 40, height: 40 }],
  ["right of", "no room above or below it", { left: 8, top: 8, width: 40, height: SCREEN.height - 16 }],
  ["left of", "room only to its left", { left: SCREEN.width - 48, top: 8, width: 40, height: SCREEN.height - 16 }],
] as const) {
  test(`a part with ${room} has its tip ${side} it, touching it`, async ({ page }) => {
    // Arrange and act
    const seen = await pointAtPartAt(page, at);

    // Assert
    expect({ side: seen.side, faults: seen.faults, isCentered: seen.off < 1 }).toEqual({ side: side.split(" ")[0], faults: [], isCentered: true });
  });
}

// A part that fills the screen leaves its tip no side to stand on. The tip
// still shows, whole and inside the window, over the part.
test("a part with no room on any side keeps its tip inside the window", async ({ page }) => {
  // Arrange and act
  const seen = await pointAtPartAt(page, { left: 8, top: 8, width: SCREEN.width - 16, height: SCREEN.height - 16 });

  // Assert: the one thing wrong with it is that it cannot touch its part.
  expect(seen.faults).toEqual(["'Pause this goblin at its next stopping point' does not touch its part"]);
});

// A part in a corner of the screen keeps its tip against it, never off in a
// corner of its own.
for (const [corner, side, at] of [
  ["top left", "below", { left: 8, top: 8, width: 40, height: 40 }],
  ["top right", "below", { left: SCREEN.width - 48, top: 8, width: 40, height: 40 }],
  ["bottom left", "above", { left: 8, top: SCREEN.height - 48, width: 40, height: 40 }],
  ["bottom right", "above", { left: SCREEN.width - 48, top: SCREEN.height - 48, width: 40, height: 40 }],
] as const) {
  test(`a part in the screen's ${corner} corner has its tip ${side} it, touching it`, async ({ page }) => {
    // Arrange and act
    const seen = await pointAtPartAt(page, at);

    // Assert
    expect({ side: seen.side, faults: seen.faults }).toEqual({ side, faults: [] });
  });
}

// Every hover hint is the board's own tip: no part carries a native title,
// which the browser shows late, at the pointer, in its own style.
test("no part on the board, a task's panel, the Command Center or the canvas carries a native title", async ({ page }) => {
  // Arrange
  const readAt = new Date().toISOString();
  await open(page, TASKS, [QUESTION], [{ provider: "claude", status: "available", percent_remaining: 75, read_at: readAt, resets_at: "2026-12-01T00:00:00Z", source: "oauth" }]);
  const titled = () => page.evaluate(() => [...document.querySelectorAll("body [title], svg title")].map((element) => element.outerHTML.slice(0, 160)));
  const seen: string[] = [];

  // Act: the board, a task's panel, the Command Center, then the canvas.
  seen.push(...await titled());
  await page.locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText("working-two", { exact: true }) }).locator(".task-card").click();
  await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
  seen.push(...await titled());
  await openItem(page, "Which layout should the board open in");
  await expect(page.locator("dialog.question-modal")).toBeVisible();
  seen.push(...await titled());
  await page.locator("dialog.question-modal").getByRole("button", { name: "Close the Command Center", exact: true }).click();
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await expect(page.locator(".canvas-controls")).toBeVisible();
  seen.push(...await titled());

  // Assert
  expect(seen).toEqual([]);
});

// The tip of a shortened title goes with the press that starts the card's
// drag, before the drag has moved any card.
test("a card being dragged shows no tip", async ({ page }) => {
  await page.setViewportSize({ width: 820, height: 900 });
  await open(page, [{ ...TASKS[0], title: LONGEST }, ...TASKS.slice(1)]);
  const card = page.locator(".task-board [data-sort-id='queued-one'] .task-card");
  await card.hover();
  const tip = page.getByRole("tooltip");
  await expect(tip).toHaveText(LONGEST);
  const box = (await card.boundingBox())!;
  const x = box.x + box.width / 2, y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x, y + 12, { steps: 4 });
  await expect(page.locator(".task-board [data-sort-id='queued-one']")).toHaveClass(/dragging/);
  await expect(tip).toHaveCount(0);
  await page.mouse.up();
  await expect(page.locator(".task-cards.sorting")).toHaveCount(0);
});

// A tip stands on a surface of its own that nothing shows through, with an
// edge, whatever it floats over: a card, a terminal or the workshop picture.
test("a tip's surface is solid, with an edge, and stands out from the card under it", async ({ page }) => {
  await page.setViewportSize({ width: 2400, height: 900 });
  await open(page);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText("working-one", { exact: true }) });
  await shell.getByRole("button", { name: /^Pause / }).hover();
  const tip = page.getByRole("tooltip");
  await expect(tip).toHaveText("Pause");
  const look = await tip.evaluate((node) => {
    const style = getComputedStyle(node);
    const channels = (color: string) => (color.match(/[\d.]+/g) || []).map(Number);
    const [red, green, blue, alpha = 1] = channels(style.backgroundColor);
    return { alpha, image: style.backgroundImage, opacity: style.opacity, edge: parseFloat(style.borderTopWidth), edgeAlpha: channels(style.borderTopColor)[3] ?? 1, light: red + green + blue };
  });
  expect(look).toMatchObject({ alpha: 1, image: "none", opacity: "1", edge: 1, edgeAlpha: 1 });
  // The cards are close to black (their base is #04060a); the tip is a step lighter.
  expect(look.light).toBeGreaterThan(60);
});

// The Overlord, 2026-10-09: "also speed make it instant on hover or off". A
// tip shows on the frame the pointer or the keyboard's focus comes to its
// part, takes the next part's place on the frame the pointer moves to it, and
// goes on the frame the pointer leaves, with no fade either way. Each look is
// taken in the first frame after the event, so a wait or a fade of any length
// is seen.
test("a tip shows, swaps and goes on the frame the pointer or focus moves, with no fade", async ({ page }) => {
  // Arrange: Pause and Stop sit side by side on a card.
  await open(page);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText("working-one", { exact: true }) });
  const pause = shell.getByRole("button", { name: /^Pause / }), stop = shell.getByRole("button", { name: /^Stop / });
  await page.evaluate(() => {
    const looks: { event: string; part: string; tips: string[] }[] = [];
    Object.assign(window, { tipLooks: looks });
    for (const type of ["pointerover", "pointerout", "focusin", "focusout"]) addEventListener(type, (event) => {
      const part = event.target instanceof Element ? event.target.closest("[data-tip]")?.getAttribute("data-tip") ?? "" : "";
      requestAnimationFrame(() => looks.push({ event: type, part, tips: [...document.querySelectorAll<HTMLElement>("[role=tooltip]")].map((tip) => {
        const style = getComputedStyle(tip);
        return tip.textContent + (style.opacity !== "1" || style.transitionDuration !== "0s" || style.animationName !== "none" ? " (fading)" : "");
      }) }));
    }, true);
  });
  const center = async (part: typeof pause) => { const box = (await part.boundingBox())!; return [box.x + box.width / 2, box.y + box.height / 2] as const; };
  const [pauseX, pauseY] = await center(pause), [stopX, stopY] = await center(stop);
  const lookAfter = (event: string, part: string) => page.evaluate(([event, part]) => (window as unknown as { tipLooks: { event: string; part: string; tips: string[] }[] }).tipLooks.filter((look) => look.event === event && look.part === part).at(-1)?.tips, [event, part]);

  // Act: the pointer comes to Pause, moves straight on to Stop, and leaves.
  await page.mouse.move(pauseX, pauseY);
  await expect.poll(() => lookAfter("pointerover", "Pause")).toBeDefined();
  await page.mouse.move(stopX, stopY);
  await expect.poll(() => lookAfter("pointerover", "Stop")).toBeDefined();
  await page.mouse.move(1, 1);
  await expect.poll(() => lookAfter("pointerout", "Stop")).toBeDefined();

  // Assert
  expect([await lookAfter("pointerover", "Pause"), await lookAfter("pointerover", "Stop"), await lookAfter("pointerout", "Stop")]).toEqual([["Pause"], ["Stop"], []]);

  // Act: the keyboard's focus comes to Stop, and leaves.
  await pause.focus();
  await page.keyboard.press("Tab");
  await expect(stop).toBeFocused();
  await expect.poll(() => lookAfter("focusin", "Stop")).toBeDefined();
  await stop.evaluate((button) => (button as HTMLElement).blur());
  await expect.poll(() => lookAfter("focusout", "Stop")).toBeDefined();

  // Assert
  expect([await lookAfter("focusin", "Stop"), await lookAfter("focusout", "Stop")]).toEqual([["Stop"], []]);
});

// The Overlord, 2026-10-08: no tip is left open after a scroll. A scroll
// that moves a part out from under the pointer takes its tip away within two
// frames, as the browser hands the pointer to what is under it now, which may
// show a tip of its own, and a tip the keyboard's focus shows stays with its
// part through a scroll, which moving the focus can start.
test("no tip is left open after a scroll moves its part away", async ({ page }) => {
  // Arrange: enough cards that the Tasks column scrolls, each with a title of
  // its own, so a tip names its card.
  await page.setViewportSize({ width: 1440, height: 700 });
  const queued = Array.from({ length: 24 }, (_, index) => task("queued-" + index, "queued", { title: LONG + ", card " + index, generation: "", brief: true, queue_revision: "q1" }));
  await open(page, [...queued, ...TASKS.slice(2)]);
  const card = page.locator(".task-board [data-sort-id='queued-0'] .task-card");
  const tip = page.getByRole("tooltip");
  await card.hover();
  await expect(tip).toHaveText(LONG + ", card 0");

  // Act: the box the card sits in scrolls it 200 pixels, and within two
  // frames the tips are read, with the tip of the part now under the pointer.
  const box = (await card.boundingBox())!;
  const seen = await card.evaluate(async (node, [x, y]) => {
    let box = node.parentElement;
    while (box && !(box.scrollHeight > box.clientHeight && /auto|scroll/.test(getComputedStyle(box).overflowY))) box = box.parentElement;
    (box ?? document.scrollingElement!).scrollBy(0, 200);
    for (let frame = 0; frame < 2; frame++) await new Promise(requestAnimationFrame);
    const under = document.elementFromPoint(x, y)?.closest("[data-tip], .task-card");
    return { tips: [...document.querySelectorAll("[role=tooltip]")].map((tip) => tip.textContent), under: under === node ? "the card that was scrolled" : under?.getAttribute("data-tip") ?? "" };
  }, [box.x + box.width / 2, box.y + box.height / 2]);

  // Assert: the card left the pointer, and no tip but that of the part under
  // the pointer now is left.
  expect(seen.under).not.toBe("the card that was scrolled");
  expect(seen.tips.filter((text) => text !== seen.under)).toEqual([]);

  // Act: the keyboard's focus comes to the card, and its box scrolls.
  await page.mouse.move(0, 0);
  await card.focus();
  await page.keyboard.press("Shift+Tab");
  await page.keyboard.press("Tab");
  await expect(tip).toHaveText(LONG + ", card 0");
  await card.evaluate((node) => {
    let box = node.parentElement;
    while (box && !(box.scrollHeight > box.clientHeight && /auto|scroll/.test(getComputedStyle(box).overflowY))) box = box.parentElement;
    (box ?? document.scrollingElement!).scrollBy(0, 40);
  });

  // Assert: it stays, beside its card.
  await expect(tip).toHaveText(LONG + ", card 0");
});

// No tip is left open after a press. A node on the canvas keeps the press
// that starts its drag to itself, which left its tip over the canvas for the
// whole drag.
test("a press takes a tip away, even on a part that keeps the press to itself", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 1440, height: 900 });
  await open(page, [task("working-one", "working", { harness: "codex", goblin_name: "Jerry", goblin_title: "Code Designer" })]);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  // Focus leaving a part takes its tip away too, so nothing holds it here.
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  const node = page.locator(".flow-node-main[data-tip]").first();
  const tip = page.getByRole("tooltip");
  await node.hover();
  await expect(tip).toBeVisible();

  // Act
  await page.mouse.down();

  // Assert
  await expect(tip).toHaveCount(0);
  await page.mouse.up();
});

// A tip follows its part only while the part moves. Once the part is still
// nothing keeps asking for frames, however long the pointer rests on it,
// whether the part shows a tip or is a card that has none. The board itself
// asks for one when a snapshot arrives, so a few in half a second is still,
// and a tip drawn again on every frame asks for thirty.
for (const [name, find, shows] of [
  ["a part with a tip", (shell: Locator) => shell.getByRole("button", { name: /^Pause / }), { shown: 1, faults: [], side: "above" }],
  ["a card with no tip", (shell: Locator) => shell.locator(".task-card"), { shown: 0, faults: [] }],
] as const) {
  test(`a pointer at rest on ${name} asks for no frames`, async ({ page }) => {
    // Arrange
    await page.addInitScript(() => {
      const frame = window.requestAnimationFrame.bind(window);
      window.framesAsked = 0;
      window.requestAnimationFrame = (callback) => { window.framesAsked!++; return frame(callback); };
    });
    await open(page);
    const part = find(page.locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText("working-one", { exact: true }) }));

    // Act: the pointer comes to the part, which lifts under it, and rests.
    await part.hover();
    await expect.poll(async () => (await part.evaluate(tipFaults)).shown).toBe(shows.shown);
    await expect.poll(() => page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running").length)).toBe(0);
    const asked = (await page.evaluate(() => window.framesAsked))!;
    await page.waitForTimeout(500);

    // Assert: nothing asked for frames, and a tip still touches its part.
    expect((await page.evaluate(() => window.framesAsked))! - asked).toBeLessThan(5);
    expect(await part.evaluate(tipFaults)).toMatchObject(shows);
  });
}

declare global {
  interface Window { framesAsked?: number }
}
