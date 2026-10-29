import { expect, test, type Page } from "./site";
import { at, box, field, GOBLIN, open, openGoblin, PAGE, snapshot, typed } from "./session-board";

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
const WAITING = "Waiting for the goblin's next step";
const readAt = (page: Page, when: string) => page.evaluate((when) => "Read " + new Date(when).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" }), when);

// The Overlord, 2026-10-29: "the user can't really tell what's been sent and
// what's getting sent. And the double scrolls and having its own little
// scroll for queued messages doesn't really work." One scroll, the chat's: a
// message he typed while the session works is his own bubble at the chat's
// end, dimmed, waiting for its next step with its controls, and turns solid
// with Read and the time in place once the harness takes it, as its record
// shows; one sent while it is idle is solid with Sent until then. A goblin's
// record reads the stamps the board types his words behind as his, so its
// row is his own words, as a CFO's is.
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

  test("taken by the harness, his waiting message turns solid with Read and the time, its controls gone, shown once", async ({ page }) => {
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

    // Assert: his one bubble, solid, read, with nothing to do with it.
    const mine = log(page).locator(".message-prompt", { hasText: words });
    await expect(mine).toHaveCount(1);
    await expect(log(page).getByText(words, { exact: true })).toHaveCount(1);
    await expect(mine.locator(".message-state")).toHaveText(await readAt(page, taken));
    await expect(entry(page)).toHaveCount(0);

    // Act and Assert: the board's own record catches up, and nothing changes.
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, actions: [queued({ text: words, status: "succeeded", updated_at: "2026-10-22T09:06:00Z" })] }));
    await expect(log(page).getByText(words, { exact: true })).toHaveCount(1);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(page.locator(".messages-view:not([hidden])").getByRole("button", { name, exact: true })).toHaveCount(0);
  });

  test("a message sent while the session is idle is solid with Sent as it goes, and Read once its record shows it", async ({ page }) => {
    // Arrange
    const words = "start on the invoices";
    await open(page, snapshot({ actions: [queued({ text: words, status: "running", message: "Typing it into the terminal." })] }, { phase: "settled" }));
    await openGoblin(page);

    // Assert: on its way.
    await expect(entry(page).locator(".queued-state")).toHaveText("Sent");
    expect(Number(await entry(page).locator(".queued-bubble").evaluate((element) => getComputedStyle(element).opacity))).toBe(1);
    await expect(entry(page).locator(".queued-actions")).toHaveCount(0);

    // Act: its prompt starts the turn.
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
      { ...PAGE, messages: [...PAGE.messages, { id: "m9", seq: 9, at, kind: "prompt", source: "overlord", text: words }] });

    // Assert
    await expect(log(page).locator(".message-prompt", { hasText: words }).locator(".message-state")).toHaveText(await readAt(page, at));
    await expect(log(page).getByText(words, { exact: true })).toHaveCount(1);
  });

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
    await expect(log(page).locator(".message-prompt", { hasText: words }).locator(".message-state")).toHaveText(await readAt(page, taken));
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
    await expect(entry(page).locator(".queued-state")).toHaveText(WAITING);

    // Act: the harness takes the new one.
    const taken = "2026-10-22T09:05:00Z";
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
      { ...PAGE, messages: [...PAGE.messages, { id: "m9", seq: 9, at: taken, kind: "prompt", source: "overlord", queued: true, text: "yes" }] });

    // Assert
    await expect(log(page).locator(".message-prompt", { hasText: "yes" }).locator(".message-state")).toHaveText(await readAt(page, taken));
    await expect(entry(page)).toHaveCount(0);
    await expect(log(page).getByText("yes", { exact: true })).toHaveCount(1);
  });

  // The record keeps a shell command he typed without its "!", as Claude Code
  // writes it, and the row shows it as he typed it, read as his words are.
  test("a shell command he typed while the session works is read by its command row, which says Read and the time", async ({ page }) => {
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
    await expect(log(page).locator(".message-prompt.shell .message-state")).toHaveText(await readAt(page, taken));
    await expect(entry(page)).toHaveCount(0);
    await expect(log(page).getByText("!git status", { exact: true })).toHaveCount(1);
  });

  // A row the CFO typed into the goblin is the CFO's, even in his words.
  test("a later row of the CFO's in the same words does not read his waiting message", async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({ actions: [queued({ text: "merge main", created_at: "2026-10-22T09:01:00Z" })] }));
    await openGoblin(page);
    relay.say({ type: "screen", asking: false, open: false, working: true });
    await expect(entry(page)).toBeVisible();

    // Act: the CFO steers the goblin, "CFO: merge main".
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })),
      { ...PAGE, messages: [...PAGE.messages, { id: "m9", seq: 9, at: "2026-10-22T09:05:00Z", kind: "prompt", source: "cfo", text: "merge main" }] });

    // Assert
    await expect(log(page).locator(".message-prompt.cfo", { hasText: "merge main" })).toBeVisible();
    await expect(entry(page)).toBeVisible();
    await expect(entry(page).locator(".queued-state")).toHaveText(WAITING);
    for (const name of ["Send now", "Edit", "Cancel"]) await expect(entry(page).getByRole("button", { name, exact: true })).toBeVisible();
  });

  test("a message waiting in a conversation that holds nothing yet shows with no No messages yet over it", async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({ actions: [queued()] }));
    await openGoblin(page);
    relay.say({ type: "screen", asking: false, open: false, working: true });

    // Act: the session's record holds nothing.
    await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })), { messages: [], more: false });

    // Assert
    await expect(log(page).locator("[data-message]")).toHaveCount(0);
    await expect(entry(page)).toBeVisible();
    await expect(page.locator(".messages-view:not([hidden])").getByText("No messages yet", { exact: true })).toHaveCount(0);
  });

  // The Overlord, 2026-10-29: a message he typed for the CFO the day before
  // still sat as queued, and its Cancel said "that queued message is no
  // longer waiting, so nothing was deleted". The board typed it and still
  // watches for a late take, but it waits nowhere.
  test("a message from an earlier day the harness was not seen taking is not waiting on load, with nothing to cancel", async ({ page }) => {
    // Arrange and Act
    const advice = "Your answer was typed for the goblin, which has not picked it up. Open its terminal and press Enter if your answer is waiting in its box; if it is not there, type it to the goblin.";
    await openWith(page, queued({ status: "uncertain", message: advice, advice, awaiting: { task: GOBLIN.id, since: "2026-10-21T22:04:16Z" } }));

    // Assert
    await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
    await expect(entry(page)).toHaveCount(0);
    await expect(page.locator(".messages-view:not([hidden])")).not.toContainText("hold the merge");
    await expect(page.locator(".messages-view:not([hidden])").getByRole("button", { name: "Cancel", exact: true })).toHaveCount(0);
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
  await expect(page.locator(".composer-banner")).toHaveCount(0);
  expect(posts.map((post) => post.kind)).toEqual(["message_now", "message_withdraw"]);
});

// One already typed is the harness's to take: the board can no longer send
// it, take it back or cancel it, so it offers none of them.
test("a message already typed into a working session says the turn takes it, and offers nothing to do with it", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({ actions: [queued({ status: "running", awaiting: { task: "cg-touch" }, message: "Typed while it was working; it takes the message at its next tool call, or as its current turn ends." })] }));
  await openGoblin(page);

  // Act
  relay.say({ type: "screen", asking: false, open: false, working: true });

  // Assert
  await expect(entry(page).locator(".queued-state")).toHaveText(WAITING);
  await expect(entry(page).locator(".queued-bubble")).toHaveAccessibleDescription(/takes the message at its next tool call/);
  await expect(entry(page).locator(".queued-actions")).toHaveCount(0);
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
