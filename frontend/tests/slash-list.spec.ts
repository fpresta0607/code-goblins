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

// The Overlord, 2026-11-11 (#2372): "cant type / commands into modern chat
// like terminal". A harness's own list runs the command it has picked on
// Enter, so the box's list does too: it only wrote the name into the box, and
// a second Enter sent it. Tab still writes it with room for its arguments,
// and Enter on a Codex skill still completes the mention, as Codex's own does.
for (const harness of ["claude", "codex", "pi"] as const) {
  test(`${harness}: Enter on the picked command runs it at once, and Tab writes it into the box`, async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({}, { harness }));
    await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS[harness] } }));
    await openGoblin(page);
    await field(page).fill("/comp");
    await expect(list(page).getByRole("option").first()).toHaveAttribute("aria-selected", "true");

    // Act
    await page.keyboard.press("Enter");

    // Assert: the whole command reached the session once, and the box is empty.
    await expect.poll(() => typed(relay)).toBe(pasted("/compact") + "\r");
    await expect(field(page)).toHaveValue("");
    await expect(list(page)).toHaveCount(0);

    // Act: Tab on a pick.
    await field(page).fill("/mod");
    await page.keyboard.press("Tab");

    // Assert: it waits in the box for its arguments, and nothing more was sent.
    await expect(field(page)).toHaveValue("/model ");
    expect(typed(relay)).toBe(pasted("/compact") + "\r");
  });

  test(`${harness}: Enter on the picked command while a turn runs hands the whole command to the board once`, async ({ page }) => {
    // Arrange
    const posts: string[] = [];
    const relay = await open(page, snapshot({}, { harness }));
    await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS[harness] } }));
    await page.route("**/api/actions", (route) => {
      const body = route.request().postDataJSON() as { kind?: string; text?: string };
      if (body.kind === "message") posts.push(body.text ?? "");
      return route.fulfill({ status: 202, json: { status: "queued" } });
    });
    await openGoblin(page);
    relay.say({ type: "screen", asking: false, open: false, working: true });
    await expect(page.locator(".reply-working")).toBeVisible();
    await field(page).fill("/comp");
    await expect(list(page).getByRole("option").first()).toHaveAttribute("aria-selected", "true");

    // Act
    await page.keyboard.press("Enter");

    // Assert: the board decides what its harness does with it in a turn, so
    // nothing is typed from here.
    await expect.poll(() => posts).toEqual(["/compact"]);
    await expect(field(page)).toHaveValue("");
    expect(typed(relay)).toBe("");
  });
}

test("codex: Enter on a picked skill completes the mention and sends nothing", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { harness: "codex" }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS.codex } }));
  await openGoblin(page);
  await field(page).fill("$alpha");

  // Act
  await page.keyboard.press("Enter");

  // Assert
  await expect(field(page)).toHaveValue("$alpha-notes ");
  expect(typed(relay)).toBe("");
});

test("codex: Enter on a skill a slash word found completes the mention and sends nothing", async ({ page }) => {
  // Arrange
  const posts: string[] = [];
  const relay = await open(page, snapshot({}, { harness: "codex" }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS.codex } }));
  await page.route("**/api/actions", (route) => {
    const body = route.request().postDataJSON() as { kind?: string; text?: string };
    if (body.kind === "message") posts.push(body.text ?? "");
    return route.fulfill({ status: 202, json: { status: "queued" } });
  });
  await openGoblin(page);
  await field(page).fill("/notes");
  await expect(list(page).locator(".popup-option-label")).toHaveText(["$alpha-notes", "$kit:find-notes"]);

  // Act
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");

  // Assert: the pick is a mention, whatever sigil found it, so it waits in the
  // box and nothing is typed into the session or handed to the board.
  await expect(field(page)).toHaveValue("$kit:find-notes ");
  expect(typed(relay)).toBe("");
  expect(posts).toEqual([]);
});

// The list also offers a command whose name or description only holds the
// word he typed. That one is no pick of his, so a whole command his harness
// knows and the list lacks goes as he typed it, as it does in the terminal,
// unless a listed name starts with it (#2490).
for (const lacked of [
  { draft: "/settings", commands: ANSWERS.claude, found: "/config", by: "description" },
  { draft: "/rc", commands: [...ANSWERS.claude, { name: "search-notes", description: "Search the notes of the project", source: "skill", sigil: "/" }], found: "/search-notes", by: "name" },
]) {
  test(`Enter on a whole command the list lacks sends it as he typed it: ${lacked.draft}, held by the ${lacked.by} of ${lacked.found}`, async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({}, { harness: "claude" }));
    await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: lacked.commands } }));
    await openGoblin(page);
    await field(page).fill(lacked.draft);
    await expect(list(page).locator(".popup-option-label")).toHaveText([lacked.found]);

    // Act
    await page.keyboard.press("Enter");

    // Assert: what he typed reached the session once, and the box is empty.
    await expect.poll(() => typed(relay)).toBe(pasted(lacked.draft) + "\r");
    await expect(field(page)).toHaveValue("");
  });
}

test("Enter runs a pick he moved to with the arrows, until the box changes", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { harness: "claude" }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS.claude } }));
  await openGoblin(page);
  const names = list(page).locator(".popup-option-label");
  await field(page).fill("/notes");
  await expect(names).toHaveText(["/kit-one:find-notes", "/release-notes"]);

  // Act: he moves to an entry whose name only holds the word, and runs it.
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");

  // Assert
  await expect.poll(() => typed(relay)).toBe(pasted("/release-notes") + "\r");

  // Act: he moves again, then types a command the list lacks over it.
  await field(page).fill("/notes");
  await expect(names).toHaveText(["/kit-one:find-notes", "/release-notes"]);
  await page.keyboard.press("ArrowDown");
  await field(page).fill("/settings");
  await expect(names).toHaveText(["/config"]);
  await page.keyboard.press("Enter");

  // Assert: the move does not outlive the draft it was made on.
  await expect.poll(() => typed(relay)).toBe(pasted("/release-notes") + "\r" + pasted("/settings") + "\r");
});

// Each guard of "the pick is his" on its own: the arrow that wraps upward,
// the move forgotten once he types on, and the compare without case.
test("Enter runs a pick he moved up to with the arrows", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { harness: "claude" }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS.claude } }));
  await openGoblin(page);
  await field(page).fill("/notes");
  await expect(list(page).locator(".popup-option-label")).toHaveText(["/kit-one:find-notes", "/release-notes"]);

  // Act: Up from the first entry wraps to the last.
  await page.keyboard.press("ArrowUp");
  await page.keyboard.press("Enter");

  // Assert
  await expect.poll(() => typed(relay)).toBe(pasted("/release-notes") + "\r");
});

test("a move with the arrows is forgotten once he types on, even back to the same word", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { harness: "claude" }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS.claude } }));
  await openGoblin(page);
  const names = list(page).locator(".popup-option-label");
  await field(page).fill("/notes");
  await expect(names).toHaveText(["/kit-one:find-notes", "/release-notes"]);
  await page.keyboard.press("ArrowDown");

  // Act: he types on and comes back to the word he moved on.
  await field(page).fill("/note");
  await field(page).fill("/notes");
  await expect(names).toHaveText(["/kit-one:find-notes", "/release-notes"]);
  await expect(list(page).getByRole("option").first()).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Enter");

  // Assert: the pick is the list's again, not his, so the box goes as typed.
  await expect.poll(() => typed(relay)).toBe(pasted("/notes") + "\r");
  await expect(field(page)).toHaveValue("");
});

test("Enter runs the pick whose name starts with what he typed in capitals", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { harness: "claude" }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS.claude } }));
  await openGoblin(page);
  await field(page).fill("/COMP");
  await expect(list(page).locator(".popup-option-label").first()).toHaveText("/compact");
  await expect(list(page).getByRole("option").first()).toHaveAttribute("aria-selected", "true");

  // Act
  await page.keyboard.press("Enter");

  // Assert
  await expect.poll(() => typed(relay)).toBe(pasted("/compact") + "\r");
});

test("codex: Enter on a skill a slash word found, with no arrow pressed, still completes the mention and sends nothing", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { harness: "codex" }));
  await page.route("**/api/commands**", (route) => route.fulfill({ json: { commands: ANSWERS.codex } }));
  await openGoblin(page);
  await field(page).fill("/notes");
  await expect(list(page).locator(".popup-option-label")).toHaveText(["$alpha-notes", "$kit:find-notes"]);

  // Act
  await page.keyboard.press("Enter");

  // Assert
  await expect(field(page)).toHaveValue("$alpha-notes ");
  expect(typed(relay)).toBe("");
});
