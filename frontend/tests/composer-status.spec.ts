import { expect, test } from "./site";
import { box, card, field, GOBLIN, NAME, open, openGoblin, pasted, send, snapshot, typed } from "./session-board";

test.use({ permissions: ["microphone"], launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] } });

for (const style of ["classic", "modern"]) {
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
        let draft = "Keep this draft\n" + "A long message that stays readable while waiting. ".repeat(40);
        await field(page).fill(draft);
        await page.route("**/api/composer/file**", (route) => route.fulfill({ status: 201, json: { path: "C:/session-files/design.txt" } }));
        await box(page).locator('input[type="file"]').setInputFiles({ name: "design.txt", mimeType: "text/plain", buffer: Buffer.from("Keep this attachment") });
        draft = draft.trimEnd() + "\nC:/session-files/design.txt";
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
        await expect(page.locator(".terminal-held-line:visible")).toHaveCount(0);
        await expect(box(page).locator(".composer-note")).toHaveText("Review in progress. Send to queue until it finishes.");
        await expect(page.locator(".voice-bubble:visible")).toHaveCount(1);
        await expect(send(page)).toBeInViewport();
        expect(await box(page).locator(".composer-note").evaluate((note) => getComputedStyle(note).color)).toBe(await box(page).locator(".composer-control").first().evaluate((control) => getComputedStyle(control).color));

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
        expect(requests).toHaveLength(2);
        expect(requests[1]).toEqual(requests[0]);
        await expect(box(page).getByRole("alert")).toHaveCount(0);
        await expect(box(page).locator(".composer-note")).toHaveText("Messages will be queued.");
        expect(typed(relay)).toBe(before);

        // Act: the durable entry remains after the hold changes.
        await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: data })), snapshot({ revision: 2, actions: [{ id: "q1", kind: "message", task_id: GOBLIN.id, text: draft, status: "queued", message: "Waiting until the session takes input", can_send_now: false }] }));
        await expect(page.locator(".queued-message:visible")).toHaveCount(1);
        const queueSurface = testInfo.outputPath("queue-with-long-message.png");
        await page.screenshot({ path: queueSurface });
        await testInfo.attach("queue-with-long-message", { path: queueSurface, contentType: "image/png" });
        await expect(send(page)).toBeInViewport();
        await expect(field(page)).toBeInViewport();
        await field(page).fill("another draft");
        relay.say({ type: "held", reason: "" });
        if (style === "classic") await page.getByRole("button", { name: "Back to messages", exact: true }).click();
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
        await expect(box(page).locator(".composer-note")).toHaveText("Review in progress. Send to queue until it finishes.");
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
        await expect(page.locator(".terminal-unread:visible")).toHaveCount(0);
        await expect(box(page).locator(".composer-note")).toHaveText("Messages will be queued.");
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
        await expect(page.locator(".terminal-reconnecting")).toHaveCount(1);
        await expect(field(page)).toBeVisible();
        await expect(field(page)).toHaveValue("Keep this through the reconnect");
        await expect(box(page).locator(".composer-note")).toHaveText("Messages will be queued.");
        await send(page).click();
        await expect(field(page)).toHaveValue("");
        expect(requests).toHaveLength(2);
        expect(typed(relay)).toBe(before);

        // Act: the relay comes back, and after reconnecting an ended session
        // keeps an unsent draft.
        relay.accept();
        const reconnect = page.locator(".terminal-closed").getByRole("button", { name: "Reconnect", exact: true });
        if (await reconnect.count()) await reconnect.click();
        await field(page).focus();
        await expect(page.locator(".terminal-reconnecting")).toHaveCount(0);
        await expect(field(page)).toBeFocused();
        await page.keyboard.type("still in the draft");
        await expect(field(page)).toHaveValue("still in the draft");
        expect(typed(relay)).toBe(before);
        if (style === "classic") await page.getByRole("button", { name: "Back to messages", exact: true }).click();
        await field(page).fill("Keep this when the terminal ends");
        relay.end();
        await expect(page.locator(".terminal-closed")).toHaveCount(1);
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
          await expect(box(page).locator(".composer-note")).toHaveText("Review in progress. Send to queue until it finishes.");
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
          if (style === "classic" && await page.getByRole("button", { name: "Back to messages", exact: true }).isVisible()) await page.getByRole("button", { name: "Back to messages", exact: true }).click();
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
            await expect(box(page).getByRole("alert")).toHaveCount(0);
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
        await expect(field(page)).toHaveValue("Keep the attachment\nC:/session-files/pending.txt");
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
        if (style === "modern" && width === 390) {
          await box(page).getByRole("button", { name: "Style: modern", exact: true }).tap();
          await page.getByRole("group", { name: "Chat style", exact: true }).getByRole("button", { name: "Classic", exact: true }).tap();
          await expect(recording).toBeVisible();
          await expect(field(page)).toBeFocused();
        }
        await page.waitForTimeout(1200);
        if (width === 390) await recording.tap();
        else await page.mouse.up();

        // Assert: recording finished into the same draft without sending.
        await expect(field(page)).toHaveValue("please rebase onto main");
        expect(typed(relay)).toBe("");
        if (style === "classic" && width === 390) {
          await page.getByRole("button", { name: "Copy terminal text", exact: true }).tap();
          const text = page.getByRole("dialog", { name: "Terminal text", exact: true });
          await expect(text).toContainText("> ready");
          await text.getByRole("button", { name: "Done", exact: true }).tap();
          await expect(field(page)).toHaveValue("please rebase onto main");
        }
      });
      test(`${style} shows usage unavailability once with accessible details at ${width}px`, async ({ page }, testInfo) => {
        // Arrange
        await page.addInitScript((value) => localStorage.setItem("cfo-chat-style", value), style);
        const relay = await open(page);
        await card(page, NAME).click();
        if (style === "classic") await page.getByRole("button", { name: "Back to messages", exact: true }).click();
        await field(page).fill("Keep this while inspecting usage");
        const footer = page.locator(".session-footer:visible");

        // Assert: one concise state without a second explanation in the footer.
        await expect(footer.locator(".session-fact").filter({ hasText: /usage/i })).toHaveCount(1);
        await expect(footer).toContainText("Usage unavailable");
        await expect(footer).not.toContainText("its Claude usage could not be read");
        const surface = testInfo.outputPath("usage-unavailable-surface.png");
        await page.screenshot({ path: surface });
        await testInfo.attach("usage-unavailable-surface", { path: surface, contentType: "image/png" });

        // Act: keyboard and touch both open useful details in the existing control.
        const usage = box(page).getByRole("button", { name: "Context usage unavailable", exact: true });
        if (width === 390) await usage.tap();
        else await usage.focus();
        const details = page.getByRole("tooltip", { name: "Session statistics", exact: true });

        // Assert
        await expect(details).toContainText("its Claude usage could not be read");
        await usage.press("Escape");
        await expect(details).toHaveCount(0);
        await expect(field(page)).toHaveValue("Keep this while inspecting usage");
        expect(typed(relay)).toBe("");
      });
      if (style === "classic" && width === 390) test("a terminal recording stays stoppable after opening the composer and messages", async ({ page }) => {
        // Arrange: record in the ready Classic terminal before it has a composer.
        await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
        const relay = await open(page);
        await card(page, NAME).tap();
        await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
        await page.getByRole("button", { name: "Dictate", exact: true }).tap();
        await expect(page.getByRole("button", { name: "Stop and send", exact: true })).toBeVisible();

        // Act: custody changes and the active surface moves to messages.
        relay.say({ type: "held", reason: "pipeline owns this task" });
        await expect(box(page).getByRole("button", { name: "Stop terminal dictation", exact: true })).toBeVisible();
        await expect(page.locator(".voice-bubble:visible")).toHaveCount(1);
        await page.getByRole("button", { name: "Back to messages", exact: true }).tap();
        await page.waitForTimeout(1200);
        await box(page).getByRole("button", { name: "Stop terminal dictation", exact: true }).tap();

        // Assert: no held input was typed, and speech remains recoverable in history.
        await expect(box(page).getByRole("button", { name: "Dictate", exact: true })).toBeVisible();
        expect(typed(relay)).toBe("");
        await box(page).getByRole("button", { name: "Speech history", exact: true }).tap();
        await page.getByRole("button", { name: "Paste into the message: rebase onto main", exact: true }).tap();
        await expect(field(page)).toHaveValue("rebase onto main");
      });
      if (style === "classic" && width === 390) test("a terminal microphone failure remains visible and recoverable after the composer opens", async ({ page }) => {
        // Arrange: the terminal's microphone request remains pending until the composer appears.
        await page.addInitScript(() => {
          localStorage.setItem("cfo-chat-style", "classic");
          const original = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
          let isFirst = true;
          navigator.mediaDevices.getUserMedia = (constraints) => {
            if (!isFirst) return original(constraints);
            isFirst = false;
            return new Promise<MediaStream>((_resolve, reject) => {
              Object.defineProperty(window, "failFixtureMicrophone", { value: () => reject(new DOMException("No microphone", "NotFoundError")) });
            });
          };
        });
        const relay = await open(page);
        await card(page, NAME).tap();
        await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
        await page.getByRole("button", { name: "Dictate", exact: true }).tap();
        await expect.poll(() => page.evaluate(() => typeof Reflect.get(window, "failFixtureMicrophone"))).toBe("function");
        relay.say({ type: "held", reason: "pipeline owns this task" });
        await expect(field(page)).toBeVisible();
        await field(page).fill("Keep this draft");

        // Act: the terminal-owned request fails after the composer takes over.
        await page.evaluate(() => {
          const fail: unknown = Reflect.get(window, "failFixtureMicrophone");
          if (typeof fail !== "function") throw new Error("The microphone request did not start");
          fail();
        });

        // Assert: the active surface says what failed once and retains the draft.
        await expect(box(page).locator(".composer-note")).toHaveText("No microphone was found.");
        await expect(page.locator(".terminal-note:visible")).toHaveCount(0);
        await expect(field(page)).toHaveValue("Keep this draft");
        expect(typed(relay)).toBe("");

        // Act and assert: a second tap-to-record attempt succeeds into the same draft.
        await box(page).getByRole("button", { name: "Dictate", exact: true }).tap();
        const stop = box(page).getByRole("button", { name: "Stop dictating", exact: true });
        await expect(stop).toBeVisible();
        await page.waitForTimeout(1200);
        await stop.tap();
        await expect(field(page)).toHaveValue("Keep this draft rebase onto main");
        expect(typed(relay)).toBe("");
      });
    });
  }
}

test("a live Classic terminal keeps its own dictation after the hold that showed the box clears", async ({ page }) => {
  // Arrange: a hold shows the box beside Zeke's terminal, and letting go leaves
  // the box there while the terminal takes typing again.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
  const relay = await open(page);
  await card(page, NAME).click();
  await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
  relay.say({ type: "held", reason: "pipeline owns this task" });
  await expect(field(page)).toBeVisible();
  relay.say({ type: "held", reason: "" });
  await expect(box(page).locator(".composer-note")).toHaveText("");
  const before = typed(relay);

  // Act: he clicks the terminal, holds the shortcut and speaks.
  await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
  await page.keyboard.down("Control");
  await page.keyboard.down("Shift");
  await page.keyboard.down("Space");
  await expect(box(page).getByRole("button", { name: "Stop terminal dictation", exact: true })).toBeVisible();
  await expect(page.locator(".voice-bubble:visible")).toHaveCount(1);
  await page.waitForTimeout(1200);
  await page.keyboard.up("Space");
  await page.keyboard.up("Shift");
  await page.keyboard.up("Control");

  // Assert: the words reached the terminal it heard them for, once, not a draft.
  await expect.poll(() => typed(relay)).toBe(before + pasted("rebase onto main"));
  await expect(field(page)).toHaveValue("");
  await expect(box(page).locator(".composer-note")).toHaveText("");

  // Assert: Speech history holds the one dictation, and it types nothing more.
  await box(page).getByRole("button", { name: "Speech history", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Recent messages", exact: true }).locator(".voice-text")).toHaveText(["rebase onto main"]);
  await box(page).getByRole("button", { name: "Speech history", exact: true }).click();
  expect(typed(relay)).toBe(before + pasted("rebase onto main"));
  await expect(field(page)).toHaveValue("");
});

test("a recording started in the terminal never pulls its box into the stepped-in Modern terminal", async ({ page }) => {
  // Arrange: a message opening a harness screen steps Zeke's terminal into the
  // Modern conversation's place, where no composer shows.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "modern"));
  const relay = await open(page);
  await openGoblin(page);
  await field(page).fill("/model");
  await field(page).press("Enter");
  await expect.poll(() => relay.submissions.length).toBe(1);
  relay.say({ type: "screen", asking: true, open: true, working: false });
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

test("a Classic box already shown keeps one control when the terminal starts its own recording", async ({ page }) => {
  // Arrange: a hold shows Zeke's Classic box; letting go leaves it there while
  // the terminal takes typing again.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
  const relay = await open(page);
  await card(page, NAME).click();
  await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
  relay.say({ type: "held", reason: "pipeline owns this task" });
  await expect(field(page)).toBeVisible();
  relay.say({ type: "held", reason: "" });
  await expect(box(page).locator(".composer-note")).toHaveText("");
  await expect(box(page).getByRole("button", { name: "Hold to dictate", exact: true })).toBeVisible();
  const before = typed(relay);

  // Act: with the box already shown, he focuses the terminal and records.
  await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
  await page.keyboard.down("Control");
  await page.keyboard.down("Shift");
  await page.keyboard.down("Space");

  // Assert: one control is on screen for the recording, the terminal's own
  // layer stays hidden beneath the shown box, and the box does not change.
  await expect(box(page).getByRole("button", { name: "Stop terminal dictation", exact: true })).toBeVisible();
  await expect(page.locator(".voice-bubble:visible")).toHaveCount(1);
  await expect(page.locator(".host-terminal .voice-dock .voice-bubble:visible")).toHaveCount(0);
  await expect(field(page)).toBeVisible();
  await page.waitForTimeout(1200);
  await page.keyboard.up("Space");
  await page.keyboard.up("Shift");
  await page.keyboard.up("Control");

  // Assert: the words reached the terminal, and the box kept its one control.
  await expect.poll(() => typed(relay)).toBe(before + pasted("rebase onto main"));
  await expect(field(page)).toHaveValue("");
  await expect(box(page).locator(".composer-note")).toHaveText("");
  await expect(box(page).getByRole("button", { name: "Hold to dictate", exact: true })).toBeVisible();
});

for (const style of ["classic", "modern"]) {
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
      const draft = "A draft to retain while the branch is recovered. ".repeat(18);
      await field(page).fill(draft);
      await field(page).focus();
      if (width === 390) await page.evaluate(() => {
        const keyboard: unknown = Reflect.get(window, "fixtureKeyboard");
        if (typeof keyboard !== "function") throw new Error("Keyboard fixture unavailable");
        keyboard(300, 20);
      });

      const note = box(page).locator(".composer-note");
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
      await expect(note).toHaveText("Detached worktree. Send to queue until a branch is ready.");
      await expect(page.locator(".terminal-held-line:visible")).toHaveCount(0);
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
      await expect(note).toHaveText("Review in progress. Send to queue until it finishes.");
      relay.say({ type: "held", reason: "" });
      await expect(field(page)).toHaveValue("Keep this until recovery");
      expect(typed(relay)).toBe("");
      await field(page).press("Enter");
      await expect.poll(() => typed(relay)).toContain("Keep this until recovery");
      expect(requests).toHaveLength(2);
    });
  }
}
