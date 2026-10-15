import { expect, openItem, test, type Locator, type Page } from "./site";

// The Overlord, 2026-10-13 about 14:30Z, of a Command Center question with
// Other chosen and his answer typed in its box: "In the Command Center, if I
// click the other box and start and I'm in that text box, Please put the same
// microphone dictation functionality inside that text box for me to use it.
// In the bottom right corner, same style function as we would in any other
// panel." Every box he writes his own answer in holds the same microphone,
// inside the box at its bottom right corner, and what he dictates joins what
// he wrote as it does in the message box under a goblin.

// Chromium's fake microphone (periodic beeps) stands in for his.
test.use({
  locale: "en-US",
  timezoneId: "UTC",
  permissions: ["microphone"],
  launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] },
});

// The box each card offers, as a person finds it: the card is opened from the
// Command Center as he opens it, and Other is chosen where the card has choices.
const BOXES = [
  { card: "question", choose: "Other", box: "Your written answer" },
  { card: "change", choose: "Other", box: "Your written answer" },
  { card: "review", choose: "", box: "Your answer" },
];

// open serves the board its fixture, has the supervisor's speech model hear
// "rebase onto main" in every sound, and opens the box of one card.
async function open(page: Page, { card, choose, box }: (typeof BOXES)[number]) {
  const heard: (string | null)[] = [];
  await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
  await page.route("**/api/dictation", async (route) => {
    if (route.request().method() === "GET") return route.fulfill({ json: { engine: "test-model", state: "ready" } });
    heard.push(await route.request().headerValue("X-CFO-Token"));
    return route.fulfill({ json: { text: "rebase onto main", engine: "test-model" } });
  });
  await page.clock.install({ time: new Date("2026-10-13T14:30:00Z") });
  await page.goto("/tests/fixtures/answer-dictation.html");
  if (card === "question") await openItem(page, "Should Corey and Densie work with the numbers alone?");
  else if (card === "review") await openItem(page, "Does the Other box read right?");
  else {
    await page.locator(".command-center-menu > summary").click();
    await page.locator(".command-center-updates").getByText("History").click();
    await page.getByRole("button", { name: /^Change the answer to The CFO: Merge PocketPiggies 125/ }).click();
  }
  const dialog = page.getByRole("dialog");
  if (choose) await dialog.getByRole("radio", { name: new RegExp("^" + choose) }).check();
  return { heard, dialog, textbox: dialog.getByRole("textbox", { name: box }), microphone: dialog.getByRole("button", { name: "Hold to dictate" }) };
}

// dictate holds the microphone for a moment, as he does.
async function dictate(page: Page, microphone: Locator) {
  await microphone.hover();
  await page.mouse.down();
  await expect(page.getByRole("button", { name: "Listening" })).toHaveClass(/recording/);
  await page.waitForTimeout(1200);
  await page.mouse.up();
}

for (const target of BOXES) {
  test(`dictation joins what he wrote in the ${target.card} card's answer box, as in the message box`, async ({ page }) => {
    // Arrange
    const { heard, textbox, microphone } = await open(page, target);
    await textbox.fill("please");

    // Act
    await dictate(page, microphone);

    // Assert
    await expect(textbox).toHaveValue("please rebase onto main");
    await expect(page.getByRole("button", { name: "Hold to dictate" })).not.toHaveClass(/recording/);
    expect(heard).toEqual(["fixture"]);

    // Act: a second dictation joins the first.
    await dictate(page, microphone);

    // Assert
    await expect(textbox).toHaveValue("please rebase onto main rebase onto main");
  });
}

// The Overlord, 2026-10-15 about 00:10Z: "dictation in command center never
// pasted". Choosing Other leaves the keyboard on the choice, not in the box
// it opens, and the shortcut held there was heard by nothing. Held anywhere
// in the card it dictates into the card's box, which takes the caret.
for (const target of BOXES) {
  test(`Ctrl+Shift+Space held anywhere in the ${target.card} card dictates into its answer box`, async ({ page }) => {
    // Arrange
    const { heard, textbox } = await open(page, target);
    await expect(textbox, "the card opens with the keyboard on its question or on the Other just chosen").not.toBeFocused();
    await expect(textbox).toBeVisible();
    expect(await page.evaluate(() => !!document.activeElement?.closest(".question-card")), "the keyboard is in the card").toBe(true);

    // Act
    await page.keyboard.down("Control");
    await page.keyboard.down("Shift");
    await page.keyboard.down("Space");
    await expect(page.getByRole("button", { name: "Listening" })).toHaveClass(/recording/);
    await page.waitForTimeout(1200);
    await page.keyboard.up("Space");
    await page.keyboard.up("Shift");
    await page.keyboard.up("Control");

    // Assert
    await expect(textbox).toHaveValue("rebase onto main");
    await expect(textbox).toBeFocused();
    expect(heard).toEqual(["fixture"]);
  });
}

for (const { name, viewport } of [{ name: "desktop", viewport: { width: 1440, height: 900 } }, { name: "phone", viewport: { width: 390, height: 844 } }]) {
  for (const colorScheme of ["dark", "light"] as const) {
    test(`the microphone sits inside the Other box at its bottom right corner on a ${name} in ${colorScheme}`, async ({ page }, testInfo) => {
      // Arrange
      await page.setViewportSize(viewport);
      await page.emulateMedia({ colorScheme });
      const { dialog, textbox, microphone } = await open(page, BOXES[0]);

      // Act
      await expect(microphone).toBeVisible();
      const box = (await textbox.boundingBox())!;
      const mic = (await microphone.boundingBox())!;

      // Assert: inside the box, close to its bottom right corner.
      expect(mic.x).toBeGreaterThanOrEqual(box.x);
      expect(mic.y).toBeGreaterThanOrEqual(box.y);
      expect(mic.x + mic.width).toBeLessThanOrEqual(box.x + box.width);
      expect(mic.y + mic.height).toBeLessThanOrEqual(box.y + box.height);
      expect(box.x + box.width - (mic.x + mic.width)).toBeLessThan(40);
      expect(box.y + box.height - (mic.y + mic.height)).toBeLessThan(20);
      await expect(dialog.getByRole("button", { name: "Send decision" })).toBeVisible();
      await dialog.locator(".card-stage").screenshot({ path: testInfo.outputPath(`other-box-${name}-${colorScheme}.png`) });
    });
  }
}
