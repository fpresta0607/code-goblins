import { expect, test, type Page } from "./site";
import { at, box, field, GOBLIN, open, openGoblin, PAGE, pasted, send, snapshot, typed } from "./session-board";

// A long paste into Modern Chat goes as a text file (#2404). The Overlord,
// 2026-11-17: "there should be no size limit on when you paste into the
// modern terminal. Should be able to accept any size, just like a text file
// if it's over a certain limit". A paste over 4,000 bytes, or one that would
// take his message past the 16,000 bytes one message holds, is kept where the
// session reads it and shows in the box as a chip that names its lines; Send
// names the file with its line count, and the conversation shows the chip,
// which opens to the text. Each paste is a real Ctrl+V or Alt+V from the
// clipboard.

const KEPT_TEXT = "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-1.txt";
const CEO_TEXT = "C:\\fleet\\state\\composer-files\\pasted-1.txt";
// Five thousand lines of a test run's output, about 400 KB.
const OUTPUT = Array.from({ length: 5000 }, (_, at) => String(at + 1).padStart(5) + " | " + "ok  example.com/harbor/internal/cache/shard".padEnd(64, ".") + " 0.41s").join("\n");
// What a failed test printed, which a pasted text's window shows.
const FAILURE = "FAIL TestHarborCache\n    cache_test.go:41: got 3, want 4\nFAIL\texample.com/harbor";
// A picture of one pixel, as PNG data.
const PICTURE = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";

interface Upload { name: string; task: string; text: string }

// keepTexts plays the supervisor's composer files: each upload is kept as the
// next pasted-N, with its name's extension, in the folder of the session it
// was kept for, and a kept text reads back whole to the board's token.
async function keepTexts(page: Page, kept = KEPT_TEXT, texts: Record<string, string> = {}) {
  const uploads: Upload[] = [];
  const reads: { path: string; task: string }[] = [];
  await page.route("**/api/composer/file**", async (route) => {
    const query = new URL(route.request().url()).searchParams;
    if (route.request().method() === "POST") {
      const name = query.get("name") ?? "";
      uploads.push({ name, task: query.get("task") ?? "", text: route.request().postDataBuffer()?.toString("utf8") ?? "" });
      return route.fulfill({ status: 201, json: { path: kept.replace("pasted-1.txt", "pasted-" + uploads.length + name.slice(name.lastIndexOf("."))) } });
    }
    const path = query.get("path") ?? "";
    reads.push({ path, task: query.get("task") ?? "" });
    return query.get("token") === "fixture" && path in texts ? route.fulfill({ body: texts[path], contentType: "text/plain; charset=utf-8" }) : route.fulfill({ status: 404, json: { error: "That file is no longer kept." } });
  });
  return { uploads, reads };
}

async function pasteText(page: Page, text: string, name?: string, key = "Control+V") {
  await page.evaluate((words) => navigator.clipboard.writeText(words), text);
  await field(page, name).focus();
  await page.keyboard.press(key);
}

const attachments = (page: Page, name?: string) => box(page, name).getByRole("list", { name: "Attached files" });

test.beforeEach(async ({ context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
});

for (const key of ["Control+V", "Alt+v"]) {
  test(`a paste of 5,000 lines with ${key} goes as one text file, shown in the box as its chip, and Send names it with its lines`, async ({ page }) => {
    // Arrange
    const relay = await open(page);
    const kept = await keepTexts(page);
    await openGoblin(page);
    await field(page).fill("why does this fail?");

    // Act
    await pasteText(page, OUTPUT, undefined, key);

    // Assert: one chip that names its lines, every line kept for the goblin's
    // task, and his words untouched.
    await expect(attachments(page).getByRole("listitem")).toHaveCount(1);
    await expect(attachments(page)).toContainText("Pasted text, 5,000 lines");
    expect(kept.uploads).toEqual([{ name: "pasted.txt", task: GOBLIN.id, text: OUTPUT }]);
    await expect(field(page)).toHaveValue("why does this fail?");

    // Act
    await send(page).click();

    // Assert: his words, then the file with its line count on a line of its
    // own, as one paste, and Enter; the box empties.
    await expect.poll(() => typed(relay)).toContain(pasted("why does this fail?\rPasted text, 5,000 lines: " + KEPT_TEXT) + "\r");
    await expect(field(page)).toHaveValue("");
    await expect(attachments(page)).toHaveCount(0);
  });
}

test("two long pastes in one message go as two files, each named with its lines", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  const kept = await keepTexts(page);
  await openGoblin(page);
  const first = OUTPUT.split("\n").slice(0, 300).join("\n");
  const second = OUTPUT.split("\n").slice(300, 1000).join("\n");

  // Act
  await pasteText(page, first);
  await expect(attachments(page)).toContainText("Pasted text, 300 lines");
  await pasteText(page, second);
  await expect(attachments(page)).toContainText("Pasted text, 700 lines");
  await send(page).click();

  // Assert
  expect(kept.uploads.map((upload) => upload.text)).toEqual([first, second]);
  const secondPath = KEPT_TEXT.replace("pasted-1", "pasted-2");
  await expect.poll(() => typed(relay)).toContain(pasted("Pasted text, 300 lines: " + KEPT_TEXT + "\rPasted text, 700 lines: " + secondPath) + "\r");
});

test("removing a pasted text's chip takes the file out of what Send sends", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await keepTexts(page);
  await openGoblin(page);
  await field(page).fill("never mind the log");
  await pasteText(page, OUTPUT);
  await expect(attachments(page)).toContainText("Pasted text, 5,000 lines");

  // Act
  await attachments(page).getByRole("button", { name: "Remove Pasted text, 5,000 lines" }).click();
  await send(page).click();

  // Assert
  await expect.poll(() => typed(relay)).toContain(pasted("never mind the log") + "\r");
  expect(typed(relay)).not.toContain("pasted-1.txt");
});

// A paste the board cannot keep, as one past the 10 MiB a kept file holds,
// says why over the box, and his words stay as they were.
test("a long paste the board cannot keep says why over the box, and his words stay", async ({ page }) => {
  // Arrange
  await open(page);
  await page.route("**/api/composer/file**", (route) => route.fulfill({ status: 413, json: { error: "A pasted file may be at most 10 MiB." } }));
  await openGoblin(page);
  await field(page).fill("why does this fail?");

  // Act
  await pasteText(page, OUTPUT);

  // Assert
  await expect(box(page).getByRole("alert")).toHaveText("That paste was not added: A pasted file may be at most 10 MiB.");
  await expect(field(page)).toHaveValue("why does this fail?");
  await expect(attachments(page)).toHaveCount(0);
});

// A selection copied from a conversation holds its pictures inline in its
// HTML (#2543): its long words go as a pasted text, whose chip comes before
// each of its pictures, kept in that order.
test("a selection copied from a conversation with long words and its pictures goes as a pasted text, then each picture", async ({ page }) => {
  // Arrange
  await open(page);
  const kept = await keepTexts(page);
  await openGoblin(page);
  const words = OUTPUT.split("\n").slice(0, 60).join("\n");
  await page.evaluate(async ({ words, picture }) => {
    const inline = `<img data-conversation-picture src="data:image/png;base64,${picture}" alt="">`;
    const html = `<p>${words}</p>${inline}${inline}`;
    await navigator.clipboard.write([new ClipboardItem({ "text/plain": new Blob([words], { type: "text/plain" }), "text/html": new Blob([html], { type: "text/html" }) })]);
  }, { words, picture: PICTURE });

  // Act
  await field(page).focus();
  await page.keyboard.press("Control+V");

  // Assert
  const items = attachments(page).getByRole("listitem");
  await expect(items).toHaveCount(3);
  await expect(items.first()).toHaveText("Pasted text, 60 lines");
  for (const at of [1, 2]) await expect(items.nth(at).getByRole("img")).toHaveCount(1);
  expect(kept.uploads.map((upload) => upload.name)).toEqual(["pasted.txt", "pasted.png", "pasted.png"]);
  expect(kept.uploads[0].text).toBe(words);
  await expect(field(page)).toHaveValue("");
});

// A paste goes into the box as text, as it always has, up to 4,000 bytes and
// while the message it joins stays within the 16,000 bytes one message holds.
// Its size is the box's, whose line breaks are one byte each where Windows'
// clipboard holds them as two, as it holds every paste here.
const SIZES = [
  { what: "a paste of 4,000 bytes goes into the box as text", before: 0, paste: 4000, isFile: false },
  { what: "a paste of 4,001 bytes goes as a file", before: 0, paste: 4001, isFile: true },
  { what: "a paste that keeps the message within 16,000 bytes goes into the box as text", before: 12000, paste: 3000, isFile: false },
  { what: "a paste that would take the message past 16,000 bytes goes as a file", before: 14000, paste: 3000, isFile: true },
];

for (const { what, before, paste, isFile } of SIZES) {
  test(what, async ({ page }) => {
    // Arrange
    await open(page);
    const kept = await keepTexts(page);
    await openGoblin(page);
    const typedBefore = "w".repeat(before);
    const text = ("x".repeat(99) + "\n").repeat(Math.ceil(paste / 100)).slice(0, paste);
    if (typedBefore) await field(page).fill(typedBefore);

    // Act
    await pasteText(page, text);

    // Assert
    if (isFile) {
      await expect(attachments(page)).toContainText("Pasted text, " + Math.ceil(paste / 100) + " lines");
      expect(kept.uploads.map((upload) => upload.text)).toEqual([text]);
      await expect(field(page)).toHaveValue(typedBefore);
    } else {
      await expect(field(page)).toHaveValue(typedBefore + text);
      expect(kept.uploads).toEqual([]);
      await expect(attachments(page)).toHaveCount(0);
    }
  });
}

// A ! command is run by the shell as he wrote it, so a script pasted into one
// stays its words, however long.
test("a long paste into a ! command stays the command's words, which the shell runs", async ({ page }) => {
  // Arrange
  await open(page);
  const kept = await keepTexts(page);
  await openGoblin(page);
  const script = "Write-Output 'checked'\n".repeat(300);
  await field(page).fill("!");

  // Act
  await pasteText(page, script);

  // Assert
  await expect(field(page)).toHaveValue("!" + script);
  expect(kept.uploads).toEqual([]);
  await expect(attachments(page)).toHaveCount(0);
});

// The CEO's box queues through the board while the CEO works, the road a long
// paste was refused on with "Invalid action JSON" before.
test("a long paste to the CEO while it works is queued as one message that names its file, kept in the CEO's folder", async ({ page }) => {
  // Arrange
  const posts: { kind?: string; task_id?: string; text?: string }[] = [];
  const relay = await open(page);
  const kept = await keepTexts(page, CEO_TEXT);
  await page.route("**/api/actions", (route) => {
    posts.push(route.request().postDataJSON());
    return route.fulfill({ status: 202, json: { status: "queued" } });
  });
  await page.keyboard.press("Control+Alt+1");
  await expect(page.locator("#panel-title")).toHaveText("CEO");
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  relay.say({ type: "screen", asking: false, open: false, working: true });
  await expect(page.locator(".reply-working")).toBeVisible();
  await field(page, "CEO").fill("this failed overnight");

  // Act
  await pasteText(page, OUTPUT, "CEO");
  await expect(attachments(page, "CEO")).toContainText("Pasted text, 5,000 lines");
  await field(page, "CEO").press("Enter");

  // Assert
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toMatchObject({ kind: "message", task_id: "", text: "this failed overnight\nPasted text, 5,000 lines: " + CEO_TEXT });
  expect(kept.uploads).toEqual([{ name: "pasted.txt", task: "", text: OUTPUT }]);
  expect(typed(relay)).toBe("");
});

// Claude Code's record keeps a message the board typed on one line, so the
// pasted text is named at the end of his words' line there.
const PASTED_ROW = { ...PAGE, messages: [{ id: "m0", seq: 0, at, kind: "prompt", source: "overlord", text: "why does it fail? Pasted text, 3 lines: " + KEPT_TEXT }, PAGE.messages[1]] };

test("a pasted text in the conversation shows as its chip, which opens to the text", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), undefined, PASTED_ROW);
  const kept = await keepTexts(page, KEPT_TEXT, { [KEPT_TEXT]: FAILURE });
  await openGoblin(page);
  const row = page.locator('[data-message="m0"]');
  await expect(row.locator(".message-bubble")).toHaveText("why does it fail?");

  // Act
  await row.getByRole("button", { name: "Pasted text, 3 lines" }).click();

  // Assert: a window named for the paste shows every line of the text, read
  // from where the board kept it for the goblin's task.
  const shown = page.getByRole("dialog", { name: "Pasted text, 3 lines" });
  await expect.poll(() => shown.locator("pre").textContent()).toBe(FAILURE);
  expect(kept.reads).toEqual([{ path: KEPT_TEXT, task: GOBLIN.id }]);

  // Act
  await shown.getByRole("button", { name: "Close" }).click();

  // Assert
  await expect(shown).toHaveCount(0);
});

test("a pasted text the board no longer keeps opens to a window that says so", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), undefined, PASTED_ROW);
  await keepTexts(page);
  await openGoblin(page);

  // Act
  await page.locator('[data-message="m0"]').getByRole("button", { name: "Pasted text, 3 lines" }).click();

  // Assert
  const shown = page.getByRole("dialog", { name: "Pasted text, 3 lines" });
  await expect(shown).toContainText("This paste is no longer available.");
  await expect(shown.locator("pre")).toHaveCount(0);
});

test("a queued message shows its pasted text as a chip, and Edit gives it back to the box as one", async ({ page }) => {
  // Arrange
  const posts: { kind?: string }[] = [];
  const relay = await open(page);
  await keepTexts(page);
  await page.route("**/api/actions", (route) => {
    posts.push(route.request().postDataJSON());
    return route.fulfill({ status: 202, json: { status: "succeeded" } });
  });
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: true });
  await expect(page.locator(".reply-working")).toBeVisible();
  const action = { id: "q1", kind: "message", task_id: GOBLIN.id, generation: "", status: "queued", text: "check this\nPasted text, 12 lines: " + KEPT_TEXT, message: "Queued until its turn ends.", can_send_now: true, updated_at: at };
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, actions: [action] }));
  const shown = page.locator(".queued-message.pane").filter({ hasText: "check this" });
  await expect(shown.getByRole("button", { name: "Pasted text, 12 lines" })).toBeVisible();

  // Act
  await shown.getByRole("button", { name: "Edit", exact: true }).click();

  // Assert
  await expect.poll(() => posts.map((post) => post.kind)).toEqual(["message_withdraw"]);
  await expect(field(page)).toHaveValue("check this");
  await expect(attachments(page)).toContainText("Pasted text, 12 lines");
});

// The window is the conversation's, so it stays open while the queued
// message whose chip opened it gives way to its row in the record.
test("a queued message's chip opens its text, which stays open while the message gives way to its row in the record", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  const kept = await keepTexts(page, KEPT_TEXT, { [KEPT_TEXT]: FAILURE });
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: true });
  await expect(page.locator(".reply-working")).toBeVisible();
  const action = { id: "q1", kind: "message", task_id: GOBLIN.id, generation: "", status: "queued", text: "check this\nPasted text, 3 lines: " + KEPT_TEXT, message: "Queued until its turn ends.", can_send_now: true, updated_at: at };
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, actions: [action] }));
  const queued = page.locator(".queued-message.pane").filter({ hasText: "check this" });

  // Act
  await queued.getByRole("button", { name: "Pasted text, 3 lines" }).click();

  // Assert: the window shows every line, read from where the board kept it
  // for the goblin's task.
  const shown = page.getByRole("dialog", { name: "Pasted text, 3 lines" });
  await expect.poll(() => shown.locator("pre").textContent()).toBe(FAILURE);
  expect(kept.reads).toEqual([{ path: KEPT_TEXT, task: GOBLIN.id }]);

  // Act: the goblin takes the message, and its record keeps it on one line.
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 3, actions: [{ ...action, status: "succeeded", message: "Accepted by the goblin in its native terminal." }] }));
  await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })), { ...PAGE, messages: [...PAGE.messages, { id: "m2", seq: 2, at, kind: "prompt", source: "overlord", text: "check this Pasted text, 3 lines: " + KEPT_TEXT }] });

  // Assert: the record's row shows the chip in the queued message's place,
  // and the window still shows the text, read once.
  await expect(queued).toHaveCount(0);
  await expect(page.locator('[data-message="m2"] .pasted-text')).toHaveText("Pasted text, 3 lines");
  await expect(shown).toBeVisible();
  expect(await shown.locator("pre").textContent()).toBe(FAILURE);
  expect(kept.reads).toHaveLength(1);

  // Act
  await page.keyboard.press("Escape");

  // Assert
  await expect(shown).toHaveCount(0);
});
