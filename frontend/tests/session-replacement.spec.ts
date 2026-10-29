import { expect, test } from "./site";
import { at, box, card, field, GOBLIN, NAME, open, PAGE, pasted, snapshot, typed } from "./session-board";

test.use({ launchOptions: { args: ["--disable-webgl"] } });

const HISTORY = Array.from({ length: 300 }, (_, index) => "Codex history " + index).join("\r\n") + "\r\n> ready";
const pi = { id: "pi/old", task_id: GOBLIN.id, host_id: GOBLIN.id, role: "goblin", harness: "pi", generation: "pi-1", phase: "working" };
const codex = { ...pi, id: "codex/current", harness: "codex", generation: "codex-2", phase: "started" };
const pending = { id: "pending-one", kind: "message", task_id: GOBLIN.id, status: "queued", text: "keep the queued request", at };

for (const state of ["held", "reconnecting", "ended"] as const) {
  for (const input of ["wheel", "keyboard", "touch"] as const) {
    test(`Classic full-screen Codex opens readable history with ${input} while ${state}`, async ({ page }, testInfo) => {
      if (input === "touch") await page.setViewportSize({ width: 390, height: 844 });
      await page.addInitScript(() => {
        localStorage.setItem("cfo-chat-style", "classic");
        sessionStorage.setItem("cfo-draft:cg-touch", "keep my draft");
      });
      const messages = { messages: Array.from({ length: 80 }, (_, seq) => ({ ...PAGE.messages[0], id: "m" + seq, seq, text: "History " + seq + " " + "readable text ".repeat(15) })), more: false };
      const relay = await open(page, snapshot({ actions: [pending] }, { harness: "codex" }), "", messages, "\x1b[?1049h" + HISTORY);
      await card(page, NAME).click();
      const terminal = page.locator(".terminal-view:not(.staged) > .xterm .xterm-screen");
      await expect(terminal).toBeVisible();
      if (state === "held") {
        relay.say({ type: "held", reason: "pipeline owns this task" });
        // Classic's terminal says the hold itself, with no box beside it.
        await expect(page.locator(".context-pane").getByText("Typing is paused.", { exact: true })).toBeVisible();
      } else if (state === "ended") {
        relay.end();
        await expect(page.locator(".terminal-foot.box")).toBeVisible();
      } else relay.drop();
      if (input === "keyboard") await page.getByRole("textbox", { name: "Terminal input", exact: true }).press("Shift+PageUp");
      // The wheel goes over the terminal's top rows, and the history is what
      // the wheel asks for.
      else if (input === "wheel") { const bounds = (await terminal.boundingBox())!; await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + Math.min(60, bounds.height / 4)); await page.mouse.wheel(0, -45); }
      else {
        const touch = await page.context().newCDPSession(page);
        await touch.send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 1 });
        const bounds = (await terminal.boundingBox())!;
        const x = bounds.x + bounds.width / 2, y = bounds.y + bounds.height * .3;
        await touch.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y }] });
        await touch.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x, y: y + 100 }] });
        await touch.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
        await touch.detach();
      }
      const conversation = page.locator(".messages-view:not([hidden]) .conversation");
      await expect(conversation).toBeVisible();
      await expect(field(page)).toHaveValue("keep my draft");
      await expect(page.locator(".messages-view:not([hidden]) .conversation-queue").getByText(pending.text, { exact: true })).toBeVisible();
      // The queue pinned above the box already lists what waits, so the box does not repeat it.
      await expect(box(page, NAME).locator(".composer-sent-text")).toHaveCount(0);
      const before = await conversation.evaluate((element) => element.scrollTop);
      await conversation.focus();
      await page.keyboard.press("Shift+PageUp");
      await expect.poll(() => conversation.evaluate((element) => element.scrollTop)).toBeLessThan(before);
      expect(typed(relay)).toBe("");
      // Reading back opens the conversation and leaves the saved Classic alone:
      // only Back to messages saves Modern (#1584).
      expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style"))).toBe("classic");
      await page.screenshot({ path: testInfo.outputPath(`codex-${state}-${input}-history.png`) });
    });
  }
}

// xterm's scrollbar takes a wheel first whenever it has room to scroll, as
// for a moment after an ended terminal's box shrinks under its closed line,
// and xterm then never asks the board's wheel handler. The board decides
// first, so the wheel still opens the history.
test("Classic full-screen Codex opens readable history with wheel while ended though xterm's scrollbar would take the wheel", async ({ page }) => {
  // Arrange
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
  const relay = await open(page, snapshot({}, { harness: "codex" }), "", PAGE, "\x1b[?1049h" + HISTORY);
  await card(page, NAME).click();
  const terminal = page.locator(".terminal-view:not(.staged) > .xterm .xterm-screen");
  await expect(terminal).toBeVisible();
  relay.end();
  await expect(page.locator(".terminal-foot.box")).toBeVisible();
  await page.locator(".terminal-view:not(.staged) .xterm-scrollable-element").evaluate((scrollbar) => scrollbar.addEventListener("wheel", (event) => { event.preventDefault(); event.stopPropagation(); }));

  // Act
  await terminal.hover();
  await page.mouse.wheel(0, -45);

  // Assert: the history opens and the saved Classic stays, since only Back
  // to messages saves Modern (#1584).
  await expect(page.locator(".messages-view:not([hidden]) .conversation")).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style"))).toBe("classic");
});

// Reading back out of Classic shows the conversation and its box while the
// saved style stays Classic, so Classic chosen in that box must bring the
// terminal back: the read-back outlived the choice, and the view stayed as it
// was (the Overlord, 2026-10-28: "speech bubble doesnt chagne the screen").
test("Classic chosen in the box while reading back out of Classic returns the terminal at once", async ({ page }) => {
  // Arrange
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
  const relay = await open(page, snapshot({}, { harness: "codex" }), "", PAGE, "[?1049h" + HISTORY);
  await card(page, NAME).click();
  const terminal = page.locator(".terminal-view:not(.staged) > .xterm .xterm-screen");
  await expect(terminal).toBeVisible();
  relay.end();
  await expect(page.locator(".terminal-foot.box")).toBeVisible();
  await terminal.hover();
  await page.mouse.wheel(0, -45);
  const conversation = page.locator(".messages-view:not([hidden]) .conversation");
  await expect(conversation).toBeVisible();

  // Act
  await box(page, NAME).getByRole("button", { name: "Style: classic", exact: true }).click();
  await page.getByRole("group", { name: "Chat style", exact: true }).getByRole("button", { name: "Classic", exact: true }).click();

  // Assert
  await expect(terminal).toBeVisible();
  await expect(conversation).toHaveCount(0);
  await expect(box(page, NAME)).toBeHidden();
  expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style"))).toBe("classic");
});

// The right button held while the wheel turns zooms the text, even over a
// full-screen program that a wheel up alone would leave for its history.
test("Classic full-screen Codex zooms on a right-button wheel up while ended and keeps its terminal", async ({ page }) => {
  // Arrange
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
  const relay = await open(page, snapshot({}, { harness: "codex" }), "", PAGE, "\x1b[?1049h" + HISTORY);
  await card(page, NAME).click();
  const terminal = page.locator(".terminal-view:not(.staged) > .xterm .xterm-screen");
  await expect(terminal).toBeVisible();
  relay.end();
  await expect(page.locator(".terminal-foot.box")).toBeVisible();
  const keptSize = () => page.evaluate(() => localStorage.getItem("cfo-terminal-font-size"));
  const before = await keptSize();

  // Act
  await terminal.hover();
  await page.mouse.down({ button: "right" });
  await page.mouse.wheel(0, -100);
  await page.mouse.up({ button: "right" });

  // Assert
  await expect.poll(keptSize).not.toBe(before);
  await expect(page.locator(".messages-view:not([hidden])")).toHaveCount(0);
  expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style"))).toBe("classic");
});

for (const style of ["classic", "modern"] as const) {
  test(`${style} follows an ended CFO host into Codex without reload or losing its draft`, async ({ page }) => {
    await page.addInitScript((style) => {
      localStorage.setItem("cfo-chat-style", style);
      sessionStorage.setItem("cfo-draft:cfo", "keep the CFO draft");
    }, style);
    const oldSince = "2026-10-22T09:00:00Z", newSince = "2026-10-22T10:00:00Z";
    const cfoPending = { ...pending, task_id: "" };
    const relay = await open(page, snapshot({ cfo_harness: "pi", cfo_terminal_since: oldSince, actions: [cfoPending] }), "", PAGE, HISTORY);
    await page.keyboard.press("Control+Alt+1");
    await expect.poll(() => relay.addresses.some((address) => new URL(address).searchParams.get("since") === oldSince)).toBe(true);
    relay.end();

    await page.evaluate((detail) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail })), snapshot({ revision: 2, cfo_harness: "codex", cfo_terminal_since: newSince, actions: [cfoPending] }));

    await expect.poll(() => relay.addresses.some((address) => new URL(address).searchParams.get("since") === newSince)).toBe(true);
    await expect(page.locator(".terminal-foot.box")).toHaveCount(0);
    if (style === "modern") {
      await expect(field(page, "CFO")).toHaveValue("keep the CFO draft");
      await expect(page.locator(".messages-view:not([hidden]) .conversation-queue").getByText(pending.text, { exact: true })).toBeVisible();
      // The queue pinned above the box already lists what waits, so the box does not repeat it.
      await expect(box(page, "CFO").locator(".composer-sent-text")).toHaveCount(0);
      await field(page, "CFO").press("Enter");
      await expect.poll(() => typed(relay)).toContain(pasted("keep the CFO draft") + "\r");
    } else {
      relay.write("\r\nThe CFO keeps working in Codex\r\n");
      await expect(page.locator(".deck-slot:not([hidden]) .terminal-view:not(.staged) > .xterm .xterm-rows")).toContainText("The CFO keeps working in Codex");
      await page.getByRole("textbox", { name: "Terminal input", exact: true }).press("Shift+Tab");
      await expect.poll(() => typed(relay)).toBe("\x1b[Z");
      expect(await page.evaluate(() => sessionStorage.getItem("cfo-draft:cfo"))).toBe("keep the CFO draft");
    }
    expect(relay.inputs.every((input) => new URL(input.address).searchParams.get("since") === newSince)).toBe(true);
  });

  test(`${style} follows an ended Pi root into Codex without reload or losing its draft`, async ({ page }, testInfo) => {
    await page.addInitScript(() => sessionStorage.setItem("cfo-draft:cg-touch", "keep my draft"));
    await page.addInitScript((style) => localStorage.setItem("cfo-chat-style", style), style);
    const initial = snapshot({ sessions: [pi], actions: [pending] }, { harness: "pi", session: pi.id, generation: pi.generation });
    const relay = await open(page, initial, "", PAGE, HISTORY);
    await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    await page.getByRole("button", { name: new RegExp(NAME) }).click();
    await expect.poll(() => relay.addresses.some((address) => new URL(address).searchParams.get("generation") === pi.generation)).toBe(true);
    await expect(page.locator(".deck-slot:not([hidden]) .terminal-view:not(.staged)")).toHaveCount(1);
    relay.end();
    if (style === "classic") await expect(page.getByText("No terminal is running for this task.", { exact: true })).toBeVisible();
    const next = snapshot({ revision: 2, sessions: [{ ...pi, phase: "ended" }, codex], actions: [pending] }, { harness: "codex", session: codex.id, generation: codex.generation, runtime: { state: "working", at } });
    await page.evaluate((detail) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail })), next);

    await expect.poll(() => relay.addresses.some((address) => new URL(address).searchParams.get("generation") === codex.generation)).toBe(true);
    await expect(page.locator(".terminal-foot.box")).toHaveCount(0);
    await expect(page.getByText("No terminal is running for this task.", { exact: true })).toHaveCount(0);
    relay.write("\r\nCodex is still progressing\r\n");
    if (style === "modern") {
      await expect(field(page)).toHaveValue("keep my draft");
      await field(page).press("Enter");
      await expect.poll(() => typed(relay)).toContain(pasted("keep my draft") + "\r");
    } else {
      await expect(page.locator(".deck-slot:not([hidden]) .terminal-view:not(.staged) > .xterm .xterm-rows")).toContainText("Codex is still progressing");
      await page.getByRole("textbox", { name: "Terminal input", exact: true }).press("Shift+Tab");
      await expect.poll(() => typed(relay)).toBe("\x1b[Z");
      expect(await page.evaluate(() => sessionStorage.getItem("cfo-draft:cg-touch"))).toBe("keep my draft");
    }
    expect(relay.inputs.every((input) => new URL(input.address).searchParams.get("generation") === codex.generation)).toBe(true);
    relay.say({ type: "held", reason: "pipeline owns this task" });
    // Modern keeps its conversation and the queue pinned above its box; Classic keeps the
    // terminal alone, which says typing is paused.
    if (style === "modern") {
      await expect(page.locator(".messages-view:not([hidden]) .conversation-queue").getByText(pending.text, { exact: true })).toBeVisible();
      await expect(field(page)).toBeInViewport();
    } else await expect(page.locator(".context-pane").getByText("Typing is paused.", { exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`${style}-codex-replaced-pi.png`) });
  });
}

for (const phone of [false, true]) {
  test.describe(phone ? "phone" : "desktop", () => {
    test.use({ viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 1000 }, hasTouch: phone });
    for (const style of ["classic", "modern"] as const) {
      test(`${style} history scrolls while busy, held, reconnected and ended`, async ({ page }, testInfo) => {
        await page.addInitScript((style) => localStorage.setItem("cfo-chat-style", style), style);
        const messages = { messages: Array.from({ length: 80 }, (_, seq) => ({ ...PAGE.messages[0], id: "m" + seq, seq, text: "History " + seq + " " + "readable text ".repeat(15) })), more: false };
        const relay = await open(page, snapshot({}, { harness: "codex", runtime: { state: "working", at } }), "", messages, HISTORY);
        await card(page, NAME).click();
        const surface = page.locator(style === "modern" ? ".conversation" : ".terminal-view:not(.staged) > .xterm .xterm-screen");
        const rows = page.locator(".terminal-view:not(.staged) > .xterm .xterm-rows > div").first();
        const position = async () => style === "modern" ? surface.evaluate((element) => element.scrollTop) : rows.textContent();
        await expect(surface).toBeVisible();
        await expect(page.locator(".terminal-view.staged")).toHaveCount(0);
        if (style === "modern") await field(page).fill("draft survives reconnect");
        const scroll = async (distance: number) => {
          if (!phone) {
            const bounds = (await surface.boundingBox())!;
            await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + Math.min(60, bounds.height / 4));
            await page.mouse.wheel(0, -distance);
            return;
          }
          const bounds = (await surface.boundingBox())!;
          const x = bounds.x + bounds.width / 2, y = bounds.y + bounds.height * .3;
          const touch = await page.context().newCDPSession(page);
          await touch.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y }] });
          for (let step = 1; step <= 10; step++) {
            await touch.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x, y: y + Math.min(distance, bounds.height * .4) * step / 10 }] });
            await page.waitForTimeout(16);
          }
          await page.waitForTimeout(100);
          await touch.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
          await touch.detach();
        };
        const latest = await position();
        await scroll(460);
        await expect.poll(position).not.toBe(latest);
        const jump = page.getByRole("button", { name: style === "modern" ? "Jump to latest" : "Jump to bottom", exact: true });
        await expect(jump).toBeVisible();
        const anchor = await position();
        relay.say({ type: "unread", unread: true });
        relay.write("\r\nThe Codex turn continues\r\n");
        await expect.poll(position).toBe(anchor);
        const asked = relay.resizes.length;
        relay.say({ type: "held", reason: "pipeline owns this task" });
        if (style === "modern") await expect(field(page)).toBeInViewport();
        else {
          // Classic's note takes a row or two from the terminal, which asks
          // the pane for the rows left and keeps its last line, moving its
          // first by as much as a short swipe; the swipe counts from where
          // that leaves the history.
          await expect(page.locator(".context-pane").getByText("Typing is paused.", { exact: true })).toBeVisible();
          await expect.poll(() => relay.resizes.length).toBeGreaterThan(asked);
        }
        let held: unknown;
        await expect.poll(async () => {
          const prior = held;
          held = await position();
          return held === prior;
        }).toBe(true);
        await scroll(45);
        await expect.poll(position).not.toBe(held);
        const beforeKey = await position();
        if (style === "modern") await surface.focus();
        else await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
        await page.keyboard.press("Shift+PageUp");
        await expect.poll(position).not.toBe(beforeKey);
        // The key's scroll glides to its stop, so where it stops is read once
        // it has, as a held view's is above.
        let beforeReconnect: unknown;
        await expect.poll(async () => {
          const prior = beforeReconnect;
          beforeReconnect = await position();
          return beforeReconnect === prior;
        }).toBe(true);
        relay.drop();
        await expect.poll(() => relay.views).toBe(2);
        await expect(page.locator(".terminal-view.staged")).toHaveCount(0);
        await expect.poll(position).toBe(beforeReconnect);
        if (style === "modern") await expect(field(page)).toHaveValue("draft survives reconnect");
        relay.end();
        await scroll(100);
        await expect.poll(position).not.toBe(beforeReconnect);
        await page.screenshot({ path: testInfo.outputPath(`${style}-${phone ? "phone" : "desktop"}-history.png`) });
      });
    }
  });
}
