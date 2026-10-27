import { expect, test } from "./site";
import { box, field, GOBLIN, NAME, open, openGoblin, PAGE, pasted, snapshot, typed } from "./session-board";

const USAGE = { input_tokens: 12000, output_tokens: 800, context_tokens: 24000, context_window: 100000, model: "recorded-model" };

for (const width of [1440, 390]) {
  test(`a branch reset drops abandoned context statistics at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await open(page, snapshot({}, { harness: "pi" }), "", { ...PAGE, usage: USAGE });
    await openGoblin(page);
    await field(page).fill("keep this draft");
    const composer = box(page);
    await expect(composer.getByRole("button", { name: "Context: 24% used", exact: true })).toBeVisible();

    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })), { messages: PAGE.messages.slice(0, 1), more: false });

    const ring = composer.getByRole("button", { name: "Context usage unavailable", exact: true });
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
      await expect(composer.getByRole("button", { name: "Change model", exact: true })).toHaveText(USAGE.model);
      await expect(composer.getByRole("button", { name: "Change effort", exact: true })).toHaveText("Effort");

      const ring = composer.getByRole("button", { name: "Context: 24% used", exact: true });
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
      await composer.getByRole("button", { name: "Speech history", exact: true }).click();
      await page.getByRole("dialog", { name: "Recent messages", exact: true }).getByRole("button", { name: "Paste into the message: from speech", exact: true }).click();
      await expect(field(page, name)).toHaveValue("preserve this draft from speech");

      await composer.getByRole("button", { name: "Open in Windows Terminal", exact: true }).click();
      await expect.poll(() => posted).toEqual([who === "CFO" ? { native: "cfo=cfo-term" } : { native: `task=${GOBLIN.id}&generation=${GOBLIN.generation}` }]);
      await expect(page.locator(".panel-top").getByRole("button", { name: "Open in Windows Terminal", exact: true })).toHaveCount(0);
      await expect(field(page, name)).toHaveValue("preserve this draft from speech");
      expect(typed(relay)).toBe("");
      await page.evaluate((usage) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "usage", data: usage } })), { ...USAGE, context_tokens: 72000, model: "changed-model" });
      await expect(composer.getByRole("button", { name: "Context: 72% used", exact: true })).toBeVisible();
      await expect(composer.getByRole("button", { name: "Change model", exact: true })).toHaveText("changed-model");
      await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, cfo_harness: "codex", cfo_terminal: "new-cfo" }, { harness: "codex", generation: "replacement" }));
      await expect(composer.getByRole("button", { name: "Context: 24% used", exact: true })).toBeVisible();
      await expect.poll(() => relay.addresses.some((address) => address.includes(who === "CFO" ? "cfo=new-cfo" : "generation=replacement"))).toBe(true);
      relay.say({ type: "screen", asking: false, open: false, working: false });
      await composer.getByRole("button", { name: "Open in Windows Terminal", exact: true }).click();
      await expect.poll(() => posted[1]).toEqual(who === "CFO" ? { native: "cfo=new-cfo" } : { native: `task=${GOBLIN.id}&generation=replacement` });
      await expect(field(page, name)).toHaveValue("preserve this draft from speech");
      await expect(composer.getByRole("button", { name: "Change model", exact: true })).toHaveText(USAGE.model);
      await expect(composer.getByRole("button", { name: "Change model", exact: true })).toBeEnabled();
      const controls = await composer.locator(".composer-controls").last().locator("button").evaluateAll((buttons) => buttons.map((button) => {
        const bounds = button.getBoundingClientRect();
        return { top: bounds.top, right: bounds.right };
      }));
      expect(new Set(controls.map((control) => control.top)).size).toBe(1);
      expect(controls.every((control) => control.right <= width)).toBe(true);
      await page.mouse.move(0, 0);
      await page.screenshot({ path: testInfo.outputPath(`session-controls-${who}-${width}.png`) });
      await ring.focus();
      await page.screenshot({ path: testInfo.outputPath(`session-stats-${who}-${width}.png`) });
    });
  }
  });
}

for (const width of [1440, 390]) {
test.describe(`${width}px native settings`, () => {
test.use({ viewport: { width, height: 900 }, hasTouch: width === 390 });
for (const harness of ["codex", "claude", "pi"]) {
  for (const who of harness === "codex" ? ["CFO", "goblin"] : ["goblin"]) {
  for (const setting of ["model", "effort"] as const) {
    test(`${harness} ${who} ${setting} opens its own guarded picker and keeps the draft`, async ({ page }) => {
      const relay = await open(page, snapshot({ cfo_harness: harness }, { harness }));
      if (who === "CFO") await page.keyboard.press("Control+Alt+1");
      else await openGoblin(page);
      const name = who === "CFO" ? "CFO" : NAME;
      await field(page, name).fill("my unsent words");
      await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
      relay.say({ type: "screen", asking: false, open: false, working: false });
      const control = box(page, name).getByRole("button", { name: setting === "model" ? "Change model" : "Change effort", exact: true });
      await expect(control).toBeEnabled();
      if (width === 390) await control.tap();
      else await control.click();
      const command = setting === "model" || harness === "codex" ? "/model" : harness === "claude" ? "/effort" : "/thinking";
      await expect.poll(() => typed(relay)).toBe(pasted(command) + "\r");
      // The picker opens in the chat as a list, and a numbered choice there
      // presses its digit, which the harness takes as that choice at once.
      relay.say({ type: "screen", asking: true, open: true, working: false, panel: { rows: ["Select model"], choices: [{ text: "Default", number: 1, focused: true }, { text: "Other", number: 2 }], hints: ["Enter to confirm · Esc to exit"] } });
      const picker = box(page, name).getByRole("group", { name: name + " asks", exact: true });
      const other = picker.getByRole("button", { name: "Other", exact: true });
      if (width === 390) await other.tap();
      else await other.click();
      await expect.poll(() => typed(relay)).toBe(pasted(command) + "\r2");
      await expect(field(page, name)).toHaveValue("my unsent words");
      await expect(control).toBeDisabled();
      relay.say({ type: "screen", asking: false, open: false, working: true });
      await expect(control).toBeEnabled();
      relay.say({ type: "screen", asking: false, open: false, working: false });
      relay.say({ type: "held", reason: "pipeline owns this task" });
      await expect(control).toBeDisabled();
      relay.say({ type: "held", reason: "" });
      await expect(control).toBeEnabled();
      const views = relay.views;
      relay.drop();
      await expect(control).toBeDisabled();
      await expect.poll(() => relay.views).toBeGreaterThan(views);
      relay.say({ type: "screen", asking: false, open: false, working: false });
      await expect(control).toBeEnabled();
      relay.end();
      await expect(control).toBeDisabled();
      await expect(field(page, name)).toHaveValue("my unsent words");
      expect(typed(relay)).toBe(pasted(command) + "\r2");
    });
  }
  }
}
});
}

test("a refused terminal window leaves the draft and reports the actual refusal", async ({ page }) => {
  const relay = await open(page);
  await page.route("**/api/terminal/open", (route) => route.fulfill({ status: 403, json: { error: "Refresh the board before submitting an action" } }));
  await openGoblin(page);
  await field(page).fill("keep this");
  await box(page).getByRole("button", { name: "Open in Windows Terminal", exact: true }).click();
  await expect(box(page)).toContainText("Refresh the board before submitting an action");
  await expect(field(page)).toHaveValue("keep this");
  expect(typed(relay)).toBe("");
});

test("a narrow desktop panel keeps all session controls inside the composer", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("cfo-pane-width", "360");
    localStorage.setItem("cfo-terminal-maximized", "false");
  });
  await open(page, snapshot(), "", { ...PAGE, usage: USAGE });
  await openGoblin(page);
  const bounds = await box(page).evaluate((element) => {
    const edge = element.getBoundingClientRect();
    return { left: edge.left, right: edge.right, controls: [...element.querySelectorAll(".composer-tools button")].map((button) => {
      const control = button.getBoundingClientRect();
      return { left: control.left, right: control.right };
    }) };
  });
  expect(bounds.controls.every((control) => control.left >= bounds.left && control.right <= bounds.right)).toBe(true);
});
