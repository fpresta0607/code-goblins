import { expect, test, type Page } from "./site";
import { at, box, card, field, GOBLIN, NAME, open, openGoblin, pasted, send, snapshot, typed } from "./session-board";

test.describe("phone speech history", () => {
  test.use({ hasTouch: true });
  for (const style of ["classic", "modern"]) {
    for (const width of [360, 390]) {
      test(`phone ${style} inserts an older speech entry at the selection without reading the clipboard at ${width}px`, async ({ page }) => {
        await page.setViewportSize({ width, height: 800 });
        await page.addInitScript((chatStyle) => {
          localStorage.setItem("cfo-chat-style", chatStyle);
          let reads = 0;
          Object.defineProperty(navigator, "clipboard", { configurable: true, value: { read: async () => { reads++; return []; } } });
          Object.assign(window, { clipboardReads: () => reads });
        }, style);
        const relay = await open(page);
        await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: [
          { text: "newest dictation", at: Date.parse("2026-10-22T09:31:00Z") },
          { text: "older dictation", at: Date.parse("2026-10-22T09:30:00Z") },
        ] } }));
        await card(page, NAME).tap();
        await expect.poll(() => relay.views).toBeGreaterThan(0);
        if (style === "classic") relay.say({ type: "held", reason: "pipeline owns this task" });
        await expect(field(page)).toBeVisible();
        await field(page).fill("before replace after");
        await field(page).evaluate((element) => { const box = element as HTMLTextAreaElement; box.focus(); box.setSelectionRange(7, 14); });

        await box(page).getByRole("button", { name: "Speech history", exact: true }).tap();
        const history = page.getByRole("dialog", { name: "Recent messages" });
        await expect(history).toBeVisible();
        await history.getByRole("button", { name: "Paste into the message: older dictation", exact: true }).tap();

        await expect(field(page)).toHaveValue("before older dictation after");
        await expect(field(page)).toBeFocused();
        await expect.poll(() => field(page).evaluate((element) => (element as HTMLTextAreaElement).selectionStart)).toBe(22);
        await expect(history).toHaveCount(0);
        await expect(box(page).locator(".composer-note")).not.toContainText("Nothing to paste");
        expect(await page.evaluate(() => { const reads: unknown = Reflect.get(window, "clipboardReads"); return typeof reads === "function" ? reads() : -1; })).toBe(0);
        expect(typed(relay)).toBe("");
      });
    }
  }
});

// The Overlord chose design A on 2026-10-22, and two tabs that evening: a
// Claude Code session's Terminal view shows its messages, and the box under
// the messages takes the place of the terminal's input. Send types what he wrote through the terminal's own
// view, as one paste and an Enter, and what keeps it from the terminal says so
// beside the box, his draft kept (tests/session-board.ts plays the
// supervisor).
// Chromium's fake microphone (periodic beeps) stands in for the Overlord's,
// which the speech model played here hears as words.
test.use({ permissions: ["microphone"], launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] } });

for (const width of [1440, 390]) {
  test(`the slim composer keeps plain controls around the input at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await open(page);
    await openGoblin(page);
    const composer = box(page);
    const input = composer.locator(".composer-input");
    const add = composer.getByRole("button", { name: "Add files", exact: true });
    const microphone = composer.locator(".voice-bubble");
    const style = composer.getByRole("button", { name: "Style: modern", exact: true });

    await expect(input).toBeVisible();
    const reply = page.locator(".message-reply");
    const replyText = await reply.locator(".message-words").boundingBox();
    const replyActions = await reply.locator(".message-reply-actions").boundingBox();
    expect(replyText && replyActions && replyActions.y >= replyText.y + replyText.height).toBeTruthy();
    await expect(reply.locator(".message-reply-actions time")).toBeVisible();
    const bounds = await Promise.all([input, add, field(page), microphone, style].map((part) => part.boundingBox()));
    const [bar, plus, prompt, mic, footer] = bounds;
    expect(bar && plus && prompt && mic && footer).toBeTruthy();
    if (!bar || !plus || !prompt || !mic || !footer) return;
    expect(plus.x + plus.width).toBeLessThanOrEqual(prompt.x);
    expect(prompt.x + prompt.width).toBeLessThanOrEqual(mic.x);
    expect(footer.y).toBeGreaterThanOrEqual(bar.y + bar.height);
    expect(bar.height).toBeLessThanOrEqual(60);
    for (const button of [add, microphone, style, composer.getByRole("button", { name: "Speech history", exact: true })]) {
      expect(await button.evaluate((element) => ({ border: getComputedStyle(element).borderTopWidth, background: getComputedStyle(element).backgroundColor }))).toEqual({ border: "0px", background: "rgba(0, 0, 0, 0)" });
    }
    await field(page).fill("Keep this draft while changing the view");
    await expect(field(page)).toHaveValue("Keep this draft while changing the view");
    await expect(send(page)).toBeEnabled();
    await composer.screenshot({ path: testInfo.outputPath(`slim-composer-${width}.png`) });
  });
}

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

test("busy, held and reconnecting sessions save messages in the durable queue", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);
  const note = box(page).locator(".composer-note");
  await field(page).fill("merge it");
  const actions: object[] = [];
  await page.route("**/api/actions", (route) => { actions.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: {} }); });

  relay.say({ type: "unread", unread: true });
  await expect(note).toHaveText("Messages will be queued.");
  await expect(send(page)).toBeEnabled();
  await field(page).press("Enter");
  await expect.poll(() => actions.length).toBe(1);
  expect(actions[0]).toMatchObject({ kind: "message", task_id: GOBLIN.id, text: "merge it" });
  await expect(field(page)).toHaveValue("");
  await field(page).fill("merge it");
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
  await expect(page.getByRole("status").filter({ hasText: /^Reconnecting$/ })).toHaveCount(1);
  const before = typed(relay);
  expect(before, "nothing was typed while the program was busy").not.toContain("merge it");
  await send(page).click();

  await expect(note).toHaveText("Messages will be queued.");
  await expect(field(page)).toHaveValue("");
  expect(actions).toHaveLength(2);
  expect(actions[1]).toMatchObject({ kind: "message", task_id: GOBLIN.id, text: "merge it" });
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
  const working = page.locator(".session-working");
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

test("a Codex session's panel shows Modern messages with its reachable composer", async ({ page }) => {
  await open(page, snapshot({}, { title: "Swipes scroll the phone terminal; Codex", harness: "codex", model: "gpt-5.5" }));

  await card(page, NAME).click();

  await expect(page.locator("#panel-title")).toHaveText(NAME);
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
  await expect(field(page)).toBeInViewport();
});

test.describe("dictation into the box", () => {
  test("holding the visible microphone adds dictated words without sending", async ({ page }) => {
    const relay = await open(page);
    await openGoblin(page);
    await field(page).fill("please");

    await box(page).getByRole("button", { name: "Hold to dictate", exact: true }).hover();
    await page.mouse.down();
    await expect(box(page).getByRole("button", { name: "Listening", exact: true })).toHaveClass(/recording/);
    await page.waitForTimeout(1200);
    await page.mouse.up();

    await expect(field(page)).toHaveValue("please rebase onto main");
    expect(typed(relay)).toBe("");
  });

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

    await box(page).getByRole("button", { name: "Speech history" }).click();
    await page.getByRole("dialog", { name: "Recent messages" }).getByRole("button", { name: "Paste into the message: check the phone spec" }).click();

    await expect(field(page)).toHaveValue("check the phone spec");
  });

  test("Escape closes the recent dictations, leaving the panel open and the keyboard on Recent messages", async ({ page }) => {
    await open(page);
    await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: [] } }));
    await openGoblin(page);
    const recent = box(page).getByRole("button", { name: "Speech history" });
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
  await expect(note).toHaveText("Messages will be queued.");
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

// A 1x1 PNG, the smallest picture a paste can carry.
const PICTURE = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
const KEPT = "/home/goblin/composer-files/pasted-1.png";

async function pastePicture(page: Page) {
  await field(page).evaluate((element, picture) => {
    const bytes = Uint8Array.from(atob(picture), (letter) => letter.charCodeAt(0));
    const clipboard = new DataTransfer();
    clipboard.items.add(new File([bytes], "shot.png", { type: "image/png" }));
    element.dispatchEvent(new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: clipboard }));
  }, PICTURE);
}

async function readClipboardOf(page: Page, text: string, picture = "") {
  await page.addInitScript(({ words, image }) => {
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { read: async () => {
      if (!image) return [{ types: ["text/plain"], getType: async () => new Blob([words], { type: "text/plain" }) }];
      const bytes = Uint8Array.from(atob(image), (letter) => letter.charCodeAt(0));
      return [{ types: ["image/png"], getType: async () => new File([bytes], "shot.png", { type: "image/png" }) }];
    } } });
  }, { words: text, image: picture });
}

test("a picture pasted into the box is kept where the session reads it, and the message names its path", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  const posts: { url: string; body: string }[] = [];
  await page.route("**/api/composer/file**", async (route) => {
    posts.push({ url: route.request().url(), body: route.request().postDataBuffer()?.toString("base64") ?? "" });
    await route.fulfill({ json: { path: KEPT } });
  });
  await openGoblin(page);

  // Act
  await pastePicture(page);

  // Assert
  await expect(field(page)).toHaveValue(KEPT);
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0].url).toContain("task=" + GOBLIN.id);
  expect(posts[0].url).toContain("name=shot.png");
  expect(posts[0].body.length, "the picture's own bytes went, not the clipboard's name").toBeGreaterThan(80);
  await send(page).click();
  await expect.poll(() => typed(relay)).toContain(pasted(KEPT) + "\r");
});

test("a picture the board cannot keep says so in the box rather than disappearing", async ({ page }) => {
  // Arrange
  await open(page);
  await page.route("**/api/composer/file**", (route) => route.fulfill({ status: 500, json: { error: "The file could not be kept: no room" } }));
  await openGoblin(page);

  // Act
  await pastePicture(page);

  // Assert
  await expect(field(page)).toHaveValue("");
  await expect(box(page).locator(".composer-note")).toHaveText("That picture was not added: The file could not be kept: no room");
});

test("Alt+V pastes the clipboard's text into the box, as Ctrl+V does", async ({ page }) => {
  // Arrange
  await readClipboardOf(page, "from the clipboard");
  await open(page);
  await openGoblin(page);

  // Act
  await field(page).focus();
  await page.keyboard.press("Alt+v");

  // Assert
  await expect(field(page)).toHaveValue("from the clipboard");
});

test("Alt+V pastes a picture into the box by the path the session reads", async ({ page }) => {
  // Arrange
  await readClipboardOf(page, "", PICTURE);
  await open(page);
  await page.route("**/api/composer/file**", (route) => route.fulfill({ json: { path: KEPT } }));
  await openGoblin(page);

  // Act
  await field(page).focus();
  await page.keyboard.press("Alt+v");

  // Assert
  await expect(field(page)).toHaveValue(KEPT);
});

test("a slash command offers the session's own commands, filters, takes the keyboard and keeps its arguments", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ["compact", "context", "cost"] } }));
  await openGoblin(page);
  const list = page.getByRole("listbox", { name: "Commands", exact: true });

  // Act: the command word filters the list.
  await field(page).fill("/co");

  // Assert
  await expect(list).toBeVisible();
  await expect(list.getByRole("option")).toHaveText(["/compact", "/context", "/cost"]);
  await field(page).fill("/cos");
  await expect(list.getByRole("option")).toHaveText(["/cost"]);

  // Act: the keyboard picks it.
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");

  // Assert: the command is in the box with room for its arguments.
  await expect(field(page)).toHaveValue("/cost ");
  await expect(list).toHaveCount(0);

  // Act: Escape closes the list without clearing the draft.
  await field(page).fill("/co");
  await expect(list).toBeVisible();
  await page.keyboard.press("Escape");

  // Assert
  await expect(field(page)).toHaveValue("/co");
  await expect(list).toHaveCount(0);
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toContain(pasted("/co") + "\r");
});

test("a leading ! command is typed into the session as the harness's own shell command", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);

  // Act
  await field(page).fill("!git status --short");
  await field(page).press("Enter");

  // Assert
  await expect.poll(() => typed(relay)).toContain(pasted("!git status --short") + "\r");
});

test("a delivered message appears once in Modern chat and the queue count includes every pending message", async ({ page }) => {
  const actions = Array.from({ length: 8 }, (_, index) => ({ id: "q" + index, kind: "message", task_id: "cg-touch", text: "queued request " + index, status: "queued" }));
  await open(page, snapshot({ actions: [...actions, { id: "delivered", kind: "message", task_id: "cg-touch", text: "already in the conversation", status: "done" }] }), "", {
    messages: [{ id: "sent", seq: 0, kind: "prompt", source: "overlord", text: "already in the conversation" },
      { id: "long", seq: 1, kind: "reply", blocks: [{ kind: "text", text: "Earlier conversation paragraph.\n\n".repeat(80) }] }], more: false,
  });
  await openGoblin(page);
  await expect(page.locator(".context-pane").getByText("already in the conversation", { exact: true })).toHaveCount(1);
  const queue = page.getByRole("button", { name: "8 queued messages", exact: true });
  await expect(queue).toBeVisible();
  await page.getByRole("log", { name: "Messages of " + NAME }).evaluate((list) => { list.scrollTop = 0; });
  const firstQueued = page.locator('.queued-message[data-queued="q0"]');
  await expect(firstQueued).not.toBeInViewport();
  await queue.click();
  await expect(firstQueued).toBeInViewport();
});
