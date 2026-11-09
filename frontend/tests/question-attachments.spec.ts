import { expect, openItem, test, type Page } from "./site";

// Files in a question card's written answer (#2206). The Overlord asked on
// 2026-11-09 for attachments in the Other box, because a written answer often
// needs a screenshot. A picture pasted into the box, a file dropped on it and
// a file picked with Add files are kept where the asker reads them, as the
// composer keeps them, and show above his words as chips he can remove, each
// by the name it was kept under; Send names each kept path on its own line
// after his words.

const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
const KEPT = "C:\\state\\tasktmp\\cg-ask\\composer-files\\";

// keepFiles plays the supervisor's composer files: each upload is kept as the
// next pasted-N with its name's extension and answers its path, and a kept
// picture reads back to the board's token. release lets a held upload finish.
async function keepFiles(page: Page, { isHeld = false } = {}) {
  const uploads: { name: string; task: string | null }[] = [];
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/composer/file**", async (route) => {
    const query = new URL(route.request().url()).searchParams;
    if (route.request().method() !== "POST") return route.fulfill({ body: PNG, contentType: "image/png" });
    const name = query.get("name") ?? "";
    uploads.push({ name, task: query.get("task") });
    const number = uploads.length;
    if (isHeld) await held;
    return route.fulfill({ status: 201, json: { path: KEPT + "pasted-" + number + name.slice(name.lastIndexOf(".")) } });
  });
  return { uploads, release };
}

async function recordAnswers(page: Page) {
  const sent: Record<string, unknown>[] = [];
  await page.route("**/api/actions", async (route) => {
    const body = route.request().postDataJSON();
    sent.push(body);
    await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
  });
  return sent;
}

const answerBox = (page: Page) => page.getByRole("dialog").getByRole("textbox", { name: "Your written answer" });
const chips = (page: Page) => page.getByRole("dialog").getByRole("list", { name: "Attached files" });

async function pastePicture(page: Page) {
  await answerBox(page).evaluate((element, bytes) => {
    const data = new DataTransfer();
    data.items.add(new File([new Uint8Array(bytes)], "image.png", { type: "image/png" }));
    const event = new ClipboardEvent("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(event, "clipboardData", { value: data });
    element.dispatchEvent(event);
  }, [...PNG]);
}

async function dropFile(page: Page, name: string) {
  return answerBox(page).evaluate((element, name) => {
    const data = new DataTransfer();
    data.items.add(new File(["# notes"], name, { type: "text/markdown" }));
    const over = new DragEvent("dragover", { bubbles: true, cancelable: true, dataTransfer: data });
    element.dispatchEvent(over);
    element.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer: data }));
    return over.defaultPrevented;
  }, name);
}

for (const { label, viewport, isTouch } of [
  { label: "a desktop", viewport: { width: 1440, height: 900 }, isTouch: false },
  { label: "a phone", viewport: { width: 390, height: 844 }, isTouch: true },
]) {
  test.describe(`a question's written answer on ${label}`, () => {
    test.use({ viewport, hasTouch: isTouch, isMobile: isTouch });

    // pickFile picks a file with Add files, which on a phone opens its menu
    // first, as the composer's does.
    const pickFile = async (page: Page, name: string) => {
      const chooser = page.waitForEvent("filechooser");
      const add = page.getByRole("dialog").getByRole("button", { name: "Add files", exact: true });
      if (isTouch) {
        await add.tap();
        await page.getByRole("dialog").getByRole("button", { name: "Photos or files", exact: true }).tap();
      } else await add.click();
      await (await chooser).setFiles({ name, mimeType: "text/plain", buffer: Buffer.from("the plan") });
    };

    test("a pasted picture, a dropped file and a picked file show as chips, one comes out, and Send names each kept file", async ({ page }, testInfo) => {
      // Arrange
      const sent = await recordAnswers(page);
      const kept = await keepFiles(page);
      await page.goto("/tests/fixtures/question-input.html");
      await openItem(page, "Which layout should the page use?");
      await page.getByRole("dialog").getByRole("radio", { name: /^Other/ }).check();
      await answerBox(page).fill("Like this screenshot");

      // Act
      await pastePicture(page);
      const isDropTaken = await dropFile(page, "notes.md");
      await pickFile(page, "plan.txt");

      // Assert: each is kept for the goblin that asked and shows above his
      // words, which are untouched.
      expect(isDropTaken, "the box takes the drag, so the drop is not refused").toBe(true);
      await expect(chips(page).getByRole("img", { name: "Picture 1" })).toBeVisible();
      await expect(chips(page)).toContainText("pasted-2.md");
      await expect(chips(page)).toContainText("pasted-3.txt");
      expect(kept.uploads).toEqual([{ name: "image.png", task: "cg-ask" }, { name: "notes.md", task: "cg-ask" }, { name: "plan.txt", task: "cg-ask" }]);
      await expect(answerBox(page)).toHaveValue("Like this screenshot");
      const [list, words] = await Promise.all([chips(page).boundingBox(), answerBox(page).boundingBox()]);
      expect(list && words && list.y + list.height <= words.y + 0.5, "the chips sit above his words").toBe(true);
      await page.getByRole("dialog").screenshot({ path: testInfo.outputPath("answer-attachments.png") });

      // Act
      await chips(page).getByRole("button", { name: "Remove pasted-2.md" }).click();
      await page.getByRole("dialog").getByRole("button", { name: "Send decision" }).click();

      // Assert
      await expect.poll(() => sent.length).toBe(1);
      expect(sent[0]).toMatchObject({ kind: "goblin_answer", question_id: "layout", answer_kind: "other", text: "Like this screenshot\n" + KEPT + "pasted-1.png\n" + KEPT + "pasted-3.txt" });
    });

    test("an answer to the CFO keeps its file in the CFO's folder and may be the file alone", async ({ page }) => {
      // Arrange
      const sent = await recordAnswers(page);
      const kept = await keepFiles(page);
      await page.goto("/tests/fixtures/question-input.html");
      await openItem(page, "How should we handle the history?");
      const send = page.getByRole("dialog").getByRole("button", { name: "Send decision" });
      await expect(send).toBeDisabled();

      // Act
      await pickFile(page, "history.txt");

      // Assert
      await expect(chips(page)).toContainText("pasted-1.txt");
      expect(kept.uploads).toEqual([{ name: "history.txt", task: null }]);
      await send.click();
      await expect.poll(() => sent.length).toBe(1);
      expect(sent[0]).toMatchObject({ kind: "cfo_answer", question_id: "written", answer_kind: "other", text: KEPT + "pasted-1.txt" });
    });

    test("Send waits while a file is still being kept, and a file the board refuses says why and adds nothing", async ({ page }) => {
      // Arrange
      const sent = await recordAnswers(page);
      const kept = await keepFiles(page, { isHeld: true });
      await page.goto("/tests/fixtures/question-input.html");
      await openItem(page, "How should we handle the history?");
      await answerBox(page).fill("Keep the history");
      const send = page.getByRole("dialog").getByRole("button", { name: "Send decision" });

      // Act
      await pastePicture(page);

      // Assert
      await expect.poll(() => kept.uploads.length).toBe(1);
      await expect(send).toBeDisabled();
      await answerBox(page).press("Enter");
      kept.release();
      await expect(chips(page).getByRole("img", { name: "Picture 1" })).toBeVisible();
      await expect(send).toBeEnabled();
      expect(sent).toEqual([]);

      // Act
      await page.unroute("**/api/composer/file**");
      await page.route("**/api/composer/file**", (route) => route.fulfill({ status: 409, json: { error: "The goblin that task names has ended, so the file was not kept." } }));
      await dropFile(page, "late.md");

      // Assert
      await expect(page.getByRole("dialog").getByRole("status").filter({ hasText: "That file was not added" })).toContainText("so the file was not kept");
      await expect(chips(page).getByRole("listitem")).toHaveCount(1);
    });

    if (isTouch) test("Paste a picture adds the picture the clipboard holds", async ({ page }) => {
      // Arrange
      await page.addInitScript((bytes) => {
        const item = { types: ["image/png"], getType: async () => new Blob([new Uint8Array(bytes)], { type: "image/png" }) };
        Object.defineProperty(navigator, "clipboard", { configurable: true, value: { read: async () => [item] } });
      }, [...PNG]);
      const kept = await keepFiles(page);
      await page.goto("/tests/fixtures/question-input.html");
      await openItem(page, "How should we handle the history?");

      // Act
      await page.getByRole("dialog").getByRole("button", { name: "Add files", exact: true }).tap();
      await page.getByRole("dialog").getByRole("button", { name: "Paste a picture", exact: true }).tap();

      // Assert
      await expect(chips(page).getByRole("img", { name: "Picture 1" })).toBeVisible();
      expect(kept.uploads).toEqual([{ name: "pasted.png", task: null }]);
      await expect(page.getByRole("dialog").getByRole("button", { name: "Paste a picture", exact: true })).toHaveCount(0);
    });
  });
}
