import { expect, test, type Page } from "./site";
import { at, field, GOBLIN, open, openGoblin, PAGE, snapshot, typed } from "./session-board";

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
  await expect(page.locator(".session-working")).toBeVisible();
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
