import { expect, openItem, servePages, test } from "./site";

// The Overlord, 2026-10-17, of a CFO card that gave a page to copy: "when
// there's a link, I like the copy button, but I should be able to click it as
// a hyperlink to navigate to it." Every http or https address in a Command
// Center card and in History is a link that opens in a new tab, which the
// desktop window hands to his default browser, and any other scheme stays
// text. Since 2026-10-28 a link has no copy button beside it ("why is copy
// icon showing up"); a value that is not a link keeps its own.
test.use({ locale: "en-US", timezoneId: "America/Chicago" });

test("a card's links open in a new tab with no copy button beside them, and other schemes stay text", async ({ page, context }, testInfo) => {
  // Arrange
  await context.route(/^https:\/\/(github|docs\.github)\.com\//, (route) => route.fulfill({ contentType: "text/html", body: "<title>GitHub</title>" }));
  await page.goto("/tests/fixtures/command-center-links.html");
  await openItem(page, "Roll the GitHub token");
  const card = page.getByRole("dialog").locator(".question-card");
  const value = card.getByRole("link", { name: "https://github.com/settings/tokens" });
  await card.screenshot({ path: testInfo.outputPath("card-links.png") });

  // Act
  const opened = page.waitForEvent("popup");
  await value.click();
  const tab = await opened;

  // Assert
  expect(tab.url()).toBe("https://github.com/settings/tokens");
  await expect(card.getByRole("button", { name: /^Copy https?:/ })).toHaveCount(0);
  await expect(card.locator(".copy-value")).toHaveCount(0);
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

test("phone links in prose, link values and History open the private Scrawl route", async ({ page, context }) => {
  const origin = "https://pc.example.ts.net:4311";
  const review = "http://127.0.0.1:4387/session/private-review?mode=notes#answer";
  const privateLink = "/api/remote/scrawl-link?url=" + encodeURIComponent(review);
  await servePages(context, origin);
  await context.route(origin + "/api/remote/scrawl-link?**", (route) => route.fulfill({ contentType: "text/html", body: "<title>Private Scrawl</title>" }));
  await page.goto(origin + "/tests/fixtures/command-center-links.html");
  await openItem(page, "Review the private Scrawl page");
  const card = page.getByRole("dialog").locator(".question-card");
  const links = card.getByRole("link", { name: review, exact: true });
  await expect(links).toHaveCount(2);
  for (const link of await links.all()) {
    await expect(link).toHaveAttribute("href", privateLink);
    const opened = page.waitForEvent("popup");
    await link.click();
    const tab = await opened;
    expect(tab.url()).toBe(origin + privateLink);
    await tab.close();
  }
  await expect(card.getByRole("button", { name: "Copy " + review })).toHaveCount(0);

  await page.keyboard.press("Escape");
  await page.locator(".command-center-menu > summary").click();
  const panel = page.locator(".command-center-updates");
  await panel.getByText("History").click();
  const row = panel.locator(".inbox-history li").filter({ hasText: "Private Scrawl feedback" });
  await row.locator(".history-open").click();
  const history = row.locator(".inbox-summary").getByRole("link", { name: review, exact: true });
  await expect(history).toHaveAttribute("href", privateLink);
  const opened = page.waitForEvent("popup");
  await history.click();
  const tab = await opened;
  expect(tab.url()).toBe(origin + privateLink);
});
