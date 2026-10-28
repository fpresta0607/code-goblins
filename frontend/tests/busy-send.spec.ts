import { expect, test, type Page } from "./site";
import { at, box, card, field, GOBLIN, NAME, open, openGoblin, PAGE, snapshot, typed } from "./session-board";

// The Overlord, 2026-10-27: "im not seeing queued messages appear in the
// modern chat right away, it lags", "there's no edit or send now function
// again. queued should exist at the bottom of the conversation until sent",
// and "or cancel of queued message". A message he sends while the session
// works shows at once as his bubble marked queued, pinned at the bottom with
// Edit, Send now and Cancel; nothing is typed into the turn; and once the
// harness takes it, it shows once, as sent. The supervisor is played by
// tests/session-board.ts.

interface Posted { id: string; kind: string; target: string; text: string }

// sendWhileWorking opens a working session of harness and records every
// action the board is sent.
async function sendWhileWorking(page: Page, harness: string) {
  const posts: Posted[] = [];
  const relay = await open(page, snapshot({ cfo_harness: harness }, { harness }));
  await page.route("**/api/actions", (route) => {
    const body = route.request().postDataJSON() as Partial<Posted>;
    posts.push({ id: body.id ?? "", kind: body.kind ?? "", target: body.target ?? "", text: body.text ?? "" });
    return route.fulfill({ status: 202, json: { status: "queued" } });
  });
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: true });
  await expect(page.locator(".reply-working")).toBeVisible();
  return { relay, posts };
}

// pressEnter presses Enter in the box and reads, in the next frame, whether
// his bubble is on screen with text, and how many milliseconds after the
// key it was drawn.
async function pressEnter(page: Page, text: string) {
  await field(page).evaluate((box, words) => {
    const shown = () => [...document.querySelectorAll(".queued-message.pane")].some((pane) => pane.textContent?.includes(words));
    box.addEventListener("keydown", (event) => {
      if ((event as KeyboardEvent).key !== "Enter") return;
      const pressed = performance.now();
      const seen = window as unknown as { nextFrame?: boolean; drawnAfter?: number };
      requestAnimationFrame(() => { seen.nextFrame = shown(); });
      const watch = new MutationObserver(() => { if (shown()) { seen.drawnAfter = performance.now() - pressed; watch.disconnect(); } });
      watch.observe(document.body, { subtree: true, childList: true, characterData: true });
    }, { capture: true, once: true });
  }, text);
  await field(page).press("Enter");
  await expect.poll(() => page.evaluate(() => (window as unknown as { nextFrame?: boolean }).nextFrame)).toBe(true);
  return page.evaluate(() => (window as unknown as { drawnAfter?: number }).drawnAfter ?? -1);
}

const bubble = (page: Page, text: string) => page.locator(".queued-message.pane").filter({ hasText: text });

for (const harness of ["claude", "codex", "pi"]) {
  test(`${harness}: a message sent while it works shows at once at the bottom, queued, with Edit, Send now and Cancel, and lands once as sent`, async ({ page }, testInfo) => {
    // Arrange
    const { relay, posts } = await sendWhileWorking(page, harness);
    await field(page).fill("check the flaky test first");

    // Act
    const drawnAfter = await pressEnter(page, "check the flaky test first");

    // Assert: his bubble is on screen within one frame of Enter, at the very
    // bottom of the conversation, with its three actions, and nothing was
    // typed into the turn.
    await testInfo.attach("enter-to-bubble", { body: harness + ": " + drawnAfter.toFixed(1) + " ms" });
    const shown = bubble(page, "check the flaky test first");
    await expect(shown).toHaveCount(1);
    expect(await shown.evaluate((pane) => pane === [...document.querySelectorAll(".messages-view:not([hidden]) [data-message], .messages-view:not([hidden]) .queued-message.pane")].at(-1)), "pinned at the bottom").toBe(true);
    await expect.poll(() => posts.length).toBe(1);
    expect(posts[0]).toMatchObject({ kind: "message", text: "check the flaky test first" });
    expect(typed(relay), "nothing typed into the turn").toBe("");
    await expect(field(page)).toHaveValue("");

    // Act: the board carries it as its own queued action.
    const action = { id: posts[0].id, kind: "message", task_id: GOBLIN.id, generation: "", status: "queued", text: "check the flaky test first", message: "Queued until " + GOBLIN.id + "'s turn ends.", can_send_now: true, updated_at: at };
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, cfo_harness: harness, actions: [action] }, { harness }));

    // Assert: still one bubble, now with its actions live.
    await expect(shown).toHaveCount(1);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(shown.getByRole("button", { name, exact: true })).toBeEnabled();
    await shown.getByRole("button", { name: "Send now", exact: true }).click();
    await expect.poll(() => posts.map((post) => post.kind)).toEqual(["message", "message_now"]);
    expect(posts[1].target).toBe(posts[0].id);

    // Act: the harness takes it; the board says sent and its record shows it.
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 3, cfo_harness: harness, actions: [{ ...action, status: "succeeded", message: "Accepted by the goblin in its native terminal." }] }, { harness }));
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })), { ...PAGE, messages: [...PAGE.messages, { id: "m2", seq: 2, at, kind: "prompt", source: "overlord", text: "check the flaky test first" }] });

    // Assert: it shows once, as his sent message.
    const conversation = page.getByRole("log");
    await expect(conversation.getByText("check the flaky test first", { exact: true })).toHaveCount(1);
    await expect(bubble(page, "check the flaky test first")).toHaveCount(0);
  });
}

test("Cancel takes a queued message back, so it never reaches the harness", async ({ page }) => {
  // Arrange
  const { relay, posts } = await sendWhileWorking(page, "claude");
  await field(page).fill("never mind that");
  await field(page).press("Enter");
  await expect.poll(() => posts.length).toBe(1);
  const action = { id: posts[0].id, kind: "message", task_id: GOBLIN.id, generation: "", status: "queued", text: "never mind that", message: "Queued until its turn ends.", can_send_now: true, updated_at: at };
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, actions: [action] }));

  // Act
  await bubble(page, "never mind that").getByRole("button", { name: "Cancel", exact: true }).click();

  // Assert
  await expect.poll(() => posts.map((post) => post.kind)).toEqual(["message", "message_withdraw"]);
  expect(posts[1].target).toBe(posts[0].id);
  expect(typed(relay)).toBe("");
});

// A message to the CFO names no goblin, so its Cancel names the message by
// its own identity alone, and the bubble goes once the board takes it back.
test("Cancel takes a message held for the CFO's turn back, and its bubble goes", async ({ page }) => {
  // Arrange: the CFO works, and a message he sent it waits for its turn.
  const posts: (Posted & { task: string })[] = [];
  const relay = await open(page);
  await page.route("**/api/actions", (route) => {
    const body = route.request().postDataJSON() as Partial<Posted> & { task_id?: string };
    posts.push({ id: body.id ?? "", kind: body.kind ?? "", target: body.target ?? "", text: body.text ?? "", task: body.task_id ?? "" });
    return route.fulfill({ status: 202, json: { status: body.kind === "message_withdraw" ? "succeeded" : "queued" } });
  });
  await page.keyboard.press("Control+Alt+1");
  await expect(page.locator("#panel-title")).toHaveText("CFO");
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  relay.say({ type: "screen", asking: false, open: false, working: true });
  await expect(page.locator(".reply-working")).toBeVisible();
  await field(page, "CFO").fill("stop and wait for me");
  await field(page, "CFO").press("Enter");
  await expect.poll(() => posts.length).toBe(1);
  const action = { id: posts[0].id, kind: "message", task_id: "", generation: "", status: "queued", text: "stop and wait for me", message: "Queued until the CFO's turn ends.", can_send_now: true, updated_at: at };
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, actions: [action] }));
  const shown = bubble(page, "stop and wait for me");
  await expect(shown).toHaveCount(1);

  // Act
  await shown.getByRole("button", { name: "Cancel", exact: true }).click();

  // Assert: the withdrawal names the CFO's held message, nothing reached its
  // terminal, and once the board has taken it back the bubble goes.
  await expect.poll(() => posts.map((post) => post.kind)).toEqual(["message", "message_withdraw"]);
  expect(posts[1]).toMatchObject({ task: "", target: posts[0].id, text: "stop and wait for me" });
  await expect(shown.locator(".queued-problem")).toHaveCount(0);
  expect(typed(relay)).toBe("");
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 3, actions: [] }));
  await expect(shown).toHaveCount(0);
});

// The board looks at a held message again about every second, marking it
// running while it reads the session's screen, and holds it once more: until
// it awaits the turn it is not typed, so it keeps waiting with its actions.
test("a held message the board looks at again keeps waiting with Edit, Send now and Cancel", async ({ page }) => {
  // Arrange
  const { posts } = await sendWhileWorking(page, "claude");
  await field(page).fill("hold this one");
  await field(page).press("Enter");
  await expect.poll(() => posts.length).toBe(1);
  const action = { id: posts[0].id, kind: "message", task_id: GOBLIN.id, generation: "", status: "queued", text: "hold this one", message: "Queued until " + GOBLIN.id + "'s turn ends.", can_send_now: true, updated_at: at };
  const show = async (revision: number, status: string) => {
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision, actions: [{ ...action, status }] }));
    await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  };
  await show(2, "queued");
  const shown = bubble(page, "hold this one");
  for (const name of ["Send now", "Edit", "Cancel"]) await expect(shown.getByRole("button", { name, exact: true })).toBeEnabled();

  for (const [revision, status] of [[3, "running"], [4, "queued"]] as const) {
    // Act
    await show(revision, status);

    // Assert
    await expect(shown.locator(".queued-state")).toHaveAttribute("aria-label", action.message);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(shown.getByRole("button", { name, exact: true }), name + " while " + status).toBeEnabled();
  }
});

test("Edit changes a queued message's words before it is sent", async ({ page }) => {
  // Arrange
  const { posts } = await sendWhileWorking(page, "pi");
  await field(page).fill("rebase onto main");
  await field(page).press("Enter");
  await expect.poll(() => posts.length).toBe(1);
  const action = { id: posts[0].id, kind: "message", task_id: GOBLIN.id, generation: "", status: "queued", text: "rebase onto main", message: "Queued until its turn ends.", can_send_now: true, updated_at: at };
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, cfo_harness: "pi", actions: [action] }, { harness: "pi" }));

  // Act
  const shown = bubble(page, "rebase onto main");
  await shown.getByRole("button", { name: "Edit", exact: true }).click();
  await shown.getByRole("textbox", { name: "Edit message: rebase onto main" }).fill("rebase onto main, then push");
  await shown.getByRole("button", { name: "Save", exact: true }).click();

  // Assert
  await expect.poll(() => posts.map((post) => post.kind)).toEqual(["message", "message_edit"]);
  expect(posts[1]).toMatchObject({ target: posts[0].id, text: "rebase onto main, then push" });
});

// The Overlord, 2026-10-28: "it's sending and appending messages together
// that were sent previously". A message typed into a busy harness stayed on
// its prompt line unsent, and the next one joined it there. Each message he
// sends while the session works, or while a screen of its own is open that
// is no question, is its own queued message, in order, and nothing is typed.
for (const harness of ["claude", "codex", "pi"]) {
  test(`${harness}: three messages sent while it works are three queued messages, in order, with nothing typed`, async ({ page }) => {
    // Arrange
    const { relay, posts } = await sendWhileWorking(page, harness);

    // Act
    for (const text of ["first, check the train", "second, merge it", "third, tell me"]) {
      await field(page).fill(text);
      await field(page).press("Enter");
      await expect(field(page)).toHaveValue("");
    }

    // Assert
    await expect.poll(() => posts.map((post) => post.kind + ":" + post.text)).toEqual(["message:first, check the train", "message:second, merge it", "message:third, tell me"]);
    expect(new Set(posts.map((post) => post.id)).size, "each its own message").toBe(3);
    expect(typed(relay), "nothing typed onto the prompt line").toBe("");
  });
}

test("a screen of the harness's own that is no question, such as a usage limit's, holds a message in the queue and types nothing", async ({ page }) => {
  // Arrange: Claude Code's own screen at its limit, its words and a way out.
  const posts: Posted[] = [];
  const relay = await open(page, snapshot({}, { phase: "settled" }));
  await page.route("**/api/actions", (route) => {
    const body = route.request().postDataJSON() as Partial<Posted>;
    posts.push({ id: body.id ?? "", kind: body.kind ?? "", target: body.target ?? "", text: body.text ?? "" });
    return route.fulfill({ status: 202, json: { status: "queued" } });
  });
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: true, working: false, panel: { rows: ["⚠ Usage limit reached · limit resets 10pm"], choices: [], hints: ["Continuing automatically at 10pm · Esc"] } });

  // Act
  for (const text of ["one", "two"]) {
    await field(page).fill(text);
    await field(page).press("Enter");
    await expect(field(page)).toHaveValue("");
  }

  // Assert
  await expect.poll(() => posts.map((post) => post.text)).toEqual(["one", "two"]);
  expect(typed(relay)).toBe("");
});

// Words left on the session's prompt line, typed in its terminal or put back
// when it was stopped, would join anything typed now and go in with it as one
// message (the Overlord, 2026-10-28: "it's sending and appending messages
// together that were sent previously"), so the box holds the message in the
// queue and types nothing onto them.
test("a prompt line holding words never sent holds a message in the queue and types nothing", async ({ page }) => {
  // Arrange
  const posts: Posted[] = [];
  const relay = await open(page, snapshot({}, { phase: "settled" }));
  await page.route("**/api/actions", (route) => {
    const body = route.request().postDataJSON() as Partial<Posted>;
    posts.push({ id: body.id ?? "", kind: body.kind ?? "", target: body.target ?? "", text: body.text ?? "" });
    return route.fulfill({ status: 202, json: { status: "queued" } });
  });
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false, submission: "", draft: true });

  // Act
  await field(page).fill("merge the train");
  await field(page).press("Enter");

  // Assert
  await expect.poll(() => posts.map((post) => post.text)).toEqual(["merge the train"]);
  await expect(field(page)).toHaveValue("");
  expect(typed(relay)).toBe("");
});

// A Classic box beside the terminal, which has no conversation, at a screen
// of the harness's own that is no question, such as its usage limit's, saves
// the message through the board's queue it lists, as it does while held: it
// types nothing, and never says the live terminal is reconnecting.
test("a Classic box at a usage limit's screen queues a message through the board and types nothing", async ({ page }) => {
  // Arrange: a hold shows the box beside Zeke's Classic terminal, and letting
  // go leaves it there.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
  const posts: Posted[] = [];
  const relay = await open(page);
  await page.route("**/api/actions", (route) => {
    const body = route.request().postDataJSON() as Partial<Posted>;
    posts.push({ id: body.id ?? "", kind: body.kind ?? "", target: body.target ?? "", text: body.text ?? "" });
    return route.fulfill({ status: 202, json: { status: "queued" } });
  });
  await card(page, NAME).click();
  await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
  relay.say({ type: "held", reason: "pipeline owns this task" });
  await expect(field(page)).toBeVisible();
  relay.say({ type: "held", reason: "" });
  await expect(box(page).locator(".composer-note")).toHaveText("");
  relay.say({ type: "screen", asking: false, open: true, working: false, panel: { rows: ["⚠ Usage limit reached · limit resets 10pm"], choices: [], hints: ["Continuing automatically at 10pm · Esc"] } });
  const typedBefore = typed(relay);
  const submittedBefore = relay.submissions.length;

  // Act
  await field(page).fill("continue");
  await field(page).press("Enter");

  // Assert
  await expect.poll(() => posts.map((post) => post.kind + ":" + post.text)).toEqual(["message:continue"]);
  await expect(field(page)).toHaveValue("");
  await expect(box(page).locator(".composer-refusal")).toHaveCount(0);
  expect(typed(relay), "nothing typed into its screen").toBe(typedBefore);
  expect(relay.submissions.length, "nothing submitted to its screen").toBe(submittedBefore);
});
