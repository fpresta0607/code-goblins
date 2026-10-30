import { expect, test } from "./site";
import { box, field, open, openGoblin, pasted, snapshot, typed } from "./session-board";

for (const { harness, isBracketed } of ["claude", "codex", "pi"].flatMap((harness) => [true, false].map((isBracketed) => ({ harness, isBracketed })))) {
  test(`${harness} Chat sends and answers harness controls before mounting a terminal, bracketed paste ${isBracketed ? "on" : "off"}`, async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({}, { harness }), "", undefined, isBracketed ? "> ready\r\n" : "\x1b[?2004l> ready\r\n");
    const queued: { kind: string; text: string }[] = [];
    await page.route("**/api/actions", (route) => { queued.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: {} }); });
    await openGoblin(page);

    // Act
    await field(page).fill("Read the brief\r\nSecond line\nThird line \x1b[201~ stays in the paste");
    await field(page).press("Enter");

    // Assert
    const text = "Read the brief\rSecond line\rThird line  stays in the paste";
    const expected = isBracketed ? pasted(text) : text;
    await expect.poll(() => relay.typed).toEqual([expected, "\r"]);
    expect(relay.submissions).toHaveLength(1);
    await expect(page.locator(".xterm")).toHaveCount(0);
    await expect(field(page)).toHaveValue("");

    // Act
    await field(page).press("Shift+Tab");
    await expect.poll(() => typed(relay)).toBe(expected + "\r\x1b[Z");
    relay.say({ type: "screen", asking: false, open: false, working: false, draft: true });
    await expect(box(page)).toHaveAttribute("data-queues", "");
    const before = typed(relay);
    await field(page).fill("Keep this separate from the native draft");
    await field(page).press("Enter");

    // Assert
    await expect.poll(() => queued.map((message) => message.text)).toEqual(["Keep this separate from the native draft"]);
    expect(typed(relay)).toBe(before);
    await expect(field(page)).toHaveValue("");
    await expect(page.locator(".xterm")).toHaveCount(0);
  });
}

test("showing a terminal and returning to Chat keep one connection and stop its hidden animation", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  const connections = relay.views;
  await expect(page.locator(".xterm")).toHaveCount(0);

  // Act
  relay.say({ type: "screen", asking: true, open: true, working: false, panel: { rows: ["Continue?"], choices: [{ text: "Yes", number: 1, focused: true }] } });
  await page.getByRole("button", { name: "Show the terminal", exact: true }).click();
  await expect(page.locator(".xterm").first()).toBeVisible();
  await page.getByRole("button", { name: "Back to messages", exact: true }).click();
  relay.say({ type: "screen", asking: false, open: false, working: false });

  // Assert
  await expect(page.locator(".xterm")).toHaveCount(0);
  expect(relay.views).toBe(connections);
  const spinners = await page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running" && (animation as CSSAnimation).animationName === "terminal-spin").length);
  expect(spinners).toBe(0);
  await field(page).fill("Continue after switching back");
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toBe(pasted("Continue after switching back") + "\r");
});

for (const isCfo of [true, false]) {
  for (const completion of ["repaint", "fallback"] as const) {
    test(`${isCfo ? "CFO" : "goblin"} terminal reopens after its first ${completion} completes while hidden`, async ({ page }) => {
      const relay = await open(page, snapshot(), "", undefined, "> ready\r\n", false);
      if (isCfo) await page.keyboard.press("Control+Alt+1");
      else await openGoblin(page);
      await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
      await page.evaluate(() => document.fonts.ready);
      relay.say({ type: "screen", asking: true, open: true, working: false, panel: { rows: ["Continue?"], choices: [{ text: "Yes", number: 1, focused: true }] } });
      const show = page.getByRole("button", { name: "Show the terminal", exact: true });
      await expect(show).toBeVisible();
      const connections = relay.views;
      const now = new Date("2026-10-27T12:00:05Z");
      await page.clock.install({ time: now });
      await page.clock.pauseAt(now);

      await show.click({ force: true });
      await page.clock.runFor(200);
      await expect.poll(() => relay.resizes.length).toBeGreaterThan(0);
      await expect(page.locator(".terminal-view.staged")).toHaveCount(1);
      await page.getByRole("button", { name: "Back to messages", exact: true }).click({ force: true });
      await expect(page.locator(".xterm")).toHaveCount(0);
      if (completion === "repaint") relay.write("First repaint complete\r\n");
      await page.clock.runFor(800);
      const sizes = relay.resizes.length;
      await show.click({ force: true });
      await page.clock.runFor(200);

      const input = page.getByRole("textbox", { name: "Terminal input", exact: true });
      await expect(page.locator(".xterm")).toBeVisible();
      await expect(input).toBeFocused();
      await page.keyboard.type("x");
      await expect.poll(() => typed(relay)).toBe("x");
      expect(relay.resizes).toHaveLength(sizes);
      expect(relay.views).toBe(connections);
    });
  }

  test(`${isCfo ? "CFO" : "goblin"} terminal first opens its replayed screen after the connection closes`, async ({ page }) => {
    const relay = await open(page);
    if (isCfo) await page.keyboard.press("Control+Alt+1");
    else await openGoblin(page);
    await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
    relay.say({ type: "screen", asking: true, open: true, working: false, panel: { rows: ["Continue?"], choices: [{ text: "Yes", number: 1, focused: true }] } });
    const show = page.getByRole("button", { name: "Show the terminal", exact: true });
    await expect(show).toBeVisible();
    const connections = relay.views;

    relay.end();
    await expect(page.locator(".terminal-foot.box")).toHaveText("No terminal is running for this task.");
    await show.click();

    await expect(page.locator(".xterm")).toBeVisible();
    await expect(page.locator(".terminal-view.staged")).toHaveCount(0);
    await expect(page.locator(".terminal-foot.box")).toBeVisible();
    await expect(page.locator(".terminal-connecting")).toHaveCount(0);
    expect(relay.resizes).toEqual([]);
    expect(relay.views).toBe(connections);
  });
}

test("Chat consumes and acknowledges a large native replay without mounting or resizing a terminal", async ({ page }) => {
  // Arrange
  const output = "A line from native history\r\n".repeat(4000);
  const relay = await open(page, snapshot(), "", undefined, output);
  await openGoblin(page);

  // Act
  await field(page).fill("Continue after replay");
  await field(page).press("Enter");

  // Assert
  await expect.poll(() => relay.acknowledged.at(-1)).toBe(Buffer.byteLength("\x1b[?2004h" + output));
  await expect.poll(() => relay.typed).toEqual([pasted("Continue after replay"), "\r"]);
  expect(relay.submissions).toHaveLength(1);
  expect(relay.resizes).toEqual([]);
  await expect(page.locator(".xterm")).toHaveCount(0);
});

test("Chat follows a harness change on the same native connection", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  const connections = relay.views;
  let expected = "";

  for (const [index, harness] of ["codex", "pi", "claude"].entries()) {
    // Act
    const next = snapshot({ revision: index + 2 }, { harness, model: "" });
    await page.evaluate((detail) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail })), next);
    await expect(box(page).getByRole("img", { name: harness === "codex" ? "Codex" : harness === "pi" ? "Pi" : "Claude Code", exact: true })).toBeVisible();
    await field(page).fill("Message to " + harness);
    await field(page).press("Enter");

    // Assert
    expected += pasted("Message to " + harness) + "\r";
    await expect.poll(() => typed(relay)).toBe(expected);
    expect(relay.submissions).toHaveLength(index + 1);
    expect(relay.views).toBe(connections);
    await expect(page.locator(".xterm")).toHaveCount(0);
  }
});
