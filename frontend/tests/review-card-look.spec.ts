import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-11 about 01:50Z, with a screenshot of Tad's card for
// the AFK report: "i dont like scrawl alert ui and command cneter header text
// being duplicates?? also do we need to have fully yellow color or can we keep
// lantern styling but be siqstack styled". A card says each thing once: the
// goblin's name and title, then the item's own title. A card for a Scrawl page
// shows the page itself with one button and no empty box or explaining line.
// The lantern keeps its shape in SIQstack's card, mint and green.
test.use({ locale: "en-US", timezoneId: "America/Chicago", viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

const TASK_TITLE = "The AFK report shows one Merged group";
const PREVIEW = "<!doctype html><title>Your AFK report</title><h1>Your AFK report with one Merged group</h1>";

async function open(page: Page, item = "") {
  await page.clock.install({ time: new Date("2026-10-11T01:50:00Z") });
  const asked: string[] = [];
  await page.route("**/api/reviews/*/preview", (route) => {
    asked.push(new URL(route.request().url()).pathname);
    return route.fulfill({ contentType: "text/html", headers: { "Content-Security-Policy": "sandbox allow-scripts; frame-ancestors 'self'" }, body: PREVIEW });
  });
  await page.goto("/tests/fixtures/review-card-look.html" + (item ? "?item=" + item : ""));
  return asked;
}

test("a goblin's Scrawl card names the goblin, says its title once and shows the page with one button", async ({ page }, testInfo) => {
  // Arrange
  const asked = await open(page);
  const card = page.getByRole("dialog").locator(".question-card");

  // Act
  const preview = card.locator(".page-preview iframe");

  // Assert
  await expect(card.locator(".asker strong")).toHaveText("Tad - Report Fixer");
  await expect(card.getByText(TASK_TITLE)).toHaveCount(0);
  await expect(card.getByText("Your AFK report with one Merged group: before and after pictures")).toHaveCount(1);
  await expect(preview).toHaveAttribute("sandbox", "allow-scripts");
  await expect(page.frameLocator(".page-preview iframe").getByRole("heading", { name: "Your AFK report with one Merged group" })).toBeVisible();
  expect(asked).toContain("/api/reviews/cg-afk-report-one-merged/preview");
  await expect(card.getByRole("link", { name: /^Open review/ })).toHaveCount(1);
  await expect(card.getByRole("link", { name: /^Open review/ })).toHaveAttribute("href", "http://127.0.0.1:4387/session/afk-report");
  await expect(card.getByText(/Scrawl page|Waiting for your answer|conversation box|closes this card/)).toHaveCount(0);
  await expect(card.getByRole("textbox")).toHaveCount(0);
  await page.getByRole("dialog").locator(".card-stage").screenshot({ path: testInfo.outputPath("tad-card.png") });
});

test("the CFO's delivered picture shows as its page with one button", async ({ page }) => {
  // Arrange
  await open(page, "picture");
  const card = page.getByRole("dialog").locator(".question-card");

  // Act
  const heading = card.getByRole("heading", { name: "The orchestration card, after the fit" });

  // Assert
  await expect(heading).toBeVisible();
  await expect(card.locator(".asker strong")).toHaveText("The CFO");
  await expect(card.locator(".page-preview iframe")).toHaveAttribute("src", "/api/reviews/board-card-text-fits-20261010/preview");
  await expect(card.getByRole("link", { name: /^Open review/ })).toHaveCount(1);
});

test("the Command Center keeps its lantern box in SIQstack's card, mint and green, never yellow", async ({ page }, testInfo) => {
  // Arrange
  await open(page, "box");

  // Act
  await page.locator(".command-center-menu > summary").click();
  const row = page.locator(".inbox-list.waiting li").filter({ hasText: "Your AFK report" });

  // Assert
  await expect(row.locator(".inbox-text strong")).toHaveText("Tad - Report Fixer");
  const look = await page.evaluate(() => {
    const color = (selector: string, property: string, pseudo?: string) => getComputedStyle(document.querySelector(selector)!, pseudo).getPropertyValue(property).trim();
    return {
      lantern: getComputedStyle(document.documentElement).getPropertyValue("--lantern").trim(),
      hide: getComputedStyle(document.documentElement).getPropertyValue("--hide").trim(),
      asker: color(".inbox-list.waiting li .inbox-text strong", "color"),
      box: color(".cfo-command", "background-image", "::before"),
      card: color(".inbox-list.waiting li", "background-color", "::before"),
    };
  });
  expect(look.lantern).toBe("#65efcd");
  expect(look.hide).toBe("#0c1219");
  expect(look.asker).toBe("rgb(101, 239, 205)");
  expect(look.box).toContain("rgb(0, 229, 155)");
  expect(look.card).toBe("rgb(12, 18, 25)");
  await page.screenshot({ path: testInfo.outputPath("lantern-box.png") });
});
