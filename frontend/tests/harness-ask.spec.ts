import { expect, test } from "./site";
import { asks, at, box, field, NAME, open, openGoblin, snapshot, typed } from "./session-board";

// Claude Code's permission prompt for a command, as the relay reads it off the
// terminal: a pane with gutter bars down its sides, the command it quotes
// wrapped at the terminal's width, and its numbered choices.
const PROMPT = {
  rows: ["╭────────────────────────────────────────╮", "│ Bash command                           │", "│                                        │",
    "│   gh pr checks 2221 --json name,bucket │", "│   Read the pull request's checks       │", "│                                        │",
    "│ This command needs approval because it │", "│ reads the network.                     │", "╰────────────────────────────────────────╯", "Do you want to proceed?"],
  choices: [{ text: "Yes", number: 1, focused: true }, { text: "Yes, and don't ask again for gh pr checks", number: 2 }, { text: "No", number: 3 }],
  hints: ["Esc to cancel"],
  cols: 42,
};
// Claude Code's question after a switch of model, a screen of its own after
// the one before.
const SWITCH = {
  rows: ["Switch model?", "Your next response will be slower and use more tokens"],
  choices: [{ text: "Yes, switch to Haiku 5.5", number: 1, focused: true }, { text: "No, go back", number: 2 }],
  hints: [],
};
// Codex's question with no pane around it, wrapped at a terminal of 40
// columns: the relay takes off the indent of two the rows shared and sends
// the 38 columns left, so only those join its wrapped words.
const WRAPPED = {
  rows: ["Run this command?", "This command needs approval because it", "reads the network."],
  choices: [{ text: "Yes", number: 1, focused: true }, { text: "No", number: 2 }],
  hints: [],
  cols: 38,
};
const GUTTER = /[│┃║▌▍▎▏╎╏┆┇┊┋╭╮╰╯]/;
// A conversation long enough to read up in.
const LONG = {
  messages: Array.from({ length: 60 }, (_, seq) => seq % 2
    ? { id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text: `Answer ${seq}\n\nIt runs over a few lines\nso the page has height to read up in.` }] }
    : { id: "m" + seq, seq, at, kind: "prompt", source: "overlord", text: "Question " + seq }),
  conversation: "fixture-conversation", more: false,
};

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

    test("joins a prompt's words where the terminal wrapped them at the columns the relay sent, with no pane to measure", async ({ page }) => {
      // Arrange
      const relay = await open(page);
      await openGoblin(page);

      // Act
      relay.say({ type: "screen", asking: true, open: true, working: false, submission: "his", panel: WRAPPED });

      // Assert: one paragraph, as the harness wrote it.
      const prompt = asks(page);
      await expect(prompt.locator(".harness-panel-title")).toHaveText("Run this command?");
      await expect(prompt.getByText("This command needs approval because it reads the network.", { exact: true })).toBeVisible();
      await expect(prompt.getByText("reads the network.", { exact: true })).toHaveCount(0);
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

    test("a prompt that opens while he reads up comes into view", async ({ page }) => {
      // Arrange
      const relay = await open(page, snapshot(), "rebase onto main", LONG);
      await openGoblin(page);
      const conversation = page.getByRole("log", { name: "Messages of " + NAME });
      await expect(conversation).toContainText("Answer 59");
      await conversation.evaluate((log) => { log.scrollTop = 0; });
      await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();

      // Act
      relay.say({ type: "screen", asking: true, open: true, working: false, submission: "his", panel: PROMPT });

      // Assert
      await expect(asks(page)).toBeVisible();
      await expect.poll(async () => {
        const [promptBox, logBox] = [await asks(page).boundingBox(), await conversation.boundingBox()];
        return !!promptBox && !!logBox && promptBox.y >= logBox.y && promptBox.y + promptBox.height <= logBox.y + logBox.height + 1;
      }, { message: "the prompt is within the conversation's visible box" }).toBe(true);
      await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
    });

    test("the next screen's choices take a press at once after a press on the one before", async ({ page }) => {
      // Arrange
      const relay = await open(page);
      await openGoblin(page);
      relay.say({ type: "screen", asking: true, open: true, working: false, submission: "his", panel: PROMPT });
      await asks(page).getByRole("button", { name: "Yes", exact: true }).click();
      await expect.poll(() => typed(relay)).toBe("1");

      // Act
      relay.say({ type: "screen", asking: true, open: true, working: false, submission: "his", panel: SWITCH });
      const goBack = asks(page).getByRole("button", { name: "No, go back", exact: true });
      await expect(goBack).toBeEnabled({ timeout: 1000 });
      await goBack.click();

      // Assert
      await expect.poll(() => typed(relay), { timeout: 1000 }).toBe("12");
    });
  });
}
