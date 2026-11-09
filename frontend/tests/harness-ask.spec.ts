import { expect, test } from "./site";
import { asks, box, field, NAME, open, openGoblin, typed } from "./session-board";

// Claude Code's permission prompt for a command, as the relay reads it off the
// terminal: a pane with gutter bars down its sides, the command it quotes
// wrapped at the terminal's width, and its numbered choices.
const PROMPT = {
  rows: ["╭────────────────────────────────────────╮", "│ Bash command                           │", "│                                        │",
    "│   gh pr checks 2221 --json name,bucket │", "│   Read the pull request's checks       │", "│                                        │",
    "│ This command needs approval because it │", "│ reads the network.                     │", "╰────────────────────────────────────────╯", "Do you want to proceed?"],
  choices: [{ text: "Yes", number: 1, focused: true }, { text: "Yes, and don't ask again for gh pr checks", number: 2 }, { text: "No", number: 3 }],
  hints: ["Esc to cancel"],
};
const GUTTER = /[│┃║▌▍▎▏╎╏┆┇┊┋╭╮╰╯]/;

for (const width of [390, 1440]) {
  test.describe(`a goblin waiting on its harness's own prompt at ${width}px`, () => {
    test.use({ viewport: { width, height: 860 }, hasTouch: width < 600 });

    test("shows the prompt as the conversation's last message, never a popup over the box, and a choice reaches the goblin once", async ({ page }) => {
      // Arrange
      const relay = await open(page);
      await openGoblin(page);
      const conversation = page.getByRole("log", { name: "Messages of " + NAME });

      // Act
      relay.say({ type: "screen", asking: true, open: true, working: false, submission: "his", panel: PROMPT });

      // Assert: it is a message in the conversation, read as the chat reads.
      const prompt = asks(page);
      await expect(prompt).toBeVisible();
      await expect(conversation.locator(".conversation-ask").getByRole("group", { name: NAME + " asks" })).toBeVisible();
      await expect(box(page).locator(".harness-panel")).toHaveCount(0);
      await expect(page.locator(".harness-panel:not(.in-chat)")).toHaveCount(0);
      await expect(prompt.locator(".harness-panel-title")).toHaveText("Bash command");
      await expect(prompt.locator("pre")).toHaveText("gh pr checks 2221 --json name,bucket\nRead the pull request's checks");
      await expect(prompt.getByText("This command needs approval because it reads the network.", { exact: true })).toBeVisible();
      expect(await conversation.innerText()).not.toMatch(GUTTER);
      const [promptBox, fieldBox] = [await prompt.boundingBox(), await field(page).boundingBox()];
      expect(promptBox && fieldBox && promptBox.y + promptBox.height <= fieldBox.y, "the prompt sits above the box, not over it").toBe(true);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

      // Act: he presses Yes, and presses again before the harness has taken it.
      const yes = prompt.getByRole("button", { name: "Yes", exact: true });
      if (width < 600) { await yes.tap(); await yes.tap({ force: true }); } else await yes.dblclick();

      // Assert: the goblin is sent Yes once.
      await expect.poll(() => typed(relay)).toBe("1");
      await page.waitForTimeout(300);
      expect(typed(relay)).toBe("1");
      relay.say({ type: "screen", asking: false, open: false, working: true, submission: "" });
      await expect(prompt).toHaveCount(0);
    });

    test("takes its choices from the keyboard in the empty box", async ({ page }) => {
      // Arrange
      const relay = await open(page);
      await openGoblin(page);
      relay.say({ type: "screen", asking: true, open: true, working: false, submission: "his", panel: PROMPT });
      await expect(asks(page)).toBeVisible();

      // Act
      await field(page).focus();
      await field(page).press("3");

      // Assert
      await expect.poll(() => typed(relay)).toBe("3");
      await expect(field(page)).toHaveValue("");
    });
  });
}
