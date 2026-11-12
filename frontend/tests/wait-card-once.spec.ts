import { expect, test, ORIGIN } from "./site";

// The Overlord, 2026-10-02, on a goblin's wait card whose sentence headed the
// card and filled its page tile, with Open review in the tile and again in
// the action row: "it is redundant, with its text and the button added; clean
// it up, the text is only needed once". A card says its words once, and one
// control opens its page.
const SAYS = "Audit of what travels to his agent is ready on Scrawl, read-only, nothing built.";
const ANSWERS_PAGE = "https://docs.example.com/answers/unconfirmed";
const ADVICE = "Your answer was typed for the CEO, which has not picked it up. See " + ANSWERS_PAGE + " for what to do, and never paste javascript:alert(1) into its box.";

test("a goblin's wait with a page says its words once and opens the page from one button", async ({ page }) => {
  // Arrange
  await page.route(ORIGIN + "/api/reviews/waiting-pd-agent-context-7/preview", (route) => route.fulfill({ contentType: "text/html", body: "<!doctype html><title>Audit</title>" }));
  await page.goto("/tests/fixtures/wait-card-once.html");

  // Act
  await page.getByRole("button", { name: "Open the wait", exact: true }).click();

  // Assert
  const card = page.getByRole("dialog").locator(".question-card");
  await expect(card.getByText(SAYS)).toHaveCount(1);
  await expect(card.locator('a[href="http://127.0.0.1:4387/session/f26e"]')).toHaveCount(1);
  await expect(card.getByRole("link", { name: "Open review" })).toHaveCount(1);
  await expect(card.locator(".page-preview iframe")).toHaveAttribute("src", "/api/reviews/waiting-pd-agent-context-7/preview");
  // He answers on the page, so the card has no box for words (the Overlord,
  // 2026-10-17: "There does not need to be a text box in when there is a
  // scroll open review page involved").
  await expect(card.getByRole("button", { name: "Something else?" })).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Skip this card" })).toBeVisible();
});

test("another review with a page opens it from one button and never names it Scrawl page", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/wait-card-once.html");

  // Act
  await page.getByRole("button", { name: "Open the review", exact: true }).click();

  // Assert
  const card = page.getByRole("dialog").locator(".question-card");
  await expect(card.getByText("Do the proof screenshots read well?")).toHaveCount(1);
  await expect(card.locator('a[href="http://127.0.0.1:4387/session/a1b2"]')).toHaveCount(1);
  await expect(card.getByRole("link", { name: "Open review: Do the proof screenshots read well?" })).toHaveCount(1);
  await expect(card.getByText("Scrawl page")).toHaveCount(0);
});

// An answer the CEO never picked up keeps its card, which says what to do in
// the supervisor's words: a web address in them opens in a new tab (#2360),
// and a javascript: string stays words.
test("a review answer that was never picked up says what to do, its web address a link and a javascript: one text", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/wait-card-once.html");
  await page.getByRole("button", { name: "Open the review", exact: true }).click();
  const card = page.getByRole("dialog").locator(".question-card");
  await expect(card.getByText("Do the proof screenshots read well?")).toHaveCount(1);

  // Act
  await page.evaluate(() => window.answerUnconfirmed());

  // Assert
  const outcome = card.locator(".question-outcome");
  await expect(outcome).toHaveText(ADVICE);
  const link = outcome.getByRole("link");
  await expect(link).toHaveText(ANSWERS_PAGE);
  await expect(link).toHaveAttribute("href", ANSWERS_PAGE);
  await expect(link).toHaveAttribute("target", "_blank");
  await expect(outcome.locator('a[href^="javascript:"]')).toHaveCount(0);
});
