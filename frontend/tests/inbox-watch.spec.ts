import { expect, test } from "@playwright/test";

// The Overlord, 2026-09-28, seeing "Pages: Browser walkthrough running" for
// cg-board-kill's own test walkthroughs while Waiting on you said nothing:
// "why is this in command center". Only what needs him reaches the Command
// Center: a goblin's own test run stays off it, a walkthrough its goblin asks
// him to watch is listed under Waiting on you, and everything waiting on him
// is listed there.
test("waiting on you lists what needs him, and a goblin's own test run stays off", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/inbox-watch.html");

  // Act
  await page.getByLabel("Command Center, 2 waiting on you").click();

  // Assert
  const waiting = page.getByRole("region", { name: "Waiting on you" });
  await expect(waiting.getByRole("listitem")).toHaveCount(2);
  await expect(waiting.getByRole("listitem").nth(1)).toContainText("Checkout race fix");
  await expect(waiting.getByRole("listitem").nth(1)).toContainText("Watch the checkout walkthrough I am running for you");
  await expect(waiting.getByRole("link", { name: "Watch: Watch the checkout walkthrough I am running for you" })).toHaveAttribute("href", "http://127.0.0.1:5174/checkout");
  await expect(page.getByText("Kill switch for the board")).toHaveCount(0);
  await expect(page.getByText("Browser walkthrough running")).toHaveCount(0);
  await expect(page.getByText("Everything that needs you is listed here. A goblin's own test runs stay off this list.")).toBeVisible();
});
