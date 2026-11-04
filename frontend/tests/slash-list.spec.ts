import { expect, test, type Page } from "./site";
import { field, open, openGoblin, pasted, snapshot, typed } from "./session-board";

// Made-up commands and skills each harness's supervisor answer could hold:
// a plugin's or a project's own, then a few built-ins, enough to scroll.
const ANSWERS = {
  claude: [
    { name: "kit-one:find-notes", description: "Find the notes a project keeps about a change, by what they say and when they were written, across every folder of the project", source: "skill", sigil: "/" },
    { name: "kit-one:tidy", description: "Tidy the working folder", source: "command", sigil: "/" },
    { name: "release-notes", description: "Write the release notes for the branch", source: "skill", sigil: "/" },
    { name: "clear", description: "Clear the conversation history and free up context.", source: "builtin", sigil: "/" },
    { name: "compact", description: "Free up context by summarizing the conversation so far.", source: "builtin", sigil: "/" },
    { name: "config", description: "Open the settings interface.", source: "builtin", sigil: "/" },
    { name: "context", description: "Visualize current context usage as a colored grid.", source: "builtin", sigil: "/" },
    { name: "model", description: "Switch the AI model and save it as your default for new sessions.", source: "builtin", sigil: "/" },
    { name: "usage", description: "Show plan usage limits and rate limit status.", source: "builtin", sigil: "/" },
  ],
  codex: [
    { name: "alpha-notes", description: "Keep running notes for the task at hand", source: "skill", sigil: "$" },
    { name: "kit:find-notes", description: "Find the notes a project keeps about a change, by what they say and when they were written", source: "skill", sigil: "$" },
    { name: "compact", description: "summarize conversation to prevent hitting the context limit", source: "builtin", sigil: "/" },
    { name: "diff", description: "show git diff (including untracked files)", source: "builtin", sigil: "/" },
    { name: "model", description: "choose what model and reasoning effort to use", source: "builtin", sigil: "/" },
    { name: "review", description: "review my current changes and find issues", source: "builtin", sigil: "/" },
    { name: "status", description: "show current session configuration and token usage", source: "builtin", sigil: "/" },
  ],
  pi: [
    { name: "plan-note", description: "Draft a plan for the task", source: "command", sigil: "/" },
    { name: "skill:release-notes", description: "Write the release notes for the branch", source: "skill", sigil: "/" },
    { name: "compact", description: "Manually compact the session context", source: "builtin", sigil: "/" },
    { name: "model", description: "Select model (opens selector UI)", source: "builtin", sigil: "/" },
    { name: "quit", description: "Quit pi", source: "builtin", sigil: "/" },
  ],
} as const;

const list = (page: Page) => page.getByRole("listbox", { name: "Commands", exact: true });

// Where the list stands against the conversation above it and the box below.
const placement = (page: Page) => list(page).evaluate((element) => {
  const card = element.getBoundingClientRect();
  const view = element.closest(".messages-view")?.getBoundingClientRect();
  const input = element.parentElement?.querySelector(".composer-input")?.getBoundingClientRect();
  const first = element.querySelector("[role=option]");
  const label = first?.querySelector(".popup-option-label")?.getBoundingClientRect();
  const detail = first?.querySelector(".popup-option-detail")?.getBoundingClientRect();
  return { top: card.top, bottom: card.bottom, viewTop: view?.top ?? 0, inputTop: input?.top ?? 0, labelBottom: label?.bottom ?? 0, detailTop: detail?.top ?? 0, scrolls: element.scrollHeight > element.clientHeight };
});

for (const width of [390, 1440]) {
  for (const harness of ["claude", "codex", "pi"] as const) {
    test(`${harness} at ${width}px: a slash opens every command and skill above the box, each its name over what it does, and a pick goes in as the harness takes it`, async ({ page }) => {
      // Arrange
      await page.setViewportSize({ width, height: width === 390 ? 640 : 900 });
      const relay = await open(page, snapshot({}, { harness }));
      await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS[harness] } }));
      await openGoblin(page);

      // Act
      await field(page).fill("/");

      // Assert: every entry is offered, on a card that stands inside the
      // conversation and above the box, its name over what it does.
      await expect(list(page).locator(".popup-option-label")).toHaveText(ANSWERS[harness].map((each) => each.sigil + each.name));
      await expect(list(page).getByRole("option").first()).toBeInViewport();
      const at = await placement(page);
      expect(at.top).toBeGreaterThanOrEqual(at.viewTop);
      expect(at.bottom).toBeLessThanOrEqual(at.inputTop);
      expect(at.labelBottom).toBeLessThanOrEqual(at.detailTop);

      // Act: a click or a tap on the harness's own skill takes it.
      const skill = ANSWERS[harness].find((each) => each.source === "skill")!;
      await list(page).getByRole("option", { name: skill.sigil + skill.name }).click();

      // Assert
      await expect(field(page)).toHaveValue(skill.sigil + skill.name + " ");
      await expect(list(page)).toHaveCount(0);
      await field(page).press("Enter");
      const sent = skill.sigil === "$" ? skill.sigil + skill.name + " " : skill.sigil + skill.name;
      await expect.poll(() => typed(relay)).toContain(pasted(sent) + "\r");
    });
  }
}

test("codex: a dollar offers only the skills Codex takes as mentions, and a slash word finds them too", async ({ page }) => {
  // Arrange
  await open(page, snapshot({}, { harness: "codex" }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS.codex } }));
  await openGoblin(page);
  const names = list(page).locator(".popup-option-label");

  // Act and Assert
  await field(page).fill("$");
  await expect(names).toHaveText(["$alpha-notes", "$kit:find-notes"]);
  await field(page).fill("/notes");
  await expect(names).toHaveText(["$alpha-notes", "$kit:find-notes"]);
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Tab");
  await expect(field(page)).toHaveValue("$kit:find-notes ");
});

test("a long list scrolls inside its card and the keyboard keeps the pick in view", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 640 });
  await open(page, snapshot({}, { harness: "claude" }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS.claude } }));
  await openGoblin(page);
  await field(page).fill("/");
  expect((await placement(page)).scrolls).toBe(true);

  // Act
  for (let step = 1; step < ANSWERS.claude.length; step++) await page.keyboard.press("ArrowDown");

  // Assert
  const last = list(page).getByRole("option").last();
  await expect(last).toHaveAttribute("aria-selected", "true");
  await expect(last).toBeInViewport();
});
