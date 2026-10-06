import { expect, holdStream, openItem, test, type Locator, type Page } from "./site";

// Every tip on the board holds its whole text on a solid surface, stays
// inside the window and is cut off by nothing, wherever its part is: a card,
// the CFO's bar, the top bar, a task's panel, the Command Center or the
// Orchestration canvas, on a wide window and on a phone.
const GB = 2 ** 30;
const since = "2026-10-02T09:00:00Z";
// Commit is the tighter of memory and commit, so Start and Resume carry the
// longest tips they have.
const memory = { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, paged_pool: 0.6 * GB, nonpaged_pool: 0.4 * GB, available: 3.4 * GB, commit_available: 2.5 * GB };
const LONG = "Paused goblins resume by themselves when the reason for the pause clears, and the board says which goblin each one was waiting on and for how long it has waited";
// The title whose tip lay over the memory meter on 2026-10-02, and the longest
// title the queue held that day.
// Its "; Claude Code" is shown as the card's harness mark now, so the same
// width of title carries other words.
const SEEN = "Paused goblins resume by themselves when the reason for the pause clears, in Claude Code";
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
async function open(page: Page, tasks: Record<string, unknown>[] = TASKS, questions: Record<string, unknown>[] = []) {
  const snapshot = { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [], memory, tasks, questions };
  // The walks point at one part after another, so the board must hold still.
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

// What is wrong with the tip a part shows now, and how many tips it shows. A
// tip is the board's floating one, or one a stylesheet draws from the part's
// ::after, which is measured the same way so that such a tip coming back is
// held to the same rules.
function tipFaults(part: Element): { shown: number; faults: string[] } {
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
  for (const tip of tips) {
    const say = (what: string) => faults.push("'" + want + "' " + what);
    if (tip.text !== want) say("shows '" + tip.text + "'");
    const { box } = tip;
    if (box.left < 0 || box.top < 0 || box.right > document.documentElement.clientWidth || box.bottom > document.documentElement.clientHeight) say("leaves the window");
    if (tip.node && (tip.node.scrollWidth > tip.node.clientWidth + 1 || tip.node.scrollHeight > tip.node.clientHeight + 1)) say("does not hold its whole text");
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
  return { shown: tips.length, faults };
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
    let seen = { shown: 0, faults: [] as string[] };
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

// On 2026-10-02 the tip of the first queued card lay over the memory meter.
// What a card's tip covers of the meter and of the cards' controls and links.
async function covers(page: Page, title: string, width: number) {
  await page.setViewportSize({ width, height: 900 });
  const queued = (id: string, fields: Record<string, unknown> = {}) => task(id, "queued", { generation: "", brief: true, queue_revision: "q1", ...fields });
  await open(page, [queued("queued-one", { title }), ...["two", "three", "four", "five"].map((name) => queued("queued-" + name)), ...TASKS.slice(2)]);
  const tasks = page.getByRole("region", { name: "Tasks", exact: true });
  await expect(tasks.getByRole("group", { name: "Memory" })).toBeVisible();
  await tasks.locator("[data-sort-id='queued-one'] .task-card").hover();
  const tip = page.getByRole("tooltip");
  await expect(tip).toHaveText(title);
  return tip.evaluate((node) => {
    const box = node.getBoundingClientRect();
    const meets = (other: DOMRect) => Math.min(box.right, other.right) - Math.max(box.left, other.left) > 1 && Math.min(box.bottom, other.bottom) - Math.max(box.top, other.top) > 1;
    return [...document.querySelectorAll(".memory, .task-card-shell a, .task-card-shell button:not(.task-card)")].filter((part) => meets(part.getBoundingClientRect()))
      .map((part) => part.getAttribute("aria-label") || part.className);
  });
}

// At 820 px the board is narrow enough to stack and to shorten that title to
// its three lines.
for (const [layout, width] of [["side by side", 2400], ["stacked", 820]] as const) {
  test(`with the columns ${layout}, the first queued card's tip covers neither the memory meter nor another card's controls`, async ({ page }) => {
    expect(await covers(page, SEEN, width)).toEqual([]);
  });

  // A title this long makes a tip taller than the room between two cards, so
  // it covers the least it can, and that is never the meter.
  test(`with the columns ${layout}, the tip of the longest title keeps off the memory meter`, async ({ page }) => {
    expect(await covers(page, LONGEST, width)).not.toContain("Memory");
  });
}

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
