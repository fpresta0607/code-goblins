import { expect, test, type Locator, type Page, type Route } from "./site";
import { recordingBegins, slowMicrophone } from "./dictation-hold";

// The Overlord, 2026-10-15: "again theres no way for me to answer this
// command with a skip or other", and then where: at the bottom beside the
// decision buttons, a pencil, on every card with no choices or answer box of
// its own, that opens a little window to write in before the card clears.
// Later that day, of that window: "Control shift space hotkey doesn't work to
// dictate on the skip card. Also, instead of skip this card question mark,
// like something else. And it shouldn't say skip. It should just say send
// here." and "i also should be able to paste images in the something else
// box". The window is his answer in his own words, sent with Send, with the
// pictures he pastes, and dictated with the shortcut as every answer box is.
// Skip stays on the card itself, as a question card has it.

// Chromium's fake microphone (periodic beeps) stands in for his.
test.use({
  permissions: ["microphone"],
  launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] },
});

// record answers every board request the cards send and keeps what each said.
async function record(page: Page): Promise<{ path: string; body: Record<string, unknown> }[]> {
  const sent: { path: string; body: Record<string, unknown> }[] = [];
  const answer = async (route: Route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    sent.push({ path: new URL(route.request().url()).pathname, body });
    await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
  };
  await page.route("**/api/actions", answer);
  await page.route("**/api/credentials/**", answer);
  return sent;
}

const CARDS = [
  { open: "Open the command", skip: { kind: "run_skip", run_id: "run-shop-signup-7", generation: "r".repeat(64) }, other: { kind: "run_answer", run_id: "run-shop-signup-7", generation: "r".repeat(64) } },
  { open: "Open the wait", skip: { kind: "review_clear", review_id: "waiting-shop-signup-8", generation: "w".repeat(64) }, other: { kind: "review_answer", review_id: "waiting-shop-signup-8", generation: "w".repeat(64) } },
  { open: "Open the credential", skip: { id: "cred-4444444444444444", generation: "c".repeat(32), action: "dismiss" }, other: { id: "cred-4444444444444444", generation: "c".repeat(32), action: "answer" } },
] as const;

async function openCard(page: Page, name: string) {
  await page.goto("/tests/fixtures/skip-or-other.html");
  await page.getByRole("button", { name, exact: true }).click();
  const card = page.getByRole("dialog").locator(".question-card, .run-card").first();
  await expect(card).toBeVisible();
  return card;
}

const pencilOf = (card: ReturnType<Page["locator"]>) => card.locator(".card-actions").getByRole("button", { name: "Something else?" });
const noteWindow = (page: Page) => page.getByRole("dialog", { name: "Something else?" });

async function pastePicture(note: Locator) {
  await note.evaluate((box) => {
    const bytes = Uint8Array.from(atob("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="), (character) => character.charCodeAt(0));
    const clipboard = new DataTransfer();
    clipboard.items.add(new File([bytes], "image.png", { type: "image/png" }));
    box.dispatchEvent(new ClipboardEvent("paste", { clipboardData: clipboard, bubbles: true, cancelable: true }));
  });
}

for (const card of CARDS) {
  test(`${card.open}: Skip beside its decision button skips it`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const shown = await openCard(page, card.open);

    // Act
    await shown.locator(".card-actions").getByRole("button", { name: "Skip this card" }).click();

    // Assert
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0].body).toMatchObject(card.skip);
    await expect(shown.locator(".asker").getByRole("button")).toHaveCount(0);
  });

  test(`${card.open}: the pencil's window asks for his answer and sends it with Send`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const shown = await openCard(page, card.open);
    const word = "Use the shop's bot account instead";

    // Act
    await pencilOf(shown).click();
    const send = noteWindow(page).getByRole("button", { name: "Send", exact: true });
    const isSendOffWhenEmpty = await send.isDisabled();
    await noteWindow(page).getByLabel("Your note").fill(word);
    await send.click();

    // Assert
    expect(isSendOffWhenEmpty).toBe(true);
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0].body).toMatchObject({ ...card.other, text: word });
  });

  test(`${card.open}: Ctrl+Shift+Space in the pencil's window dictates into his note`, async ({ page }) => {
    // Arrange
    const heard: (string | null)[] = [];
    await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
    await page.route("**/api/dictation", async (route) => {
      if (route.request().method() === "GET") return route.fulfill({ json: { engine: "test-model", state: "ready" } });
      heard.push(await route.request().headerValue("X-CEO-Token"));
      return route.fulfill({ json: { text: "use the bot account", engine: "test-model" } });
    });
    const shown = await openCard(page, card.open);
    await pencilOf(shown).click();
    const note = noteWindow(page).getByLabel("Your note");
    await expect(note).toBeFocused();

    // Act
    const begun = recordingBegins(page);
    await page.keyboard.down("Control");
    await page.keyboard.down("Shift");
    await page.keyboard.down("Space");
    await expect(noteWindow(page).getByRole("button", { name: "Listening" })).toHaveClass(/recording/);
    await begun;
    await page.waitForTimeout(1200);
    await page.keyboard.up("Space");
    await page.keyboard.up("Shift");
    await page.keyboard.up("Control");

    // Assert
    await expect(note).toHaveValue("use the bot account");
    expect(heard).toEqual(["fixture-token"]);
  });
}

test("nothing in the pencil's window says skip", async ({ page }) => {
  // Arrange
  await record(page);
  const shown = await openCard(page, "Open the command");

  // Act
  await pencilOf(shown).click();
  await noteWindow(page).getByLabel("Your note").fill("Use the shop's bot account instead");

  // Assert
  await expect(noteWindow(page).getByRole("heading", { name: "Something else?" })).toBeVisible();
  expect((await noteWindow(page).innerText()).toLowerCase()).not.toContain("skip");
  expect(await noteWindow(page).getByRole("button", { name: "Send", exact: true }).locator("svg").count()).toBe(1);
});

test("the pencil entry and its dialog say Something else? and Send uses the board green", async ({ page }) => {
  await record(page);
  const shown = await openCard(page, "Open the command");
  const pencil = shown.locator(".skip-with-note");
  const green = await shown.getByRole("button", { name: "Run in PowerShell" }).evaluate((button) => getComputedStyle(button, "::before").backgroundImage);
  await pencil.click();
  const dialog = page.locator("dialog.skip-note");
  await dialog.getByLabel("Your note").fill("Use this account");

  await expect.soft(pencil).toHaveAccessibleName("Something else?");
  await expect.soft(dialog).toHaveAccessibleName("Something else?");
  expect(green).not.toBe("none");
  await expect.poll(() => dialog.getByRole("button", { name: "Send", exact: true }).evaluate((button) => getComputedStyle(button, "::before").backgroundImage)).toBe(green);
});

for (const viewport of [{ width: 1707, height: 1067 }, { width: 390, height: 844 }]) {
  test(`dictation in the question's own answer box works at ${viewport.width}`, async ({ page }, testInfo) => {
    await slowMicrophone(page);
    const sent = await record(page);
    await page.setViewportSize(viewport);
    await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
    await page.route("**/api/dictation", (route) => route.fulfill({ json: route.request().method() === "GET"
      ? { engine: "test-model", state: "ready" } : { text: "use a trial account", engine: "test-model" } }));
    const shown = await openCard(page, "Open the question");
    await shown.getByRole("radio", { name: /^Other/ }).check();
    const answer = shown.getByLabel("Your written answer");
    await answer.focus();

    const begun = recordingBegins(page);
    await page.keyboard.down("Control");
    await page.keyboard.down("Shift");
    await page.keyboard.down("Space");
    await expect(shown.getByRole("button", { name: "Listening" })).toHaveClass(/recording/);
    await begun;
    await page.waitForTimeout(1200);
    await page.keyboard.up("Space");
    await page.keyboard.up("Shift");
    await page.keyboard.up("Control");

    await expect(answer).toHaveValue("use a trial account");
    await expect(answer).toBeFocused();
    await page.screenshot({ path: testInfo.outputPath(`question-answer-${viewport.width}.png`) });
    expect(sent).toEqual([]);
  });
}

for (const input of ["shortcut from Cancel", "microphone"] as const) {
  test(`${input} adds dictation to the answer without submitting it`, async ({ page }) => {
    const sent = await record(page);
    await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
    await page.route("**/api/dictation", (route) => route.fulfill({ json: route.request().method() === "GET"
      ? { engine: "test-model", state: "ready" } : { text: "with this picture", engine: "test-model" } }));
    const shown = await openCard(page, "Open the command");
    await pencilOf(shown).click();
    const note = noteWindow(page).getByLabel("Your note");
    await note.fill("Use this account");

    const begun = recordingBegins(page);
    if (input === "shortcut from Cancel") {
      await noteWindow(page).getByRole("button", { name: "Cancel" }).focus();
      await page.keyboard.down("Control");
      await page.keyboard.down("Shift");
      await page.keyboard.down("Space");
    } else {
      await noteWindow(page).getByRole("button", { name: "Hold to dictate" }).hover();
      await page.mouse.down();
    }
    await expect(noteWindow(page).getByRole("button", { name: "Listening" })).toHaveClass(/recording/);
    await begun;
    await page.waitForTimeout(1200);
    if (input === "shortcut from Cancel") {
      await page.keyboard.up("Space");
      await page.keyboard.up("Shift");
      await page.keyboard.up("Control");
    } else {
      await page.mouse.up();
    }

    await expect(note).toHaveValue("Use this account with this picture");
    await expect(note).toBeFocused();
    expect(sent).toEqual([]);
  });
}

// The pictures and files his answer carries go through the chat's own box and
// route (#2538), each kept where the asker reads it and named on a line of its
// own after his words, as the chat names them.
const KEPT = String.raw`C:\fleet\state\tasktmp\shop-signup\composer-files\pasted-1.png`;
const KEPT_ANSWER = KEPT.replace("pasted-1.png", "pasted-2.txt");
const ONE_PIXEL = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==";
async function keep(page: Page, wait?: Promise<void>) {
  const kept: { task: string | null; name: string | null; credential: string | null; type: string | undefined; body: string }[] = [];
  await page.route("**/api/composer/file**", async (route) => {
    if (route.request().method() !== "POST") return route.fulfill({ status: 200, contentType: "image/png", body: Buffer.from(ONE_PIXEL, "base64") });
    const url = new URL(route.request().url());
    const name = url.searchParams.get("name");
    kept.push({ task: url.searchParams.get("task"), name, credential: url.searchParams.get("credential"), type: route.request().headers()["content-type"], body: route.request().postDataBuffer()?.toString("utf8") ?? "" });
    await wait;
    await route.fulfill({ status: 201, json: { path: name === "answer.txt" ? KEPT_ANSWER : KEPT } });
  });
  return kept;
}

test("Send waits for a pasted picture and accepts a picture without words", async ({ page }) => {
  const sent = await record(page);
  let release: () => void = () => {};
  await keep(page, new Promise<void>((resolve) => { release = resolve; }));
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  await pastePicture(noteWindow(page).getByLabel("Your note"));

  const send = noteWindow(page).getByRole("button", { name: "Send", exact: true });
  await expect(send).toBeDisabled();
  await expect(noteWindow(page).getByRole("status")).toHaveText("Saving files...");
  release();
  await expect(send).toBeEnabled();
  await send.click();

  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ kind: "run_answer", text: KEPT });
});

test("a picture pasted into the pencil's window is kept beside the task and sent with his note", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  const kept = await keep(page);
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  const note = noteWindow(page).getByLabel("Your note");
  await note.fill("Like this one");

  // Act
  await pastePicture(note);
  await expect(noteWindow(page).getByRole("img", { name: "Picture 1" })).toBeVisible();
  await noteWindow(page).getByRole("button", { name: "Send", exact: true }).click();

  // Assert
  expect(kept.map(({ task, type }) => ({ task, type }))).toEqual([{ task: "shop-signup", type: "image/png" }]);
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ kind: "run_answer", run_id: "run-shop-signup-7", text: "Like this one\n" + KEPT });
});

test("a selection copied from a conversation and pasted into the pencil's window keeps each of its pictures and its words", async ({ page, context }) => {
  // Arrange: the clipboard holds what the board's copy of a chat selection
  // holds (#2543): its words, HTML with its two pictures inline, each marked
  // as a conversation's, and its first picture as a picture. Each picture is
  // kept as the next pasted-N, as the supervisor keeps it.
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await record(page);
  const kept: (string | null)[] = [];
  await page.route("**/api/composer/file**", (route) => {
    if (route.request().method() !== "POST") return route.fulfill({ status: 200, contentType: "image/png", body: Buffer.from(ONE_PIXEL, "base64") });
    kept.push(new URL(route.request().url()).searchParams.get("name"));
    return route.fulfill({ status: 201, json: { path: KEPT.replace("pasted-1", "pasted-" + kept.length) } });
  });
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  const note = noteWindow(page).getByLabel("Your note");
  await page.evaluate(async (png) => {
    const picture = `<img data-conversation-picture src="data:image/png;base64,${png}" alt="">`;
    await navigator.clipboard.write([new ClipboardItem({
      "text/plain": new Blob(["Like these two"], { type: "text/plain" }),
      "text/html": new Blob([`<p>Like these</p>${picture}${picture}<p>two</p>`], { type: "text/html" }),
      "image/png": new Blob([Uint8Array.from(atob(png), (character) => character.charCodeAt(0))], { type: "image/png" }),
    })]);
  }, ONE_PIXEL);

  // Act
  await note.focus();
  await page.keyboard.press("Control+V");

  // Assert: both pictures are kept and shown, and his words are in the note.
  await expect(noteWindow(page).getByRole("img", { name: "Picture 2" })).toBeVisible();
  expect(kept).toEqual(["pasted.png", "pasted.png"]);
  await expect(note).toHaveValue("Like these two");
});

// The Overlord, 2026-11-13, of this window: "It shouldn't have a character
// cap. I should be able to paste like large attachments like I do in the
// goblins chat" (#2538). A long answer goes whole as a file the asker reads,
// named with its opening words, so no card's limit refuses it and no line
// break is lost; a picture goes with it. A credential card's is marked so the
// board refuses one holding a key before keeping it.
const OPENING = "Use the shop's bot account, not mine. ".repeat(7) + "Use the shop's bot account, not mi";
for (const card of CARDS) {
  test(`${card.open}: an answer of 20,000 characters and a picture reach the asker whole`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const kept = await keep(page);
    const shown = await openCard(page, card.open);
    await pencilOf(shown).click();
    const note = noteWindow(page).getByLabel("Your note");
    const long = "Use the shop's bot account, not mine.\n".repeat(527).slice(0, 20000);
    await note.fill(long);
    await expect(note).toHaveValue(long);
    await pastePicture(note);
    await expect(noteWindow(page).getByRole("img", { name: "Picture 1" })).toBeVisible();

    // Act
    await noteWindow(page).getByRole("button", { name: "Send", exact: true }).click();

    // Assert
    await expect.poll(() => sent.length).toBe(1);
    const credential = card.open === "Open the credential" ? "1" : null;
    expect(kept.filter((each) => each.name === "answer.txt")).toEqual([{ task: "shop-signup", name: "answer.txt", credential, type: "application/octet-stream", body: long.trim() }]);
    expect(sent[0].body).toMatchObject({ ...card.other, text: "My whole answer is in " + KEPT_ANSWER + ". It begins: " + OPENING + "...\n" + KEPT });
  });
}

const REFUSALS = [
  { open: "Open the command", status: 413, error: "A pasted file may be at most 10 MiB." },
  { open: "Open the credential", status: 400, error: "Your answer holds a word shaped like a key, so nothing was kept or sent." },
] as const;
for (const refusal of REFUSALS) {
  test(`${refusal.open}: a long answer the board does not keep is said in the window, with his words, and nothing is sent`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    await page.route("**/api/composer/file**", (route) => route.fulfill({ status: refusal.status, json: { error: refusal.error } }));
    const shown = await openCard(page, refusal.open);
    await pencilOf(shown).click();
    const note = noteWindow(page).getByLabel("Your note");
    const long = "Use the shop's bot account, not mine.\n".repeat(150).trim();
    await note.fill(long);

    // Act
    await noteWindow(page).getByRole("button", { name: "Send", exact: true }).click();

    // Assert
    await expect(noteWindow(page).getByRole("status").filter({ hasText: "Your answer was not sent" })).toHaveText("Your answer was not sent: " + refusal.error);
    await expect(note).toHaveValue(long);
    await expect(noteWindow(page).getByRole("button", { name: "Send", exact: true })).toBeEnabled();
    expect(sent).toEqual([]);
  });
}

test("Cancel while a long answer is being kept sends nothing", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  let release: () => void = () => {};
  const kept = await keep(page, new Promise<void>((resolve) => { release = resolve; }));
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  await noteWindow(page).getByLabel("Your note").fill("Use the shop's bot account, not mine.\n".repeat(150));
  await noteWindow(page).getByRole("button", { name: "Send", exact: true }).click();
  await expect.poll(() => kept.length).toBe(1);

  // Act
  await noteWindow(page).getByRole("button", { name: "Cancel" }).click();
  const answered = page.waitForResponse("**/api/composer/file**");
  release();
  await answered;
  await page.waitForTimeout(500);

  // Assert
  await expect(noteWindow(page)).toHaveCount(0);
  expect(sent).toEqual([]);
});

// A path of length characters in a folder of his files, as the board keeps it.
const keptPath = (length: number, name: string) => "C:\\" + "d".repeat(length - 19 - name.length) + "\\composer-files\\" + name;

for (const { what, words, length } of [
  { what: "files whose paths alone pass the limit", words: "", length: 2131 },
  { what: "a short note with files whose paths are just under the limit", words: "ok", length: 1999 },
] as const) {
  test(`${what} hold Send and ask for fewer`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    let count = 0;
    await page.route("**/api/composer/file**", (route) => route.request().method() === "POST"
      ? route.fulfill({ status: 201, json: { path: keptPath(length, "pasted-" + ++count + ".png") } })
      : route.fulfill({ status: 200, contentType: "image/png", body: Buffer.from(ONE_PIXEL, "base64") }));
    const shown = await openCard(page, "Open the command");
    await pencilOf(shown).click();
    const note = noteWindow(page).getByLabel("Your note");
    await note.fill(words);
    await pastePicture(note);
    await expect(noteWindow(page).getByRole("img", { name: "Picture 1" })).toBeVisible();
    const isSendOnWithOne = await noteWindow(page).getByRole("button", { name: "Send", exact: true }).isEnabled();

    // Act
    await pastePicture(note);
    await expect(noteWindow(page).getByRole("img", { name: "Picture 2" })).toBeVisible();

    // Assert
    expect(isSendOnWithOne).toBe(true);
    await expect(noteWindow(page).getByText("Use fewer files.")).toBeVisible();
    await expect(noteWindow(page).getByRole("button", { name: "Send", exact: true })).toBeDisabled();
    await note.press("Enter");
    expect(count).toBe(2);
    expect(sent).toEqual([]);
  });
}

test("a long answer's opening is cut so the answer and his file's path fit in 4000 bytes", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  const picture = keptPath(1900, "pasted-1.png");
  const answer = keptPath(1900, "pasted-2.txt");
  await page.route("**/api/composer/file**", (route) => route.request().method() === "POST"
    ? route.fulfill({ status: 201, json: { path: new URL(route.request().url()).searchParams.get("name") === "answer.txt" ? answer : picture } })
    : route.fulfill({ status: 200, contentType: "image/png", body: Buffer.from(ONE_PIXEL, "base64") }));
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  const note = noteWindow(page).getByLabel("Your note");
  await note.fill("Use the shop's bot account, not mine.\n".repeat(150));
  await pastePicture(note);
  await expect(noteWindow(page).getByRole("img", { name: "Picture 1" })).toBeVisible();

  // Act
  await noteWindow(page).getByRole("button", { name: "Send", exact: true }).click();

  // Assert
  await expect.poll(() => sent.length).toBe(1);
  // 4000 bytes less the two 1900-byte paths and the answer's own 39 leave 161 for the opening.
  expect(sent[0].body).toMatchObject({ kind: "run_answer", text: "My whole answer is in " + answer + ". It begins: " + "Use the shop's bot account, not mine. ".repeat(4) + "Use the s...\n" + picture });
});

test("a picture can be taken back out of the pencil's window before Send", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  await keep(page);
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  const note = noteWindow(page).getByLabel("Your note");
  await note.fill("No picture after all");
  await pastePicture(note);

  // Act
  await noteWindow(page).getByRole("button", { name: "Remove Picture 1" }).click();
  await noteWindow(page).getByRole("button", { name: "Send", exact: true }).click();

  // Assert
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ kind: "run_answer", text: "No picture after all" });
});

test("a picture the supervisor could not keep is said in the window and nothing is sent", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  await page.route("**/api/composer/file**", (route) => route.fulfill({ status: 413, json: { error: "A pasted file may be at most 10 MiB." } }));
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  const note = noteWindow(page).getByLabel("Your note");

  // Act
  await pastePicture(note);

  // Assert
  await expect(noteWindow(page).getByText("That picture was not added: A pasted file may be at most 10 MiB.")).toBeVisible();
  await expect(noteWindow(page).getByRole("img")).toHaveCount(0);
  await expect(noteWindow(page).getByRole("button", { name: "Send", exact: true })).toBeDisabled();
  expect(sent).toEqual([]);
});

test("Cancel in the pencil's window sends nothing and leaves the card as it was", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  const shown = await openCard(page, "Open the command");

  // Act
  await pencilOf(shown).click();
  await noteWindow(page).getByRole("button", { name: "Cancel" }).click();

  // Assert
  await expect(noteWindow(page)).toHaveCount(0);
  await expect(shown.getByRole("button", { name: "Run in PowerShell" })).toBeVisible();
  expect(sent).toEqual([]);
});

test("a question with choices and its own Other has a plain Skip beside its Send, and no pencil", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  const shown = await openCard(page, "Open the question");
  const hasOtherChoice = await shown.getByRole("radio", { name: /^Other/ }).isVisible();
  const pencils = await pencilOf(shown).count();

  // Act
  await shown.locator(".card-actions").getByRole("button", { name: "Skip this card" }).click();

  // Assert
  expect(hasOtherChoice).toBe(true);
  expect(pencils).toBe(0);
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ kind: "question_clear", question_id: "q-shop-plan", generation: "q".repeat(64) });
});

test("the pager moves on with Next, never with a second Skip", async ({ page }) => {
  // Arrange
  await record(page);
  await page.goto("/tests/fixtures/skip-or-other.html");

  // Act
  await page.getByRole("button", { name: "Open the command", exact: true }).click();
  const dialog = page.getByRole("dialog");

  // Assert
  await expect(dialog.getByRole("group", { name: "Move between items" }).getByRole("button", { name: "Next" })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Skip", exact: true })).toHaveCount(0);
});

for (const [where, viewport] of [["in his window", { width: 1707, height: 1067 }], ["on a phone", { width: 390, height: 844 }]] as const) {
  test(`the pencil and its window fit every card ${where}`, async ({ page }, testInfo) => {
    // Arrange
    await record(page);
    await keep(page);
    await page.setViewportSize(viewport);

    for (const name of ["Open the command", "Open the wait", "Open the credential"]) {
      // Act
      const shown = await openCard(page, name);
      await pencilOf(shown).scrollIntoViewIfNeeded();
      await page.screenshot({ path: testInfo.outputPath(`skip-or-other-${name.split(" ").pop()}-${viewport.width}.png`) });
      await pencilOf(shown).click();
      const note = noteWindow(page).getByLabel("Your note");
      await note.fill("Use the shop's bot account instead");
      await pastePicture(note);

      // Assert
      await expect(shown.locator(".card-actions").getByRole("button", { name: "Skip this card" })).toBeAttached();
      await expect(noteWindow(page).getByRole("img", { name: "Picture 1" })).toBeVisible();
      await expect(noteWindow(page).getByRole("button", { name: "Send", exact: true })).toBeInViewport();
      const picture = await noteWindow(page).getByRole("img", { name: "Picture 1" }).boundingBox();
      const noteBox = await note.boundingBox();
      const send = await noteWindow(page).getByRole("button", { name: "Send", exact: true }).boundingBox();
      expect(picture).not.toBeNull();
      expect(noteBox).not.toBeNull();
      expect(send).not.toBeNull();
      // The picture sits above his words, as the chat's box shows it.
      expect(picture!.y + picture!.height).toBeLessThanOrEqual(noteBox!.y);
      expect(noteBox!.y + noteBox!.height).toBeLessThanOrEqual(send!.y);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`skip-or-other-${name.split(" ").pop()}-${viewport.width}-note.png`) });
    }
  });
}
