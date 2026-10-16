import { expect, test, type Page, type Route } from "./site";

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

const pencilOf = (card: ReturnType<Page["locator"]>) => card.locator(".card-actions").getByRole("button", { name: "Answer in your own words" });
const noteWindow = (page: Page) => page.getByRole("dialog", { name: "Your answer" });

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
      heard.push(await route.request().headerValue("X-CFO-Token"));
      return route.fulfill({ json: { text: "use the bot account", engine: "test-model" } });
    });
    const shown = await openCard(page, card.open);
    await pencilOf(shown).click();
    const note = noteWindow(page).getByLabel("Your note");
    await expect(note).toBeFocused();

    // Act
    await page.keyboard.down("Control");
    await page.keyboard.down("Shift");
    await page.keyboard.down("Space");
    await expect(noteWindow(page).getByRole("button", { name: "Listening" })).toHaveClass(/recording/);
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
  await expect(noteWindow(page).getByRole("heading", { name: "Your answer" })).toBeVisible();
  expect((await noteWindow(page).innerText()).toLowerCase()).not.toContain("skip");
  expect(await noteWindow(page).getByRole("button", { name: "Send", exact: true }).locator("svg").count()).toBe(1);
});

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

test("Send waits for a pasted picture and accepts a picture without words", async ({ page }) => {
  const sent = await record(page);
  let release: () => void = () => {};
  const upload = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/answer-images**", async (route) => {
    await upload;
    await route.fulfill({ status: 201, json: { path: "C:\\fleet\\pasted-1.png" } });
  });
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  await noteWindow(page).getByLabel("Your note").evaluate((box) => {
    const clipboard = new DataTransfer();
    clipboard.items.add(new File([new Uint8Array([137, 80, 78, 71])], "image.png", { type: "image/png" }));
    box.dispatchEvent(new ClipboardEvent("paste", { clipboardData: clipboard, bubbles: true, cancelable: true }));
  });

  const send = noteWindow(page).getByRole("button", { name: "Send", exact: true });
  await expect(send).toBeDisabled();
  await expect(noteWindow(page).getByRole("status")).toHaveText("Saving picture...");
  release();
  await expect(send).toBeEnabled();
  await send.click();

  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ kind: "run_answer", text: "Image: C:\\fleet\\pasted-1.png" });
});

test("a picture pasted into the pencil's window is kept beside the task and sent with his note", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  const kept: { task: string | null; type: string | undefined; size: number }[] = [];
  const saved = "C:\\fleet\\state\\tasktmp\\shop-signup\\answer-images\\pasted-1.png";
  await page.route("**/api/answer-images**", async (route) => {
    const url = new URL(route.request().url());
    kept.push({ task: url.searchParams.get("task"), type: route.request().headers()["content-type"], size: route.request().postDataBuffer()?.length || 0 });
    await route.fulfill({ status: 201, json: { path: saved } });
  });
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  const note = noteWindow(page).getByLabel("Your note");
  await note.fill("Like this one");

  // Act
  await note.evaluate((box) => {
    const bytes = Uint8Array.from(atob("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="), (c) => c.charCodeAt(0));
    const clipboard = new DataTransfer();
    clipboard.items.add(new File([bytes], "image.png", { type: "image/png" }));
    box.dispatchEvent(new ClipboardEvent("paste", { clipboardData: clipboard, bubbles: true, cancelable: true }));
  });
  await expect(noteWindow(page).getByRole("img", { name: "Pasted picture 1" })).toBeVisible();
  await noteWindow(page).getByRole("button", { name: "Send", exact: true }).click();

  // Assert
  expect(kept).toEqual([{ task: "shop-signup", type: "image/png", size: 70 }]);
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ kind: "run_answer", run_id: "run-shop-signup-7", text: "Like this one\n\nImage: " + saved });
});

test("a picture can be taken back out of the pencil's window before Send", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  await page.route("**/api/answer-images**", (route) => route.fulfill({ status: 201, json: { path: "C:\\fleet\\pasted-1.png" } }));
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  const note = noteWindow(page).getByLabel("Your note");
  await note.fill("No picture after all");
  await note.evaluate((box) => {
    const clipboard = new DataTransfer();
    clipboard.items.add(new File([new Uint8Array([137, 80, 78, 71])], "image.png", { type: "image/png" }));
    box.dispatchEvent(new ClipboardEvent("paste", { clipboardData: clipboard, bubbles: true, cancelable: true }));
  });

  // Act
  await noteWindow(page).getByRole("button", { name: "Remove picture 1" }).click();
  await noteWindow(page).getByRole("button", { name: "Send", exact: true }).click();

  // Assert
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ kind: "run_answer", text: "No picture after all" });
});

test("a picture the supervisor could not keep is said in the window and nothing is sent", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  await page.route("**/api/answer-images**", (route) => route.fulfill({ status: 400, json: { error: "Only a PNG, JPEG, GIF or WebP picture can be pasted." } }));
  const shown = await openCard(page, "Open the command");
  await pencilOf(shown).click();
  const note = noteWindow(page).getByLabel("Your note");

  // Act
  await note.evaluate((box) => {
    const clipboard = new DataTransfer();
    clipboard.items.add(new File([new Uint8Array([1, 2, 3])], "image.png", { type: "image/png" }));
    box.dispatchEvent(new ClipboardEvent("paste", { clipboardData: clipboard, bubbles: true, cancelable: true }));
  });

  // Assert
  await expect(noteWindow(page).getByRole("status")).toHaveText("Only a PNG, JPEG, GIF or WebP picture can be pasted.");
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
    await page.route("**/api/answer-images**", (route) => route.fulfill({ status: 201, json: { path: "C:\\fleet\\pasted-1.png" } }));
    await page.setViewportSize(viewport);

    for (const name of ["Open the command", "Open the wait", "Open the credential"]) {
      // Act
      const shown = await openCard(page, name);
      await pencilOf(shown).scrollIntoViewIfNeeded();
      await page.screenshot({ path: testInfo.outputPath(`skip-or-other-${name.split(" ").pop()}-${viewport.width}.png`) });
      await pencilOf(shown).click();
      const note = noteWindow(page).getByLabel("Your note");
      await note.fill("Use the shop's bot account instead");
      await note.evaluate((box) => {
        const bytes = Uint8Array.from(atob("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="), (c) => c.charCodeAt(0));
        const clipboard = new DataTransfer();
        clipboard.items.add(new File([bytes], "image.png", { type: "image/png" }));
        box.dispatchEvent(new ClipboardEvent("paste", { clipboardData: clipboard, bubbles: true, cancelable: true }));
      });

      // Assert
      await expect(shown.locator(".card-actions").getByRole("button", { name: "Skip this card" })).toBeAttached();
      await expect(noteWindow(page).getByRole("img", { name: "Pasted picture 1" })).toBeVisible();
      await expect(noteWindow(page).getByRole("button", { name: "Send", exact: true })).toBeInViewport();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`skip-or-other-${name.split(" ").pop()}-${viewport.width}-note.png`) });
    }
  });
}
