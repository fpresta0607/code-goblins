import { expect, test, type Page } from "./site";
import { box, field, open, openGoblin, PAGE, pasted, snapshot, typed } from "./session-board";

// The Overlord, 2026-10-28, on the model pane: "open model modal with effort
// takes a second or two to load also combining the tables is good but i was
// thinking a horizontal model stems off the side of the hovered or clicked
// model", "also effort coloring in the modal should match the coloring of
// text when selected", "remove the chat input lighter green border so it
// matches tool tips and models", and from his phone: "the auto mode and all
// that other stuff could be a selector within the Opus 5.5 model button along
// with effort ... have the selected model at the very top and checked ...
// Then you can remove the button from there". One pane, opened from the model
// button, holds the session's models, each one's efforts in a flyout off its
// side, and its permission mode; the box keeps only +, Style, the model, the
// microphone and Send. The supervisor is played by tests/session-board.ts.

const CATALOG = { harnesses: [{ id: "claude", name: "Claude Code", models: [
  { id: "claude-sonnet-5-5", name: "Sonnet 5.5", efforts: ["low", "medium", "high", "xhigh", "max"], default_effort: "high" },
  { id: "claude-opus-5-5", name: "Opus 5.5", efforts: ["low", "medium", "high", "xhigh", "max"], default_effort: "xhigh" },
  { id: "claude-haiku-5-5", name: "Haiku 5.5", efforts: ["low", "medium", "high"], default_effort: "medium" },
] }] };
// MANY offers more models than the pane shows at once, so its list scrolls.
const MANY = { harnesses: [{ ...CATALOG.harnesses[0], models: [...CATALOG.harnesses[0].models, ...["Fable 5.2", "Opus 5", "Sonnet 5", "Haiku 5", "Opus 4.8", "Sonnet 4.8", "Haiku 4.8", "Opus 4.6", "Sonnet 4.6"]
  .map((name) => ({ id: "claude-" + name.toLowerCase().replace(/[ .]/g, "-"), name, efforts: ["low", "medium", "high"], default_effort: "medium" }))] }] };
// CODEX is Codex's catalog as session-controls.spec.ts has it: two models of
// four efforts each. A Codex terminal names no mode, so the pane has no Mode
// row and its list of models is shorter than a model's efforts.
const CODEX = { harnesses: [{ id: "codex", name: "Codex", models: [
  { id: "gpt-6-astra", name: "GPT-6-Astra", efforts: ["low", "medium", "high", "xhigh"], default_effort: "medium" },
  { id: "gpt-6.1-sol", name: "GPT-6.1-Sol", efforts: ["low", "medium", "high", "xhigh"], default_effort: "low" },
] }] };
const USAGE = { input_tokens: 1200, output_tokens: 80, context_tokens: 2400, context_window: 100000 };
// What the goblin's task record names, which the box reads.
const RECORD = { model: "claude-opus-5-5", effort: "xhigh" };
const BACK_TAB = "\x1b[Z";

// idle tells the box's terminal view the harness waits at its prompt, its
// footer naming mode.
const idle = (mode = "bypass permissions") => ({ type: "screen", asking: false, open: false, working: false, mode });

// setUp opens the goblin's box with the harness idle, its footer naming mode,
// running what record says, and catalog answered, counting the catalog's
// reads.
async function setUp(page: Page, record: { harness?: string; model: string; effort: string } = RECORD, catalog = CATALOG, mode?: string) {
  const relay = await open(page, snapshot({}, { phase: "settled", ...record }), "", { ...PAGE, usage: USAGE });
  const reads: number[] = [];
  await page.route("**/api/engines", (route) => {
    reads.push(Date.now());
    return route.fulfill({ json: catalog });
  });
  await openGoblin(page);
  relay.say(idle(mode));
  const control = box(page).getByRole("button", { name: "Change model", exact: true });
  await expect(control).toBeEnabled();
  return { relay, reads, control, pane: box(page).getByRole("menu", { name: "Model", exact: true }), flyout: box(page).getByRole("menu", { name: "Effort", exact: true }) };
}

// scrollUp scrolls the pane's list by a finger's drag up it on a phone, and
// on the desktop by the wheel over its right edge, its scroll bar, which
// points at no model, as a pointer over a model brings that model's efforts
// out.
async function scrollUp(page: Page, surface: { x: number; y: number; width: number; height: number }, isTouch: boolean) {
  if (!isTouch) {
    await page.mouse.move(surface.x + surface.width - 4, surface.y + surface.height / 2);
    await page.mouse.wheel(0, 180);
    return;
  }
  const session = await page.context().newCDPSession(page);
  const start = { x: Math.round(surface.x + surface.width - 60), y: Math.round(surface.y + surface.height - 30) };
  await session.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [start] });
  for (let step = 1; step <= 10; step++) await session.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: start.x, y: start.y - step * 18 }] });
  await session.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
}

// places reads where each of the pane's models is drawn.
const places = (page: Page) => box(page).locator(".engine-pane [data-model]").evaluateAll((rows) => rows.map((row) => {
  const shape = row.getBoundingClientRect();
  return [Math.round(shape.x), Math.round(shape.y), Math.round(shape.width), Math.round(shape.height)];
}));

for (const [label, width, isTouch] of [["the desktop", 1280, false], ["a phone", 393, true]] as const) {
  test.describe(`on ${label}`, () => {
    test.use({ viewport: { width, height: 852 }, hasTouch: isTouch, isMobile: isTouch });

    test("the box's row holds only +, Speech history, on a phone Clear message, the model and the microphone, and the model's pane holds the checked model on top, the efforts and the mode", async ({ page }) => {
      // Arrange
      const { control, pane, flyout } = await setUp(page);

      // Act
      const row = await box(page).locator(".composer-row button").evaluateAll((buttons) => buttons.filter((button) => button.getClientRects().length).map((button) => button.getAttribute("aria-label")));
      if (isTouch) await control.tap();
      else await control.click();

      // Assert
      expect(row).toEqual(["Add files", "Speech history", ...isTouch ? ["Clear message"] : [], "Change model", isTouch ? "Dictate" : "Hold to dictate"]);
      const models = pane.locator("[data-model]");
      await expect(models).toHaveCount(3);
      await expect(models.first()).toHaveAccessibleName("Opus 5.5");
      await expect(models.first()).toHaveAttribute("aria-checked", "true");
      await expect(pane.getByRole("menuitemradio", { checked: true })).toHaveCount(2);
      await expect(flyout.getByRole("menuitemradio", { name: "xhigh", exact: true })).toHaveAttribute("aria-checked", "true");
      const modes = pane.getByRole("group", { name: "Mode", exact: true }).getByRole("menuitemradio");
      await expect(modes).toHaveText(["Manual", "Accept edits", "Plan", "Auto", "Don't ask", "Bypass"]);
      await expect(pane.getByRole("menuitemradio", { name: "Bypass", exact: true })).toHaveAttribute("aria-checked", "true");
      await expect(pane).toBeInViewport({ ratio: 1 });
      await expect(flyout).toBeInViewport({ ratio: 1 });
    });

    test("an effort picked on another model sets that model, then its effort once the harness took the model", async ({ page }) => {
      // Arrange
      const { relay, control, pane, flyout } = await setUp(page);
      if (isTouch) await control.tap();
      else await control.click();
      const sonnet = pane.getByRole("menuitemradio", { name: "Sonnet 5.5", exact: true });

      // Act
      if (isTouch) await sonnet.tap();
      else await sonnet.hover();
      const max = flyout.getByRole("menuitemradio", { name: "max", exact: true });
      await expect(max).toHaveAttribute("aria-checked", "false");
      if (isTouch) await max.tap();
      else await max.click();

      // Assert: the model first, the effort only once the harness took it.
      await expect(pane).toHaveCount(0);
      await expect.poll(() => typed(relay)).toBe(pasted("/model claude-sonnet-5-5") + "\r");
      relay.say(idle());
      await expect.poll(() => typed(relay)).toBe(pasted("/model claude-sonnet-5-5") + "\r" + pasted("/effort max") + "\r");
      await expect(control).toContainText("Sonnet 5.5");
      await expect(control.locator(".effort-level")).toHaveText("max");
      await expect(field(page)).toBeFocused();
    });

    test("a mode picked in the pane is set by Shift+Tab, once per mode the footer names, until it names that one", async ({ page }) => {
      // Arrange
      const { relay, control, pane } = await setUp(page);
      if (isTouch) await control.tap();
      else await control.click();
      const plan = pane.getByRole("menuitemradio", { name: "Plan", exact: true });

      // Act
      if (isTouch) await plan.tap();
      else await plan.click();

      // Assert
      await expect(pane).toHaveCount(0);
      await expect.poll(() => typed(relay)).toBe(BACK_TAB);
      relay.say(idle("manual mode"));
      await expect.poll(() => typed(relay)).toBe(BACK_TAB.repeat(2));
      relay.say(idle("accept edits"));
      await expect.poll(() => typed(relay)).toBe(BACK_TAB.repeat(3));
      relay.say(idle("plan mode"));
      if (isTouch) await control.tap();
      else await control.click();
      await expect(pane.getByRole("menuitemradio", { name: "Plan", exact: true })).toHaveAttribute("aria-checked", "true");
      expect(typed(relay)).toBe(BACK_TAB.repeat(3));
    });

    // The Overlord, 2026-11-24: "make model selector ui more conencted
    // rather than two seperate paenls for mdoel and effrot ... when i scroll
    // with second panel open on screen it does t stay connected" (#2845).
    test("a model's efforts and the models are one surface, with nothing of the chat between them", async ({ page }) => {
      // Arrange
      const { control, pane, flyout } = await setUp(page);
      if (isTouch) await control.tap();
      else await control.click();
      await expect(flyout).toBeVisible();
      const [list, out] = await Promise.all([pane.boundingBox(), flyout.boundingBox()]);
      if (!list || !out) throw new Error("the pane and its efforts are drawn");

      // Act: what is drawn where the efforts meet the models, level with the
      // efforts' title.
      const joint = await page.evaluate(([x, y]) => !!document.elementFromPoint(x, y)?.closest(".engine-panes"), [(out.x + out.width + list.x) / 2, out.y + 12]);

      // Assert
      expect(list.x - (out.x + out.width), "no gap between the efforts and the models").toBeLessThanOrEqual(1);
      expect(joint, "the pane's own surface where they meet, not the chat behind it").toBe(true);
    });

    test("a model's efforts move with the list as it scrolls, and show nowhere past its edge once their model has gone from it", async ({ page }) => {
      // Arrange: more models than the pane shows at once, with the running
      // model's efforts out at the top.
      const { control, pane, flyout } = await setUp(page, RECORD, MANY);
      if (isTouch) await control.tap();
      else await control.click();
      const opus = pane.getByRole("menuitemradio", { name: "Opus 5.5", exact: true });
      await expect(opus).toHaveAttribute("aria-expanded", "true");
      const surface = await box(page).locator(".engine-panes").boundingBox();
      const row = await opus.boundingBox();
      if (!surface || !row) throw new Error("the pane and its model are drawn");

      // Act
      await scrollUp(page, surface, isTouch);

      // Assert: Opus 5.5 has gone above the list's top edge and its efforts
      // with it, level with its row, and nothing of them shows above that edge.
      await expect.poll(async () => (await opus.boundingBox())?.y ?? 0).toBeLessThan(surface.y - row.height);
      await expect(opus).toHaveAttribute("aria-expanded", "true");
      const [moved, out] = await Promise.all([opus.boundingBox(), flyout.boundingBox()]);
      if (!moved || !out) throw new Error("the model and its efforts are laid out");
      expect(Math.abs(out.y - moved.y), "level with its model's row").toBeLessThanOrEqual(1);
      const isShownAbove = await page.evaluate(([x, y]) => !!document.elementFromPoint(x, y)?.closest("[role=menu][aria-label=Effort]"), [out.x + out.width / 2, surface.y - 8]);
      expect(isShownAbove, "no effort shows above the list's top edge").toBe(false);
    });

    test("a model's efforts taller than the list end at the list's foot, and the list does not move", async ({ page }) => {
      // Arrange: a Codex session running its second model, whose efforts are
      // out as the pane opens, and a terminal that names no mode.
      const { control, pane, flyout } = await setUp(page, { harness: "codex", model: "gpt-6.1-sol", effort: "low" }, CODEX, "");
      if (isTouch) await control.tap();
      else await control.click();
      await expect(flyout.getByRole("menuitemradio")).toHaveCount(4);
      await expect(pane.getByRole("group", { name: "Mode", exact: true })).toHaveCount(0);
      const before = await places(page);
      const astra = pane.getByRole("menuitemradio", { name: "GPT-6 Astra", exact: true });

      // Act
      if (isTouch) await astra.tap();
      else await astra.hover();

      // Assert
      await expect(astra).toHaveAttribute("aria-expanded", "true");
      await expect(flyout, "the efforts lie wholly on the pane's surface").toBeInViewport({ ratio: 1 });
      expect(await places(page), "the models stay where they were").toEqual(before);
      const [list, out, surface] = await Promise.all([pane.boundingBox(), flyout.boundingBox(), box(page).locator(".engine-panes").boundingBox()]);
      if (!list || !out || !surface) throw new Error("the pane, its list and its efforts are drawn");
      expect(out.height, "four efforts stand taller than two models").toBeGreaterThan(list.height);
      expect(Math.abs(surface.y + surface.height - (list.y + list.height)), "the list's foot is the pane's foot").toBeLessThanOrEqual(1);
      expect(Math.abs(out.y + out.height - (list.y + list.height)), "the efforts end at the list's foot").toBeLessThanOrEqual(1);
      expect(list.x - (out.x + out.width), "no gap between the efforts and the models").toBeLessThanOrEqual(1);
      const joint = await page.evaluate(([x, y]) => !!document.elementFromPoint(x, y)?.closest(".engine-panes"), [(out.x + out.width + list.x) / 2, list.y + list.height - 12]);
      expect(joint, "the pane's own surface where they meet, not the chat behind it").toBe(true);
    });
  });
}

// The Overlord, 2026-10-28: "fyi defautl for opus 5 should be xhigh 5.5". A
// model picked by itself, with no effort from its flyout, goes in at its own
// default effort: Opus 5.5 at xhigh, whatever the session ran before.
for (const [name, id, effort] of [["Opus 5.5", "claude-opus-5-5", "xhigh"], ["Haiku 5.5", "claude-haiku-5-5", "medium"]] as const) {
  test(`${name} picked by itself goes in at its default effort, ${effort}`, async ({ page }) => {
    // Arrange: the session runs Sonnet 5.5 at high.
    const { relay, control, pane } = await setUp(page, { model: "claude-sonnet-5-5", effort: "high" });
    await control.click();

    // Act
    await pane.getByRole("menuitemradio", { name, exact: true }).click();

    // Assert: the model first, its default effort once the harness took it.
    await expect(pane).toHaveCount(0);
    await expect.poll(() => typed(relay)).toBe(pasted("/model " + id) + "\r");
    relay.say(idle());
    await expect.poll(() => typed(relay)).toBe(pasted("/model " + id) + "\r" + pasted("/effort " + effort) + "\r");
    await expect(control).toContainText(name);
    await expect(control.locator(".effort-level")).toHaveText(effort);
  });
}

test("the pane opens with its models and efforts within 200 ms of the click, reading nothing over the network", async ({ page }) => {
  // Arrange: the box has read the catalog ahead, and the pane is timed from
  // the press to its efforts drawn, in the page.
  const { reads, control } = await setUp(page);
  await expect.poll(() => reads.length).toBe(1);
  await control.evaluate((button) => {
    button.addEventListener("pointerdown", () => {
      const pressed = performance.now();
      const look = () => {
        if (document.querySelector(".engine-pane [data-model]") && document.querySelector(".engine-flyout [role=menuitemradio]")) document.body.dataset.paneShown = String(performance.now() - pressed);
        else requestAnimationFrame(look);
      };
      look();
    }, { once: true });
  });

  // Act
  await control.click();

  // Assert
  await expect(page.locator("body")).toHaveAttribute("data-pane-shown", /\d/);
  expect(Number(await page.locator("body").getAttribute("data-pane-shown"))).toBeLessThan(200);
  expect(reads, "no read of the catalog after the click").toHaveLength(1);
});

test("a hovered model brings its efforts out beside it, and the list of models does not move", async ({ page }) => {
  // Arrange
  const { control, pane, flyout } = await setUp(page);
  await control.click();
  await expect(flyout).toBeVisible();
  const before = await places(page);
  const haiku = pane.getByRole("menuitemradio", { name: "Haiku 5.5", exact: true });

  // Act
  await haiku.hover();

  // Assert: Haiku's three efforts, on the pane's side, level with its row.
  await expect(haiku).toHaveAttribute("aria-expanded", "true");
  await expect(flyout.getByRole("menuitemradio")).toHaveText(["low", "medium", "high"]);
  const [row, out, list] = await Promise.all([haiku.boundingBox(), flyout.boundingBox(), pane.boundingBox()]);
  expect(row && out && list, "all three drawn").toBeTruthy();
  if (!row || !out || !list) return;
  expect(out.x + out.width, "beside the pane, not over it").toBeLessThanOrEqual(list.x);
  expect(out.y, "level with the model's row").toBeLessThanOrEqual(row.y + row.height);
  expect(out.y + out.height).toBeGreaterThanOrEqual(row.y);
  expect(await places(page)).toEqual(before);
});

test("the keyboard reaches a model's efforts with the right arrow and comes back with the left", async ({ page }) => {
  // Arrange
  const { control, pane, flyout } = await setUp(page);
  await control.click();
  const opus = pane.getByRole("menuitemradio", { name: "Opus 5.5", exact: true });
  await expect(opus).toBeFocused();

  // Act
  await page.keyboard.press("ArrowRight");

  // Assert
  await expect(flyout.getByRole("menuitemradio", { name: "xhigh", exact: true })).toBeFocused();
  await page.keyboard.press("ArrowLeft");
  await expect(opus).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(pane.getByRole("menuitemradio", { name: "Sonnet 5.5", exact: true })).toBeFocused();
  await expect(flyout.getByRole("menuitemradio")).toHaveCount(5);
});

test("each effort wears in the pane the colour it wears on the model button once picked", async ({ page }) => {
  // Arrange
  const { control, flyout } = await setUp(page);
  const record = (effort: string) => page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: data })), snapshot({}, { phase: "settled", ...RECORD, effort }));
  const look = (element: Element) => [getComputedStyle(element).color, getComputedStyle(element).backgroundImage].join(" ");

  for (const level of ["low", "medium", "high", "xhigh", "max"]) {
    // Act
    await record(level);
    await expect(control.locator(".effort-level")).toHaveText(level);
    const picked = await control.locator(".effort-level").evaluate(look);
    await control.click();
    const listed = await flyout.getByRole("menuitemradio", { name: level, exact: true }).locator(".effort-level").evaluate(look);
    await page.keyboard.press("Escape");

    // Assert
    expect(listed, level).toBe(picked);
  }
});

test("each mode wears a colour of its own and no icon", async ({ page }) => {
  // Arrange
  const { control, pane } = await setUp(page);
  await control.click();
  const modes = pane.getByRole("group", { name: "Mode", exact: true }).getByRole("menuitemradio");

  // Act
  const looks = await modes.evaluateAll((choices) => choices.map((choice) => ({ color: getComputedStyle(choice).color, hasIcon: !!choice.querySelector("svg, .icon") })));

  // Assert
  expect(looks).toHaveLength(6);
  expect(new Set(looks.map((each) => each.color)).size).toBe(6);
  expect(looks.filter((each) => each.hasIcon)).toEqual([]);
});

test("the box's edge is the model pane's edge, while he writes in it and while he does not", async ({ page }) => {
  // Arrange
  const { control, pane } = await setUp(page);
  const edge = (selector: string) => page.locator(selector).first().evaluate((element) => getComputedStyle(element).borderTopColor);
  await control.click();
  await expect(pane).toBeVisible();
  const paneEdge = await edge(".messages-view:not([hidden]) .engine-panes");
  await page.keyboard.press("Escape");

  // Act
  await field(page).focus();
  const writing = await edge(".messages-view:not([hidden]) .composer-input");
  await field(page).blur();
  const resting = await edge(".messages-view:not([hidden]) .composer-input");

  // Assert
  expect(writing).toBe(paneEdge);
  expect(resting).toBe(paneEdge);
});
