import { expect, openItem, test, type Locator, type Page, type Route } from "./site";

// The Overlord, 2026-10-20 about 15:20Z: "when i hit enter in something else
// command center modal it should send... shift +enter can be the new line".
// Every box he writes his own answer in sends on Enter and adds a line on
// Shift+Enter, as the message box under a goblin does. An Enter that ends an
// input method's composition does nothing, and neither does one on an empty box.

const WORD = "Use the shop's bot account instead";

// record answers every board request the cards send and keeps what each said.
async function record(page: Page): Promise<Record<string, unknown>[]> {
  const sent: Record<string, unknown>[] = [];
  await page.route("**/api/actions", async (route: Route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    sent.push(body);
    await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
  });
  return sent;
}

// The box each card offers, as a person finds it: opened from the Command
// Center as he opens it, with Other chosen where the card has choices.
const CARDS = [
  { name: "question card", fixture: "answer-dictation", choose: "Other", box: "Your written answer",
    open: (page: Page) => openItem(page, "Should Corey and Densie work with the numbers alone?") },
  { name: "change card", fixture: "answer-dictation", choose: "Other", box: "Your written answer",
    open: async (page: Page) => {
      await page.locator(".command-center-menu > summary").click();
      await page.locator(".command-center-updates").getByText("History").click();
      await page.getByRole("button", { name: /^Change the answer to The CFO: Merge PocketPiggies 125/ }).click();
    } },
  { name: "review card", fixture: "answer-dictation", choose: "", box: "Your answer",
    open: (page: Page) => openItem(page, "Does the Other box read right?") },
  { name: "Something else window", fixture: "skip-or-other", choose: "", box: "Your note",
    open: async (page: Page) => {
      await page.getByRole("button", { name: "Open the wait", exact: true }).click();
      await page.getByRole("dialog").locator(".card-actions").getByRole("button", { name: "Something else?" }).click();
    } },
] as const;

async function boxOf(page: Page, card: (typeof CARDS)[number]): Promise<Locator> {
  await page.goto(`/tests/fixtures/${card.fixture}.html`);
  await card.open(page);
  const dialog = page.getByRole("dialog").last();
  if (card.choose) await dialog.getByRole("radio", { name: new RegExp("^" + card.choose) }).check();
  const box = dialog.getByRole("textbox", { name: card.box });
  await expect(box).toBeVisible();
  return box;
}

for (const card of CARDS) {
  test(`Enter in the ${card.name}'s box sends his answer`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const box = await boxOf(page, card);
    await box.fill(WORD);

    // Act
    await box.press("Enter");

    // Assert
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0]).toMatchObject({ text: WORD });
  });

  test(`Shift+Enter in the ${card.name}'s box adds a line and sends nothing`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const box = await boxOf(page, card);
    await box.fill("First line");

    // Act
    await box.press("Shift+Enter");
    await box.pressSequentially("Second line");

    // Assert
    await expect(box).toHaveValue("First line\nSecond line");
    await box.press("Enter");
    await expect.poll(() => sent.length, "only the Enter that follows sends").toBe(1);
    expect(sent[0]).toMatchObject({ text: "First line\nSecond line" });
  });

  test(`Enter while composing in the ${card.name}'s box does nothing`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const box = await boxOf(page, card);
    await box.fill(WORD);

    // Act
    const isPrevented = await box.evaluate((field) => {
      const composing = new KeyboardEvent("keydown", { key: "Enter", isComposing: true, bubbles: true, cancelable: true });
      field.dispatchEvent(composing);
      return composing.defaultPrevented;
    });

    // Assert
    expect(isPrevented, "the input method keeps its Enter").toBe(false);
    await box.press("Enter");
    await expect.poll(() => sent.length, "only the Enter after the composition sends").toBe(1);
    expect(sent[0]).toMatchObject({ text: WORD });
  });

  test(`Enter on an empty ${card.name} box sends nothing`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const box = await boxOf(page, card);
    await box.fill("   ");

    // Act
    await box.press("Enter");
    await box.fill(WORD);
    await box.press("Enter");

    // Assert
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0], "the first send is the words, not the empty box").toMatchObject({ text: WORD });
  });
}
