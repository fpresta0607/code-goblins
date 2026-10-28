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
const RECORD = { input_tokens: 1200, output_tokens: 80, context_tokens: 2400, context_window: 100000, model: "claude-opus-5-5", effort: "xhigh" };
const BACK_TAB = "\x1b[Z";

// idle tells the box's terminal view the harness waits at its prompt, its
// footer naming mode.
const idle = (mode = "bypass permissions") => ({ type: "screen", asking: false, open: false, working: false, mode });

// setUp opens the goblin's box with the harness idle, running what record
// says, and its catalog answered, counting the catalog's reads.
async function setUp(page: Page, record = RECORD) {
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", { ...PAGE, usage: record });
  const reads: number[] = [];
  await page.route("**/api/engines", (route) => {
    reads.push(Date.now());
    return route.fulfill({ json: CATALOG });
  });
  await openGoblin(page);
  relay.say(idle());
  const control = box(page).getByRole("button", { name: "Change model", exact: true });
  await expect(control).toBeEnabled();
  return { relay, reads, control, pane: box(page).getByRole("menu", { name: "Model", exact: true }), flyout: box(page).getByRole("menu", { name: "Effort", exact: true }) };
}

// places reads where each of the pane's models is drawn.
const places = (page: Page) => box(page).locator(".engine-pane [data-model]").evaluateAll((rows) => rows.map((row) => {
  const shape = row.getBoundingClientRect();
  return [Math.round(shape.x), Math.round(shape.y), Math.round(shape.width), Math.round(shape.height)];
}));

for (const [label, width, isTouch] of [["the desktop", 1280, false], ["a phone", 393, true]] as const) {
  test.describe(`on ${label}`, () => {
    test.use({ viewport: { width, height: 852 }, hasTouch: isTouch, isMobile: isTouch });

    test("the box's row holds only +, Style, the model and the microphone, and the model's pane holds the checked model on top, the efforts and the mode", async ({ page }) => {
      // Arrange
      const { control, pane, flyout } = await setUp(page);

      // Act
      const row = await box(page).locator(".composer-row button").evaluateAll((buttons) => buttons.map((button) => button.getAttribute("aria-label")));
      if (isTouch) await control.tap();
      else await control.click();

      // Assert
      expect(row).toEqual(["Add files", "Style: modern", "Change model", isTouch ? "Dictate" : "Hold to dictate"]);
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
  });
}

// The Overlord, 2026-10-28: "fyi defautl for opus 5 should be xhigh 5.5". A
// model picked by itself, with no effort from its flyout, goes in at its own
// default effort: Opus 5.5 at xhigh, whatever the session ran before.
for (const [name, id, effort] of [["Opus 5.5", "claude-opus-5-5", "xhigh"], ["Haiku 5.5", "claude-haiku-5-5", "medium"]] as const) {
  test(`${name} picked by itself goes in at its default effort, ${effort}`, async ({ page }) => {
    // Arrange: the session runs Sonnet 5.5 at high.
    const { relay, control, pane } = await setUp(page, { ...RECORD, model: "claude-sonnet-5-5", effort: "high" });
    await control.click();

    // Act
    await pane.getByRole("menuitemradio", { name, exact: true }).click();

    // Assert: the model first, its default effort once the harness took it.
    await expect(pane).toHaveCount(0);
    await expect.poll(() => typed(relay)).toBe(pasted("/model " + id) + "");
    relay.say(idle());
    await expect.poll(() => typed(relay)).toBe(pasted("/model " + id) + "" + pasted("/effort " + effort) + "");
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
  const record = (effort: string) => page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "usage", data } })), { ...RECORD, effort });
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
  const paneEdge = await edge(".messages-view:not([hidden]) .engine-pane");
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
