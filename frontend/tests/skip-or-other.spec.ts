import { expect, test, type Page, type Route } from "./site";

// The Overlord, 2026-10-15: "again theres no way for me to answer this
// command with a skip or other". Every Command Center card has Skip and Other
// at its top: Skip clears the card and tells its goblin to go on without it,
// and Other is his own answer, typed or dictated, which reaches the goblin as
// his word.

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

for (const card of CARDS) {
  test(`${card.open}: Skip at its top clears it`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const shown = await openCard(page, card.open);
    const skip = shown.locator(".asker").getByRole("button", { name: "Skip this card" });

    // Act
    await expect(skip).toBeInViewport();
    await skip.click();

    // Assert
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0].body).toMatchObject(card.skip);
  });

  test(`${card.open}: Other sends his own words`, async ({ page }) => {
    // Arrange
    const sent = await record(page);
    const shown = await openCard(page, card.open);
    const word = "Use the shop's bot account instead";

    // Act
    await shown.locator(".asker").getByRole("button", { name: "Other" }).click();
    await shown.getByLabel("Your answer").fill(word);
    await shown.getByRole("button", { name: "Send answer" }).click();

    // Assert
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0].body).toMatchObject({ ...card.other, text: word });
  });
}

test("a question has Skip at its top beside its own Other choice for a written answer", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  const shown = await openCard(page, "Open the question");

  const hasOtherChoice = await shown.getByRole("radio", { name: /^Other/ }).isVisible();

  // Act
  await shown.locator(".asker").getByRole("button", { name: "Skip this card" }).click();

  // Assert
  expect(hasOtherChoice).toBe(true);
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
  test(`Skip and Other fit at the top of every card ${where}`, async ({ page }, testInfo) => {
    // Arrange
    await record(page);
    await page.setViewportSize(viewport);

    for (const name of ["Open the command", "Open the wait", "Open the question", "Open the credential"]) {
      // Act
      const shown = await openCard(page, name);
      if (name !== "Open the question") await shown.locator(".asker").getByRole("button", { name: "Other" }).click();

      // Assert
      await expect(shown.locator(".asker").getByRole("button", { name: "Skip this card" })).toBeInViewport();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`skip-or-other-${name.split(" ").pop()}-${viewport.width}.png`) });
    }
  });
}
