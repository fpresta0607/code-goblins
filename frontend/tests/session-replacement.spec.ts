import { expect, test } from "./site";
import { at, card, field, GOBLIN, NAME, open, PAGE, pasted, snapshot, typed } from "./session-board";

test.use({ launchOptions: { args: ["--disable-webgl"] } });

const HISTORY = Array.from({ length: 300 }, (_, index) => "Codex history " + index).join("\r\n") + "\r\n> ready";
const pi = { id: "pi/old", task_id: GOBLIN.id, host_id: GOBLIN.id, role: "goblin", harness: "pi", generation: "pi-1", phase: "working" };
const codex = { ...pi, id: "codex/current", harness: "codex", generation: "codex-2", phase: "started" };
const pending = { id: "pending-one", kind: "message", task_id: GOBLIN.id, status: "queued", text: "keep the queued request", at };

for (const style of ["classic", "modern"] as const) {
  test(`${style} follows an ended Pi root into Codex without reload or losing its draft`, async ({ page }, testInfo) => {
    await page.addInitScript(() => sessionStorage.setItem("cfo-draft:cg-touch", "keep my draft"));
    await page.addInitScript((style) => localStorage.setItem("cfo-chat-style", style), style);
    const initial = snapshot({ sessions: [pi], actions: [pending] }, { harness: "pi", session: pi.id, generation: pi.generation });
    const relay = await open(page, initial, "", PAGE, HISTORY);
    await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    await page.getByRole("button", { name: new RegExp(NAME) }).click();
    await expect.poll(() => relay.addresses.some((address) => new URL(address).searchParams.get("generation") === pi.generation)).toBe(true);
    relay.end();
    if (style === "classic") await expect(page.locator(".terminal-closed")).toContainText("No terminal is running");
    const next = snapshot({ revision: 2, sessions: [{ ...pi, phase: "ended" }, codex], actions: [pending] }, { harness: "codex", session: codex.id, generation: codex.generation, runtime: { state: "working", at } });
    await page.evaluate((detail) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail })), next);

    await expect.poll(() => relay.addresses.some((address) => new URL(address).searchParams.get("generation") === codex.generation)).toBe(true);
    await expect(page.locator(".terminal-closed")).toHaveCount(0);
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
    await expect(page.locator(".context-pane").getByText(pending.text, { exact: true })).toBeVisible();
    await expect(field(page)).toBeInViewport();
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
        const position = () => style === "modern" ? surface.evaluate((element) => element.scrollTop) : rows.textContent();
        await expect(surface).toBeVisible();
        await expect(page.locator(".terminal-view.staged")).toHaveCount(0);
        if (style === "modern") await field(page).fill("draft survives reconnect");
        const latest = await position();
        if (phone) {
          const bounds = (await surface.boundingBox())!;
          const x = bounds.x + bounds.width / 2, y = bounds.y + bounds.height * .3;
          const touch = await page.context().newCDPSession(page);
          await touch.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y }] });
          for (let step = 1; step <= 10; step++) await touch.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x, y: y + bounds.height * .04 * step }] });
          await touch.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
          await touch.detach();
        } else {
          await surface.hover();
          await page.mouse.wheel(0, -460);
        }
        await expect.poll(position).not.toBe(latest);
        const jump = page.getByRole("button", { name: style === "modern" ? "Jump to latest" : "Jump to bottom", exact: true });
        await expect(jump).toBeVisible();
        const anchor = await position();
        relay.say({ type: "unread", unread: true });
        relay.write("\r\nThe Codex turn continues\r\n");
        await expect.poll(position).toBe(anchor);
        relay.say({ type: "held", reason: "pipeline owns this task" });
        await expect(field(page)).toBeInViewport();
        await surface.hover();
        await page.mouse.wheel(0, -45);
        await expect.poll(position).not.toBe(anchor);
        const beforeKey = await position();
        if (style === "modern") await surface.focus();
        else await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
        await page.keyboard.press("Shift+PageUp");
        await expect.poll(position).not.toBe(beforeKey);
        const beforeReconnect = await position();
        relay.drop();
        await expect.poll(() => relay.views).toBe(2);
        await expect(page.locator(".terminal-view.staged")).toHaveCount(0);
        await expect.poll(position).toBe(beforeReconnect);
        if (style === "modern") await expect(field(page)).toHaveValue("draft survives reconnect");
        relay.end();
        await surface.hover();
        await page.mouse.wheel(0, -100);
        await expect.poll(position).not.toBe(beforeReconnect);
        await page.screenshot({ path: testInfo.outputPath(`${style}-${phone ? "phone" : "desktop"}-history.png`) });
      });
    }
  });
}
