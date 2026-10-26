import { expect, test, type Page } from "./site";
import { GOBLIN, open, openGoblin, snapshot } from "./session-board";

// The Overlord, 2026-10-26: "I can't see cued messages. There should be a way
// to see what messages are cue in the chat pane" and "Does this seem to be a
// way to like send now or edit the cue messages either or delete them?".
// A message the board holds for delivery shows in the conversation with the
// reason it waits, and its three icon actions; the queue control offers them
// too, and nothing about delivery changes.

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
  await expect(entry(page).getByRole("button", { name: "Delete" })).toBeVisible();
  // Icon-only: none of the three carries words of its own.
  for (const name of ["Send now", "Edit", "Delete"]) await expect(entry(page).getByRole("button", { name })).toHaveText("");
  // The queue control is a quiet count that reveals the entry, not a second
  // copy of its words.
  await expect(page.getByRole("button", { name: "1 queued message", exact: true })).toBeVisible();
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
  await entry(page).getByRole("button", { name: "Delete" }).click();

  // Assert
  await expect.poll(() => posts).toContainEqual({ kind: "message_withdraw", target: "q1", text: "hold the merge until the gate lets go" });
});

test("a message already typed says the turn takes it and offers no Send now", async ({ page }) => {
  // Arrange
  await openWith(page, queued({ status: "running", awaiting: { task: "cg-touch" }, message: "Typed while it was working; it takes the message at its next tool call, or as its current turn ends." }));

  // Assert
  await expect(entry(page).locator(".queued-state")).toBeVisible();
  await expect(entry(page).getByRole("button", { name: "Send now" })).toHaveCount(0);
  await expect(entry(page).getByRole("button", { name: "Delete" })).toBeVisible();
});
