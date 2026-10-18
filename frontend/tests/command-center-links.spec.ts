import { expect, openItem, test } from "./site";

// The Overlord, 2026-10-17, of a CFO card that gave a page to copy: "when
// there's a link, I like the copy button, but I should be able to click it as
// a hyperlink to navigate to it." Every http or https address in a Command
// Center card and in History is a link that opens in a new tab, which the
// desktop window hands to his default browser; a value keeps its copy button
// beside it, and any other scheme stays text.
test.use({ locale: "en-US", timezoneId: "America/Chicago" });

test("a card's links open in a new tab, the value's copy button beside it, and other schemes stay text", async ({ page, context }, testInfo) => {
  // Arrange
  await context.route(/^https:\/\/(github|docs\.github)\.com\//, (route) => route.fulfill({ contentType: "text/html", body: "<title>GitHub</title>" }));
  await page.goto("/tests/fixtures/command-center-links.html");
  await openItem(page, "Roll the GitHub token");
  const card = page.getByRole("dialog").locator(".question-card");
  const value = card.locator(".copy-value").filter({ hasText: "https://github.com/settings/tokens" });
  await card.screenshot({ path: testInfo.outputPath("card-links.png") });

  // Act
  const opened = page.waitForEvent("popup");
  await value.getByRole("link", { name: "https://github.com/settings/tokens" }).click();
  const tab = await opened;

  // Assert
  expect(tab.url()).toBe("https://github.com/settings/tokens");
  await expect(value.getByRole("button", { name: "Copy https://github.com/settings/tokens" })).toBeVisible();
  const prose = card.getByRole("link", { name: "https://docs.github.com/en/authentication" });
  await expect(prose).toHaveAttribute("href", "https://docs.github.com/en/authentication");
  await expect(prose).toHaveAttribute("target", "_blank");
  await expect(prose).toHaveAttribute("rel", "noreferrer");
  await expect(card.getByRole("link")).toHaveCount(2);
  await expect(card).toContainText("Never ftp://example.com or javascript:alert(1).");
});

test("a History row's link opens in a new tab once the row unfolds", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/command-center-links.html");
  await page.locator(".command-center-menu > summary").click();
  const panel = page.locator(".command-center-updates");
  await panel.getByText("History").click();
  const row = panel.locator(".inbox-history li").filter({ hasText: "Pick a store layout" });

  // Act
  await row.locator(".history-open").click();

  // Assert
  const link = row.locator(".inbox-summary").getByRole("link", { name: "https://example.com/session/f26e" });
  await expect(link).toHaveAttribute("href", "https://example.com/session/f26e");
  await expect(link).toHaveAttribute("target", "_blank");
  await expect(row.locator(".inbox-summary")).toHaveText("Pick a store layout on the page (page https://example.com/session/f26e)");
});
