import { expect, test, type Page } from "./site";
import { openPictured } from "./chat-pictures";
import { WINDOW } from "./desktop-window";
import { box, field } from "./session-board";

// The Overlord, 2026-11-17 (#2543): "When I click and drag In, like, a
// terminal or a window inside Co-Goblins in the chat window, and it selects an
// image. it should copy that image along with the text to the clipboard."
// Each test drags across the words of the fixture's reply and the three
// pictures its calls took between them, as a mouse does, in a browser and in
// the desktop window, whose own script runs over the same page.
const PLACES = [
  { name: "a browser", script: "" },
  { name: "the desktop window", script: WINDOW },
];
const FIRST = "Reading the screenshots.";
const LAST = "The button overlaps the title.";
const KEPT = "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-";

// Every page of a test tells pictures apart the same way: signature draws one,
// from its address or its bytes, as the colours of an 8 by 8 grid. Each is
// decoded as an image from an address, its bytes read as data, since a
// picture decoded as a bitmap instead shrinks what is see-through its own
// way. shown reads what a part of a page shows, its words and its pictures in
// their order, each picture by its signature, or by its path where it is held
// as an address rather than inline.
const READERS = `window.signature = async (from) => {
  const picture = new Image();
  picture.src = typeof from === "string" ? from : await new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(from);
  });
  await picture.decode();
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 8;
  const context = canvas.getContext("2d");
  context.drawImage(picture, 0, 0, 8, 8);
  return [...context.getImageData(0, 0, 8, 8).data];
};
window.shown = async (root) => {
  const parts = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (node.nodeName === "IMG") parts.push(node.src.startsWith("data:") ? await window.signature(node.src) : "address " + new URL(node.src).pathname);
    else if (node.nodeType === Node.TEXT_NODE && node.textContent.trim()) parts.push(node.textContent.trim());
  }
  return parts;
};`;
type Part = string | number[];
type Readers = { signature: (from: string | Blob) => Promise<number[]>; shown: (root: Node) => Promise<Part[]> };

// originalsOf waits until the reply's three pictures are drawn and answers
// them by their signatures, in their order.
async function originalsOf(page: Page) {
  const pictures = page.locator('[data-message="m1"] .call-picture img');
  await expect(pictures).toHaveCount(3);
  for (const picture of await pictures.all()) await expect.poll(() => picture.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0)).toBe(true);
  return pictures.evaluateAll((images: HTMLImageElement[]) => Promise.all(images.map((image) => (window as unknown as Readers).signature(image.src))));
}

// drag selects from the start of the reply's first words to the end of its
// last, across its calls and their pictures, and answers the reply's pictures
// by their signatures and what the selection reads as text.
async function drag(page: Page, from = FIRST, to = LAST) {
  const originals = await originalsOf(page);
  const reply = page.locator('[data-message="m1"]');
  const start = (await reply.getByText(from).boundingBox())!;
  const end = (await reply.getByText(to).boundingBox())!;
  await page.mouse.move(start.x + 1, start.y + start.height / 2);
  await page.mouse.down();
  await page.mouse.move(end.x + end.width - 1, end.y + end.height / 2, { steps: 12 });
  await page.mouse.up();
  return { originals, text: await page.evaluate(() => getSelection()?.toString() ?? "") };
}

// clipboardOf reads what the clipboard holds: its types, its text as a page
// reads it, what its HTML shows, and the signature of the picture it holds as
// a picture. A copy that lands while it reads changes the clipboard under the
// read, as the chat's own copy does once a key is released, so a read that
// finds it changed is tried again, five times at most.
async function clipboardOf(page: Page) {
  return page.evaluate(async () => {
    for (let attempt = 1; ; attempt++) {
      try {
        const [item] = await navigator.clipboard.read();
        const read = async (type: string) => item.types.includes(type) ? item.getType(type) : null;
        const text = (await (await read("text/plain"))?.text() ?? "").replace(/\r\n/g, "\n");
        const html = await (await read("text/html"))?.text() ?? "";
        const picture = await read("image/png");
        return {
          types: [...item.types].sort(),
          text,
          html,
          shown: await (window as unknown as Readers).shown(new DOMParser().parseFromString(html, "text/html").body),
          picture: picture && await (window as unknown as Readers).signature(picture),
        };
      } catch (error) {
        if (attempt >= 5 || !(error instanceof DOMException && error.name === "InvalidStateError")) throw error;
      }
    }
  });
}

// named names each picture shown by its place among the reply's pictures,
// whose signature is within 8 of its own in every colour of every cell, and
// keeps only the reply's first and last words besides, so a list reads as the
// words and pictures in their order.
function named(parts: Part[], originals: number[][]): string[] {
  return parts.flatMap((part) => {
    if (typeof part === "string") return part === FIRST || part === LAST || part.startsWith("address ") ? [part] : [];
    const at = originals.findIndex((original) => original.every((value, index) => Math.abs(value - part[index]) <= 8));
    return [at < 0 ? "an unknown picture" : "picture " + (at + 1)];
  });
}
const IN_ORDER = [FIRST, "picture 1", "picture 2", "picture 3", LAST];

for (const place of PLACES) {
  test.describe(`in ${place.name}`, () => {
    test.beforeEach(async ({ context }) => {
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      await context.addInitScript({ content: READERS });
      if (place.script) await context.addInitScript({ content: place.script });
    });

    test("a drag across words and pictures copies both in their order, so a rich paste shows both and a plain one the words", async ({ page, context }) => {
      // Arrange
      await openPictured(page);

      // Act
      const selection = await drag(page);
      await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
      const copied = await clipboardOf(page);

      // Assert: the words as text, as a selection without pictures copies
      // them, HTML with the words and the pictures in their order, each
      // inline, and the first picture as a picture.
      expect(copied.types).toEqual(["image/png", "text/html", "text/plain"]);
      expect(copied.text).toBe(selection.text);
      expect(named(copied.shown, selection.originals)).toEqual(IN_ORDER);
      expect(named([copied.picture ?? []], selection.originals)).toEqual(["picture 1"]);

      // Act: he pastes it into a rich target and a plain one.
      const target = await context.newPage();
      await target.setContent('<div contenteditable="true" aria-label="Rich"></div><textarea aria-label="Plain"></textarea>');
      await target.getByLabel("Rich").focus();
      await target.keyboard.press("Control+V");
      await target.getByLabel("Plain").focus();
      await target.keyboard.press("Control+V");

      // Assert: the rich one shows the words and the pictures in their order,
      // the plain one the words.
      await expect.poll(async () => named(await target.getByLabel("Rich").evaluate((element) => (window as unknown as Readers).shown(element)), selection.originals)).toEqual(IN_ORDER);
      await expect(target.getByLabel("Plain")).toHaveValue(selection.text);
    });

    test("Ctrl+C on words and pictures copies the same, and never a picture's board address", async ({ page }) => {
      // Arrange: the drag has copied, and something else was copied since.
      await openPictured(page);
      const selection = await drag(page);
      await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
      await page.evaluate(() => navigator.clipboard.writeText("copied since"));

      // Act
      await page.keyboard.down("Control");
      await page.keyboard.down("KeyC");
      const held = await clipboardOf(page);
      await page.keyboard.up("KeyC");
      await page.keyboard.up("Control");

      // Assert: while the key is down no picture's board address, whose
      // query carries the board's token, is on the clipboard, and once it is
      // up the words and the pictures are, in their order.
      expect(held.html).not.toContain("/api/");
      expect(named(held.shown, selection.originals)).not.toContain("address /api/messages");
      await expect.poll(async () => (await clipboardOf(page)).types).toEqual(["image/png", "text/html", "text/plain"]);
      const copied = await clipboardOf(page);
      expect(copied.text).toBe(selection.text);
      expect(named(copied.shown, selection.originals)).toEqual(IN_ORDER);
    });

    test("a selection of a picture alone copies the picture, and never its board address", async ({ page }) => {
      // Arrange: in the list, the first call's thumbnail and no words are
      // selected, as a drag across that thumbnail alone selects it, and
      // something else was copied since.
      await openPictured(page);
      const originals = await originalsOf(page);
      await page.evaluate(() => navigator.clipboard.writeText("copied since"));
      await page.getByRole("log", { name: /^Messages of / }).focus();
      await page.locator('[data-message="m1"] .call-picture').first().evaluate((thumbnail) => {
        const range = document.createRange();
        range.selectNode(thumbnail);
        getSelection()?.removeAllRanges();
        getSelection()?.addRange(range);
      });

      // Act
      await page.keyboard.press("Control+C");

      // Assert: the clipboard holds that picture, inline in its HTML and as
      // a picture, and no board address.
      await expect.poll(async () => (await clipboardOf(page)).types).toContain("image/png");
      const copied = await clipboardOf(page);
      expect(copied.html).not.toContain("/api/");
      expect(named(copied.shown, originals)).toEqual(["picture 1"]);
      expect(named([copied.picture ?? []], originals)).toEqual(["picture 1"]);
    });

    test("pasting the selection into the chat's box attaches its pictures in their order and keeps its words", async ({ page }) => {
      // Arrange: the box keeps each picture as the supervisor does.
      await openPictured(page);
      const uploads: string[] = [];
      await page.route("**/api/composer/file**", (route) => {
        if (route.request().method() !== "POST") return route.fallback();
        uploads.push("data:image/png;base64," + route.request().postDataBuffer()!.toString("base64"));
        return route.fulfill({ status: 201, json: { path: KEPT + uploads.length + ".png" } });
      });
      const selection = await drag(page);
      await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();

      // Act
      await field(page).focus();
      await page.keyboard.press("Control+V");

      // Assert: each picture is attached as a pasted picture is, in their
      // order, and his box holds the words.
      const attached = box(page).getByRole("list", { name: "Attached files" }).getByRole("listitem");
      await expect(attached).toHaveCount(3);
      const kept = await page.evaluate((each) => Promise.all(each.map((upload) => (window as unknown as Readers).signature(upload))), uploads);
      expect(named(kept, selection.originals)).toEqual(["picture 1", "picture 2", "picture 3"]);
      await expect(field(page)).toHaveValue(selection.text);
    });

    test("a selection with no picture copies its words alone, as it always has", async ({ page }) => {
      // Arrange
      await openPictured(page);

      // Act: a drag across the reply's first words only.
      await drag(page, FIRST, FIRST);
      await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
      const dragged = await clipboardOf(page);
      await page.evaluate(() => navigator.clipboard.writeText("copied since"));
      await page.keyboard.press("Control+C");

      // Assert: the drag and Ctrl+C each leave the words alone on it.
      expect(dragged.types).toEqual(["text/plain"]);
      expect(dragged.text).toBe(FIRST);
      await expect.poll(async () => { const copied = await clipboardOf(page); return [copied.types, copied.text]; }).toEqual([["text/plain"], FIRST]);
    });
  });
}
