import { expect, test } from "./site";
import { box, boxMenu, card, field, GOBLIN, NAME, open, openGoblin, pasted, send, snapshot, typed } from "./session-board";

test.use({
  viewport: { width: 360, height: 800 }, hasTouch: true, isMobile: true,
  permissions: ["microphone"],
  launchOptions: { args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] },
});

for (const who of ["CFO", "goblin"] as const) {
  test(`phone Clear message removes the ${who} multiline draft and keeps queued messages and Speech history`, async ({ page }, testInfo) => {
    // Arrange
    const name = who === "CFO" ? "CFO" : NAME;
    const task = who === "CFO" ? "" : GOBLIN.id;
    const actions: object[] = [];
    const relay = await open(page, snapshot({ actions: [{ id: "q1", kind: "message", task_id: task, text: "Already queued", status: "queued" }] }));
    await page.route("**/api/actions", async (route) => { actions.push(route.request().postDataJSON()); await route.fulfill({ json: {} }); });
    await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: [{ text: "Dictated first line\nDictated second line", at: Date.now() }] } }));
    if (who === "goblin") await openGoblin(page);
    else await page.keyboard.press("Control+Alt+1");
    const composer = box(page, name);
    const input = field(page, name);
    const clear = composer.getByRole("button", { name: "Clear message", exact: true });
    await expect(clear).toBeDisabled();
    await input.fill("Typed first line\nTyped second line\n");
    await (await boxMenu(page, name)).getByRole("button", { name: "Speech history", exact: true }).tap();
    await page.getByRole("dialog", { name: "Speech history" }).getByRole("button", { name: /^Paste into the message:/ }).tap();
    await expect(input).toHaveValue("Typed first line\nTyped second line\nDictated first line Dictated second line");
    const draftKey = await page.evaluate(() => Object.keys(sessionStorage).find((key) => key.startsWith("cfo-draft:")));
    expect(draftKey).toBeTruthy();
    const bounds = await clear.boundingBox();
    expect(bounds?.width).toBeGreaterThanOrEqual(44);
    expect(bounds?.height).toBeGreaterThanOrEqual(44);

    // Act
    relay.say({ type: "screen", asking: false, open: false, working: true, draft: true });
    await clear.tap();

    // Assert
    await expect(input).toHaveValue("");
    await expect(input).toBeFocused();
    await expect(clear).toBeDisabled();
    await expect(clear).toHaveAttribute("title", "Clear this message field only");
    expect(await page.evaluate((key) => sessionStorage.getItem(key!), draftKey)).toBeNull();
    expect(typed(relay)).toBe("");
    expect(relay.submissions).toEqual([]);
    expect(actions).toEqual([]);
    await expect(page.locator('.queued-message[data-queued="q1"]')).toContainText("Already queued");
    await expect(page.getByRole("log", { name: "Messages of " + name, exact: true })).toContainText("Read the brief");
    await composer.screenshot({ path: testInfo.outputPath(`clear-${who}-phone.png`) });
    await (await boxMenu(page, name)).getByRole("button", { name: "Speech history", exact: true }).tap();
    await expect(page.getByRole("dialog", { name: "Speech history" })).toContainText("Dictated first line");
    await page.reload();
    await expect(page.locator(".board-column").first()).toBeVisible();
    if (who === "goblin") await openGoblin(page);
    else await page.keyboard.press("Control+Alt+1");
    await expect(input).toHaveValue("");
    await input.fill("Replacement text");
    await expect(input).toHaveValue("Replacement text");
    expect(typed(relay)).toBe("");
  });

  for (const activity of ["recording", "failed send"] as const) {
    test(`phone Classic Clear message keeps the ${who} replacement field after ${activity}`, async ({ page }) => {
      const name = who === "CFO" ? "CFO" : NAME;
      const task = who === "CFO" ? "" : GOBLIN.id;
      const actions: object[] = [];
      await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
      const relay = await open(page, snapshot({ actions: [{ id: "q1", kind: "message", task_id: task, text: "Already queued", status: "queued" }] }));
      await page.route("**/api/actions", async (route) => { actions.push(route.request().postDataJSON()); await route.fulfill({ json: {} }); });
      if (who === "goblin") await card(page, NAME).tap();
      else await page.keyboard.press("Control+Alt+1");
      await expect.poll(() => relay.addresses.some((address) => who === "goblin" ? new URL(address).searchParams.get("task") === GOBLIN.id : new URL(address).searchParams.has("cfo"))).toBe(true);
      const composer = box(page, name);
      const input = field(page, name);
      const modern = page.locator(".context-pane").getByRole("button", { name: "Switch to Modern chat", exact: true });
      await expect(modern).toBeVisible();
      await expect(composer).toBeHidden();
      await modern.tap();
      await input.fill("Unsent first line\nUnsent second line");
      if (activity === "recording") {
        await composer.getByRole("button", { name: "Dictate", exact: true }).tap();
        await expect(composer.getByRole("button", { name: "Stop dictating", exact: true })).toHaveAttribute("aria-pressed", "true");
      } else {
        relay.end();
        await expect(page.locator(".terminal-foot.box")).toHaveCount(1);
        await send(page, name).tap();
        await expect(composer.getByRole("alert")).toHaveText("Not sent. The terminal is closed.");
      }
      await (await boxMenu(page, name)).getByRole("group", { name: "Chat style", exact: true }).getByRole("button", { name: "Classic", exact: true }).tap();
      await expect(input).toBeVisible();
      await expect(input).toBeFocused();

      await composer.getByRole("button", { name: "Clear message", exact: true }).tap();

      await expect(input).toBeVisible();
      await expect(input).toHaveValue("");
      await expect(input).toBeFocused();
      await expect(composer.getByRole("button", { name: "Clear message", exact: true })).toBeDisabled();
      await expect(composer.getByRole("alert")).toHaveCount(0);
      await expect(composer.getByRole("button", { name: "Dictate", exact: true })).toHaveAttribute("aria-pressed", "false");
      expect(await page.evaluate(() => Object.keys(sessionStorage).filter((key) => key.startsWith("cfo-draft:")))).toEqual([]);
      await page.keyboard.insertText("Replacement text");
      await expect(input).toHaveValue("Replacement text");
      await (await boxMenu(page, name)).getByRole("button", { name: "Style: classic", exact: true }).press("Escape");
      await expect(input).toBeVisible();
      await expect(composer.locator('.queued-message[data-queued="q1"]')).toContainText("Already queued");
      expect(typed(relay)).toBe("");
      expect(relay.submissions).toEqual([]);
      expect(actions).toEqual([]);

      if (activity === "recording") {
        await input.focus();
        await page.keyboard.press("Enter");
        await expect(composer).toBeHidden();
        await expect.poll(() => typed(relay)).toBe(pasted("Replacement text") + "\r");
        expect(relay.submissions).toHaveLength(1);
      } else {
        await modern.focus();
        await expect(composer).toBeHidden();
        await modern.tap();
        await expect(input).toHaveValue("Replacement text");
        await composer.getByRole("button", { name: "Clear message", exact: true }).tap();
        await (await boxMenu(page, name)).getByRole("group", { name: "Chat style", exact: true }).getByRole("button", { name: "Classic", exact: true }).tap();
        await expect(input).toHaveValue("");
        await expect(input).toBeFocused();
      }
    });
  }
}

for (const state of ["recording", "awaiting transcription"] as const) {
  test(`phone Clear message discards dictation while ${state} without restoring late words`, async ({ page }) => {
    // Arrange: a speech service whose late answer ignores cancellation.
    await page.addInitScript(() => {
      const originalFetch = window.fetch;
      window.fetch = (resource, options) => {
        if (resource !== "/api/dictation" || options?.method !== "POST") return originalFetch(resource, options);
        window.dispatchEvent(new Event("speech-requested"));
        return new Promise<Response>((resolve) => window.addEventListener("speech-finished", () => {
          const response = new Response(JSON.stringify({ text: "late dictated words" }), { headers: { "Content-Type": "application/json" } });
          const json = response.json.bind(response);
          response.json = async () => {
            const value: unknown = await json();
            setTimeout(() => document.documentElement.dataset.speechFinished = "true");
            return value;
          };
          resolve(response);
        }, { once: true }));
      };
      window.addEventListener("speech-requested", () => document.documentElement.dataset.speechRequested = "true");
    });
    const relay = await open(page);
    await openGoblin(page);
    if (state === "recording") await field(page).fill("Unsent first line\nUnsent second line");
    await box(page).getByRole("button", { name: "Dictate", exact: true }).tap();
    await expect(box(page).getByRole("button", { name: "Stop dictating", exact: true })).toHaveAttribute("aria-pressed", "true");
    await page.waitForTimeout(1200);
    if (state === "awaiting transcription") {
      await box(page).getByRole("button", { name: "Stop dictating", exact: true }).tap();
      await expect(page.locator("html")).toHaveAttribute("data-speech-requested", "true");
    }

    // Act
    await box(page).getByRole("button", { name: "Clear message", exact: true }).tap();
    await field(page).fill("Replacement");
    await page.evaluate(() => window.dispatchEvent(new Event("speech-finished")));
    if (state === "awaiting transcription") await expect(page.locator("html")).toHaveAttribute("data-speech-finished", "true");

    // Assert
    await expect(box(page).getByRole("button", { name: "Dictate", exact: true })).toHaveAttribute("aria-pressed", "false");
    await expect(field(page)).toHaveValue("Replacement");
    await expect(field(page)).toBeFocused();
    expect(typed(relay)).toBe("");
    expect(relay.submissions).toEqual([]);
  });
}
