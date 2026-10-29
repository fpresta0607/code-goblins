import { expect, test } from "./site";
import { box, card, field, GOBLIN, NAME, open, openGoblin, pasted, send, snapshot, typed } from "./session-board";

test.use({ permissions: ["microphone"], launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] } });

for (const style of ["modern"]) {
  for (const width of [1440, 390]) {
    test.describe(`${style} at ${width}px`, () => {
      test.use({ viewport: { width, height: 900 }, hasTouch: width === 390 });
      test(`${style} keeps held queue status compact and send failures visible at ${width}px`, async ({ page }, testInfo) => {
        test.slow();
        // Arrange
        await page.setViewportSize({ width, height: 900 });
        await page.addInitScript((value) => localStorage.setItem("cfo-chat-style", value), style);
        const relay = await open(page);
        await card(page, NAME).click();
        await expect.poll(() => relay.views).toBeGreaterThan(0);
        await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
        relay.say({ type: "held", reason: "pipeline owns this task; use cfo pipeline respond" });
        await expect(field(page)).toBeVisible();
        const draft = "Keep this draft\n" + "A long message that stays readable while waiting. ".repeat(40);
        await field(page).fill(draft);
        await page.route("**/api/composer/file**", (route) => route.fulfill({ status: 201, json: { path: "C:/session-files/design.txt" } }));
        await box(page).locator('input[type="file"]').setInputFiles({ name: "design.txt", mimeType: "text/plain", buffer: Buffer.from("Keep this attachment") });
        // What Send sends: his words, then the kept file's path on its own line.
        const sentText = draft.trimEnd() + "\nC:/session-files/design.txt";
        await expect(box(page).getByRole("list", { name: "Attached files" })).toContainText("design.txt");
        await expect(field(page)).toHaveValue(draft);
        const before = typed(relay);
        const requests: unknown[] = [];
        await page.route("**/api/actions", (route) => {
          requests.push(route.request().postDataJSON());
          return route.fulfill({ status: 503, json: { error: "The message could not be queued. Try again." } });
        });
        const surface = testInfo.outputPath("review-queue-surface.png");
        await page.screenshot({ path: surface });
        await testInfo.attach("review-queue-surface", { path: surface, contentType: "image/png" });

        // Act and Assert: routine waiting is shown once, beside its one input.
        await expect(page.locator(".terminal-foot.line:visible")).toHaveCount(0);
        await expect(box(page)).toHaveAttribute("data-queues", "");
        expect(await box(page).locator(".composer-banner").count()).toBe(0);
        await expect(page.locator(".voice-bubble:visible")).toHaveCount(1);
        await expect(send(page)).toBeInViewport();

        // Act: a genuine failure must take priority over the hold.
        await send(page).click();

        // Assert
        await expect(box(page).getByRole("alert")).toContainText("The message could not be queued. Try again.");
        await expect(field(page)).toHaveValue(draft);
        expect(typed(relay)).toBe(before);
        relay.say({ type: "held", reason: "pipeline: native custody status check failed" });
        await expect(box(page).getByRole("alert")).toContainText("The message could not be queued. Try again.");
        await testInfo.attach("held-send-failure", { body: await page.screenshot(), contentType: "image/png" });

        // Act: retry keeps the same submission identity and clears the error.
        await page.route("**/api/actions", (route) => {
          requests.push(route.request().postDataJSON());
          return route.fulfill({ status: 202, json: {} });
        });
        await send(page).click();

        // Assert
        await expect(field(page)).toHaveValue("");
        await expect(field(page)).toBeEnabled();
        expect(requests).toHaveLength(2);
        expect(requests[1]).toEqual(requests[0]);
        expect(requests[0]).toMatchObject({ text: sentText });
        expect(await box(page).getByRole("alert").count()).toBe(0);
        expect(await box(page).locator(".composer-banner").count()).toBe(0);
        expect(typed(relay)).toBe(before);

        // Act: the durable entry remains after the hold changes.
        // The board carries it under the id the box sent it with.
        const sent = (requests[0] as { id: string }).id;
        await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: data })), snapshot({ revision: 2, actions: [{ id: sent, kind: "message", task_id: GOBLIN.id, text: sentText, status: "queued", message: "Waiting until the session takes input", can_send_now: false }] }));
        await expect(page.locator(".queued-message:visible")).toHaveCount(1);
        const queueSurface = testInfo.outputPath("queue-with-long-message.png");
        await page.screenshot({ path: queueSurface });
        await testInfo.attach("queue-with-long-message", { path: queueSurface, contentType: "image/png" });
        await expect(send(page)).toBeInViewport();
        await expect(field(page)).toBeInViewport();
        await field(page).fill("another draft");
        relay.say({ type: "held", reason: "" });
        await expect(field(page)).toHaveValue("another draft");
        await expect(page.locator(".queued-message:visible")).toHaveCount(1);
        expect(typed(relay)).toBe(before);
      });
      test(`${style} gives a held send failure priority at ${width}px`, async ({ page }, testInfo) => {
        // Arrange
        await page.addInitScript((value) => localStorage.setItem("cfo-chat-style", value), style);
        const relay = await open(page);
        await card(page, NAME).click();
        await expect.poll(() => relay.views).toBeGreaterThan(0);
        relay.say({ type: "held", reason: "pipeline owns this task" });
        await expect(box(page)).toHaveAttribute("data-queues", "");
        await field(page).fill("Preserve this failed message");
        await page.route("**/api/actions", (route) => route.fulfill({ status: 503, json: { error: "The message could not be queued. Try again." } }));

        // Act
        await send(page).click();
        await expect(send(page)).toBeEnabled();
        const surface = testInfo.outputPath("held-failed-send-surface.png");
        await page.screenshot({ path: surface });
        await testInfo.attach("held-failed-send-surface", { path: surface, contentType: "image/png" });

        // Assert
        await expect(box(page).getByRole("alert")).toContainText("The message could not be queued. Try again.");
        await expect(field(page)).toHaveValue("Preserve this failed message");
      });
      test(`${style} preserves composing and queue safety through busy, reconnecting and ended states at ${width}px`, async ({ page }, testInfo) => {
        test.slow();
        // Arrange
        await page.addInitScript((value) => localStorage.setItem("cfo-chat-style", value), style);
        const relay = await open(page);
        await card(page, NAME).click();
        await expect.poll(() => relay.views).toBeGreaterThan(0);
        const requests: unknown[] = [];
        await page.route("**/api/actions", (route) => {
          requests.push(route.request().postDataJSON());
          return route.fulfill({ status: 202, json: {} });
        });
        const before = typed(relay);

        // Act: a busy session remains readable and accepts durable messages.
        relay.say({ type: "unread", unread: true });
        await expect(field(page)).toBeVisible();
        await field(page).fill("Read this after the current turn");
        await expect(page.getByText("Program busy. Typed keys are waiting.").filter({ visible: true })).toHaveCount(0);
        await expect(box(page)).toHaveAttribute("data-queues", "");
        expect(await box(page).locator(".composer-banner").count()).toBe(0);
        await send(page).click();
        await expect(field(page)).toHaveValue("");
        expect(requests).toHaveLength(1);
        expect(typed(relay)).toBe(before);

        // Act: a relay that has not come back keeps composing, and a Send is
        // queued rather than typed into a view with no screen.
        await field(page).fill("Keep this through the reconnect");
        relay.say({ type: "unread", unread: false });
        relay.refuse();
        relay.drop();
        await expect(page.locator(".terminal-corner.warning")).toHaveCount(1);
        await expect(field(page)).toBeVisible();
        await expect(field(page)).toHaveValue("Keep this through the reconnect");
        await expect(box(page)).toHaveAttribute("data-queues", "");
        expect(await box(page).locator(".composer-banner").count()).toBe(0);
        await send(page).click();
        await expect(field(page)).toHaveValue("");
        expect(requests).toHaveLength(2);
        expect(typed(relay)).toBe(before);

        // Act: the relay comes back, and after reconnecting an ended session
        // keeps an unsent draft.
        relay.accept();
        const reconnect = page.locator(".terminal-foot.box").getByRole("button", { name: "Reconnect", exact: true });
        if (await reconnect.count()) await reconnect.click();
        await field(page).focus();
        await expect(page.locator(".terminal-corner.warning")).toHaveCount(0);
        await expect(field(page)).toBeFocused();
        await page.keyboard.type("still in the draft");
        await expect(field(page)).toHaveValue("still in the draft");
        expect(typed(relay)).toBe(before);
        await field(page).fill("Keep this when the terminal ends");
        relay.end();
        await expect(page.locator(".terminal-foot.box")).toHaveCount(1);
        await send(page).click();

        // Assert
        await expect(box(page).getByRole("alert")).toHaveText("Not sent. The terminal is closed.");
        await expect(field(page)).toHaveValue("Keep this when the terminal ends");
        await expect(send(page)).toBeInViewport();
        expect(requests).toHaveLength(2);
        expect(typed(relay)).toBe(before);
        await testInfo.attach("ended-draft-recovery", { body: await page.screenshot(), contentType: "image/png" });
      });
      test(`${style} retains delayed sends and uploads when a hold clears at ${width}px`, async ({ page }) => {
        // Arrange
        await page.addInitScript((value) => localStorage.setItem("cfo-chat-style", value), style);
        const relay = await open(page);
        await card(page, NAME).click();
        await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
        const before = typed(relay);

        for (const status of [503, 202]) {
          relay.say({ type: "held", reason: "pipeline owns this task" });
          const draft = "Keep this pending message " + status;
          await field(page).fill(draft);
          await expect(box(page)).toHaveAttribute("data-queues", "");
          let isPending = false;
          let submission: unknown;
          let complete: () => Promise<void> = async () => { throw new Error("No pending send"); };
          await page.route("**/api/actions", (route) => {
            isPending = true;
            submission = route.request().postDataJSON();
            complete = () => route.fulfill({ status, json: status === 503 ? { error: "Queue unavailable. Try again." } : {} });
          });

          // Act: the terminal becomes ready before Send returns.
          await send(page).click();
          await expect.poll(() => isPending).toBe(true);
          relay.say({ type: "held", reason: "" });
          await expect(field(page)).toBeVisible();
          await complete();

          // Assert: the same input owns the response after changing surfaces.
          if (status === 503) {
            await expect(box(page).getByRole("alert")).toContainText("Queue unavailable. Try again.");
            await expect(field(page)).toHaveValue(draft);
            let retry: unknown;
            await page.route("**/api/actions", (route) => {
              retry = route.request().postDataJSON();
              return route.fulfill({ status: 202, json: {} });
            });
            await send(page).click();
            await expect(field(page)).toHaveValue("");
            expect(retry).toEqual(submission);
          } else {
            await expect(field(page)).toHaveValue("");
            await expect(field(page)).toBeEnabled();
            expect(await box(page).getByRole("alert").count()).toBe(0);
          }
          expect(typed(relay)).toBe(before);
        }

        // Act: an attachment arrives after the held surface becomes ready.
        relay.say({ type: "held", reason: "pipeline owns this task" });
        await field(page).fill("Keep the attachment");
        let isUploading = false;
        let upload: () => Promise<void> = async () => { throw new Error("No pending upload"); };
        await page.route("**/api/composer/file**", (route) => {
          isUploading = true;
          upload = () => route.fulfill({ status: 201, json: { path: "C:/session-files/pending.txt" } });
        });
        await box(page).locator('input[type="file"]').setInputFiles({ name: "pending.txt", mimeType: "text/plain", buffer: Buffer.from("pending") });
        await expect.poll(() => isUploading).toBe(true);
        relay.say({ type: "held", reason: "" });
        await expect(field(page)).toBeVisible();
        await upload();

        // Assert
        await expect(field(page)).toHaveValue("Keep the attachment");
        await expect(box(page).getByRole("list", { name: "Attached files" })).toContainText("pending.txt");
        expect(typed(relay)).toBe(before);
      });
      test(`${style} keeps recording controls and terminal output usable through release at ${width}px`, async ({ page }) => {
        // Arrange
        await page.addInitScript((value) => localStorage.setItem("cfo-chat-style", value), style);
        const relay = await open(page);
        await card(page, NAME).click();
        await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
        relay.say({ type: "held", reason: "pipeline owns this task" });
        await expect(field(page)).toBeVisible();
        await field(page).fill("please");

        // Act: release custody while the composer is recording.
        if (width === 390) await box(page).getByRole("button", { name: "Dictate", exact: true }).tap();
        else {
          await box(page).getByRole("button", { name: "Hold to dictate", exact: true }).hover();
          await page.mouse.down();
        }
        const recording = box(page).getByRole("button", { name: width === 390 ? "Stop dictating" : "Listening", exact: true });
        await expect(recording).toBeVisible();
        relay.say({ type: "held", reason: "" });
        await expect(recording).toBeVisible();
        if (width === 390) {
          await box(page).getByRole("button", { name: "Style: modern", exact: true }).tap();
          await page.getByRole("group", { name: "Chat style", exact: true }).getByRole("button", { name: "Classic", exact: true }).tap();
          await expect(recording).toBeVisible();
          await expect(field(page)).toBeFocused();
        }
        await page.waitForTimeout(1200);
        if (width === 390) await recording.tap();
        else await page.mouse.up();

        // Assert: recording finished into the same draft without sending. On
        // the phone, Classic, chosen while it recorded, puts the box away once
        // the recording ends, and Modern shows the words in its draft.
        if (width === 390) {
          await expect(box(page)).toBeHidden();
          await page.locator(".context-pane").getByRole("button", { name: "Switch to Modern chat", exact: true }).tap();
        }
        await expect(field(page)).toHaveValue("please rebase onto main");
        expect(typed(relay)).toBe("");
      });
      // A session whose usage cannot be read shows nothing of it, never
      // words for why (the Overlord, 2026-10-28).
      test(`${style} shows no usage and no words for it when the session's usage cannot be read at ${width}px`, async ({ page }, testInfo) => {
        // Arrange
        await page.addInitScript((value) => localStorage.setItem("cfo-chat-style", value), style);
        const relay = await open(page);

        // Act
        await card(page, NAME).click();
        await field(page).fill("Keep this draft");

        // Assert
        await expect(page.locator(".usage-bar:visible")).toHaveCount(0);
        await expect(box(page)).not.toContainText(/usage|unavailable|could not be read/i);
        await expect(page.getByText(/usage unavailable|usage could not be read/i)).toHaveCount(0);
        const surface = testInfo.outputPath("usage-unread-surface.png");
        await page.screenshot({ path: surface });
        await testInfo.attach("usage-unread-surface", { path: surface, contentType: "image/png" });
        await expect(field(page)).toHaveValue("Keep this draft");
        expect(typed(relay)).toBe("");
      });
    });
  }
}

test("a recording started in the terminal never pulls its box into the stepped-in Modern terminal", async ({ page }) => {
  // Arrange: a message opens a harness screen, whose list's Show the terminal
  // steps Zeke's terminal into the Modern conversation's place, where no
  // composer shows.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "modern"));
  const relay = await open(page);
  await openGoblin(page);
  await field(page).fill("/resume");
  await field(page).press("Enter");
  await expect.poll(() => relay.submissions.length).toBe(1);
  relay.say({ type: "screen", asking: true, open: true, working: false, panel: { rows: ["Resume a conversation"], choices: [{ text: "Fix the board", number: 1, focused: true }] } });
  await box(page).getByRole("button", { name: "Show the terminal", exact: true }).click();
  await expect(page.getByRole("button", { name: "Back to messages", exact: true })).toBeVisible();
  await expect(box(page)).toBeHidden();
  const typedBefore = typed(relay);
  // Arrange: a hold shows the box beside that terminal, and letting go takes it away.
  relay.say({ type: "held", reason: "pipeline owns this task" });
  await expect(field(page)).toBeVisible();
  relay.say({ type: "held", reason: "" });
  await expect(box(page)).toBeHidden();

  // Act: he dictates in the terminal itself.
  await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
  await page.keyboard.down("Control");
  await page.keyboard.down("Shift");
  await page.keyboard.down("Space");

  // Assert: the terminal keeps its own recording control, and no box comes in.
  const terminalMic = page.locator(".host-terminal .voice-dock .voice-bubble");
  await expect(terminalMic).toHaveClass(/recording/);
  await expect(terminalMic).toBeVisible();
  await expect(box(page)).toBeHidden();
  await expect(page.getByRole("button", { name: "Stop terminal dictation", exact: true })).toHaveCount(0);
  await expect(page.locator(".voice-bubble:visible")).toHaveCount(1);
  await page.waitForTimeout(1200);
  await page.keyboard.up("Space");
  await page.keyboard.up("Shift");
  await page.keyboard.up("Control");

  // Assert: the words reached the terminal, and the box stayed away.
  await expect.poll(() => typed(relay)).toBe(typedBefore + pasted("rebase onto main"));
  await expect(terminalMic).not.toHaveClass(/recording/);
  await expect(box(page)).toBeHidden();
});

for (const style of ["modern"]) {
  for (const width of [1440, 390]) {
    test(`a detached ${style} session keeps its input and recovery usable at ${width}px`, async ({ page }, testInfo) => {
      test.slow();
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await page.addInitScript((chatStyle) => {
        localStorage.setItem("cfo-chat-style", chatStyle);
        localStorage.setItem("cfo-terminal-font-size", "20");
        localStorage.setItem("cfo-message-font-size", "20");
        const real = window.visualViewport!;
        let keyboard = 0, top = 0;
        const viewport = new EventTarget();
        Object.defineProperties(viewport, {
          width: { get: () => real.width }, height: { get: () => real.height - keyboard },
          offsetTop: { get: () => top }, offsetLeft: { get: () => 0 }, scale: { get: () => 1 },
        });
        Object.defineProperty(window, "visualViewport", { value: viewport, configurable: true });
        Object.assign(window, { fixtureKeyboard: (height: number, offset: number) => { keyboard = height; top = offset; viewport.dispatchEvent(new Event("resize")); } });
      }, style);
      const relay = await open(page, snapshot(), "rebase onto main", undefined, "A long terminal line should fit the visible terminal width after resizing.\r\n");
      await card(page, NAME).click();
      await expect.poll(() => relay.views).toBeGreaterThan(0);
      relay.say({ type: "held", reason: "HEAD is detached at eb020be993b5, and no gate custody can be read for a detached HEAD, so typing is held" });
      await expect(field(page)).toBeVisible();
      // Why messages queue shows once over the box, and goes by itself.
      const note = box(page).locator(".composer-banner");
      await expect(note).toHaveText("Detached worktree. Send to queue until a branch is ready.");
      const draft = "A draft to retain while the branch is recovered. ".repeat(18);
      await field(page).fill(draft);
      await field(page).focus();
      if (width === 390) await page.evaluate(() => {
        const keyboard: unknown = Reflect.get(window, "fixtureKeyboard");
        if (typeof keyboard !== "function") throw new Error("Keyboard fixture unavailable");
        keyboard(300, 20);
      });

      const screenshot = testInfo.outputPath("detached-keyboard-surface.png");
      await page.screenshot({ path: screenshot });
      await testInfo.attach("detached-keyboard-surface", { path: screenshot, contentType: "image/png" });
      const geometry = await page.evaluate(() => {
        const composer = Array.from(document.querySelectorAll<HTMLElement>(".composer")).find((element) => element.getClientRects().length);
        const surface = Array.from(document.querySelectorAll<HTMLElement>(".terminal-surface, .conversation-frame")).find((element) => element.getClientRects().length && getComputedStyle(element).visibility === "visible");
        const send = composer?.querySelector<HTMLElement>(".composer-send");
        if (!composer || !surface || !send) throw new Error("The active chat surface is missing");
        const input = composer.getBoundingClientRect();
        const content = surface.getBoundingClientRect();
        return { contentBottom: content.bottom, composerTop: input.top, right: input.right, viewport: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth, sendBottom: send.getBoundingClientRect().bottom, visualBottom: window.visualViewport!.offsetTop + window.visualViewport!.height };
      });
      expect(geometry.contentBottom).toBeLessThanOrEqual(geometry.composerTop + 1);
      expect(geometry.right).toBeLessThanOrEqual(geometry.viewport);
      expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.viewport);
      expect(geometry.sendBottom).toBeLessThanOrEqual(geometry.visualBottom);
      await expect(page.locator(".terminal-foot.line:visible")).toHaveCount(0);
      await expect(page.locator(".voice-bubble:visible")).toHaveCount(1);
      await expect(send(page)).toBeInViewport();
      await expect(field(page)).toBeVisible();

      const requests: unknown[] = [];
      await page.route("**/api/actions", (route) => {
        requests.push(route.request().postDataJSON());
        return route.fulfill({ status: 503, json: { error: "Could not queue. Try again." } });
      });
      await send(page).click();
      await expect(box(page).getByRole("alert")).toContainText("Could not queue. Try again.");
      await expect(field(page)).toHaveValue(draft);
      expect(typed(relay)).toBe("");
      await page.route("**/api/actions", (route) => {
        requests.push(route.request().postDataJSON());
        return route.fulfill({ status: 202, json: {} });
      });
      await send(page).click();
      await expect(field(page)).toHaveValue("");
      expect(requests).toHaveLength(2);
      expect(requests[1]).toEqual(requests[0]);
      expect(typed(relay)).toBe("");
      await field(page).fill("Keep this until ");
      await field(page).focus();
      await page.keyboard.type("recovery");
      relay.say({ type: "held", reason: "pipeline owns this task; use cfo pipeline respond" });
      await expect(box(page)).toHaveAttribute("data-queues", "");
      await expect(box(page).getByRole("status")).toHaveText("");
      expect(await note.count()).toBe(0);
      relay.say({ type: "held", reason: "" });
      await expect(field(page)).toHaveValue("Keep this until recovery");
      expect(typed(relay)).toBe("");
      await field(page).press("Enter");
      await expect.poll(() => typed(relay)).toContain("Keep this until recovery");
      expect(requests).toHaveLength(2);
    });
  }
}
