import { expect, test, type Page } from "./site";
import { at, box, field, GOBLIN, open, openGoblin, PAGE, snapshot, typed } from "./session-board";

// The Overlord, 2026-10-26: "I can't see cued messages. There should be a way
// to see what messages are cue in the chat pane" and "Does this seem to be a
// way to like send now or edit the cue messages either or delete them?".
// A message the board holds for delivery shows under the conversation with
// the reason it waits, and its three icon actions, and nothing about delivery
// changes. Since 2026-10-28 it is pinned right above the box, the box holds
// no queue control, and Up in the empty box takes the newest back.

const queued = (fields: Record<string, unknown> = {}) => ({
  id: "q1", kind: "message", task_id: GOBLIN.id, generation: GOBLIN.generation, status: "queued",
  text: "hold the merge until the gate lets go", message: "Queued for " + GOBLIN.id + ". Its gate holds the branch, so it is delivered once the gate lets go.",
  can_send_now: false, send_now_why: "Its gate holds the branch.", updated_at: "2026-10-22T09:00:00Z", ...fields,
});

async function openWith(page: Page, action: object) {
  const posts: { kind: string; target: string; text: string }[] = [];
  await open(page, snapshot({ actions: [action] }));
  await page.route("**/api/actions", (route) => {
    const body = route.request().postDataJSON() as { kind: string; target?: string; text?: string };
    posts.push({ kind: body.kind, target: body.target ?? "", text: body.text ?? "" });
    return route.fulfill({ status: 202, json: { status: "ok" } });
  });
  await openGoblin(page);
  return posts;
}

const entry = (page: Page) => page.locator(".queued-message.pane");

test("a queued message shows in the conversation with what it waits on, and its actions are icons", async ({ page }) => {
  // Arrange
  await openWith(page, queued());

  // Assert
  await expect(entry(page)).toBeVisible();
  await expect(entry(page)).toContainText("hold the merge until the gate lets go");
  // The entry says it is queued with its icon and its dotted outline, not a
  // line of words; the reason is its tip and its accessible name.
  await expect(entry(page).locator(".queued-state")).toBeVisible();
  await expect(entry(page).locator(".queued-state")).toHaveAttribute("data-tip", /Its gate holds the branch/);
  const send = entry(page).getByRole("button", { name: "Send now" });
  await expect(send).toBeVisible();
  await expect(send).toBeDisabled();
  await expect(send).toHaveAttribute("data-tip", /Its gate holds the branch/);
  await expect(entry(page).getByRole("button", { name: "Edit" })).toBeVisible();
  await expect(entry(page).getByRole("button", { name: "Cancel" })).toBeVisible();
  // Icon-only: none of the three carries words of its own.
  for (const name of ["Send now", "Edit", "Cancel"]) await expect(entry(page).getByRole("button", { name })).toHaveText("");
  // The box holds no queue control and no second copy of its words.
  await expect(box(page).getByRole("button", { name: /queued message/ })).toHaveCount(0);
  await expect(box(page)).not.toContainText("hold the merge");
});

test("Edit changes the words that will be sent, in place", async ({ page }) => {
  // Arrange
  const posts = await openWith(page, queued());

  // Act
  await entry(page).getByRole("button", { name: "Edit" }).click();
  const editor = entry(page).getByRole("textbox", { name: /^Edit message: / });
  await editor.fill("discard this edit");
  await entry(page).getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(entry(page)).toContainText("hold the merge until the gate lets go");
  expect(posts).toEqual([]);
  await entry(page).getByRole("button", { name: "Edit", exact: true }).click();
  await editor.fill("hold the merge for one more check");
  await entry(page).getByRole("button", { name: "Save" }).click();

  // Assert
  await expect.poll(() => posts).toContainEqual({ kind: "message_edit", target: "q1", text: "hold the merge for one more check" });
});

test("Send now asks for the message at the next safe moment when it can, and Delete withdraws it", async ({ page }) => {
  // Arrange
  const posts = await openWith(page, queued({ can_send_now: true, send_now_why: "" }));

  // Act: the ready message asks to go now.
  await entry(page).getByRole("button", { name: "Send now" }).click();
  await expect.poll(() => posts).toContainEqual({ kind: "message_now", target: "q1", text: "" });

  // Act: and it is withdrawn by its own row.
  await entry(page).getByRole("button", { name: "Cancel" }).click();

  // Assert
  await expect.poll(() => posts).toContainEqual({ kind: "message_withdraw", target: "q1", text: "hold the merge until the gate lets go" });
});

test("a message already typed says the turn takes it and offers no Send now", async ({ page }) => {
  // Arrange
  await openWith(page, queued({ status: "running", awaiting: { task: "cg-touch" }, message: "Typed while it was working; it takes the message at its next tool call, or as its current turn ends." }));

  // Assert
  await expect(entry(page).locator(".queued-state")).toBeVisible();
  await expect(entry(page).getByRole("button", { name: "Send now" })).toHaveCount(0);
  await expect(entry(page).getByRole("button", { name: "Cancel" })).toBeVisible();
});

const KEPT = "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-1.png";
// controls reads a queued entry's buttons as they are drawn: name, look and
// icon, in order.
const controls = (entry: ReturnType<Page["locator"]>) => entry.locator(".queued-actions button").evaluateAll((buttons) =>
  buttons.map((button) => [button.getAttribute("aria-label"), button.className, button.querySelector("svg")?.innerHTML ?? ""]));

// The Overlord, 2026-10-28: "Why don't the queue controls match? What happens
// in the goblin with the icons for like send edit cancel? In CFO."
test("a queued message to the CFO has the same three icons, in the same order and drawn the same, as one to a goblin", async ({ page }) => {
  // Arrange
  await openWith(page, queued());
  const toGoblin = await controls(entry(page));
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })),
    snapshot({ revision: 2, actions: [queued(), queued({ id: "c1", task_id: "", generation: "", text: "wait for me", message: "Queued until the CFO's turn ends." })] }));

  // Act
  await page.keyboard.press("Control+Alt+1");
  await expect(page.locator("#panel-title")).toHaveText("CFO");

  // Assert
  const cfo = page.locator(".queued-message.pane").filter({ hasText: "wait for me" });
  await expect(cfo).toHaveCount(1);
  expect(toGoblin.map(([name]) => name)).toEqual(["Send now", "Edit", "Cancel"]);
  expect(await controls(cfo)).toEqual(toGoblin);
});

// The Overlord, 2026-10-28: "if I hit the up arrow when there's a queued
// message, it'll pull that queued message back into my chat composer
// automatically to then edit and resend it, taking it off the queue."
test("Up in the empty box takes the newest queued message back into the box, its picture included, and off the queue", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({ actions: [queued(), queued({ id: "q2", text: "take this back\n" + KEPT, updated_at: "2026-10-22T09:01:00Z" })] }));
  const posts: { kind: string; target: string }[] = [];
  await page.route("**/api/actions", (route) => {
    const body = route.request().postDataJSON() as { kind: string; target?: string };
    posts.push({ kind: body.kind, target: body.target ?? "" });
    return route.fulfill({ status: 202, json: { status: "succeeded" } });
  });
  await openGoblin(page);
  await expect(entry(page)).toHaveCount(2);

  // Act
  await field(page).focus();
  await page.keyboard.press("ArrowUp");

  // Assert: the newest is withdrawn by its own row, and its words and its
  // picture are back in the box to edit.
  await expect.poll(() => posts).toEqual([{ kind: "message_withdraw", target: "q2" }]);
  await expect(field(page)).toHaveValue("take this back");
  await expect(box(page).getByRole("list", { name: "Attached files" })).toContainText("pasted-1.png");

  // Act: the board has taken it back.
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })),
    snapshot({ revision: 2, actions: [queued(), queued({ id: "q2", status: "succeeded", text: "take this back\n" + KEPT })] }));

  // Assert: only the older one waits, and nothing reached the terminal.
  await expect(entry(page)).toHaveCount(1);
  await expect(entry(page)).toContainText("hold the merge until the gate lets go");
  expect(typed(relay)).toBe("");

  // Act: Up with words in the box takes nothing more back.
  await page.keyboard.press("ArrowUp");

  // Assert
  expect(posts).toHaveLength(1);
});

// The Overlord, 2026-10-28: "see how claude includes pictures when pasted".
test("a queued message shows the picture the box kept above its bubble, never its path", async ({ page }) => {
  // Arrange and Act
  await openWith(page, queued({ text: "what is wrong here?\n" + KEPT }));

  // Assert
  const picture = entry(page).locator(".message-picture img");
  await expect(picture).toHaveCount(1);
  await expect(entry(page).locator(".queued-bubble")).toHaveText("what is wrong here?");
  await expect(page.locator(".messages-view")).not.toContainText("composer-files");
  expect(await entry(page).evaluate((element) => {
    const picture = element.querySelector(".message-pictures")?.getBoundingClientRect(), bubble = element.querySelector(".queued-bubble")?.getBoundingClientRect();
    return !!picture && !!bubble && picture.bottom <= bubble.top;
  }), "the picture sits above the bubble").toBe(true);
});

// The Overlord, 2026-10-28: "as it's working and going through stuff, it
// should always stay at the bottom between the agent's chat response and the
// chat box, so I could always see it and control it."
test("a queued message stays directly above the box with its three icons in view while twenty tool rows stream after it", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 1280, height: 720 });
  const relay = await open(page, snapshot({ actions: [queued()] }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: true });
  const pinned = entry(page);
  await expect(pinned).toBeVisible();

  // Act: the session goes on through twenty tool calls.
  const rows = Array.from({ length: 20 }, (_, seq) => ({ id: "t" + seq, seq: 10 + seq, at, kind: "reply", blocks: [{ kind: "call", tool: "Bash", subject: "npm test -- step " + seq, state: "done", result: "ok\n".repeat(6) }] }));
  await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })), { ...PAGE, messages: [...PAGE.messages, ...rows] });
  await expect(page.getByRole("log").locator('[data-message="t19"]')).toHaveCount(1);

  // Assert: still in view, its icons too, right above the box.
  await expect(pinned).toBeInViewport();
  for (const name of ["Send now", "Edit", "Cancel"]) await expect(pinned.getByRole("button", { name, exact: true })).toBeInViewport();
  const gap = await page.evaluate(() => {
    const queue = document.querySelector(".conversation-queue")?.getBoundingClientRect(), dock = document.querySelector(".composer-dock")?.getBoundingClientRect();
    return queue && dock ? dock.top - queue.bottom : -1;
  });
  expect(gap).toBeGreaterThanOrEqual(0);
  expect(gap).toBeLessThan(24);
});
