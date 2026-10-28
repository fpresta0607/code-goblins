import { expect, test } from "./site";
import { box, card, NAME, open } from "./session-board";

// The Overlord, 2026-10-28: "Classic mode is exactly the terminal as it was
// before Modern existed. No 'Back to messages' at the top and no Modern Chat
// box at the bottom. The bottom holds only the speech button and one
// terminal-style button that toggles to Modern Chat." The supervisor is
// played by tests/session-board.ts.

for (const width of [1440, 390]) {
  test.describe(`at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 }, hasTouch: width === 390 });

    test("Classic shows the terminal alone, with only the microphone and a switch to Modern Chat under it, even while typing is held", async ({ page }) => {
      // Arrange
      await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
      const relay = await open(page);
      await card(page, NAME).click();
      await expect.poll(() => relay.views).toBeGreaterThan(0);

      // Act: the terminal is held, which once brought a box in under it.
      relay.say({ type: "held", reason: "pipeline owns this task" });

      // Assert
      const terminal = page.getByRole("region", { name: "Goblin terminal", exact: true });
      await expect(terminal.getByText("Typing is paused.", { exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: "Back to messages", exact: true })).toHaveCount(0);
      await expect(box(page)).toHaveCount(0);
      await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeHidden();
      const dock = terminal.locator(".voice-dock");
      await expect(dock.getByRole("button", { name: "Modern Chat", exact: true })).toBeInViewport();
      await expect(dock.getByRole("button", { name: /^(Dictate|Recent messages)$/ })).toBeInViewport();
      expect(await dock.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    });

    test("Classic's switch opens Modern Chat on the conversation and its box, and keeps Modern chosen", async ({ page }) => {
      // Arrange
      await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
      const relay = await open(page);
      await card(page, NAME).click();
      await expect.poll(() => relay.views).toBeGreaterThan(0);

      // Act
      await page.getByRole("button", { name: "Modern Chat", exact: true }).click();

      // Assert
      await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
      await expect(box(page)).toBeVisible();
      await expect(page.getByRole("button", { name: "Modern Chat", exact: true })).toBeHidden();
      expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style"))).toBe("modern");
    });
  });
}
