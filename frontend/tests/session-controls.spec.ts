import { expect, test } from "./site";
import { box, boxMenu, field, GOBLIN, NAME, open, openGoblin, PAGE, pasted, snapshot, typed } from "./session-board";

const USAGE = { input_tokens: 12000, output_tokens: 800, context_tokens: 24000, context_window: 100000, model: "recorded-model" };

for (const width of [1440, 390]) {
  test(`a branch reset drops abandoned context statistics at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await open(page, snapshot({}, { harness: "pi" }), "", { ...PAGE, usage: USAGE });
    await openGoblin(page);
    await field(page).fill("keep this draft");
    await expect(page.getByRole("button", { name: "Context: 24% used", exact: true })).toBeVisible();

    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })), { messages: PAGE.messages.slice(0, 1), more: false });

    const ring = page.getByRole("button", { name: "Context usage unavailable", exact: true });
    await expect(ring).toBeVisible();
    await ring.focus();
    const stats = page.getByRole("tooltip", { name: "Session statistics", exact: true });
    await expect(stats).not.toContainText("24k of 100k");
    await expect(stats).not.toContainText("recorded-model");
    await expect(field(page)).toHaveValue("keep this draft");
  });
}

for (const width of [1440, 390]) {
  test.describe(`${width}px controls`, () => {
  test.use({ viewport: { width, height: 900 }, hasTouch: width === 390 });
  for (const who of ["CFO", "goblin"]) {
    test(`${who} session controls use current data and open its window at ${width}px`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      const relay = await open(page, snapshot({ cfo_harness: "codex" }, { harness: "codex" }), "", { ...PAGE, usage: USAGE });
      const posted: unknown[] = [];
      await page.route("**/api/terminal/open", (route) => {
        posted.push(route.request().postDataJSON());
        return route.fulfill({ json: {} });
      });
      await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: [{ text: "from speech", at: 1 }] } }));
      if (who === "CFO") await page.keyboard.press("Control+Alt+1");
      else await openGoblin(page);
      relay.say({ type: "screen", asking: false, open: false, working: false });
      const name = who === "CFO" ? "CFO" : NAME;
      const composer = box(page, name);
      await field(page, name).fill("preserve this draft");
      await expect(composer.locator(".engine-control-model")).toHaveText("Recorded Model");
      await expect(composer.locator(".engine-control .effort-level"), "the model's button shows the session's effort where one is known").toHaveText(who === "CFO" ? [] : ["xhigh"]);

      const ring = page.getByRole("button", { name: "Context: 24% used", exact: true });
      await ring.focus();
      const stats = page.getByRole("tooltip", { name: "Session statistics", exact: true });
      await expect(stats).toContainText("24k of 100k");
      await expect(stats).toContainText("12k");
      await ring.press("Escape");
      await expect(stats).toHaveCount(0);
      if (width === 390) await ring.tap();
      else await ring.hover();
      await expect(stats).toBeVisible();
      await expect(stats).toBeInViewport();
      await field(page, name).click();
      await expect(stats).toHaveCount(0);
      // Choosing a dictation puts it where the caret is, so it is put at the
      // end of the draft first, as a tap on its last word does.
      await page.keyboard.press("End");
      await (await boxMenu(page, name)).getByRole("button", { name: "Speech history", exact: true }).click();
      await page.getByRole("dialog", { name: "Speech history", exact: true }).getByRole("button", { name: "Paste into the message: from speech", exact: true }).click();
      await expect(field(page, name)).toHaveValue("preserve this draft from speech");

      await (await boxMenu(page, name)).getByRole("button", { name: "Open in Windows Terminal", exact: true }).click();
      await expect.poll(() => posted).toEqual([who === "CFO" ? { native: "cfo=cfo-term" } : { native: `task=${GOBLIN.id}&generation=${GOBLIN.generation}` }]);
      await expect(page.locator(".panel-top").getByRole("button", { name: "Open in Windows Terminal", exact: true })).toHaveCount(0);
      await expect(field(page, name)).toHaveValue("preserve this draft from speech");
      expect(typed(relay)).toBe("");
      await page.evaluate((usage) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "usage", data: usage } })), { ...USAGE, context_tokens: 72000, model: "changed-model" });
      await expect(page.getByRole("button", { name: "Context: 72% used", exact: true })).toBeVisible();
      await expect(composer.locator(".engine-control-model")).toHaveText("Changed Model");
      await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, cfo_harness: "codex", cfo_terminal: "new-cfo" }, { harness: "codex", generation: "replacement" }));
      await expect(page.getByRole("button", { name: "Context: 24% used", exact: true })).toBeVisible();
      await expect.poll(() => relay.addresses.some((address) => address.includes(who === "CFO" ? "cfo=new-cfo" : "generation=replacement"))).toBe(true);
      relay.say({ type: "screen", asking: false, open: false, working: false });
      await (await boxMenu(page, name)).getByRole("button", { name: "Open in Windows Terminal", exact: true }).click();
      await expect.poll(() => posted[1]).toEqual(who === "CFO" ? { native: "cfo=new-cfo" } : { native: `task=${GOBLIN.id}&generation=replacement` });
      await expect(field(page, name)).toHaveValue("preserve this draft from speech");
      await expect(composer.locator(".engine-control-model")).toHaveText("Recorded Model");
      await expect(composer.getByRole("button", { name: "Change model", exact: true })).toBeEnabled();
      const controls = await composer.locator(".composer-row button").evaluateAll((buttons) => buttons.map((button) => {
        const bounds = button.getBoundingClientRect();
        return { middle: bounds.top + bounds.height / 2, right: bounds.right };
      }));
      expect(new Set(controls.map((control) => Math.round(control.middle))).size, "one row of controls").toBe(1);
      expect(controls.every((control) => control.right <= width)).toBe(true);
      await page.mouse.move(0, 0);
      await page.screenshot({ path: testInfo.outputPath(`session-controls-${who}-${width}.png`) });
      await ring.focus();
      await page.screenshot({ path: testInfo.outputPath(`session-stats-${who}-${width}.png`) });
    });
  }
  });
}

// What each harness offers, as GET /api/engines reads it from the harness's
// own catalog, and what the session runs and picks in this test. A pick goes
// in as the harness's own command with the value after it, for Claude Code
// and pi, and as the digits of Codex's own /model picker, which takes none.
const CATALOG = { harnesses: [
  { id: "claude", name: "Claude Code", models: [
    { id: "claude-opus-5-5", name: "Opus 5.5", efforts: ["low", "medium", "high", "xhigh", "max"], default_effort: "xhigh" },
    { id: "claude-sonnet-5-5", name: "Sonnet 5.5", efforts: ["low", "medium", "high", "xhigh", "max"], default_effort: "high" },
  ] },
  { id: "codex", name: "Codex", models: [
    { id: "gpt-6-astra", name: "GPT-6-Astra", efforts: ["low", "medium", "high", "xhigh"], default_effort: "medium" },
    { id: "gpt-6.1-sol", name: "GPT-6.1-Sol", efforts: ["low", "medium", "high", "xhigh"], default_effort: "low" },
  ] },
  { id: "pi", name: "Pi", models: [
    { id: "openrouter/z-ai/glm-5.3-flash", name: "GLM 5.3 Flash", efforts: ["off", "minimal", "low", "medium", "high", "xhigh"], default_effort: "medium" },
    { id: "openrouter/deepseek/deepseek-v4.1-flash", name: "DeepSeek V4.1 Flash", efforts: ["off", "minimal", "low", "medium", "high", "xhigh"], default_effort: "high" },
  ] },
] };
const SESSIONS = {
  claude: { model: "claude-opus-5-5", effort: "xhigh", picks: { model: ["claude-sonnet-5-5", /^Sonnet 5\.5/, "Sonnet 5.5"], effort: ["max", "max", "max"] } },
  codex: { model: "gpt-6-astra", effort: "medium", picks: { model: ["gpt-6.1-sol", /^GPT-6\.1 Sol/, "GPT-6.1 Sol"], effort: ["high", "high", "high"] } },
  pi: { model: "openrouter/z-ai/glm-5.3-flash", effort: "medium", picks: { model: ["openrouter/deepseek/deepseek-v4.1-flash", /^DeepSeek V4\.1 Flash/, "Deepseek V4.1 Flash"], effort: ["high", "high", "high"] } },
} as const;
const CODEX_MODELS = { rows: ["Select Model and Effort"], choices: [{ text: "gpt-6-astra (current)", number: 1, focused: true }, { text: "gpt-6.1-sol", number: 2 }], hints: ["Press enter to confirm or esc to go back"] };
const CODEX_EFFORTS = { rows: ["Select Reasoning Level"], choices: [{ text: "Low", number: 1 }, { text: "Medium (default)", number: 2, focused: true }, { text: "High", number: 3 }, { text: "Extra high", number: 4 }], hints: ["Press enter to confirm or esc to go back"] };
const recorded = (model: string, effort: string) => ({ ...USAGE, model, effort });

for (const width of [1440, 390]) {
test.describe(`${width}px native settings`, () => {
test.use({ viewport: { width, height: 900 }, hasTouch: width === 390 });
for (const harness of ["codex", "claude", "pi"] as const) {
  for (const who of harness === "codex" ? ["CFO", "goblin"] : ["goblin"]) {
  for (const setting of ["model", "effort"] as const) {
    test(`${harness} ${who} ${setting} opens the board's own pane, sets the same session and reads it back`, async ({ page }) => {
      // Arrange
      const session = SESSIONS[harness];
      const [value, option, shown] = session.picks[setting];
      const relay = await open(page, snapshot({ cfo_harness: harness }, { harness, model: session.model, effort: session.effort }), "", { ...PAGE, usage: recorded(session.model, session.effort) });
      await page.route("**/api/engines", (route) => route.fulfill({ json: CATALOG }));
      if (who === "CFO") await page.keyboard.press("Control+Alt+1");
      else await openGoblin(page);
      const name = who === "CFO" ? "CFO" : NAME;
      await field(page, name).fill("my unsent words");
      await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
      relay.say({ type: "screen", asking: false, open: false, working: false });
      const control = box(page, name).getByRole("button", { name: "Change model", exact: true });
      await expect(control).toBeEnabled();

      // Act: the control opens the board's own pane, never the terminal, with
      // the efforts of the model it runs out beside it.
      if (width === 390) await control.tap();
      else await control.click();
      const pane = box(page, name).getByRole("menu", { name: "Model", exact: true });
      const efforts = box(page, name).getByRole("menu", { name: "Effort", exact: true });
      await expect(pane).toBeVisible();
      await expect(pane.getByRole("menuitemradio", { checked: true })).toHaveCount(1);
      await expect(efforts.getByRole("menuitemradio", { checked: true })).toHaveCount(1);
      expect(typed(relay), "opening the pane types nothing").toBe("");
      await expect(page.getByRole("button", { name: "Back to messages", exact: true })).toHaveCount(0);
      const choice = (setting === "model" ? pane : efforts).getByRole("menuitemradio", { name: option, exact: typeof option === "string" });
      // A tap on another model brings out its efforts, and a second takes it.
      if (width === 390 && setting === "model") await choice.tap();
      if (width === 390) await choice.tap();
      else await choice.click();

      // Assert: the pick reaches the session's harness, the terminal stays out
      // of sight, and the box keeps the draft.
      await expect(pane).toHaveCount(0);
      if (harness === "codex") {
        await expect.poll(() => typed(relay)).toBe(pasted("/model") + "\r");
        relay.say({ type: "screen", asking: true, open: true, working: false, panel: CODEX_MODELS });
        await expect.poll(() => typed(relay)).toBe(pasted("/model") + "\r" + (setting === "model" ? "2" : "1"));
        relay.say({ type: "screen", asking: true, open: true, working: false, panel: CODEX_EFFORTS });
        await expect.poll(() => typed(relay)).toBe(pasted("/model") + "\r" + (setting === "model" ? "22" : "13"));
        await expect(box(page, name).getByRole("group", { name: name + " asks", exact: true }), "Codex's own picker is never drawn").toHaveCount(0);
        relay.say({ type: "screen", asking: false, open: false, working: false });
      } else {
        const command = setting === "model" ? "/model " : harness === "claude" ? "/effort " : "/thinking ";
        await expect.poll(() => typed(relay)).toBe(pasted(command + value) + "\r");
      }
      await expect(page.getByRole("button", { name: "Back to messages", exact: true })).toHaveCount(0);
      await expect(field(page, name)).toHaveValue("my unsent words");
      await expect(control).toContainText(shown);
      // The session's own record then says what it runs, which both views read.
      await page.evaluate((usage) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "usage", data: usage } })), recorded(setting === "model" ? value : session.model, setting === "effort" ? value : session.effort));
      await expect(control).toContainText(shown);
      // The controls stay guarded as before: held typing and an ended
      // terminal disable them, and the draft stays.
      relay.say({ type: "held", reason: "pipeline owns this task" });
      await expect(control).toBeDisabled();
      relay.say({ type: "held", reason: "" });
      await expect(control).toBeEnabled();
      relay.end();
      await expect(control).toBeDisabled();
      await expect(field(page, name)).toHaveValue("my unsent words");
    });
  }
  }
}

test("a setting changed in Classic shows in Modern's controls, from the session's own record", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", { ...PAGE, usage: recorded("claude-opus-5-5", "xhigh") });
  await openGoblin(page);
  const model = box(page).locator(".engine-control-model");
  const effort = box(page).locator(".engine-control .effort-level");
  await expect(model).toHaveText("Opus 5.5");
  await expect(effort).toHaveText("xhigh");

  // Act: the harness's own menu, in Classic, set them; its record says so.
  await page.evaluate((usage) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "usage", data: usage } })), recorded("claude-sonnet-5-5", "low"));

  // Assert
  await expect(model).toHaveText("Sonnet 5.5");
  await expect(effort).toHaveText("low");
});

test("a pane pick gives way once the session's record says something new, and never comes back", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot(), "", { ...PAGE, usage: recorded("claude-opus-5-5", "xhigh") });
  await page.route("**/api/engines", (route) => route.fulfill({ json: CATALOG }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  const effort = box(page).locator(".engine-control .effort-level");
  await box(page).getByRole("button", { name: "Change model", exact: true }).click();
  await box(page).getByRole("menu", { name: "Effort", exact: true }).getByRole("menuitemradio", { name: "max", exact: true }).click();
  await expect(effort).toHaveText("max");
  const record = (usage: object) => page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "usage", data } })), usage);

  // Act: the record says the session runs high, then xhigh again.
  await record(recorded("claude-opus-5-5", "high"));
  await expect(effort).toHaveText("high");
  await record(recorded("claude-opus-5-5", "xhigh"));

  // Assert: the control says what the record says, not the old pick.
  await expect(effort).toHaveText("xhigh");
});

test("typing /effort or /model in the box opens the board's pane and types nothing into the terminal", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot(), "", { ...PAGE, usage: recorded("claude-opus-5-5", "xhigh") });
  await page.route("**/api/engines", (route) => route.fulfill({ json: CATALOG }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await expect(box(page).getByRole("button", { name: "Change model", exact: true })).toBeEnabled();

  for (const [command, title] of [["/effort", "Effort"], ["/model", "Model"]]) {
    // Act
    await field(page).fill(command);
    await field(page).press("Enter");

    // Assert
    await expect(box(page).getByRole("menu", { name: title, exact: true })).toBeVisible();
    await expect(field(page)).toHaveValue("");
    await page.keyboard.press("Escape");
    await expect(box(page).getByRole("menu", { name: title, exact: true })).toHaveCount(0);
    await expect(field(page)).toBeFocused();
  }
  // A value typed after the command sets it at once, as the pane would.
  await field(page).fill("/effort high");
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toBe(pasted("/effort high") + "\r");
  await expect(page.getByRole("button", { name: "Back to messages", exact: true })).toHaveCount(0);
});

test("each effort level wears its own colour, dull at the least, a gradient at max", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot(), "", { ...PAGE, usage: recorded("claude-opus-5-5", "xhigh") });
  await page.route("**/api/engines", (route) => route.fulfill({ json: CATALOG }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await box(page).getByRole("button", { name: "Change model", exact: true }).click();
  const pane = box(page).getByRole("menu", { name: "Effort", exact: true });
  await expect(pane.getByRole("menuitemradio")).toHaveCount(5);

  // Act
  const looks = await pane.locator(".effort-level").evaluateAll((labels) => labels.map((label) => ({ text: label.textContent, color: getComputedStyle(label).color, image: getComputedStyle(label).backgroundImage })));

  // Assert: from the least to the most, every level its own colour, and max
  // a rainbow through its letters; the row shows the same colour.
  expect(looks.map((look) => look.text)).toEqual(["low", "medium", "high", "xhigh", "max"]);
  expect(new Set(looks.slice(0, 4).map((look) => look.color)).size).toBe(4);
  expect(looks[4].image).toContain("linear-gradient");
  expect(looks[4].color).toBe("rgba(0, 0, 0, 0)");
  const row = await box(page).locator(".engine-control .effort-level").evaluate((label) => getComputedStyle(label).color);
  expect(row).toBe(looks[3].color);
});
});
}

test("a refused terminal window leaves the draft and reports the actual refusal", async ({ page }) => {
  const relay = await open(page);
  await page.route("**/api/terminal/open", (route) => route.fulfill({ status: 403, json: { error: "Refresh the board before submitting an action" } }));
  await openGoblin(page);
  await field(page).fill("keep this");
  await (await boxMenu(page)).getByRole("button", { name: "Open in Windows Terminal", exact: true }).click();
  await expect(box(page)).toContainText("Refresh the board before submitting an action");
  await expect(field(page)).toHaveValue("keep this");
  expect(typed(relay)).toBe("");
});

test("a second press on its control closes the pane, and a press outside closes it leaving the keyboard where he pressed", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot(), "", { ...PAGE, usage: recorded("claude-opus-5-5", "xhigh") });
  await page.route("**/api/engines", (route) => route.fulfill({ json: CATALOG }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  const control = box(page).getByRole("button", { name: "Change model", exact: true });
  const pane = box(page).getByRole("menu", { name: "Model", exact: true });
  await control.click();
  await expect(pane.getByRole("menuitemradio", { checked: true })).toBeFocused();

  // Act
  await control.click();

  // Assert
  await expect(pane).toHaveCount(0);
  await expect(control).toHaveAttribute("aria-expanded", "false");

  // Act: a press on the conversation.
  await control.click();
  await expect(pane.getByRole("menuitemradio", { checked: true })).toBeFocused();
  await page.getByRole("log").click({ position: { x: 8, y: 8 } });

  // Assert
  await expect(pane).toHaveCount(0);
  await expect(field(page)).not.toBeFocused();
  expect(typed(relay)).toBe("");
});

test("Escape with the box's pane or style menu open closes it and never stops the working harness", async ({ page }) => {
  // Arrange: the harness works, and its catalog cannot be read, so the pane
  // only offers Retry.
  const relay = await open(page, snapshot(), "", { ...PAGE, usage: recorded("claude-opus-5-5", "xhigh") });
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: true });
  await expect(box(page).getByRole("button", { name: "Stop", exact: true })).toBeVisible();
  await field(page).fill("/model");
  await field(page).press("Enter");
  const pane = box(page).getByRole("menu", { name: "Model", exact: true });
  await expect(pane.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  await expect(pane, "the pane takes the keyboard while it only offers Retry").toBeFocused();

  // Act
  await page.keyboard.press("Escape");

  // Assert
  await expect(pane).toHaveCount(0);
  await expect(field(page)).toBeFocused();

  // Act: the style menu, open while he is back in the box.
  const style = box(page).getByRole("button", { name: "Style: modern", exact: true });
  await style.click();
  await field(page).focus();
  await field(page).press("Escape");

  // Assert
  await expect(style).toHaveAttribute("aria-expanded", "false");
  expect(typed(relay), "no Escape reached the harness").toBe("");
  await field(page).press("Escape");
  await expect.poll(() => typed(relay), "with nothing open, Escape stops it").toBe("\x1b");
});

test("a Codex pick its picker never offered leaves its control showing what the session runs", async ({ page }) => {
  // Arrange
  await page.clock.install();
  const relay = await open(page, snapshot({}, { harness: "codex", model: "gpt-6-astra", effort: "medium" }), "", { ...PAGE, usage: recorded("gpt-6-astra", "medium") });
  await page.route("**/api/engines", (route) => route.fulfill({ json: CATALOG }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  const control = box(page).getByRole("button", { name: "Change model", exact: true });
  await control.click();
  await box(page).getByRole("menu", { name: "Model", exact: true }).getByRole("menuitemradio", { name: /^GPT-6\.1 Sol/ }).click();
  await expect(control).toContainText("GPT-6.1 Sol");
  await expect.poll(() => typed(relay)).toBe(pasted("/model") + "\r");

  // Act: Codex's model list never offers it, and the box gives up.
  relay.say({ type: "screen", asking: true, open: true, working: false, panel: { ...CODEX_MODELS, choices: CODEX_MODELS.choices.slice(0, 1) } });
  await page.clock.fastForward(8_001);

  // Assert
  await expect(box(page)).toContainText("Codex did not offer that choice in its model picker.");
  await expect(control).toContainText("GPT-6 Astra");
});

test("a Codex model typed after /model keeps the session's effort when its catalog lists the model", async ({ page }) => {
  // Arrange: the session runs high, which gpt-6.1-sol offers too, while
  // Codex's reasoning list focuses Medium.
  const relay = await open(page, snapshot({}, { harness: "codex", model: "gpt-6-astra", effort: "high" }), "", { ...PAGE, usage: recorded("gpt-6-astra", "high") });
  await page.route("**/api/engines", (route) => route.fulfill({ json: CATALOG }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await expect(box(page).getByRole("button", { name: "Change model", exact: true })).toBeEnabled();

  // Act
  await field(page).fill("/model gpt-6.1-sol");
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toBe(pasted("/model") + "\r");
  relay.say({ type: "screen", asking: true, open: true, working: false, panel: CODEX_MODELS });
  await expect.poll(() => typed(relay)).toBe(pasted("/model") + "\r2");
  relay.say({ type: "screen", asking: true, open: true, working: false, panel: CODEX_EFFORTS });

  // Assert
  await expect.poll(() => typed(relay)).toBe(pasted("/model") + "\r23");
  relay.say({ type: "screen", asking: false, open: false, working: false });
  const control = box(page).getByRole("button", { name: "Change model", exact: true });
  await expect(control, "a typed value waits for the session's record").toContainText("GPT-6 Astra");
  await page.evaluate((usage) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "usage", data: usage } })), recorded("gpt-6.1-sol", "high"));
  await expect(control).toContainText("GPT-6.1 Sol");
});

test("a value typed after the command shows on its control only once the session's record says it runs it", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot(), "", { ...PAGE, usage: recorded("claude-opus-5-5", "xhigh") });
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await expect(box(page).getByRole("button", { name: "Change model", exact: true })).toBeEnabled();
  const effort = box(page).locator(".engine-control .effort-level");

  // Act: a level Claude Code does not take.
  await field(page).fill("/effort hgih");
  await field(page).press("Enter");

  // Assert: it is sent, and the control keeps what the record says.
  await expect.poll(() => typed(relay)).toBe(pasted("/effort hgih") + "\r");
  await expect(effort).toHaveText("xhigh");

  // A level it takes shows once its record says so.
  await field(page).fill("/effort high");
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toBe(pasted("/effort hgih") + "\r" + pasted("/effort high") + "\r");
  await expect(effort).toHaveText("xhigh");
  await page.evaluate((usage) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "usage", data: usage } })), recorded("claude-opus-5-5", "high"));
  await expect(effort).toHaveText("high");
});

test("a /model or /effort typed while the controls cannot change the session stays in the box and is never queued", async ({ page }) => {
  // Arrange: a review holds the goblin's typing, under which a message queues.
  const relay = await open(page, snapshot(), "", { ...PAGE, usage: recorded("claude-opus-5-5", "xhigh") });
  const actions: unknown[] = [];
  await page.route("**/api/actions", (route) => { actions.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: {} }); });
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  relay.say({ type: "held", reason: "pipeline owns this task" });
  await expect(box(page).getByRole("button", { name: "Change model", exact: true })).toBeDisabled();

  for (const command of ["/model", "/effort high"]) {
    // Act
    await field(page).fill(command);
    await field(page).press("Enter");

    // Assert
    await expect(box(page).getByRole("alert")).toHaveText("The model and effort controls need a connected, unheld session.");
    await expect(field(page)).toHaveValue(command);
  }
  await expect(box(page).getByRole("menu")).toHaveCount(0);
  expect(actions).toEqual([]);
  expect(typed(relay)).toBe("");
});

test("a narrow desktop panel keeps all session controls inside the composer", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("cfo-pane-width", "360");
    localStorage.setItem("cfo-terminal-maximized", "false");
  });
  await open(page, snapshot(), "", { ...PAGE, usage: USAGE });
  await openGoblin(page);
  const bounds = await box(page).locator(".composer-input").evaluate((element) => {
    const edge = element.getBoundingClientRect();
    return { left: edge.left, right: edge.right, controls: [...element.querySelectorAll(".composer-row button")].map((button) => {
      const control = button.getBoundingClientRect();
      return { left: control.left, right: control.right };
    }) };
  });
  expect(bounds.controls.every((control) => control.left >= bounds.left && control.right <= bounds.right)).toBe(true);
});
