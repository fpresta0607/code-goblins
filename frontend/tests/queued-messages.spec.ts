import { expect, test, type Page } from "./site";
import { at, box, field, GOBLIN, open, openGoblin, PAGE, pasted, snapshot, typed } from "./session-board";

// The Overlord, 2026-10-26: "I can't see cued messages. There should be a way
// to see what messages are cue in the chat pane" and "Does this seem to be a
// way to like send now or edit the cue messages either or delete them?".
// A message the board holds for delivery shows at the conversation's end with
// the reason it waits, and its three icon actions, and nothing about delivery
// changes. The box holds no queue control, and Up in the empty box takes the
// newest back.

const queued = (fields: Record<string, unknown> = {}) => ({
  id: "q1", kind: "message", task_id: GOBLIN.id, generation: GOBLIN.generation, status: "queued",
  text: "hold the merge until the gate lets go", message: "Queued for " + GOBLIN.id + ". Its gate holds the branch, so it is delivered once the gate lets go.",
  can_send_now: false, send_now_why: "Its gate holds the branch.", created_at: "2026-10-22T09:00:00Z", updated_at: "2026-10-22T09:00:00Z", ...fields,
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
const log = (page: Page) => page.getByRole("log");
const WAITING = "Queued";
// A row of his says nothing under its bubble once the harness has taken it;
// its date, time and Copy are in the line that shows on a hover or a tap (the
// Overlord, 2026-10-30: "we dont need read 8:26 only and th copy tomestamp
// hover line").
const unlabelled = (row: ReturnType<Page["locator"]>) => expect(row.locator(":scope > p")).toHaveCount(0);

// The Overlord, 2026-10-29: "the user can't really tell what's been sent and
// what's getting sent. And the double scrolls and having its own little
// scroll for queued messages doesn't really work." One scroll, the chat's: a
// message the board holds is his own bubble at the chat's end, dimmed, with
// Queued and its controls under it (the Overlord, 2026-10-30: "id ratehr it
// just say qeued"); typed into its session, it is solid with Sent and no
// controls (the CEO, 2026-11-02); and it turns solid with no label in place
// once the harness takes it, as its record shows. A goblin's record reads
// the stamps the board types his words behind as his, so its row is his own
// words, as a CEO's is.
test.describe("his messages in the chat's one scroll", () => {
  const LONG = Array.from({ length: 30 }, (_, line) => "line " + (line + 1) + " of the plan he wrote while it worked").join("\n");

  test("a message he typed while the session works waits dimmed at the chat's end, whole, with its controls and no scroll of its own", async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({ actions: [queued({ text: LONG })] }));
    await openGoblin(page);

    // Act
    relay.say({ type: "screen", asking: false, open: false, working: true });

    // Assert: in the conversation's own list, after everything else in it.
    const waiting = log(page).locator(".queued-message.pane");
    await expect(waiting).toBeVisible();
    expect(await waiting.evaluate((element) => element === [...element.closest("[role=log]")!.querySelectorAll("[data-message], .queued-message.pane, .reply-working")].at(-1))).toBe(true);
    await expect(waiting.locator(".queued-state")).toHaveText(WAITING);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(waiting.getByRole("button", { name, exact: true })).toBeVisible();
    expect(Number(await waiting.locator(".queued-bubble").evaluate((element) => getComputedStyle(element).opacity))).toBeLessThan(1);
    // Nothing in the view scrolls but the conversation.
    const scrolls = await page.locator(".messages-view:not([hidden])").evaluate((view) => [...view.querySelectorAll("*")]
      .filter((element) => ["auto", "scroll"].includes(getComputedStyle(element).overflowY) && element.scrollHeight > element.clientHeight + 1)
      .map((element) => element.getAttribute("role") || element.className));
    expect(scrolls).toEqual(["log"]);
  });

  test("taken by the harness, his waiting message turns solid with no label, its time and Copy in its hover line, its controls gone, shown once", async ({ page }) => {
    // Arrange
    const words = "check the deploy before you merge";
    const relay = await open(page, snapshot({ actions: [queued({ text: words })] }));
    await openGoblin(page);
    relay.say({ type: "screen", asking: false, open: false, working: true });
    await expect(log(page).locator(".queued-message.pane")).toBeVisible();

    // Act: the harness takes it at its next tool call, and its record says
    // so; the board learns it a moment later.
    const taken = "2026-10-22T09:05:00Z";
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
      { ...PAGE, messages: [...PAGE.messages, { id: "m9", seq: 9, at: taken, kind: "prompt", source: "overlord", queued: true, text: words }] });

    // Assert: his one bubble, solid, with no label under it and nothing to
    // do with it but what any message of his has.
    const mine = log(page).locator(".message-prompt", { hasText: words });
    await expect(mine).toHaveCount(1);
    await expect(log(page).getByText(words, { exact: true })).toHaveCount(1);
    await unlabelled(mine);
    await mine.hover();
    await expect(mine.locator(".message-actions time")).toHaveAttribute("datetime", taken);
    await expect(mine.locator(".message-actions").getByRole("button", { name: "Copy", exact: true })).toBeVisible();
    await expect(entry(page)).toHaveCount(0);

    // Act and Assert: the board's own record catches up, and nothing changes.
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, actions: [queued({ text: words, status: "succeeded", updated_at: "2026-10-22T09:06:00Z" })] }));
    await expect(log(page).getByText(words, { exact: true })).toHaveCount(1);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(page.locator(".messages-view:not([hidden])").getByRole("button", { name, exact: true })).toHaveCount(0);
  });

  // Typed, it is its harness's to take, whether the session works or not, so
  // Edit and Cancel go rather than fail (the Overlord, 2026-11-02: "delay
  // here when I try to cancel; if it's actually picked up that's something
  // that should be reflected").
  for (const isWorking of [false, true]) {
    test(`a message typed into ${isWorking ? "a working" : "an idle"} session is solid with Sent and no controls, and has no label once its record shows it`, async ({ page }) => {
      // Arrange
      const words = "start on the invoices";
      const relay = await open(page, snapshot({ actions: [queued({ text: words, status: "running", message: "Typed into the terminal.", awaiting: { task: GOBLIN.id, since: "2026-10-22T09:01:00Z" }, created_at: "2026-10-22T09:01:00Z" })] }, { phase: "settled" }));
      await openGoblin(page);

      // Act
      relay.say({ type: "screen", asking: false, open: false, working: isWorking });

      // Assert: on its way, out of his hands.
      await expect(entry(page).locator(".queued-state")).toHaveText("Sent");
      expect(Number(await entry(page).locator(".queued-bubble").evaluate((element) => getComputedStyle(element).opacity))).toBe(1);
      await expect(entry(page).locator(".queued-actions")).toHaveCount(0);

      // Act: the harness takes it.
      await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
        { ...PAGE, messages: [...PAGE.messages, { id: "m9", seq: 9, at: "2026-10-22T09:05:00Z", kind: "prompt", source: "overlord", text: words }] });

      // Assert
      await unlabelled(log(page).locator(".message-prompt", { hasText: words }));
      await expect(entry(page)).toHaveCount(0);
      await expect(log(page).getByText(words, { exact: true })).toHaveCount(1);
    });
  }

  // His record already holds "Read the brief" from 09:00. Sent again later,
  // even five milliseconds later, or still on its way with no time of its
  // own, it is a new message.
  for (const [created, when] of [["2026-10-22T09:10:00Z", ""], ["2026-10-22T09:00:00.005Z", " five milliseconds later"], ["", " while it reaches the board"]]) {
    test(`a message of the same words as one the session read earlier still waits${when}`, async ({ page }) => {
      // Arrange
      const relay = await open(page, snapshot({ actions: [queued({ text: "Read the brief", created_at: created, updated_at: created })] }));
      await openGoblin(page);

      // Act
      relay.say({ type: "screen", asking: false, open: false, working: true });

      // Assert
      await expect(entry(page).locator(".queued-state")).toHaveText(WAITING);
      await expect(log(page).getByText("Read the brief", { exact: true })).toHaveCount(2);
    });
  }

  // Two messages of the same words are two messages: the record's one row
  // reads the first, whether the board still has it on its way or has settled
  // it, and the second waits for a row of its own. The first was typed into
  // the working goblin; its gate then took the branch and holds the second.
  test("one record row reads one of two messages of the same words, and the other keeps waiting with its controls once the first settles", async ({ page }) => {
    // Arrange
    const words = "yes, go ahead";
    const first = queued({ id: "q1", text: words, status: "running", message: "Typed into the terminal.", awaiting: { task: GOBLIN.id, since: "2026-10-22T09:01:00Z" }, created_at: "2026-10-22T09:01:00Z" });
    const second = queued({ id: "q2", text: words, created_at: "2026-10-22T09:02:00Z" });
    const relay = await open(page, snapshot({ actions: [first, second] }));
    await openGoblin(page);
    relay.say({ type: "screen", asking: false, open: false, working: true });
    await expect(entry(page)).toHaveCount(2);

    // Act: the harness takes the first.
    const taken = "2026-10-22T09:05:00Z";
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
      { ...PAGE, messages: [...PAGE.messages, { id: "m9", seq: 9, at: taken, kind: "prompt", source: "overlord", queued: true, text: words }] });

    // Assert
    await unlabelled(log(page).locator(".message-prompt", { hasText: words }));
    await expect(entry(page)).toHaveCount(1);
    await expect(entry(page).locator(".queued-state")).toHaveText(WAITING);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(entry(page).getByRole("button", { name, exact: true })).toBeVisible();

    // Act: the board sees the take and settles the first.
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, actions: [{ ...first, status: "succeeded", awaiting: undefined, updated_at: "2026-10-22T09:06:00Z" }, second] }));

    // Assert: the row is still the first's, and the second still waits.
    await expect(page.locator(".queued-message.pane[data-queued=q1]")).toHaveCount(0);
    await expect(page.locator(".queued-message.pane[data-queued=q2]")).toBeVisible();
    await expect(entry(page).locator(".queued-state")).toHaveText(WAITING);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(entry(page).getByRole("button", { name, exact: true })).toBeVisible();
    await expect(log(page).getByText(words, { exact: true })).toHaveCount(2);
  });

  // An older message of the same words the board settled once the harness
  // took it had its row then, on an earlier page the conversation has not
  // loaded; a row written since is the new message's.
  test("a row written after an older message of the same words settled reads the new one, whose own row is not loaded", async ({ page }) => {
    // Arrange
    const older = queued({ id: "q0", text: "yes", status: "succeeded", message: "Taken by the goblin, as its own record of the conversation shows.", created_at: "2026-10-21T08:00:00Z", updated_at: "2026-10-21T08:01:00Z" });
    const newer = queued({ id: "q1", text: "yes", status: "running", message: "Typed into the terminal.", awaiting: { task: GOBLIN.id, since: "2026-10-22T09:01:00Z" }, created_at: "2026-10-22T09:01:00Z", updated_at: "2026-10-22T09:01:00Z" });
    const relay = await open(page, snapshot({ actions: [older, newer] }));
    await openGoblin(page);
    relay.say({ type: "screen", asking: false, open: false, working: true });
    await expect(entry(page).locator(".queued-state")).toHaveText("Sent");

    // Act: the harness takes the new one.
    const taken = "2026-10-22T09:05:00Z";
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
      { ...PAGE, messages: [...PAGE.messages, { id: "m9", seq: 9, at: taken, kind: "prompt", source: "overlord", queued: true, text: "yes" }] });

    // Assert
    await unlabelled(log(page).locator(".message-prompt", { hasText: "yes" }));
    await expect(entry(page)).toHaveCount(0);
    await expect(log(page).getByText("yes", { exact: true })).toHaveCount(1);
  });

  // The record keeps a shell command he typed without its "!", as Claude Code
  // writes it, and the row shows it as he typed it, read as his words are.
  test("a shell command he typed while the session works is read by its command row, which has no label", async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({ actions: [queued({ text: "!git status", status: "running", message: "Typed into the terminal.", awaiting: { task: GOBLIN.id, since: "2026-10-22T09:01:00Z" }, created_at: "2026-10-22T09:01:00Z" })] }));
    await openGoblin(page);
    relay.say({ type: "screen", asking: false, open: false, working: true });
    await expect(entry(page)).toBeVisible();

    // Act
    const taken = "2026-10-22T09:05:00Z";
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
      { ...PAGE, messages: [...PAGE.messages, { id: "m9", seq: 9, at: taken, kind: "prompt", source: "shell", text: "git status", output: "On branch main" }] });

    // Assert
    await expect(log(page).locator(".message-prompt.shell")).toContainText("On branch main");
    await unlabelled(log(page).locator(".message-prompt.shell"));
    await expect(entry(page)).toHaveCount(0);
    await expect(log(page).getByText("!git status", { exact: true })).toHaveCount(1);
  });

  // A row the CEO typed into the goblin is the CEO's, even in his words.
  test("a later row of the CEO's in the same words does not read his waiting message", async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({ actions: [queued({ text: "merge main", created_at: "2026-10-22T09:01:00Z" })] }));
    await openGoblin(page);
    relay.say({ type: "screen", asking: false, open: false, working: true });
    await expect(entry(page)).toBeVisible();

    // Act: the CEO steers the goblin, "CEO: merge main".
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
      { ...PAGE, messages: [...PAGE.messages, { id: "m9", seq: 9, at: "2026-10-22T09:05:00Z", kind: "prompt", source: "ceo", text: "merge main" }] });

    // Assert
    await expect(log(page).locator(".message-prompt.ceo", { hasText: "merge main" })).toBeVisible();
    await expect(entry(page)).toBeVisible();
    await expect(entry(page).locator(".queued-state")).toHaveText(WAITING);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(entry(page).getByRole("button", { name, exact: true })).toBeVisible();
  });

  for (const [state, fields] of [
    ["waiting", {}],
    ["not sent", { status: "failed", message: "The terminal closed before it took it." }],
  ] as const) test(`a message ${state} in a conversation that holds nothing yet shows with no No messages yet over it`, async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({ actions: [queued(fields)] }));
    await openGoblin(page);
    relay.say({ type: "screen", asking: false, open: false, working: true });

    // Act: the session's record holds nothing.
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })), { messages: [], conversation: "empty-conversation", more: false });

    // Assert
    await expect(log(page).locator("[data-message]:not([data-queued])")).toHaveCount(0);
    await expect(entry(page)).toBeVisible();
    await expect(page.locator(".messages-view:not([hidden])").getByText("No messages yet", { exact: true })).toHaveCount(0);
  });

  // The Overlord, 2026-10-29: a message he typed for the CEO the day before
  // still sat as queued, and its Cancel said "that queued message is no
  // longer waiting, so nothing was deleted". The board typed it and still
  // watches for a late take, but it waits nowhere: it says it was never seen
  // taken, with Send again, never that it is still on its way (#2242).
  test("a message from an earlier day the harness was not seen taking is not waiting on load, with nothing to cancel", async ({ page }) => {
    // Arrange and Act
    const advice = "Your answer was typed for the goblin, which has not picked it up. Open its terminal and press Enter if your answer is waiting in its box; if it is not there, type it to the goblin.";
    await openWith(page, queued({ status: "uncertain", message: advice, advice, awaiting: { task: GOBLIN.id, since: "2026-10-21T22:04:16Z" } }));

    // Assert
    const view = page.locator(".messages-view:not([hidden])");
    const ended = view.locator(".queued-message.pane").filter({ hasText: "hold the merge" });
    await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
    await expect(ended).toContainText("Not confirmed");
    await expect(ended).not.toContainText(WAITING);
    await expect(ended.getByRole("button", { name: "Send again", exact: true })).toHaveCount(1);
    await expect(view.getByRole("button", { name: "Cancel", exact: true })).toHaveCount(0);
  });
});

test("a queued message says it waits under its bubble, with what it waits on as its description, and its actions as icons", async ({ page }) => {
  // Arrange
  await openWith(page, queued());

  // Assert
  await expect(entry(page)).toBeVisible();
  await expect(entry(page)).toContainText("hold the merge until the gate lets go");
  // The entry says it waits in a few words under its bubble, with no
  // outline, no clock and no tip (the Overlord, 2026-10-28: "remove the
  // dotted lines"); the reason is its description.
  const label = entry(page).locator(".queued-state");
  const bubble = entry(page).locator(".queued-bubble");
  await expect(label).toHaveText(WAITING);
  const [above, under] = await Promise.all([bubble.boundingBox(), label.boundingBox()]);
  expect(above && under && above.y + above.height <= under.y + 1, "the words under its bubble").toBe(true);
  await expect(bubble).toHaveAccessibleDescription(/Its gate holds the branch/);
  for (const part of [label, bubble]) expect(await part.evaluate((element) => getComputedStyle(element).borderTopStyle)).toBe("none");
  await expect(entry(page).locator("svg.icon:not(.queued-actions svg)")).toHaveCount(0);
  await expect(entry(page).locator("[data-tip]")).toHaveCount(0);
  const send = entry(page).getByRole("button", { name: "Send now" });
  await expect(send).toBeVisible();
  await expect(send).toBeEnabled();
  await expect(entry(page).getByRole("button", { name: "Edit" })).toBeVisible();
  await expect(entry(page).getByRole("button", { name: "Cancel" })).toBeVisible();
  // Icon-only: none of the three carries words of its own.
  for (const name of ["Send now", "Edit", "Cancel"]) await expect(entry(page).getByRole("button", { name })).toHaveText("");
  // The box holds no queue control and no second copy of its words.
  await expect(box(page).getByRole("button", { name: /queued message/ })).toHaveCount(0);
  await expect(box(page)).not.toContainText("hold the merge");
});

// The Overlord, 2026-10-28: Edit on a queued message pulls its words back
// into the box, as Up does, and takes it off the queue, with no little edit
// window inside the chat. Words he typed in the box since stay after them,
// and a second press takes nothing twice.
test("Edit takes a queued message back into the box and off the queue, before the words he has typed since", async ({ page }) => {
  // Arrange
  const posts = await openWith(page, queued());
  await field(page).fill("and push after");

  // Act
  await entry(page).getByRole("button", { name: "Edit", exact: true }).dblclick();

  // Assert
  await expect(field(page)).toHaveValue("hold the merge until the gate lets go\nand push after");
  await expect.poll(() => posts).toEqual([{ kind: "message_withdraw", target: "q1", text: "hold the merge until the gate lets go" }]);
  await expect(entry(page).getByRole("textbox")).toHaveCount(0);
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

// The Overlord, 2026-10-28: "lack of indmepotent click on teh queue". The
// board answers at once while the snapshot still shows the message queued,
// so each act must go once however often he presses it: Send now stays
// pressed, and a Cancel taken takes the actions away.
test("Send now and Cancel each reach the board once, however often he presses them", async ({ page }) => {
  // Arrange
  const posts = await openWith(page, queued({ can_send_now: true, send_now_why: "" }));
  const sendNow = entry(page).getByRole("button", { name: "Send now" });
  const count = (kind: string) => posts.filter((post) => post.kind === kind).length;

  // Act: Send now pressed, answered, then pressed again twice over.
  await sendNow.click();
  await expect.poll(() => count("message_now")).toBe(1);
  await sendNow.click({ force: true });
  await sendNow.dblclick({ force: true });
  // Cancel pressed twice in a row.
  await entry(page).getByRole("button", { name: "Cancel", exact: true }).dblclick();
  await expect.poll(() => count("message_withdraw")).toBe(1);

  // Assert
  await expect(sendNow).toHaveCount(0);
  await expect(entry(page).locator(".queued-actions")).toHaveCount(0);
  expect(await page.locator(".composer-banner").count()).toBe(0);
  expect(posts.map((post) => post.kind)).toEqual(["message_now", "message_withdraw"]);
});

// The Overlord, 2026-11-10: "queued messages in modern chat get stuck in input
// box in terminal chat and never send" (#2306). A message the board typed
// that its harness never took as submitted is taken back off the terminal's
// prompt line and reads Not sent on its bubble, why as its description. The
// board still holds it, so Send now sends that same message, never a copy,
// and Cancel takes it off the board, each once however often he presses.
const NOT_SENT = "Not sent. It was typed for " + GOBLIN.id + " and never submitted. It was taken back off its prompt line, so nothing of it is left typed there.";
for (const [press, kind, text] of [["Send now", "message_now", ""], ["Cancel", "message_withdraw", "hold the merge until the gate lets go"]] as const) {
  test(`a message that was not sent says so on its bubble, with Send now and Cancel, and ${press} reaches the board once`, async ({ page }) => {
    // Arrange
    const posts = await openWith(page, queued({ status: "failed", message: NOT_SENT, send_now_why: "" }));
    const bubble = entry(page);

    // Assert
    await expect(bubble.locator(".queued-state")).toHaveText("Not sent");
    await expect(bubble.locator(".queued-bubble")).toHaveAccessibleDescription(/taken back off its prompt line/);
    await expect(bubble.locator(".queued-actions button")).toHaveCount(2);
    for (const name of ["Send now", "Cancel"]) await expect(bubble.getByRole("button", { name, exact: true })).toBeVisible();

    // Act
    await bubble.getByRole("button", { name: press, exact: true }).dblclick();

    // Assert
    await expect.poll(() => posts).toEqual([{ kind, target: "q1", text }]);
  });
}

// A Send now he asked for is spent once the board has tried the message
// again, so one that was not sent a second time can be sent now once more
// rather than left with Cancel alone.
test("a message not sent again after its Send now can be sent now once more", async ({ page }) => {
  // Arrange
  const unsent = queued({ status: "failed", message: NOT_SENT, send_now_why: "" });
  const posts = await openWith(page, unsent);
  const sendNow = entry(page).getByRole("button", { name: "Send now", exact: true });
  const show = (revision: number, action: object) => page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision, actions: [action] }));
  await sendNow.click();
  await expect.poll(() => posts.length).toBe(1);

  // Act: the board queues it again, tries it, and it is not sent again.
  await show(2, queued({ can_send_now: true, send_now_why: "", message: "Queued.", updated_at: "2026-10-22T09:00:05Z" }));
  await expect(entry(page).locator(".queued-state")).toHaveText(WAITING);
  await show(3, { ...unsent, updated_at: "2026-10-22T09:00:40Z" });
  await expect(entry(page).locator(".queued-state")).toHaveText("Not sent");
  await sendNow.click();

  // Assert
  await expect.poll(() => posts).toEqual([{ kind: "message_now", target: "q1", text: "" }, { kind: "message_now", target: "q1", text: "" }]);
});

// The Overlord, 2026-11-10 (#2332): a message the harness still held in its
// own queue read Sent in Modern Chat on his phone, with nothing to press.
// Sent means the harness has taken it into a turn: one typed into a turn
// under way reads Queued, dimmed, with Send now, Edit and Cancel, each of
// which the board carries to the harness's own queue, and is his own row,
// with no label, once the harness takes it.
for (const { whose, task, width } of [
  { whose: "the CEO", task: "", width: 390 }, { whose: "a goblin", task: GOBLIN.id, width: 390 }, { whose: "a goblin", task: GOBLIN.id, width: 1440 },
]) {
  test(`at ${width} wide, his message in the queue of ${whose}'s harness reads Queued with its controls, each reaching the board`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 844 });
    const words = "check the phone layout too";
    const held = queued({
      id: "h1", task_id: task, generation: task ? GOBLIN.generation : "", text: words, status: "running", can_send_now: false, send_now_why: "",
      message: task ? "Queued in the goblin's session. The goblin takes it at its next tool call, or as its current turn ends." : "Queued in the CEO's session. The CEO takes it at its next tool call, or as its current turn ends.",
      awaiting: task ? { task, since: "2026-10-22T09:01:00Z", is_held: true } : { since: "2026-10-22T09:01:00Z", is_held: true }, created_at: "2026-10-22T09:01:00Z", updated_at: "2026-10-22T09:01:01Z",
    });
    const posts: { kind: string; target: string }[] = [];
    const relay = await open(page, snapshot({ actions: [held] }));
    await page.route("**/api/actions", (route) => {
      const body = route.request().postDataJSON() as { kind: string; target?: string };
      posts.push({ kind: body.kind, target: body.target ?? "" });
      return route.fulfill({ status: 202, json: { status: "ok" } });
    });
    if (task) await openGoblin(page);
    else {
      await page.keyboard.press("Control+Alt+1");
      await expect(page.locator("#panel-title")).toHaveText("CEO");
    }
    relay.say({ type: "screen", asking: false, open: false, working: true });
    const bubble = entry(page).filter({ hasText: words });

    // Assert: Queued, never Sent, and his to act on.
    await expect(bubble.locator(".queued-state")).toHaveText(WAITING);
    expect(Number(await bubble.locator(".queued-bubble").evaluate((element) => getComputedStyle(element).opacity))).toBeLessThan(1);
    await expect(bubble.locator(".queued-bubble")).toHaveAccessibleDescription(task ? /^Queued in the goblin's session\./ : /^Queued in the CEO's session\./);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(bubble.getByRole("button", { name, exact: true })).toBeEnabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    const shot = testInfo.outputPath(`harness-held-${task ? "goblin" : "ceo"}-${width}.png`);
    await page.screenshot({ path: shot });
    await testInfo.attach(`harness-held-${task ? "goblin" : "ceo"}-${width}`, { path: shot, contentType: "image/png" });

    // Act: Send now, pressed twice.
    const sendNow = bubble.getByRole("button", { name: "Send now", exact: true });
    await sendNow.click();
    await expect(sendNow).toBeDisabled();

    // Assert: one press reaches the board, and it stays pressed until the
    // board says what came of it.
    await expect.poll(() => posts).toEqual([{ kind: "message_now", target: "h1" }]);

    // Act: its harness's key went and the message still waits there, as
    // behind a command typed in its terminal, so he may ask again.
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })),
      snapshot({ revision: 2, actions: [{ ...held, message: "Typed while it was working and sent now.", updated_at: "2026-10-22T09:01:05Z" }] }));
    await expect(sendNow).toBeEnabled();
    await sendNow.click();
    await expect.poll(() => posts).toEqual([{ kind: "message_now", target: "h1" }, { kind: "message_now", target: "h1" }]);

    // Act: Cancel.
    await bubble.getByRole("button", { name: "Cancel", exact: true }).click();

    // Assert: it names the message by its own identity, once.
    await expect.poll(() => posts.slice(2)).toEqual([{ kind: "message_withdraw", target: "h1" }]);
    await expect(bubble.locator(".queued-actions")).toHaveCount(0);
  });
}

test("Edit takes a message back out of its harness's queue and into the box, and one its harness is handing over has nothing to press", async ({ page }) => {
  // Arrange
  const words = "check the phone layout too";
  const held = queued({
    id: "h1", text: words, status: "running", can_send_now: false, send_now_why: "", message: "Queued in its session.",
    awaiting: { task: GOBLIN.id, since: "2026-10-22T09:01:00Z", is_held: true }, created_at: "2026-10-22T09:01:00Z", updated_at: "2026-10-22T09:01:01Z",
  });
  const posts = await openWith(page, held);
  const bubble = entry(page).filter({ hasText: words });

  // Act: its harness hands it to a turn, as its own record shows.
  await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
    { ...PAGE, waiting: [{ id: "w1", at: "2026-10-22T09:01:00Z", source: "overlord", text: words, sending: true, action: "h1" }] });

  // Assert: out of his hands.
  await expect(bubble.locator(".queued-state")).toHaveText("Sending");
  await expect(bubble.locator(".queued-actions")).toHaveCount(0);

  // Act: the record shows it queued there instead, and he presses Edit.
  await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
    { ...PAGE, waiting: [{ id: "w1", at: "2026-10-22T09:01:00Z", source: "overlord", text: words, action: "h1" }] });
  await expect(bubble.locator(".queued-state")).toHaveText(WAITING);
  await bubble.getByRole("button", { name: "Edit", exact: true }).click();

  // Assert: taken back by its own identity, his words in the box to change.
  await expect.poll(() => posts).toEqual([{ kind: "message_withdraw", target: "h1", text: words }]);
  await expect(field(page)).toHaveValue(words);
});

// His message to the CEO waits out a short undo window on the board, where
// Edit and Cancel work, and is then typed into the CEO's turn, where they go
// (the CEO, 2026-11-02: "The bubble shows each real state: held (Edit and
// Cancel), typed into the turn, taken. Once typed, Edit and Cancel go away
// instead of failing.").
test("his message to the CEO is Queued with its controls while held, Sent with none once typed into the turn, and gone once taken", async ({ page }) => {
  // Arrange
  const words = "rebase onto main first";
  const held = queued({ id: "c1", task_id: "", generation: "", text: words, can_send_now: true, send_now_why: "", message: "Queued for a moment, so you can still edit or cancel it.", created_at: "2026-10-22T09:01:00Z", updated_at: "2026-10-22T09:01:00Z" });
  const relay = await open(page, snapshot({ actions: [held] }));
  await page.keyboard.press("Control+Alt+1");
  await expect(page.locator("#panel-title")).toHaveText("CEO");
  relay.say({ type: "screen", asking: false, open: false, working: true });
  const bubble = page.locator(".queued-message.pane").filter({ hasText: words });

  // Assert: held.
  await expect(bubble.locator(".queued-state")).toHaveText(WAITING);
  for (const name of ["Send now", "Edit", "Cancel"]) await expect(bubble.getByRole("button", { name, exact: true })).toBeVisible();

  // Act: the undo window ends and the board types it into the CEO's turn.
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })),
    snapshot({ revision: 2, actions: [{ ...held, status: "running", can_send_now: false, message: "Typed while the CEO works; it takes the message at its next tool call, or as its turn ends.", awaiting: { since: "2026-10-22T09:01:10Z" }, updated_at: "2026-10-22T09:01:10Z" }] }));

  // Assert: typed, with nothing left to press.
  await expect(bubble.locator(".queued-state")).toHaveText("Sent");
  await expect(bubble.locator(".queued-actions")).toHaveCount(0);
  await expect(bubble.locator(".queued-bubble")).toHaveAccessibleDescription(/takes the message at its next tool call/);

  // Act: the CEO takes it, as its record shows.
  await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
    { ...PAGE, messages: [...PAGE.messages, { id: "m9", seq: 9, at: "2026-10-22T09:02:00Z", kind: "prompt", source: "overlord", queued: true, text: words }] });

  // Assert
  await expect(bubble).toHaveCount(0);
  await unlabelled(log(page).locator(".message-prompt", { hasText: words }));
});

// The Overlord, 2026-11-02: "In AFK the queued functionality where it has the
// edit and the clear should still be there." Turning AFK on wakes the CEO, so
// a message he queued a moment before was typed into the turn that started,
// and the board kept calling it Queued with Send now alone. AFK changes
// nothing here: a message the board holds keeps all three controls, and one
// typed into a turn reads Sent.
for (const [whose, task] of [["the CEO", ""], ["a goblin", GOBLIN.id]] as const) {
  test(`with AFK on, his message to ${whose} keeps Send now, Edit and Cancel while held, and one typed into the turn reads Sent`, async ({ page }) => {
    // Arrange
    const afk = { state: "on", since: new Date().toISOString(), from: "his own board (goblins-window.exe pid 4242)", decided: 0, held: [] };
    const mine = { task_id: task, generation: task ? GOBLIN.generation : "", can_send_now: true, send_now_why: "" };
    const held = queued({ ...mine, id: "h1", text: "close the third app too", message: "Queued for a moment, so you can still edit or cancel it." });
    const typedIn = queued({ ...mine, id: "t1", text: "check the WebView memory", status: "running", can_send_now: false,
      message: "Typed while it works; it takes the message at its next tool call, or as its turn ends.", awaiting: task ? { task, since: "2026-10-22T09:00:30Z" } : { since: "2026-10-22T09:00:30Z" }, created_at: "2026-10-22T08:59:00Z" });
    const relay = await open(page, snapshot({ afk, actions: [typedIn, held], sessions: [{ id: "ceo-1", harness: "claude", role: "ceo", model: "claude-opus-5-5", phase: "working" }] }));
    if (task) await openGoblin(page);
    else {
      await page.keyboard.press("Control+Alt+1");
      await expect(page.locator("#panel-title")).toHaveText("CEO");
    }

    // Act: its turn runs, as the wake AFK sends the CEO starts one.
    relay.say({ type: "screen", asking: false, open: false, working: true });

    // Assert
    const heldBubble = entry(page).filter({ hasText: "close the third app too" });
    await expect(heldBubble.locator(".queued-state")).toHaveText(WAITING);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(heldBubble.getByRole("button", { name, exact: true })).toBeVisible();
    const typedBubble = entry(page).filter({ hasText: "check the WebView memory" });
    await expect(typedBubble.locator(".queued-state")).toHaveText("Sent");
    await expect(typedBubble.locator(".queued-actions")).toHaveCount(0);
  });
}

// A Send now the supervisor refuses, as for a message it typed a moment
// before the board showed it, is a refusal of his click: it is said over the
// box, and the CEO is not woken for it, since the supervisor answered. One
// that never reached the supervisor is the board's own failure, and the CEO
// hears of that one.
test("a Send now the supervisor refuses is said over the box and not reported to the CEO, while one that never reached it is", async ({ page }) => {
  // Arrange
  const REFUSED = "It was already typed into " + GOBLIN.id + "'s turn.";
  const relay = await open(page, snapshot({ actions: [queued({ can_send_now: true, send_now_why: "" })] }));
  const reports: string[] = [];
  await page.route("**/api/ceo/report", (route) => {
    const report = route.request().postDataJSON() as { where: string; text: string };
    if (report.where.startsWith("a queued message to ")) reports.push(report.text);
    return route.fulfill({ status: 202, json: { reported: true } });
  });
  let isAnswered = true;
  await page.route("**/api/actions", (route) => isAnswered ? route.fulfill({ status: 409, json: { error: REFUSED } }) : route.abort("connectionrefused"));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: true });
  const banner = box(page).locator(".composer-banner");
  const sendNow = entry(page).getByRole("button", { name: "Send now", exact: true });

  // Act: one press the supervisor refuses, then one that never reaches it.
  await sendNow.click();
  await expect(banner).toHaveText(REFUSED);
  isAnswered = false;
  await sendNow.click();
  await expect(banner).not.toHaveText(REFUSED);
  const lost = await banner.textContent();

  // Assert
  expect(lost).toBeTruthy();
  await expect.poll(() => reports).toEqual([lost]);
});

// An Edit the supervisor refuses, as for a message it typed into the turn as
// he pressed, is said over the box and goes no further: the CEO is not woken
// for it, since the supervisor answered. One that never reached the
// supervisor is the board's own failure, and the CEO hears of that one.
test("an Edit the supervisor refuses is said over the box and not reported to the CEO, while one that never reached it is", async ({ page }) => {
  // Arrange
  const REFUSED = "It was already typed into " + GOBLIN.id + "'s turn.";
  await open(page, snapshot({ actions: [queued()] }));
  const reports: string[] = [];
  await page.route("**/api/ceo/report", (route) => {
    const report = route.request().postDataJSON() as { where: string; text: string };
    if (report.where.startsWith("taking back a message to ")) reports.push(report.text);
    return route.fulfill({ status: 202, json: { reported: true } });
  });
  let isAnswered = true;
  await page.route("**/api/actions", (route) => isAnswered ? route.fulfill({ status: 409, json: { error: REFUSED } }) : route.abort("connectionrefused"));
  await openGoblin(page);
  const banner = box(page).locator(".composer-banner");
  const edit = entry(page).getByRole("button", { name: "Edit", exact: true });

  // Act: one press the supervisor refuses, then one that never reaches it.
  await edit.click();
  await expect(banner).toHaveText("Not taken back. " + REFUSED);
  isAnswered = false;
  await edit.click();
  await expect(banner).not.toHaveText("Not taken back. " + REFUSED);
  const lost = (await banner.textContent() ?? "").replace(/^Not taken back\. /, "");

  // Assert
  expect(lost).toBeTruthy();
  await expect(field(page)).toHaveValue("");
  await expect.poll(() => reports).toEqual([lost]);
});

// The Overlord, 2026-10-28: "send now should be an option with all queued
// messages". One that cannot go now says why when pressed, and asks nothing.
// And 2026-10-29, of notices such as "that queued message is no longer
// waiting, so nothing was deleted" in a line under the box: "these should
// appear as maybe a little banner right above the chat input that is only
// temporary, goes away slightly and doesn't move the screen up".
const NOT_WAITING = "that queued message is no longer waiting, so nothing was deleted";
for (const [press, said] of [["Send now", "Its gate holds the branch."], ["Cancel", NOT_WAITING]] as const) {
  test(`${press} that cannot be done says why in a banner just above the box, which moves nothing and goes by itself`, async ({ page }) => {
    // Arrange
    await openWith(page, queued());
    const asked: string[] = [];
    await page.route("**/api/actions", (route) => {
      asked.push((route.request().postDataJSON() as { kind: string }).kind);
      return route.fulfill({ status: 409, json: { error: NOT_WAITING } });
    });
    const banner = box(page).locator(".composer-banner");
    const places = () => page.evaluate(() => {
      const top = (selector: string) => [...document.querySelectorAll(selector)].find((element) => element.getClientRects().length)!.getBoundingClientRect();
      return { log: top("[role=log]").bottom, input: top(".composer-input").top };
    });
    const before = await places();

    // Act
    await entry(page).getByRole("button", { name: press, exact: true }).click();

    // Assert: over the conversation's end, just above the box, and nothing
    // moved for it.
    await expect(banner).toHaveText(said);
    const shown = (await banner.boundingBox())!;
    expect(await places()).toEqual(before);
    expect(shown.y + shown.height).toBeLessThanOrEqual(before.input);
    expect(shown.y + shown.height).toBeGreaterThan(before.input - 40);
    await expect(entry(page)).toBeVisible();
    expect(asked).toEqual(press === "Cancel" ? ["message_withdraw"] : []);

    // Assert: it goes by itself, and a second press says it again.
    await expect(banner).toHaveCount(0, { timeout: 8000 });
    await entry(page).getByRole("button", { name: press, exact: true }).click();
    await expect(banner).toHaveText(said);
  });
}

const KEPT = "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-1.png";
// A 1x1 PNG, which the kept picture reads back as.
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
// controls reads a queued entry's buttons as they are drawn: name, look and
// icon, in order.
const controls = (entry: ReturnType<Page["locator"]>) => entry.locator(".queued-actions button").evaluateAll((buttons) =>
  buttons.map((button) => [button.getAttribute("aria-label"), button.className, button.querySelector("svg")?.innerHTML ?? ""]));

// The Overlord, 2026-10-28: "Why don't the queue controls match? What happens
// in the goblin with the icons for like send edit cancel? In CFO."
test("a queued message to the CEO has the same three icons, in the same order and drawn the same, as one to a goblin", async ({ page }) => {
  // Arrange
  await openWith(page, queued());
  const toGoblin = await controls(entry(page));
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })),
    snapshot({ revision: 2, actions: [queued(), queued({ id: "c1", task_id: "", generation: "", text: "wait for me", message: "Queued until the CEO's turn ends." })] }));

  // Act
  await page.keyboard.press("Control+Alt+1");
  await expect(page.locator("#panel-title")).toHaveText("CEO");

  // Assert
  const ceo = page.locator(".queued-message.pane").filter({ hasText: "wait for me" });
  await expect(ceo).toHaveCount(1);
  expect(toGoblin.map(([name]) => name)).toEqual(["Send now", "Edit", "Cancel"]);
  expect(await controls(ceo)).toEqual(toGoblin);
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
  await expect(box(page).getByRole("list", { name: "Attached files" }).getByRole("img", { name: "Picture 1", exact: true })).toBeVisible();

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

// A take-back disables the box while the board answers, and a disabled box
// gives up the keyboard. The box has it back from the moment it is enabled
// again: given back a frame later, a key he pressed at once reached nothing,
// and given back in a frame drawn before then, it never came back.
for (const press of ["Up", "Edit"] as const) {
  test(`${press} takes a message back with the keyboard in the box from the moment the box is enabled again`, async ({ page }) => {
    // Arrange
    await open(page, snapshot({ actions: [queued()] }));
    let answer = () => {};
    const answered = new Promise<void>((resolve) => { answer = resolve; });
    await page.route("**/api/actions", async (route) => {
      await answered;
      await route.fulfill({ status: 202, json: { status: "succeeded" } });
    });
    await openGoblin(page);
    await field(page).evaluate((box) => new MutationObserver(() => {
      if (!box.hasAttribute("disabled")) box.dataset.focusedWhenEnabled = String(document.activeElement === box);
    }).observe(box, { attributeFilter: ["disabled"] }));
    await field(page).focus();

    // Act: he takes it back, and the board answers once the disabled box has
    // given up the keyboard.
    if (press === "Up") await page.keyboard.press("ArrowUp");
    else await entry(page).getByRole("button", { name: "Edit", exact: true }).click();
    await expect(field(page)).toBeDisabled();
    await expect(field(page)).not.toBeFocused();
    answer();

    // Assert
    await expect(field(page)).toHaveValue("hold the merge until the gate lets go");
    await expect(field(page)).toHaveAttribute("data-focused-when-enabled", "true");
  });
}

// A move with the arrows through the command list belongs to the draft it was
// made on. Words that come back to the box without his typing them, as a
// queued command taken back does, are another draft: Enter sends them as they
// are, never the pick the list found for them (#2372).
test("a queued command taken back into the box is sent as it is, whatever pick he moved to before", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({ actions: [queued({ text: "/settings" })] }));
  await page.route("**/api/actions", (route) => route.fulfill({ status: 202, json: { status: "succeeded" } }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: [
    { name: "kit-one:find-notes", description: "Find the notes of the project", source: "skill", sigil: "/" },
    { name: "release-notes", description: "Write the release notes for the branch", source: "skill", sigil: "/" },
    { name: "config", description: "Open the settings interface.", source: "builtin", sigil: "/" },
  ] } }));
  await openGoblin(page);
  const names = page.getByRole("listbox", { name: "Commands", exact: true }).locator(".popup-option-label");
  await field(page).fill("/notes");
  await expect(names).toHaveText(["/kit-one:find-notes", "/release-notes"]);
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect.poll(() => typed(relay)).toBe(pasted("/release-notes") + "\r");
  await expect(field(page)).toHaveValue("");

  // Act: Up takes the queued command back, and Enter sends it.
  await page.keyboard.press("ArrowUp");
  await expect(field(page)).toHaveValue("/settings");
  await expect(names).toHaveText(["/config"]);
  await page.keyboard.press("Enter");

  // Assert
  await expect.poll(() => typed(relay)).toBe(pasted("/release-notes") + "\r" + pasted("/settings") + "\r");
});

// The Overlord, 2026-10-28: "see how claude includes pictures when pasted".
test("a queued message shows the picture the box kept above its bubble, never its path, and opens it among its own", async ({ page }) => {
  // Arrange and Act: the kept picture reads back to the board's token.
  await open(page, snapshot({ actions: [queued({ text: "what is wrong here?\n" + KEPT })] }));
  await page.route("**/api/composer/file**", (route) => route.fulfill({ body: PNG, contentType: "image/png" }));
  await openGoblin(page);

  // Assert
  const picture = entry(page).locator(".message-picture img");
  await expect(picture).toHaveCount(1);
  await expect.poll(() => picture.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth)).toBe(1);
  await expect(entry(page).locator(".queued-bubble")).toHaveText("what is wrong here?");
  await expect(page.locator(".messages-view")).not.toContainText("composer-files");
  expect(await entry(page).evaluate((element) => {
    const picture = element.querySelector(".message-pictures")?.getBoundingClientRect(), bubble = element.querySelector(".queued-bubble")?.getBoundingClientRect();
    return !!picture && !!bubble && picture.bottom <= bubble.top;
  }), "the picture sits above the bubble").toBe(true);

  // Act: a message still queued is no row of the conversation yet, so its
  // picture opens among the message's own.
  await entry(page).getByRole("button", { name: "Open picture 1" }).click();

  // Assert
  const viewer = page.getByRole("dialog", { name: "Pictures" });
  await expect(viewer.getByRole("status")).toHaveText("1 of 1");
  await expect(viewer.getByRole("img", { name: "Picture 1 of 1" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(viewer).toHaveCount(0);
});

// The Overlord, 2026-10-28: "as it's working and going through stuff, it
// should always stay at the bottom between the agent's chat response and the
// chat box, so I could always see it and control it."
test("a queued message stays at the chat's end, right above the box, with its three icons in view while twenty tool rows stream before it", async ({ page }) => {
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

  // Assert: still in view, its icons too, after the rows and right above
  // the box.
  await expect(pinned).toBeInViewport();
  for (const name of ["Send now", "Edit", "Cancel"]) await expect(pinned.getByRole("button", { name, exact: true })).toBeInViewport();
  expect(await pinned.evaluate((element) => element.compareDocumentPosition(document.querySelector('[data-message="t19"]')!) & Node.DOCUMENT_POSITION_PRECEDING)).toBeTruthy();
  const gap = await page.evaluate(() => {
    const waiting = document.querySelector(".queued-message.pane")?.getBoundingClientRect(), dock = document.querySelector(".composer-dock")?.getBoundingClientRect();
    return waiting && dock ? dock.top - waiting.bottom : -1;
  });
  expect(gap).toBeGreaterThanOrEqual(0);
  expect(gap).toBeLessThan(48);
});
