import { expect, test, type Page } from "./site";
import { at, box, field, GOBLIN, open, openGoblin, PAGE, pasted, send, snapshot, typed } from "./session-board";

// A long paste into Modern Chat goes as a text file (#2404). The Overlord,
// 2026-11-17: "there should be no size limit on when you paste into the
// modern terminal. Should be able to accept any size, just like a text file
// if it's over a certain limit". A paste over 4,000 bytes, or one that would
// take his message past the 16,000 bytes one message holds, is kept where the
// session reads it and shows in the box as a chip that names its lines; Send
// names the file with its line count, and the conversation shows the chip,
// which opens to the text. Each paste is a real Ctrl+V from the clipboard.

const KEPT_TEXT = "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-1.txt";
const CEO_TEXT = "C:\\fleet\\state\\composer-files\\pasted-1.txt";
// Five thousand lines of a test run's output, about 400 KB.
const OUTPUT = Array.from({ length: 5000 }, (_, at) => String(at + 1).padStart(5) + " | " + "ok  example.com/harbor/internal/cache/shard".padEnd(64, ".") + " 0.41s").join("\n");

interface Upload { name: string; task: string; text: string }

// keepTexts plays the supervisor's composer files: each upload is kept as the
// next pasted-N in the folder of the session it was kept for, and a kept text
// reads back whole to the board's token.
async function keepTexts(page: Page, kept = KEPT_TEXT, texts: Record<string, string> = {}) {
  const uploads: Upload[] = [];
  const reads: { path: string; task: string }[] = [];
  await page.route("**/api/composer/file**", async (route) => {
    const query = new URL(route.request().url()).searchParams;
    if (route.request().method() === "POST") {
      uploads.push({ name: query.get("name") ?? "", task: query.get("task") ?? "", text: route.request().postDataBuffer()?.toString("utf8") ?? "" });
      return route.fulfill({ status: 201, json: { path: kept.replace("pasted-1", "pasted-" + uploads.length) } });
    }
    const path = query.get("path") ?? "";
    reads.push({ path, task: query.get("task") ?? "" });
    return query.get("token") === "fixture" && path in texts ? route.fulfill({ body: texts[path], contentType: "text/plain; charset=utf-8" }) : route.fulfill({ status: 404, json: { error: "That file is no longer kept." } });
  });
  return { uploads, reads };
}

async function pasteText(page: Page, text: string, name?: string) {
  await page.evaluate((words) => navigator.clipboard.writeText(words), text);
  await field(page, name).focus();
  await page.keyboard.press("Control+V");
}

const attachments = (page: Page, name?: string) => box(page, name).getByRole("list", { name: "Attached files" });

test.beforeEach(async ({ context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
});

test("a paste of 5,000 lines goes as one text file, shown in the box as its chip, and Send names it with its lines", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  const kept = await keepTexts(page);
  await openGoblin(page);
  await field(page).fill("why does this fail?");

  // Act
  await pasteText(page, OUTPUT);

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
test("a pasted text in the conversation shows as its chip, which opens to the text", async ({ page }) => {
  // Arrange
  const text = "FAIL TestHarborCache\n    cache_test.go:41: got 3, want 4\nFAIL\texample.com/harbor";
  const messages = { ...PAGE, messages: [{ id: "m0", seq: 0, at, kind: "prompt", source: "overlord", text: "why does it fail? Pasted text, 3 lines: " + KEPT_TEXT }, PAGE.messages[1]] };
  await open(page, snapshot(), undefined, messages);
  const kept = await keepTexts(page, KEPT_TEXT, { [KEPT_TEXT]: text });
  await openGoblin(page);
  const row = page.locator('[data-message="m0"]');
  await expect(row.locator(".message-bubble")).toHaveText("why does it fail?");

  // Act
  await row.getByRole("button", { name: "Pasted text, 3 lines" }).click();

  // Assert: a window named for the paste shows every line of the text, read
  // from where the board kept it for the goblin's task.
  const shown = page.getByRole("dialog", { name: "Pasted text, 3 lines" });
  await expect.poll(() => shown.locator("pre").textContent()).toBe(text);
  expect(kept.reads).toEqual([{ path: KEPT_TEXT, task: GOBLIN.id }]);

  // Act
  await shown.getByRole("button", { name: "Close" }).click();

  // Assert
  await expect(shown).toHaveCount(0);
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
