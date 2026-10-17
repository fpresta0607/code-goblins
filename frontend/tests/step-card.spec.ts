import { doneCards, expect, recordDoneCards, servePages, test, type Page, type Route } from "./site";

// The one card a goblin sends when its browser work reaches a step only the
// Overlord can take: the exact page to open, or the goblin's own browser
// window, the fewest steps, only the inputs he enters, and Done, with Close on every card.
const CLICKS = "cred-1111111111111111";
const INPUTS = "cred-2222222222222222";
const WINDOW = "cred-3333333333333333";

interface Sent { path: string; token: string | null; body: Record<string, unknown> }

// answer records each board request a card sends and answers it as the
// supervisor does.
async function answer(page: Page): Promise<Sent[]> {
  const sent: Sent[] = [];
  await page.route("**/api/credentials/**", async (route: Route) => {
    const request = route.request();
    const body = request.postDataJSON() as Record<string, unknown>;
    sent.push({ path: new URL(request.url()).pathname, token: await request.headerValue("X-CFO-Token"), body });
    const values = Object.keys((body.values as Record<string, string> | undefined) || {});
    await route.fulfill({ status: 200, json: { id: body.id, state: "open", saved: values.filter((name) => name !== "ACCOUNT_EMAIL"), received: values.filter((name) => name === "ACCOUNT_EMAIL"), replaced: [] } });
  });
  return sent;
}

async function openCard(page: Page, id: string, origin = "") {
  await page.goto(origin + "/tests/fixtures/step-card.html");
  await page.getByRole("button", { name: "Open " + id, exact: true }).click();
  const card = page.getByRole("dialog").locator(`[data-credential-request="${id}"]`);
  await expect(card).toBeVisible();
  return card;
}

const setCard = (page: Page, id: string, changes: Record<string, unknown>) => page.evaluate(([card, update]) => window.stepBoard?.request(card as string, update as Record<string, unknown>), [id, changes] as const);

test("a card of clicks opens the exact page, numbers its steps, asks for nothing, and Done closes it with the board's token", async ({ page }) => {
  // Arrange
  const sent = await answer(page);
  const card = await openCard(page, CLICKS);
  await recordDoneCards(page);
  const steps = card.getByRole("list", { name: "What to do" }).getByRole("listitem");
  await expect(steps).toHaveText(["Open shop.example.com/signup", "Prove you are a human", "Create the account with the shop's email", "Accept the terms"]);
  await expect(steps.first().getByRole("link")).toHaveAttribute("href", "https://shop.example.com/signup");

  // Act
  await card.getByRole("button", { name: "Done" }).click();
  await expect.poll(() => sent.length).toBe(1);
  await setCard(page, CLICKS, { state: "done", closed_at: "2026-10-14T23:45:00Z" });

  // Assert
  expect(sent).toEqual([{ path: "/api/credentials/close", token: "fixture-token", body: { id: CLICKS, generation: "a".repeat(32), action: "done" } }]);
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Done.*Its goblin goes on now/));
});

test("the card of clicks shows its heading, link and steps and no value table before Done", async ({ page }) => {
  // Arrange
  await answer(page);

  // Act
  const card = await openCard(page, CLICKS);

  // Assert
  await expect(card.getByRole("heading", { name: "Create the shop's test account" })).toBeVisible();
  await expect(card.getByRole("list", { name: "What to do" }).getByRole("link")).toHaveAttribute("href", "https://shop.example.com/signup");
  await expect(card.locator("table")).toHaveCount(0);
  await expect(card.locator("input")).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Done" })).toBeEnabled();
});

// The Overlord, 2026-10-15: "there should be an exit or close button and an
// other enter command or text about a command when exited", and later that
// day that it belongs at the bottom, beside the decision buttons. The card it
// leaves names the command to give the value later, or to bring it back.
test("Skip beside the decision buttons skips a card from any board and leaves the command to give the value later", async ({ page }) => {
  // Arrange: a board opened from another machine.
  const elsewhere = "https://board.test";
  await servePages(page.context(), elsewhere);
  const sent = await answer(page);
  const card = await openCard(page, INPUTS, elsewhere);
  await recordDoneCards(page);
  const skip = card.locator(".card-actions").getByRole("button", { name: "Skip this card" });

  // Act
  await skip.click();
  await expect.poll(() => sent.length).toBe(1);
  await setCard(page, INPUTS, { state: "dismissed", closed_at: "2026-10-14T23:45:00Z" });

  // Assert
  expect(sent[0]).toEqual({ path: "/api/credentials/close", token: "fixture-token", body: { id: INPUTS, generation: "b".repeat(32), action: "dismiss" } });
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Closed.*goes on without it\. Give it later: cfo auth store --project acme-shop SHOP_API_TOKEN/));
});

test("a closed card with nothing secret to give later says how to bring it back", async ({ page }) => {
  // Arrange
  await answer(page);
  const card = await openCard(page, CLICKS);
  await recordDoneCards(page);

  // Act
  await card.getByRole("button", { name: "Skip this card" }).click();
  await setCard(page, CLICKS, { state: "dismissed", closed_at: "2026-10-14T23:45:00Z" });

  // Assert
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Closed.*Bring it back: cfo auth request --reopen cred-1111111111111111/));
});

test("each input shows by its label: a plain value is typed in the open and sent to the goblin, a secret stays hidden", async ({ page }) => {
  // Arrange
  const sent = await answer(page);
  const card = await openCard(page, INPUTS);
  const email = "shop-owner@example.com";

  // Act
  await card.getByLabel("The account's email", { exact: true }).fill(email);
  await card.getByLabel("The account's email", { exact: true }).press("Enter");
  await expect.poll(() => sent.length).toBe(1);

  // Assert
  await expect(card.getByRole("heading", { name: "Enter 2 values for acme-shop" })).toBeVisible();
  await expect(card.locator('[data-credential-name="ACCOUNT_EMAIL"] th')).toContainText("The account's email");
  await expect(card.locator('[data-credential-name="SHOP_API_TOKEN"] th')).toContainText("The API token from Settings");
  expect(sent[0]).toEqual({ path: "/api/credentials/save", token: "fixture-token", body: { id: INPUTS, generation: "b".repeat(32), values: { ACCOUNT_EMAIL: email }, replace: [] } });
  await expect(card.locator('[data-credential-name="ACCOUNT_EMAIL"]')).toHaveAttribute("data-state", "saved");
  await expect(card.locator('[data-credential-name="ACCOUNT_EMAIL"]')).toContainText("Sent");
  await expect(card.getByLabel("Value for SHOP_API_TOKEN")).toHaveAttribute("type", "text");
  await expect(card.getByRole("button", { name: "Done" })).toHaveCount(0);
});

test("the window button asks for the goblin's own browser, then the card says it is open on his screen", async ({ page }) => {
  // Arrange
  const sent = await answer(page);
  const card = await openCard(page, WINDOW);

  // Act
  await card.getByRole("button", { name: "Open the browser window" }).click();
  await expect.poll(() => sent.length).toBe(1);
  await setCard(page, WINDOW, { window_state: "requested" });
  await expect(card.locator(".step-window")).toContainText("Opening the browser window on your PC.");
  await setCard(page, WINDOW, { window_state: "open" });

  // Assert
  expect(sent[0]).toEqual({ path: "/api/credentials/window", token: "fixture-token", body: { id: WINDOW, generation: "c".repeat(32) } });
  await expect(card.locator(".step-window")).toContainText("The browser window is open on your screen. Take the step there, then press Done.");
  await expect(card.locator(".step-window .primary")).toBeDisabled();
  await expect(card.getByRole("list", { name: "What to do" }).getByRole("link")).toHaveCount(0);
});

for (const [where, viewport] of [["in his window", { width: 1707, height: 1067 }], ["on a phone", { width: 390, height: 844 }]] as const) {
  test(`each step card fits ${where} with nothing cut off`, async ({ page }, testInfo) => {
    // Arrange
    await answer(page);
    await page.setViewportSize(viewport);

    for (const id of [CLICKS, INPUTS, WINDOW]) {
      // Act
      const card = await openCard(page, id);

      // Assert
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`step-card-${id.slice(-4)}-${viewport.width}.png`) });
      await card.locator(".credential-heading").hover();
      await page.mouse.wheel(0, 4000);
      await expect(card.getByRole("button", { name: "Something else?" })).toBeInViewport();
      expect(await card.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`step-card-${id.slice(-4)}-${viewport.width}-end.png`) });
    }
  });
}

// The Overlord, 2026-10-17: "i sent a question from something else and it
// said in yellow text your words goes to... it should complete as sent".
test("his words from Something else? complete the step card as Sent, and History shows them with no warning", async ({ page }, testInfo) => {
  // Arrange
  await page.setViewportSize({ width: 1707, height: 1067 });
  const sent = await answer(page);
  const card = await openCard(page, INPUTS);
  await recordDoneCards(page);

  // Act
  await card.locator(".card-actions").getByRole("button", { name: "Something else?" }).click();
  const note = page.getByRole("dialog", { name: "Something else?" });
  await note.getByLabel("Your note").fill("which plan?");
  await note.getByRole("button", { name: "Send", exact: true }).click();
  await expect.poll(() => sent.length).toBe(1);
  await setCard(page, INPUTS, { state: "answered", reason: "which plan?", closed_at: "2026-10-14T23:45:00Z" });

  // Assert
  expect(sent[0]).toMatchObject({ path: "/api/credentials/close", body: { id: INPUTS, generation: "b".repeat(32), action: "answer", text: "which plan?" } });
  await expect(note).toBeHidden();
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^SentYou wrote: which plan\?/));
  await page.getByRole("button", { name: "Close the Command Center" }).click();
  const menu = page.locator(".command-center-menu");
  await menu.locator("> summary").click();
  await expect(menu.getByRole("region", { name: "Waiting on you" }).locator(".inbox-list li").filter({ hasText: "for acme-shop" })).toHaveCount(0);
  await menu.getByText("History").click();
  const row = menu.locator(".inbox-history .inbox-list > li").filter({ hasText: "for acme-shop" });
  await row.locator(".history-open").click();
  await expect(row.locator(".history-mark")).toHaveAttribute("aria-label", "You wrote: which plan?");
  await expect(row.locator(".history-detail")).toHaveText("You wrote: which plan?");
  await expect(page.locator(".click-feedback, .signin-note, .credential-refusal, .message-note")).toHaveCount(0);
  await menu.locator(".command-center-updates").screenshot({ path: testInfo.outputPath("step-card-answer-history.png") });
});

test("a refused answer from Something else? stays in his window in full, with his words, and no note on the card", async ({ page }) => {
  // Arrange
  const refusal = "Your answer goes to the goblin, so it is a few words of at most 4000 characters and holds no key. Nothing was sent.";
  await page.route("**/api/credentials/close", (route) => route.fulfill({ status: 400, json: { error: refusal } }));
  const card = await openCard(page, INPUTS);

  // Act
  await card.locator(".card-actions").getByRole("button", { name: "Something else?" }).click();
  const note = page.getByRole("dialog", { name: "Something else?" });
  await note.getByLabel("Your note").fill("which plan?");
  await note.getByRole("button", { name: "Send", exact: true }).click();

  // Assert
  await expect(note.getByRole("alert")).toHaveText(refusal);
  await expect(note.getByLabel("Your note")).toHaveValue("which plan?");
  await expect(note.getByRole("button", { name: "Send", exact: true })).toBeEnabled();
  await expect(card.locator(".click-feedback")).toHaveCount(0);
});
