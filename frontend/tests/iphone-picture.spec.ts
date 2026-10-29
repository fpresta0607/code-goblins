import { devices, expect, servePages, test, type Page } from "./site";
import { at, box, field, open, openGoblin, PAGE, pasted, send, typed } from "./session-board";

// The Overlord, 2026-10-29 about 03:39Z and 03:40Z, on his iPhone: "picute
// pasting doesnt work", then "picture pastong dowsnt work on mobike". An
// iPhone offers its own Paste in a plain text box only for text (WebKit's
// supportedPasteboardTypesForCurrentSelection), so a copied photo or
// screenshot never reached the box. There the box's + opens Paste a picture,
// which reads the clipboard as Safari allows once he taps its own Paste, and
// Photos or files, the phone's own picker. Each picture shows in the box as a
// thumbnail, reaches the harness by its kept path, and shows above his bubble
// once sent. This runs in WebKit as an iPhone, under the private board's
// address, since WebKit refuses the suite's own port; the clipboard is played
// by a stand-in holding what a copied screenshot holds.
const PRIVATE = "https://pc.example.ts.net:4311";
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
const KEPT = "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-1.png";

test.use({ ...devices["iPhone 15"], browserName: "webkit", baseURL: PRIVATE });

test.beforeEach(async ({ page }) => {
  await servePages(page.context(), PRIVATE);
});

// keepFiles plays the supervisor's composer files: each upload is kept as the
// next pasted-N and answers its path, and a kept picture reads back.
async function keepFiles(page: Page) {
  const uploads: string[] = [];
  await page.route("**/api/composer/file**", (route) => {
    if (route.request().method() !== "POST") return route.fulfill({ body: PNG, contentType: "image/png" });
    uploads.push(new URL(route.request().url()).searchParams.get("name") ?? "");
    return route.fulfill({ status: 201, json: { path: KEPT.replace("pasted-1", "pasted-" + uploads.length) } });
  });
  return uploads;
}

// copied puts on the clipboard what a copied screenshot puts there: one item,
// a PNG and nothing else, or nothing at all.
async function copied(page: Page, isPicture: boolean) {
  await page.addInitScript(({ bytes, isPicture }) => {
    const item = { types: ["image/png"], getType: async () => new Blob([new Uint8Array(bytes)], { type: "image/png" }) };
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { read: async () => isPicture ? [item] : [] } });
  }, { bytes: [...PNG], isPicture });
}

const thumbnail = (page: Page) => box(page).getByRole("list", { name: "Attached files" }).getByRole("img", { name: "Picture 1" });
const menuRow = (page: Page, name: string) => box(page).getByRole("button", { name, exact: true });

test("on an iPhone, Paste a picture adds the copied picture to the box, and Send names it", async ({ page }) => {
  // Arrange
  await copied(page, true);
  const relay = await open(page);
  const uploads = await keepFiles(page);
  await openGoblin(page);
  await field(page).fill("what is wrong here?");

  // Act
  await box(page).getByRole("button", { name: "Add files", exact: true }).tap();
  await menuRow(page, "Paste a picture").tap();

  // Assert: the menu closes, the picture shows in the box and was kept as a
  // PNG, and his words are as he left them.
  await expect(menuRow(page, "Paste a picture")).toHaveCount(0);
  await expect(thumbnail(page)).toBeVisible();
  await expect.poll(() => thumbnail(page).evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth)).toBe(1);
  expect(uploads).toEqual(["pasted.png"]);
  await expect(field(page)).toHaveValue("what is wrong here?");

  // Act
  await send(page).tap();

  // Assert: his words, then the picture's path on its own line, and Enter.
  await expect.poll(() => typed(relay)).toContain(pasted("what is wrong here?\r" + KEPT) + "\r");
  await expect(thumbnail(page)).toHaveCount(0);
});

test("on an iPhone, Photos or files opens the phone's picker, and a photo chosen there shows in the box", async ({ page }) => {
  // Arrange
  await open(page);
  const uploads = await keepFiles(page);
  await openGoblin(page);

  // Act
  await box(page).getByRole("button", { name: "Add files", exact: true }).tap();
  const choosing = page.waitForEvent("filechooser");
  await menuRow(page, "Photos or files").tap();
  await (await choosing).setFiles({ name: "IMG_0412.PNG", mimeType: "image/png", buffer: PNG });

  // Assert: the photo is kept as a PNG under its own name and shows in the box.
  await expect(thumbnail(page)).toBeVisible();
  expect(uploads).toEqual(["IMG_0412.png"]);
});

test("on an iPhone, Paste a picture with no picture copied says so and adds nothing", async ({ page }) => {
  // Arrange
  await copied(page, false);
  await open(page);
  const uploads = await keepFiles(page);
  await openGoblin(page);

  // Act
  await box(page).getByRole("button", { name: "Add files", exact: true }).tap();
  await menuRow(page, "Paste a picture").tap();

  // Assert
  await expect(box(page)).toContainText("The clipboard holds no picture. Copy one, then paste it here.");
  await expect(thumbnail(page)).toHaveCount(0);
  expect(uploads).toEqual([]);
});

test("on an iPhone, a picture he sent shows above his bubble", async ({ page }) => {
  // Arrange: his message names a kept picture, as Send writes it.
  await open(page, undefined, undefined, { messages: [...PAGE.messages, { id: "m2", seq: 2, at, kind: "prompt", source: "overlord", text: "what is wrong here?\n" + KEPT }], more: false });
  await keepFiles(page);

  // Act
  await openGoblin(page);

  // Assert
  const sent = page.locator('[data-message="m2"]');
  await expect(sent.locator(".message-bubble")).toHaveText("what is wrong here?");
  const picture = sent.locator(".message-picture img");
  await expect(picture).toBeVisible();
  await expect.poll(() => picture.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth)).toBe(1);
});
