import { expect, test, type Page } from "./site";
import { fold, hoverCard, openPictured } from "./chat-pictures";
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
// Another page's picture as its HTML holds it inline: a PNG of one pixel.
const PLOT = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";

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

// originalsOf waits until the reply's three pictures are drawn where it shows
// them, at the end of its folded row or in its open calls, and answers them
// by their signatures, in their order.
async function originalsOf(page: Page) {
  const pictures = page.locator('[data-message="m1"] .call-picture img').filter({ visible: true });
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
const ALL = ["image/png", "text/html", "text/plain"];

// keepUploads has the box keep each picture as the supervisor does, and
// answers each picture it sent to be kept as PNG data, in the order it sent
// them.
async function keepUploads(page: Page): Promise<string[]> {
  const uploads: string[] = [];
  await page.route("**/api/composer/file**", (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    uploads.push("data:image/png;base64," + route.request().postDataBuffer()!.toString("base64"));
    return route.fulfill({ status: 201, json: { path: KEPT + uploads.length + ".png" } });
  });
  return uploads;
}

const attachedFiles = (page: Page) => box(page).getByRole("list", { name: "Attached files" }).getByRole("listitem");

// attached waits until the box has kept every picture it was given, which it
// shows as attached files, and answers them by their signatures, in their
// order. Add files is disabled while the box still adds one.
async function attached(page: Page, uploads: string[]) {
  await expect(attachedFiles(page).first()).toBeVisible();
  await expect(box(page).getByRole("button", { name: "Add files" })).toBeEnabled();
  return page.evaluate((each) => Promise.all(each.map((upload) => (window as unknown as Readers).signature(upload))), uploads);
}

// The copies that cannot hold every picture of a drag across the reply: with
// no clipboard for a picture, as a browser offers none over plain HTTP, or
// with pictures the copy cannot read, which the page showed before and the
// supervisor no longer serves.
const FALLBACKS: { name: string; script: string; unread: string[]; types: string[]; shown: string[]; picture: string[] }[] = [
  { name: "with no clipboard for a picture copies the words alone", script: "delete window.ClipboardItem;", unread: [], types: ["text/plain"], shown: [], picture: [] },
  { name: "with a picture that cannot be read copies the words and the other pictures", script: "", unread: ["home"], types: ALL, shown: [FIRST, "picture 2", "picture 3", LAST], picture: ["picture 2"] },
  { name: "with no picture that can be read copies the words alone", script: "", unread: ["home", "team", "page"], types: ["text/plain"], shown: [], picture: [] },
];

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
      expect(copied.types).toEqual(ALL);
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

    test("Ctrl+C on words and pictures copies the same at once, reads no picture again, and never a picture's board address", async ({ page }) => {
      // Arrange: the drag has copied, and something else was copied since.
      await openPictured(page);
      const reads: string[] = [];
      page.on("request", (request) => {
        const address = new URL(request.url());
        if (request.resourceType() === "fetch" && address.searchParams.has("picture")) reads.push(address.searchParams.get("picture") ?? "");
      });
      const selection = await drag(page);
      await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
      await page.evaluate(() => navigator.clipboard.writeText("copied since"));

      // Act
      await page.keyboard.down("Control");
      await page.keyboard.down("KeyC");
      const held = await clipboardOf(page);
      await page.keyboard.up("KeyC");
      await page.keyboard.up("Control");

      // Assert: while the key is down the words and the pictures are on the
      // clipboard already, in their order, and no picture's board address,
      // whose query carries the board's token; once it is up the first
      // picture is too. The drag's copy read each picture once, and no copy
      // after it read one again.
      expect(held.html).not.toContain("/api/");
      expect(named(held.shown, selection.originals)).toEqual(IN_ORDER);
      await expect.poll(async () => (await clipboardOf(page)).types).toEqual(ALL);
      const copied = await clipboardOf(page);
      expect(copied.text).toBe(selection.text);
      expect(named(copied.shown, selection.originals)).toEqual(IN_ORDER);
      expect([...reads].sort()).toEqual(["home", "page", "team"]);
    });

    test("Ctrl+C with the pointer on a thumbnail copies the words and pictures without the card it shows", async ({ page }) => {
      // Arrange: after the drag the pointer rests on the first thumbnail,
      // whose card shows its picture larger with its verb and its place, and
      // something else was copied since.
      await openPictured(page);
      const selection = await drag(page);
      await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
      await fold(page).locator("> summary .call-picture").first().hover();
      await expect(hoverCard(page)).toBeVisible();
      await page.evaluate(() => navigator.clipboard.writeText("copied since"));

      // Act
      await page.keyboard.press("Control+C");

      // Assert: the words and the pictures in their order, each once, and
      // nothing of the card.
      await expect.poll(async () => (await clipboardOf(page)).types).toEqual(ALL);
      const copied = await clipboardOf(page);
      expect(named(copied.shown, selection.originals)).toEqual(IN_ORDER);
      expect(copied.html).not.toContain("1 of 3");
    });

    test("a drag across an open fold copies each picture once, as its calls show it, and a paste into the box attaches each once", async ({ page }) => {
      // Arrange: the reply's folded row is open, so each of its calls shows
      // its own picture, and the row's end hides the same three. The list
      // keeps its end in view, so the reply, now longer, is brought back
      // into view for the drag.
      await openPictured(page);
      const uploads = await keepUploads(page);
      await fold(page).locator("> summary").click();
      await expect(fold(page).locator("> summary .call-pictures")).toBeHidden();
      await page.locator('[data-message="m1"]').scrollIntoViewIfNeeded();

      // Act
      const selection = await drag(page);
      await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
      const copied = await clipboardOf(page);
      await field(page).focus();
      await page.keyboard.press("Control+V");

      // Assert
      expect(named(copied.shown, selection.originals)).toEqual(IN_ORDER);
      expect(named(await attached(page, uploads), selection.originals)).toEqual(["picture 1", "picture 2", "picture 3"]);
      await expect(attachedFiles(page)).toHaveCount(3);
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

    for (const key of ["Control+V", "Alt+v"]) {
      test(`pasting the selection into the chat's box with ${key} attaches its pictures in their order and keeps its words`, async ({ page }) => {
        // Arrange: the box keeps each picture as the supervisor does.
        await openPictured(page);
        const uploads = await keepUploads(page);
        const selection = await drag(page);
        await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();

        // Act
        await field(page).focus();
        await page.keyboard.press(key);

        // Assert: each picture is attached as a pasted picture is, in their
        // order, and his box holds the words.
        expect(named(await attached(page, uploads), selection.originals)).toEqual(["picture 1", "picture 2", "picture 3"]);
        await expect(attachedFiles(page)).toHaveCount(3);
        await expect(field(page)).toHaveValue(selection.text);
      });
    }

    test("Alt+V pastes a selection of pictures alone into the chat's box as each of its pictures", async ({ page }) => {
      // Arrange: the folded row's three thumbnails and no words are selected
      // and copied with Ctrl+C.
      await openPictured(page);
      const originals = await originalsOf(page);
      const uploads = await keepUploads(page);
      await page.getByRole("log", { name: /^Messages of / }).focus();
      await fold(page).locator("> summary .call-pictures").evaluate((thumbnails) => {
        const range = document.createRange();
        range.selectNode(thumbnails);
        getSelection()?.removeAllRanges();
        getSelection()?.addRange(range);
      });
      await page.keyboard.press("Control+C");
      await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();

      // Act
      await field(page).focus();
      await page.keyboard.press("Alt+v");

      // Assert: each picture is attached, in their order, and no words.
      expect(named(await attached(page, uploads), originals)).toEqual(["picture 1", "picture 2", "picture 3"]);
      await expect(attachedFiles(page)).toHaveCount(3);
      await expect(field(page)).toHaveValue("");
    });

    for (const key of ["Control+V", "Alt+v"]) {
      test(`pasting another page's words and inline picture into the chat's box with ${key} keeps the words and attaches no picture`, async ({ page }) => {
        // Arrange: the clipboard holds words with a picture inline in their
        // HTML, as a notebook's plot copied with its text holds it, and
        // nothing a conversation copied.
        await openPictured(page);
        const uploads = await keepUploads(page);
        await page.evaluate(async (plot) => {
          const html = `<p>A plot</p><img src="data:image/png;base64,${plot}" alt="A plot">`;
          await navigator.clipboard.write([new ClipboardItem({ "text/plain": new Blob(["A plot"], { type: "text/plain" }), "text/html": new Blob([html], { type: "text/html" }) })]);
        }, PLOT);

        // Act
        await field(page).focus();
        await page.keyboard.press(key);

        // Assert: the words go in, as they did before, and no picture is
        // kept or attached.
        await expect(field(page)).toHaveValue("A plot");
        await expect(box(page).getByRole("button", { name: "Add files" })).toBeEnabled();
        await expect(box(page).getByRole("list", { name: "Attached files" })).toHaveCount(0);
        expect(uploads).toEqual([]);
      });
    }

    for (const fallback of FALLBACKS) {
      test(`a drag across words and pictures ${fallback.name}`, async ({ page }) => {
        // Arrange: the page shows every picture, and the copy reads none of
        // those it cannot.
        if (fallback.script) await page.addInitScript({ content: fallback.script });
        await openPictured(page);
        await page.route((url) => url.pathname === "/api/messages" && fallback.unread.includes(url.searchParams.get("picture") ?? ""),
          (route) => route.request().resourceType() === "fetch" ? route.fulfill({ status: 404, json: { error: "No such picture is kept" } }) : route.fallback());

        // Act
        const selection = await drag(page);
        await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
        const copied = await clipboardOf(page);

        // Assert
        expect(copied.types).toEqual(fallback.types);
        expect(copied.text).toBe(selection.text);
        expect(named(copied.shown, selection.originals)).toEqual(fallback.shown);
        expect(copied.picture ? named([copied.picture], selection.originals) : []).toEqual(fallback.picture);
      });
    }

    test("a copy whose pictures could not be read reads them again for the next copy", async ({ page }) => {
      // Arrange: the drag's copy could read none of the reply's pictures, so
      // it holds the words alone, and since then they can be read again.
      await openPictured(page);
      let isReadable = false;
      await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("picture"),
        (route) => !isReadable && route.request().resourceType() === "fetch" ? route.fulfill({ status: 404, json: { error: "No such picture is kept" } }) : route.fallback());
      const selection = await drag(page);
      await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
      expect((await clipboardOf(page)).types).toEqual(["text/plain"]);
      isReadable = true;

      // Act
      await page.keyboard.press("Control+C");

      // Assert: the words and the pictures, in their order.
      await expect.poll(async () => (await clipboardOf(page)).types).toEqual(ALL);
      expect(named((await clipboardOf(page)).shown, selection.originals)).toEqual(IN_ORDER);
    });

    test("a selection with no picture copies its words alone, as it always has, and leaves Ctrl+C to the browser", async ({ page }) => {
      // Arrange
      await openPictured(page);

      // Act: a drag across the reply's first words only, then Ctrl+C on them
      // once something else was copied.
      await drag(page, FIRST, FIRST);
      await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
      const dragged = await clipboardOf(page);
      await page.evaluate(() => navigator.clipboard.writeText("copied since"));
      await page.keyboard.down("Control");
      await page.keyboard.down("KeyC");
      const held = await clipboardOf(page);
      await page.keyboard.up("KeyC");
      await page.keyboard.up("Control");

      // Assert: the drag leaves the words alone on the clipboard; while the
      // key is down the browser's own copy holds them with its HTML, and its
      // release leaves the words alone again.
      expect(dragged.types).toEqual(["text/plain"]);
      expect(dragged.text).toBe(FIRST);
      expect(held.types).toEqual(["text/html", "text/plain"]);
      expect(held.text).toBe(FIRST);
      await expect.poll(async () => { const copied = await clipboardOf(page); return [copied.types, copied.text]; }).toEqual([["text/plain"], FIRST]);
    });

    test("a selection that reaches past the conversation is left to the browser's own copy, as before", async ({ page }) => {
      // Arrange: from the reply's first words to past the conversation's
      // panel, its pictures and its box included, and something else was
      // copied since.
      await openPictured(page);
      await originalsOf(page);
      await page.getByRole("log", { name: /^Messages of / }).focus();
      await page.locator('[data-message="m1"]').getByText(FIRST).evaluate((words) => {
        const range = document.createRange();
        range.setStart(words, 0);
        range.setEndAfter(words.closest(".messages-view") ?? words);
        getSelection()?.removeAllRanges();
        getSelection()?.addRange(range);
      });
      await page.evaluate(() => navigator.clipboard.writeText("copied since"));

      // Act
      await page.keyboard.down("Control");
      await page.keyboard.down("KeyC");
      const held = await clipboardOf(page);
      await page.keyboard.up("KeyC");
      await page.keyboard.up("Control");
      await page.evaluate(() => new Promise<void>((done) => requestAnimationFrame(() => requestAnimationFrame(() => done()))));

      // Assert: the browser's own copy, with its HTML, is on the clipboard
      // while the key is down and stays there once it is up.
      expect(held.types).toEqual(["text/html", "text/plain"]);
      expect((await clipboardOf(page)).types).toEqual(["text/html", "text/plain"]);
    });
  });
}
