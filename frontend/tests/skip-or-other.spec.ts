import { expect, test, type Page, type Route } from "./site";

// The Overlord, 2026-10-15: "again theres no way for me to answer this
// command with a skip or other", and then where: at the bottom beside the
// decision buttons, a pencil, on every card with no choices or answer box of
// its own, that opens a little window to write in before the card clears.
// Empty, it is Skip and the goblin goes on without it. With a note, it is his
// answer, which reaches the goblin as his word.

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

const pencilOf = (card: ReturnType<Page["locator"]>) => card.locator(".card-actions").getByRole("button", { name: "Skip or answer in your own words" });
const noteWindow = (page: Page) => page.getByRole("dialog", { name: "Skip this card?" });

for (const card of CARDS) {
  test(`${card.open}: the pencil beside its decision button skips it`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const shown = await openCard(page, card.open);

    // Act
    await pencilOf(shown).click();
    await noteWindow(page).getByRole("button", { name: "Skip", exact: true }).click();

    // Assert
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0].body).toMatchObject(card.skip);
    await expect(shown.locator(".asker").getByRole("button")).toHaveCount(0);
  });

  test(`${card.open}: a note written in the pencil's window is his answer`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const shown = await openCard(page, card.open);
    const word = "Use the shop's bot account instead";

    // Act
    await pencilOf(shown).click();
    await noteWindow(page).getByLabel("Your note").fill(word);
    await noteWindow(page).getByRole("button", { name: "Send and skip" }).click();

    // Assert
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0].body).toMatchObject({ ...card.other, text: word });
  });
}

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
    await page.setViewportSize(viewport);

    for (const name of ["Open the command", "Open the wait", "Open the credential"]) {
      // Act
      const shown = await openCard(page, name);
      await pencilOf(shown).scrollIntoViewIfNeeded();
      await page.screenshot({ path: testInfo.outputPath(`skip-or-other-${name.split(" ").pop()}-${viewport.width}.png`) });
      await pencilOf(shown).click();
      await noteWindow(page).getByLabel("Your note").fill("Use the shop's bot account instead");

      // Assert
      await expect(noteWindow(page).getByRole("button", { name: "Send and skip" })).toBeInViewport();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`skip-or-other-${name.split(" ").pop()}-${viewport.width}-note.png`) });
    }
  });
}
