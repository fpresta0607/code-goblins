import { expect, test, type Locator, type Page, type TestInfo } from "./site";
import { fold, hoverCard, isDrawn, openPictured, placeOf, SET, viewer } from "./chat-pictures";

// Pictures in Modern Chat as Claude's app shows them (#2305). The Overlord,
// 2026-11-10: "in goblins modern chat there's no image viewer or previews
// like claude". A row whose calls read, took or produced pictures ends in
// their thumbnails, a mouse over one shows it larger with its call's verb
// and its place among the row's, and a click opens one viewer over the whole
// window whose arrows, keys and counter run across every picture of the
// conversation, his own included. A picture that cannot be read is a quiet
// mark, never the browser's broken image.

const KEPT = SET[1];

// Each look the board is asked for, as a picture for the pull request. The
// board has one look, dark, whatever the system prefers (the Overlord,
// 2026-10-27: "we dont need light mode yet").
async function picture(page: Page, testInfo: TestInfo, name: string) {
  for (const scheme of ["dark", "light"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.screenshot({ path: testInfo.outputPath(name + "-" + scheme + ".png") });
  }
}

// inWindow says whether a part lies whole inside the window.
async function inWindow(page: Page, part: Locator): Promise<boolean> {
  const box = await part.boundingBox();
  const size = page.viewportSize();
  return !!box && !!size && box.x >= 0 && box.y >= 0 && box.x + box.width <= size.width && box.y + box.height <= size.height;
}

const shownPicture = async (page: Page) => new URL(await viewer(page).getByRole("img").getAttribute("src") ?? "", page.url()).searchParams;

for (const width of [1440, 390]) {
  test(`a row's pictures show as thumbnails at its end, and a mouse over one shows it larger with its verb and place at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 900 });
    const { talk } = await openPictured(page);
    const row = fold(page).locator("> summary");
    const thumbnails = row.locator(".call-picture");

    // Assert: the folded row says what its calls did and ends in their three
    // pictures, each loaded lazily and drawn inside the row's one line.
    await expect(row).toContainText("Read 2 files, ran 1 command, called chrome take_screenshot 1 time");
    await expect(thumbnails).toHaveCount(3);
    await row.scrollIntoViewIfNeeded();
    const line = await row.boundingBox();
    for (const thumbnail of await thumbnails.all()) {
      await expect(thumbnail.locator("img")).toHaveAttribute("loading", "lazy");
      await expect.poll(() => thumbnail.locator("img").evaluate(isDrawn)).toBe(true);
      const box = await thumbnail.boundingBox();
      expect(line && box && box.y >= line.y && box.y + box.height <= line.y + line.height && box.x + box.width <= line.x + line.width, "the thumbnail inside its row").toBe(true);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await picture(page, testInfo, `thumbnails-${width}`);

    // Act
    await thumbnails.nth(0).hover();

    // Assert: the card over the board, clear of its thumbnail, with the
    // picture, the verb of the call that read it and its place in the row.
    await expect(hoverCard(page)).toBeVisible();
    await expect(hoverCard(page).locator("p > span")).toHaveText(["Read", "1 of 3"]);
    await expect.poll(() => hoverCard(page).locator("img").evaluate(isDrawn)).toBe(true);
    expect(await inWindow(page, hoverCard(page)), "the card inside the window").toBe(true);
    const [shown, under] = [await hoverCard(page).boundingBox(), await thumbnails.nth(0).boundingBox()];
    expect(shown && under && (shown.y + shown.height <= under.y || shown.y >= under.y + under.height), "the card clear of its thumbnail").toBe(true);
    await picture(page, testInfo, `hover-${width}`);

    // Act
    await thumbnails.nth(2).hover();

    // Assert
    await expect(hoverCard(page).locator("p > span")).toHaveText(["Called chrome take_screenshot", "3 of 3"]);

    // Act + Assert: Escape puts the card away with the mouse still there, and
    // only the card: the conversation stays open.
    await page.keyboard.press("Escape");
    await expect(hoverCard(page)).toHaveCount(0);
    await expect(talk).toBeVisible();

    // Act: the mouse over another, then off the pictures.
    await thumbnails.nth(1).hover();
    await expect(hoverCard(page).locator("p > span")).toHaveText(["Read", "2 of 3"]);
    await talk.getByText("Reading the screenshots.").hover();

    // Assert: the card goes, and the row never opened.
    await expect(hoverCard(page)).toHaveCount(0);
    await expect(fold(page)).toHaveJSProperty("open", false);

    // Act: the list scrolls under a card. Nothing here follows it, as the
    // list settles its own place after a scroll.
    await thumbnails.nth(0).hover();
    await expect(hoverCard(page)).toBeVisible();
    expect(await talk.evaluate((list) => list.scrollHeight - list.clientHeight), "a list with room to scroll").toBeGreaterThan(60);
    await talk.evaluate((list) => { list.scrollTop += list.scrollTop >= 60 ? -60 : 60; });

    // Assert
    await expect(hoverCard(page)).toHaveCount(0);
  });

  test(`a click on a thumbnail opens the viewer, whose arrows, keys and counter run across the conversation at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 900 });
    const { asked } = await openPictured(page);
    const thumbnail = fold(page).locator("> summary .call-picture").nth(1);
    const place = viewer(page).getByRole("status");
    const large = viewer(page).getByRole("img");
    await expect.poll(() => thumbnail.locator("img").evaluate(isDrawn)).toBe(true);
    // No picture is read before something draws it: the two past the three
    // their row shows are asked for only once the viewer reaches them.
    expect(asked.filter((each) => each === "s4" || each === "s5")).toEqual([]);

    // Act
    await thumbnail.click();

    // Assert: the viewer on that picture, inside the window and larger than
    // its thumbnail, its place counted across the conversation, and the row
    // still folded.
    await expect(viewer(page)).toBeVisible();
    await expect(place).toHaveText(placeOf("team"));
    await expect.poll(() => large.evaluate(isDrawn)).toBe(true);
    expect((await shownPicture(page)).get("picture")).toBe("team");
    expect(await inWindow(page, large), "the picture inside the window").toBe(true);
    expect((await large.boundingBox())?.width ?? 0).toBeGreaterThan((await thumbnail.boundingBox())?.width ?? 0);
    await expect(fold(page)).toHaveJSProperty("open", false);
    await picture(page, testInfo, `viewer-${width}`);

    // Act + Assert: a click on the picture shows it at its own size, where it
    // scrolls, and a second fits it to the window again.
    const stage = viewer(page).locator(".picture-stage");
    const isOwnSize = () => large.evaluate((image: HTMLImageElement) => Math.round(image.getBoundingClientRect().width) === image.naturalWidth);
    await large.click();
    await expect(stage).toHaveClass(/zoomed/);
    await expect.poll(isOwnSize).toBe(true);
    await large.click({ position: { x: 20, y: 20 } });
    await expect(stage).not.toHaveClass(/zoomed/);
    await expect.poll(() => inWindow(page, large)).toBe(true);

    // Act + Assert: the arrow keys, then the arrows, step one picture.
    await page.keyboard.press("ArrowRight");
    await expect(place).toHaveText(placeOf("page"));
    await page.keyboard.press("ArrowLeft");
    await page.keyboard.press("ArrowLeft");
    await expect(place).toHaveText(placeOf("home"));
    await viewer(page).getByRole("button", { name: "Next picture" }).click();
    await expect(place).toHaveText(placeOf("team"));
    await viewer(page).getByRole("button", { name: "Previous picture" }).click();
    await expect(place).toHaveText(placeOf("home"));

    // Act + Assert: back past the row into his own pictures, to the first,
    // where Previous rests and the key steps nowhere.
    await page.keyboard.press("ArrowLeft");
    await page.keyboard.press("ArrowLeft");
    await expect(place).toHaveText("1 of " + SET.length);
    await expect(viewer(page).getByRole("button", { name: "Previous picture" })).toHaveAttribute("aria-disabled", "true");
    await page.keyboard.press("ArrowLeft");
    await expect(place).toHaveText("1 of " + SET.length);

    // Act + Assert: on to the last, through every picture of the
    // conversation, where Next rests.
    for (let at = 2; at <= SET.length; at++) {
      await page.keyboard.press("ArrowRight");
      await expect(place).toHaveText(at + " of " + SET.length);
    }
    await expect(viewer(page).getByRole("button", { name: "Next picture" })).toHaveAttribute("aria-disabled", "true");
    expect(asked.filter((each) => each === "s4")).toHaveLength(1);

    // Act
    await page.keyboard.press("Escape");

    // Assert: the viewer is put away and the keyboard is back on the thumbnail.
    await expect(viewer(page)).toHaveCount(0);
    await expect(thumbnail).toBeFocused();

    // Act: Enter on the thumbnail.
    await page.keyboard.press("Enter");

    // Assert: the keyboard opens the viewer on it too, and never the row.
    await expect(place).toHaveText(placeOf("team"));
    await page.keyboard.press("Escape");
    await expect(viewer(page)).toHaveCount(0);
    await expect(fold(page)).toHaveJSProperty("open", false);
  });

  test(`a picture he sent opens the same viewer, which steps on into the pictures his goblin read at ${width}px`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height: 900 });
    const { talk } = await openPictured(page);
    const sent = page.locator('[data-message="m0"]');
    const place = viewer(page).getByRole("status");
    await expect.poll(() => sent.locator(".message-picture img").nth(1).evaluate(isDrawn)).toBe(true);

    // Assert: each picture is read from one of the board's own two routes
    // with its token, and his words never show the kept one's path.
    const addresses = await talk.locator("img").evaluateAll((images) => images.map((image) => (image as HTMLImageElement).src));
    for (const address of addresses.map((each) => new URL(each))) {
      expect(["/api/messages", "/api/composer/file"]).toContain(address.pathname);
      expect(address.searchParams.get("token")).toBe("fixture");
    }
    await expect(sent).not.toContainText("composer-files");

    // Act
    await sent.getByRole("button", { name: "Open picture 2" }).click();

    // Assert: his kept picture, second of them all.
    await expect(place).toHaveText(placeOf(KEPT));
    expect((await shownPicture(page)).get("path")).toBe(KEPT);

    // Act
    await page.keyboard.press("ArrowRight");

    // Assert: the next is the first his goblin read.
    await expect(place).toHaveText(placeOf("home"));
    expect((await shownPicture(page)).get("picture")).toBe("home");

    // Act
    await viewer(page).getByRole("button", { name: "Close" }).click();

    // Assert: put away, without opening his message's Copy row.
    await expect(viewer(page)).toHaveCount(0);
    await expect(sent).not.toHaveClass(/actions-open/);
  });

  test(`a picture that is gone or is no picture is a quiet mark in its row, its card and the viewer at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 900 });
    const { talk } = await openPictured(page);
    const thumbnails = page.locator('[data-message="m3"] details.message-call > summary .call-picture');
    const kept = page.locator('[data-message="m5"] .message-picture');
    const place = viewer(page).getByRole("status");

    // Assert: the one that reads back is drawn, the one that is gone and the
    // one whose bytes are no picture are marks, his kept one that is gone says
    // so, and no picture in the conversation is drawn broken.
    await expect(thumbnails).toHaveCount(3);
    await expect.poll(() => thumbnails.nth(0).locator("img").evaluate(isDrawn)).toBe(true);
    for (const at of [1, 2]) {
      await expect(thumbnails.nth(at).locator(".picture-missing")).toBeVisible();
      await expect(thumbnails.nth(at).locator("img")).toHaveCount(0);
    }
    await expect(kept.locator(".picture-missing")).toHaveText("Picture not available");
    await expect(kept.locator("img")).toHaveCount(0);
    await expect.poll(() => talk.locator("img").evaluateAll((images) => images.every((image) => (image as HTMLImageElement).complete && (image as HTMLImageElement).naturalWidth > 0))).toBe(true);

    // Act
    await thumbnails.nth(1).hover();

    // Assert
    await expect(hoverCard(page)).toContainText("This image is no longer available.");
    await expect(hoverCard(page).locator("p > span")).toHaveText(["Viewed", "2 of 3"]);
    await expect(hoverCard(page).locator("img")).toHaveCount(0);
    await picture(page, testInfo, `missing-hover-${width}`);

    // Act
    await thumbnails.nth(1).click();

    // Assert: the viewer says so in the picture's place, and counts it.
    await expect(viewer(page)).toContainText("This image is no longer available.");
    await expect(place).toHaveText(placeOf("gone"));
    await expect(viewer(page).getByRole("img")).toHaveCount(0);
    await picture(page, testInfo, `missing-viewer-${width}`);

    // Act + Assert: the one that is no picture reads the same, and the mark
    // goes with its picture, so the one before them is drawn.
    await page.keyboard.press("ArrowRight");
    await expect(place).toHaveText(placeOf("noise"));
    await expect(viewer(page)).toContainText("This image is no longer available.");
    await page.keyboard.press("ArrowLeft");
    await page.keyboard.press("ArrowLeft");
    await expect(place).toHaveText(placeOf("babies"));
    await expect.poll(() => viewer(page).getByRole("img").evaluate(isDrawn)).toBe(true);
  });

  test(`a row with more pictures than it shows counts the rest, and an open row shows its pictures under it at ${width}px`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height: 900 });
    await openPictured(page);
    const many = page.locator('[data-message="m4"] details.message-call');
    const place = viewer(page).getByRole("status");

    // Assert: three thumbnails and a count of the other two, on one line
    // that fits the window.
    await expect(many.locator("> summary .call-picture img")).toHaveCount(3);
    await expect(many.getByRole("button", { name: "Open 2 more pictures" })).toHaveText("+2");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);

    // Act
    await many.getByRole("button", { name: "Open 2 more pictures" }).click();

    // Assert: the viewer on the first picture the row did not show.
    await expect(place).toHaveText(placeOf("s4"));
    await page.keyboard.press("Escape");
    await expect(viewer(page)).toHaveCount(0);

    // Act: he opens the folded row, then the first call in it.
    await fold(page).locator("> summary").click();
    const read = fold(page).locator(".disclosure-content details.message-call", { hasText: "shots/home.png" });

    // Assert: the open fold's own end shows no picture, and each call that
    // holds one shows its own.
    await expect(fold(page).locator("> summary .call-pictures")).toBeHidden();
    await expect(read.locator("> summary .call-picture")).toHaveCount(1);
    await expect(read.locator("> summary .call-picture")).toBeVisible();

    // Act
    await read.locator("> summary").click();

    // Assert: the open call shows its picture under it, which opens the
    // conversation's viewer on it.
    await expect(read.locator("> summary .call-pictures")).toBeHidden();
    await read.locator(".disclosure-content").getByRole("button", { name: "Open picture 1" }).click();
    await expect(place).toHaveText(placeOf("home"));
  });
}
