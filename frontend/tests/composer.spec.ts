import { expect, test } from "./site";
import { at, box, card, field, NAME, open, openGoblin, pasted, send, snapshot, typed } from "./session-board";

// The Overlord chose design A on 2026-10-22, and two tabs that evening: a
// Claude Code session's Terminal view shows its messages, and the box under
// the messages takes the place of the terminal's input. Send types what he wrote through the terminal's own
// view, as one paste and an Enter, and what keeps it from the terminal says so
// beside the box, his draft kept (tests/session-board.ts plays the
// supervisor).
// Chromium's fake microphone (periodic beeps) stands in for the Overlord's,
// which the speech model played here hears as words.
test.use({ permissions: ["microphone"], launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] } });

test("Send types what he wrote into the session's terminal once, as one paste, then Enter, and empties the box", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);

  await field(page).fill("fix the swipe bug");
  await send(page).click();

  await expect.poll(() => typed(relay)).toContain(pasted("fix the swipe bug") + "\r");
  expect(typed(relay).split(pasted("fix the swipe bug")).length - 1, "typed once").toBe(1);
  await expect(field(page)).toHaveValue("");
  await field(page).fill("and test it");
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toContain(pasted("and test it") + "\r");
});

test("Shift+Enter starts a new line, and the lines go as one paste", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);

  await field(page).click();
  await page.keyboard.type("first line");
  await page.keyboard.press("Shift+Enter");
  await page.keyboard.type("second line");
  await expect(field(page)).toHaveValue("first line\nsecond line");
  await page.keyboard.press("Enter");

  await expect.poll(() => typed(relay)).toContain(pasted("first line\rsecond line") + "\r");
});

test("a busy program, a reconnecting terminal and typing a gate holds each say so beside the box and hold Send, and his draft stays, never sent late", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);
  const note = box(page).locator(".composer-note");
  await field(page).fill("merge it");

  relay.say({ type: "unread", unread: true });
  await expect(note).toHaveText("Program busy. Typed keys are waiting.");
  await expect(send(page)).toBeDisabled();
  await field(page).press("Enter");
  relay.say({ type: "unread", unread: false });
  await expect(note).toHaveText("");
  await expect(send(page)).toBeEnabled();
  await expect(field(page)).toHaveValue("merge it");
  relay.say({ type: "held", reason: "pipeline owns this task" });
  await expect(note).not.toHaveText("");
  // A message sent while the gate holds the input is queued, so Send stays
  // usable and the note says why nothing appears yet.
  await expect(send(page)).toBeEnabled();
  relay.say({ type: "held", reason: "" });
  await expect(send(page)).toBeEnabled();

  // Typing reaches a view only while it is connected, so the Send is made
  // while the view reconnects, before the relay answers its new connection.
  await page.routeWebSocket("**/api/terminal/native?*", () => {});
  relay.drop();
  await expect(page.locator(".terminal-reconnecting")).toHaveCount(1);
  const before = typed(relay);
  expect(before, "nothing was typed while the program was busy").not.toContain("merge it");
  await send(page).click();

  await expect(note).toHaveText("Not sent. The terminal is reconnecting.");
  await expect(field(page)).toHaveValue("merge it");
  await page.waitForTimeout(1500);
  expect(typed(relay)).toBe(before);
});

test("while the harness asks in its terminal an amber card says so and Send waits, and Answer in the terminal shows the terminal with the keyboard in it", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);
  await field(page).fill("yes");

  relay.say({ type: "screen", asking: true, open: true, working: false });

  const asking = box(page).locator(".composer-asking");
  await expect(asking).toContainText("Waiting on you in the terminal");
  await expect(send(page)).toBeDisabled();
  await asking.getByRole("button", { name: "Answer in the terminal" }).click();
  await expect(page.getByRole("button", { name: "Back to messages" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeFocused();
  expect(typed(relay), "nothing was typed into the harness's own screen").not.toContain("yes");
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await expect(field(page)).toBeFocused();
  await expect(box(page).locator(".composer-asking")).toHaveCount(0);
  await expect(send(page)).toBeEnabled();
});

test("while the harness works the working line's Stop presses Escape in its terminal", async ({ page }) => {
  const relay = await open(page, snapshot({}, { runtime: { state: "busy", reason: "", at } }));
  await openGoblin(page);
  const working = box(page).locator(".composer-working");
  await expect(working).toContainText("Working");

  await working.getByRole("button", { name: "Stop" }).click();

  await expect.poll(() => typed(relay)).toBe("\x1b");
});

test("his draft stays through a switch of sessions, a reconnect and a reload of the board, as an update reloads it", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);
  await field(page).fill("half a thought");

  await expect(field(page)).toHaveValue("half a thought");
  await card(page, "Ivy - Window Painter").click();
  await expect(page.locator("#panel-title")).toHaveText("Ivy - Window Painter");
  await card(page, NAME).click();
  await expect(field(page)).toHaveValue("half a thought");
  relay.drop();
  await expect.poll(() => relay.views).toBe(2);
  await expect(field(page)).toHaveValue("half a thought");
  await page.reload();
  await card(page, NAME).click();

  await expect(field(page)).toHaveValue("half a thought");
});

test("a session's panel is one view with its messages and no pill, and a switch hands its box the keyboard", async ({ page }) => {
  await open(page);
  await openGoblin(page);
  await expect(page.locator(".context-pane .panel-pill")).toHaveCount(0);
  await expect(field(page)).not.toBeFocused();

  await page.keyboard.press("Control+Alt+3");
  await expect(page.locator("#panel-title")).toHaveText("Ivy - Window Painter");
  await expect(field(page, "Ivy - Window Painter")).toBeFocused();
  await page.keyboard.press("Control+Alt+1");

  await expect(page.locator("#panel-title")).toHaveText("CFO");
  await expect(field(page, "CFO")).toBeFocused();
});

test("a Codex session's panel shows its terminal, with no messages", async ({ page }) => {
  await open(page, snapshot({}, { title: "Swipes scroll the phone terminal; Codex", harness: "codex", model: "gpt-5.5" }));

  await card(page, NAME).click();

  await expect(page.locator("#panel-title")).toHaveText(NAME);
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
});

test.describe("dictation into the box", () => {
  test("holding Ctrl+Shift+Space in the box dictates into it, and sends nothing by itself", async ({ page }) => {
    const relay = await open(page);
    await openGoblin(page);
    await field(page).fill("please");
    await field(page).focus();

    await page.keyboard.down("Control");
    await page.keyboard.down("Shift");
    await page.keyboard.down("Space");
    await page.waitForTimeout(1200);
    await page.keyboard.up("Space");
    await page.keyboard.up("Shift");
    await page.keyboard.up("Control");

    await expect(field(page)).toHaveValue("please rebase onto main");
    expect(typed(relay)).toBe("");
  });

  test.describe("on a touch screen", () => {
    test.use({ viewport: { width: 412, height: 915 }, hasTouch: true, isMobile: true });

    test("a tap on the box's microphone starts dictating, and the next adds what was said, sending nothing by itself", async ({ page }) => {
      const relay = await open(page);
      await card(page, NAME).tap();
      await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();

      await box(page).getByRole("button", { name: "Dictate" }).tap();
      await expect(box(page).getByRole("button", { name: "Stop dictating" })).toHaveClass(/recording/);
      await page.waitForTimeout(1200);
      await box(page).getByRole("button", { name: "Stop dictating" }).tap();

      await expect(field(page)).toHaveValue("rebase onto main");
      expect(typed(relay)).toBe("");
    });
  });

  test("the recent dictations paste into the box", async ({ page }) => {
    await open(page);
    await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: [{ text: "check the phone spec", at: Date.parse("2026-10-22T09:30:00Z") }] } }));
    await openGoblin(page);

    await box(page).getByRole("button", { name: "Recent messages" }).click();
    await page.getByRole("dialog", { name: "Recent messages" }).getByRole("button", { name: "Paste into the message: check the phone spec" }).click();

    await expect(field(page)).toHaveValue("check the phone spec");
  });

  test("Escape closes the recent dictations, leaving the panel open and the keyboard on Recent messages", async ({ page }) => {
    await open(page);
    await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: [] } }));
    await openGoblin(page);
    const recent = box(page).getByRole("button", { name: "Recent messages" });
    await recent.click();
    await expect(page.getByRole("dialog", { name: "Recent messages" })).toBeVisible();

    await page.keyboard.press("Escape");

    await expect(page.getByRole("dialog", { name: "Recent messages" })).toHaveCount(0);
    await expect(page.locator("#panel-title")).toHaveText(NAME);
    await expect(recent).toBeFocused();
  });
});

// The Overlord's screenshot: a custody check failing over a working goblin
// must not cover the conversation or the box, and what he types is queued
// rather than refused.
test("a failing custody check shows no banner over the conversation and keeps the box usable", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  const note = box(page).locator(".composer-note");

  // Act: the check fails, the way it did on his screenshot.
  relay.say({ type: "held", reason: "pipeline: native custody status check failed" });

  // Assert: no pill over the thread, the plain copy beside the box, and Send
  // usable so the message is queued.
  await expect(page.locator(".terminal-held")).toHaveCount(0);
  await expect(note).toHaveText("Queued. Delivery waits until this session takes input again.");
  await expect(field(page)).toBeVisible();
  await field(page).fill("send this while the check fails");
  await expect(send(page)).toBeEnabled();

  // Act: he presses Send while the check still fails, with the board's action
  // endpoint watching what it is asked to keep.
  const queued: { kind: string; task_id: string; text: string }[] = [];
  await page.route("**/api/actions", async (route) => {
    queued.push(route.request().postDataJSON() as { kind: string; task_id: string; text: string });
    await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
  });
  const before = typed(relay);
  await send(page).click();

  // Assert: the message went the durable way as an action for this goblin, and
  // nothing was typed into the held terminal.
  await expect(field(page)).toHaveValue("");
  expect(queued.map((action) => action.kind)).toEqual(["message"]);
  expect(queued[0].text).toBe("send this while the check fails");
  expect(typed(relay), "nothing is typed into a held terminal").toEqual(before);

  // Act: the check clears.
  relay.say({ type: "held", reason: "" });

  // Assert: the note is clear again and the message has not been typed twice.
  await expect(note).toHaveText("");
  expect(typed(relay), "a queued message is not typed on release by this path").toEqual(before);
});
