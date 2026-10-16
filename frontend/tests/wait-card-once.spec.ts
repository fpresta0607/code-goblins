import { expect, test, ORIGIN } from "./site";

// The Overlord, 2026-10-02, on a goblin's wait card whose sentence headed the
// card and filled its page tile, with Open review in the tile and again in
// the action row: "it is redundant, with its text and the button added; clean
// it up, the text is only needed once". A card says its words once, and one
// control opens its page.
const SAYS = "Audit of what travels to his agent is ready on Scrawl, read-only, nothing built.";

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
  await expect(card.getByRole("button", { name: "Answer in your own words" })).toBeVisible();
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
