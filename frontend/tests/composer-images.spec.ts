import { crc32, deflateSync } from "node:zlib";
import { expect, test, type Page } from "./site";
import { at, box, field, GOBLIN, open, openGoblin, PAGE, pasted, send, typed } from "./session-board";

// Pictures in Modern Chat (#1579). The Overlord, 2026-10-27: "why cant i copy
// and paste images in modern chat !! fix", then "i should see the images
// previews in chat like claude would". A picture pasted into the box, or a
// file dropped on it, is kept where the session reads it and shows in the box
// as a thumbnail he can remove; Send names its path. In the conversation a
// picture he sent shows in his bubble, and a click opens it larger.

const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
// photo is a grey PNG of width by height, as large as a real screenshot, so
// the picture opened larger can be seen to be larger than its thumbnail.
function photo(width: number, height: number): Buffer {
  const chunk = (type: string, data: Buffer) => {
    const body = Buffer.concat([Buffer.from(type), data]);
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const check = Buffer.alloc(4);
    check.writeUInt32BE(crc32(body));
    return Buffer.concat([length, body, check]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8;
  header[9] = 2;
  const rows = Buffer.alloc((width * 3 + 1) * height, 0x80);
  for (let row = 0; row < height; row++) rows[row * (width * 3 + 1)] = 0;
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", header), chunk("IDAT", deflateSync(rows)), chunk("IEND", Buffer.alloc(0))]);
}
const SCREENSHOT = photo(1600, 1000);
const KEPT = "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-1.png";

// keepFiles plays the supervisor's composer files: each upload is kept as the
// next pasted-N with its name's extension and answers its path, and a kept
// picture reads back as picture to the board's token and the goblin's task.
async function keepFiles(page: Page, picture: Buffer = PNG) {
  const uploads: string[] = [];
  const reads: string[] = [];
  await page.route("**/api/composer/file**", async (route) => {
    const query = new URL(route.request().url()).searchParams;
    if (route.request().method() === "POST") {
      const name = query.get("name") ?? "";
      uploads.push(name);
      return route.fulfill({ status: 201, json: { path: KEPT.replace("pasted-1.png", "pasted-" + uploads.length + name.slice(name.lastIndexOf("."))) } });
    }
    reads.push(query.get("path") ?? "");
    const isOurs = query.get("token") === "fixture" && query.get("task") === GOBLIN.id;
    return isOurs ? route.fulfill({ body: picture, contentType: "image/png" }) : route.fulfill({ status: 403, json: { error: "refused" } });
  });
  return { uploads, reads };
}

// The three ways a picture reaches a paste on his PC, as a browser hands
// each: a screenshot and an image file copied in Explorer in the files list,
// and an image copied from a browser only as an item with an empty files
// list, as some Chromium builds hand it.
const PASTES = [
  { what: "a Win+Shift+S screenshot, in the files list", name: "image.png", inFiles: true },
  { what: "an image file copied in Explorer, in the files list by its own name", name: "diagram.png", inFiles: true },
  { what: "an image copied from a browser, only in the items with an empty files list", name: "image.png", inFiles: false },
];

async function pastePicture(page: Page, name: string, inFiles: boolean) {
  await field(page).evaluate((element, { bytes, name, inFiles }) => {
    const picture = new File([new Uint8Array(bytes)], name, { type: "image/png" });
    const event = new ClipboardEvent("paste", { bubbles: true, cancelable: true });
    if (inFiles) {
      const data = new DataTransfer();
      data.items.add(picture);
      Object.defineProperty(event, "clipboardData", { value: data });
    } else {
      const items = [{ kind: "string", type: "text/html", getAsFile: () => null }, { kind: "file", type: "image/png", getAsFile: () => picture }];
      Object.defineProperty(event, "clipboardData", { value: { types: ["text/html", "Files"], files: [], items, getData: () => "" } });
    }
    element.dispatchEvent(event);
  }, { bytes: [...PNG], name, inFiles });
}

const attachments = (page: Page) => box(page).getByRole("list", { name: "Attached files" });

for (const paste of PASTES) {
  test(`${paste.what}: the picture shows in the box as a thumbnail, and Send names it`, async ({ page }) => {
    // Arrange
    const relay = await open(page);
    const kept = await keepFiles(page);
    await openGoblin(page);
    await field(page).fill("what is wrong here?");

    // Act
    await pastePicture(page, paste.name, paste.inFiles);

    // Assert: a thumbnail in the box's border, above his words, read back from
    // where it was kept, and his words untouched.
    const thumbnail = attachments(page).getByRole("img", { name: "Picture 1" });
    await expect(thumbnail).toBeVisible();
    await expect.poll(() => thumbnail.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth)).toBe(1);
    expect(kept.uploads).toEqual([paste.name]);
    expect(kept.reads).toEqual([KEPT]);
    await expect(field(page)).toHaveValue("what is wrong here?");
    const [border, picture, words] = await Promise.all([box(page).locator(".composer-input"), thumbnail, field(page)].map((part) => part.boundingBox()));
    expect(border && picture && words).toBeTruthy();
    if (!border || !picture || !words) return;
    expect(picture.y + picture.height).toBeLessThanOrEqual(words.y);
    expect(picture.y).toBeGreaterThanOrEqual(border.y);

    // Act
    await send(page).click();

    // Assert: his words, then the picture's path on its own line, as one
    // paste, whose line breaks a terminal takes as returns, and Enter; the
    // box empties.
    await expect.poll(() => typed(relay)).toContain(pasted("what is wrong here?\r" + KEPT) + "\r");
    await expect(field(page)).toHaveValue("");
    await expect(attachments(page)).toHaveCount(0);
  });
}

test("a file dropped on the box is kept and shows as one added with Add files", async ({ page }) => {
  // Arrange
  await open(page);
  const kept = await keepFiles(page);
  await openGoblin(page);

  // Act
  const isAccepted = await box(page).locator(".composer-input").evaluate((element, bytes) => {
    const data = new DataTransfer();
    data.items.add(new File([new Uint8Array(bytes)], "dragged.png", { type: "image/png" }));
    data.items.add(new File(["# notes"], "notes.md", { type: "text/markdown" }));
    const over = new DragEvent("dragover", { bubbles: true, cancelable: true, dataTransfer: data });
    element.dispatchEvent(over);
    element.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer: data }));
    return over.defaultPrevented;
  }, [...PNG]);

  // Assert
  expect(isAccepted, "the box takes the drag, so the drop is not refused").toBe(true);
  await expect(attachments(page).getByRole("img", { name: "Picture 1" })).toBeVisible();
  await expect(attachments(page)).toContainText("pasted-2.md");
  expect(kept.uploads).toEqual(["dragged.png", "notes.md"]);
});

test("removing a thumbnail takes the picture out of what Send sends", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await keepFiles(page);
  await openGoblin(page);
  await pastePicture(page, "first.png", true);
  await expect(attachments(page).getByRole("img", { name: "Picture 1" })).toBeVisible();
  await pastePicture(page, "second.png", true);
  await expect(attachments(page).getByRole("img", { name: "Picture 2" })).toBeVisible();

  // Act
  await attachments(page).getByRole("button", { name: "Remove Picture 1" }).click();
  await send(page).click();

  // Assert: only the second picture went, alone, and the box is empty.
  const second = KEPT.replace("pasted-1", "pasted-2");
  await expect.poll(() => typed(relay)).toContain(pasted(second) + "\r");
  expect(typed(relay)).not.toContain(KEPT);
  await expect(attachments(page)).toHaveCount(0);
});

test("a picture whose upload fails adds no thumbnail and says why", async ({ page }) => {
  // Arrange
  await open(page);
  await page.route("**/api/composer/file**", (route) => route.fulfill({ status: 413, json: { error: "A pasted file may be at most 10 MiB." } }));
  await openGoblin(page);

  // Act
  await pastePicture(page, "huge.png", false);

  // Assert
  await expect(box(page).getByRole("alert")).toHaveText("That picture was not added: A pasted file may be at most 10 MiB.");
  await expect(attachments(page)).toHaveCount(0);
});

// A conversation where he sent a picture through the box, whose message
// names its kept path, and a reply whose tool read a picture back.
const PICTURED = { messages: [
  ...PAGE.messages,
  { id: "m2", seq: 2, at, kind: "prompt", source: "overlord", text: "what is wrong here?\n" + KEPT },
  { id: "m3", seq: 3, at, kind: "reply", blocks: [{ kind: "call", tool: "Read", subject: KEPT, state: "done", result: "", pictures: [{ id: "pic1", media_type: "image/png", bytes: PNG.length }] }, { kind: "text", text: "The button overlaps the title." }] },
], more: false };

for (const width of [1440, 390]) {
  test(`a picture he sent shows above his bubble, loads lazily and opens larger on click at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 900 });
    await open(page, undefined, undefined, PICTURED);
    const kept = await keepFiles(page, SCREENSHOT);
    await openGoblin(page);
    const sent = page.locator('[data-message="m2"]');
    const bubble = sent.locator(".message-bubble");

    // Act
    await expect(bubble).toBeVisible();

    // Assert: the picture above his bubble, as Claude shows one (the
    // Overlord, 2026-10-28: "see how claude includes pictures when pasted"),
    // and the path not shown as words.
    const preview = sent.locator(".message-picture img");
    await expect(preview).toBeVisible();
    await expect(preview).toHaveAttribute("loading", "lazy");
    await expect(preview).toHaveAttribute("decoding", "async");
    await expect(preview).toHaveAttribute("alt", "Picture 1");
    await expect.poll(() => preview.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth)).toBe(1600);
    await expect(bubble).toHaveText("what is wrong here?");
    const [picture, words] = await Promise.all([preview.boundingBox(), bubble.boundingBox()]);
    expect(picture && words && picture.y + picture.height <= words.y, "the picture above the bubble").toBeTruthy();
    await expect(page.getByRole("log")).not.toContainText("composer-files");
    expect(kept.reads).toEqual([KEPT]);
    await sent.screenshot({ path: testInfo.outputPath(`bubble-preview-${width}.png`) });

    // Act
    await sent.getByRole("button", { name: "Open picture 1" }).click();

    // Assert: the picture larger, over the board, and Escape puts it away
    // without opening the message's Copy row.
    const viewer = page.getByRole("dialog", { name: "Pictures" });
    await expect(viewer).toBeVisible();
    const large = viewer.getByRole("img", { name: "Picture 1" });
    await expect(large).toBeVisible();
    expect((await large.boundingBox())?.width ?? 0).toBeGreaterThan((await preview.boundingBox())?.width ?? 0);
    await page.screenshot({ path: testInfo.outputPath(`picture-viewer-${width}.png`) });
    await page.keyboard.press("Escape");
    await expect(viewer).toHaveCount(0);
    await expect(page.locator('[data-message="m2"]')).not.toHaveClass(/actions-open/);
  });
}

test("a picture the harness read back previews in its call and opens larger", async ({ page }) => {
  // Arrange
  await open(page, undefined, undefined, PICTURED);
  await keepFiles(page);
  await page.route("**/api/messages**", (route) => new URL(route.request().url()).searchParams.get("picture") === "pic1" ? route.fulfill({ body: PNG, contentType: "image/png" }) : route.fallback());
  await openGoblin(page);
  const reply = page.locator('[data-message="m3"]');

  // Act
  await reply.getByText("Read", { exact: true }).click();
  await reply.getByRole("button", { name: "Open picture 1" }).click();

  // Assert
  await expect(page.getByRole("dialog", { name: "Pictures" }).getByRole("img", { name: "Picture 1" })).toBeVisible();
});

for (const width of [1440, 390]) {
  test(`the box's thumbnail row fits inside its border at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 900 });
    await open(page);
    await keepFiles(page);
    await openGoblin(page);
    await field(page).fill("the header overlaps on my phone");

    // Act
    await pastePicture(page, "one.png", true);
    await expect(attachments(page).getByRole("img", { name: "Picture 1" })).toBeVisible();
    await box(page).locator('input[type="file"]').setInputFiles({ name: "notes.md", mimeType: "text/markdown", buffer: Buffer.from("# notes") });
    await expect(attachments(page)).toContainText("pasted-2.md");

    // Assert
    const [border, row] = await Promise.all([box(page).locator(".composer-input"), attachments(page)].map((part) => part.boundingBox()));
    expect(border && row).toBeTruthy();
    if (!border || !row) return;
    expect(row.x).toBeGreaterThanOrEqual(border.x);
    expect(row.x + row.width).toBeLessThanOrEqual(border.x + border.width);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await box(page).screenshot({ path: testInfo.outputPath(`thumbnail-row-${width}.png`) });
  });
}
