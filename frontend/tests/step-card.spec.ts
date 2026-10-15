import { doneCards, expect, recordDoneCards, servePages, test, type Page, type Route } from "./site";

// The one card a goblin sends when its browser work reaches a step only the
// Overlord can take: the exact page to open, or the goblin's own browser
// window, the fewest steps, only the inputs he enters, and Done or Dismiss.
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

test("Dismiss takes the card back, from any board, and its goblin goes on without it", async ({ page }) => {
  // Arrange: a board opened from another machine.
  const elsewhere = "https://board.test";
  await servePages(page.context(), elsewhere);
  const sent = await answer(page);
  const card = await openCard(page, INPUTS, elsewhere);
  await recordDoneCards(page);

  // Act
  await card.getByRole("button", { name: "Dismiss this card" }).click();
  await expect.poll(() => sent.length).toBe(1);
  await setCard(page, INPUTS, { state: "dismissed", closed_at: "2026-10-14T23:45:00Z" });

  // Assert
  expect(sent[0]).toEqual({ path: "/api/credentials/close", token: "fixture-token", body: { id: INPUTS, generation: "b".repeat(32), action: "dismiss" } });
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Dismissed.*goes on without it/));
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
  const opening = await card.locator(".step-window").innerText();
  await setCard(page, WINDOW, { window_state: "open" });

  // Assert
  expect(sent[0]).toEqual({ path: "/api/credentials/window", token: "fixture-token", body: { id: WINDOW, generation: "c".repeat(32) } });
  expect(opening).toContain("browser window on your PC");
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
      const dismiss = card.getByRole("button", { name: "Dismiss this card" });
      await dismiss.scrollIntoViewIfNeeded();
      await expect(dismiss).toBeInViewport();
      expect(await card.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`step-card-${id.slice(-4)}-${viewport.width}-end.png`) });
    }
  });
}
