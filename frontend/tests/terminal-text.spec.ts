import { expect, openItem, test, type Locator, type Page } from "./site";
import { at, NAME, open, openGoblin, snapshot } from "./session-board";

// The Overlord, 2026-11-13, of Modern Chat's dropdowns (#2524): "we dont need
// the open in terminal button ... can we make it the full width of the
// dropdown ... You don't need the word copy, we just have the icon ... The
// full screen should open up like a full window or full width ... the way it
// lays out in terminal, it's really hard to read. And this is for anything
// that runs in the command center in the terminal as well. Or even the
// command center questions or prompts". One block draws terminal text in a
// chat row, on a Command Center command and in a question that quotes any:
// the command, what it printed and how it ended, with a program's colours, a
// long line wrapped at its words and never inside one, Copy as an icon and
// Full screen over the whole window.

const ESC = "\x1b";
const COMMAND = "cd frontend &&\n  npx playwright test tests/queued-messages.spec.ts \\\n    --grep \"a queued command\"";
const LONG = "Error: expect(locator).toHaveText(expected) failed while the board still drew the message as queued, after the harness had already taken it at the end of its turn and typed it";
const TRACE = String.raw`D:\work\site\frontend\tests\queued-messages.spec.ts:118:42`;
const printed = (paint: (code: number, text: string) => string) => [
  "Running 2 tests using 1 worker",
  "  " + paint(32, "✓") + "  1 a queued command runs when its turn ends",
  "  " + paint(31, "✘") + "  2 a queued command stays held",
  LONG,
  "    at " + TRACE,
  ...Array.from({ length: 24 }, (_, step) => "    step " + (step + 1) + " of the trace"),
  "1 failed",
].join("\n");
const OUTPUT = printed((code, text) => `${ESC}[${code}m${text}${ESC}[39m`);
const PLAIN = printed((_, text) => text);
const DIGEST = "0123456789abcdef".repeat(14);
const NOTES = Array.from({ length: 24 }, (_, line) => "note " + (line + 1) + " of the release checklist").join("\n");
// Paths the fixture question's block names: one with a short name's tilde,
// an ampersand, a bracket and an accented letter, and one wider than a
// phone's card.
const KEPT = String.raw`C:\Users\RUNNER~1\Temp\a&b\Café\build(1).log`;
const BUILT = String.raw`C:\work\site\dist\release\artifacts\windows-x64\installer-with-a-long-name\setup.exe`;
// Commands that fetch from a web address: a chat row's, and in the fixture's
// Command Center one it would run and one a question fences, which also
// names where it saves.
const INSTALL = "https://example.com/install.sh";
const FETCH = "curl -fsSL " + INSTALL + " | sh";
const INSTALLER = "https://example.com/install.ps1";
const SAVED = String.raw`C:\work\install.ps1`;

const call = (id: string, tool: string, subject: string, fields: object) => ({ kind: "call", call_id: id, tool, subject, state: "done", ...fields });
const ROWS = { conversation: "fixture-conversation", more: false, messages: [
  { id: "prompt-0", seq: 0, at, kind: "prompt", source: "overlord", text: "Run the queued message tests" },
  { id: "reply-1", seq: 1, at, kind: "reply", blocks: [
    { kind: "text", text: "Running them now." },
    call("bash-1", "Bash", "cd frontend && npx playwright test", { input: COMMAND, result: OUTPUT, state: "failed" }),
    { kind: "text", text: "One failed. Reading the digest it wrote." },
    call("bash-2", "Bash", "cat digest.txt", { result: DIGEST }),
    { kind: "text", text: "Installing the tool it needs." },
    call("bash-6", "Bash", FETCH, { input: FETCH, result: "installed" }),
    { kind: "text", text: "Checking the release notes before it goes out." },
    call("read-1", "Read", "notes.md", { result: NOTES }),
    call("bash-3", "Bash", "git push --force", { state: "denied" }),
    call("bash-4", "Bash", "npm run lint", { state: "interrupted" }),
    call("bash-5", "Bash", "npm run build", { state: "running" }),
    { kind: "text", text: "Done." },
  ] },
] };

// How each command row ends: Completed, Running, Failed, Denied or
// Interrupted.
const ENDINGS = [
  { command: "cat digest.txt", text: "Completed", tone: "success" },
  { command: "npm run build", text: "Running", tone: "running" },
  { command: "npx playwright test", text: "Failed", tone: "error" },
  { command: "git push --force", text: "Denied", tone: "warning" },
  { command: "npm run lint", text: "Interrupted", tone: "warning" },
];

const SIZES = [{ width: 1440, height: 1100, label: "desktop" }, { width: 390, height: 844, label: "phone" }];
const messages = (page: Page) => page.getByRole("log", { name: "Messages of " + NAME });
const fitsWidth = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth);
const clipboard = async (page: Page) => (await page.evaluate(() => navigator.clipboard.readText())).replaceAll("\r\n", "\n");
const over = (page: Page) => page.getByRole("dialog", { name: "Terminal text", exact: true });

// openRow opens the row of the command given in Zeke's chat and gives the
// row, once the card it opens to shows.
async function openRow(page: Page, command: string) {
  await open(page, snapshot(), undefined, ROWS);
  await openGoblin(page);
  const row = messages(page).locator("details.message-call", { hasText: command });
  await row.locator("> summary").click();
  await expect(row.locator(".message-code").first()).toBeVisible();
  return row;
}

// foldedWords is each place a line of terminal text wraps inside a word:
// the two characters a row's end parts, where the first is no space and no
// separator a long word may wrap after.
const foldedWords = (lines: Locator) => lines.evaluateAll((all) => all.flatMap((line) => {
  const folded: string[] = [];
  const range = document.createRange();
  const walker = document.createTreeWalker(line, NodeFilter.SHOW_TEXT);
  let top: number | null = null;
  let last = "";
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.textContent ?? "";
    for (let index = 0; index < text.length; index++) {
      if (text[index] === "\n") continue;
      range.setStart(node, index);
      range.setEnd(node, index + 1);
      const rect = range.getClientRects()[0];
      if (rect) {
        if (top !== null && rect.top > top + 1 && !/[\s\\/,;=&?|:_.-]/.test(last)) folded.push(last + text[index]);
        top = rect.top;
      }
      last = text[index];
    }
  }
  return folded;
}));

for (const { width, height, label } of SIZES) {
  test.describe(`at ${label} width`, () => {
    test.use({ viewport: { width, height } });

    test("an open command row's container is as wide as the row, starts under its arrow, and has no Open in terminal", async ({ page }, testInfo) => {
      // Arrange and Act
      const row = await openRow(page, "npx playwright test");

      // Assert
      const [summary, card] = [await row.locator("> summary").boundingBox(), await row.locator(".message-code").first().boundingBox()];
      expect(Math.abs(card!.x - summary!.x), "it starts where the row does").toBeLessThanOrEqual(1);
      expect(Math.abs(card!.width - summary!.width), "it is as wide as the row").toBeLessThanOrEqual(1);
      await expect(row.locator(".message-code")).toHaveCount(1);
      await expect(row.getByRole("button", { name: "Open in terminal" })).toHaveCount(0);
      expect(await fitsWidth(page)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`row-${label}.png`) });
    });

    test("a command reads as its lines under the prompt's mark, what it printed in its program's colours, and how it ended", async ({ page }) => {
      // Arrange and Act
      const row = await openRow(page, "npx playwright test");
      const typed = row.locator(".terminal-command .terminal-line");
      const output = row.locator(".terminal-output");

      // Assert: the command, line by line.
      await expect(typed).toHaveText(["cd frontend &&", "npx playwright test tests/queued-messages.spec.ts \\", "--grep \"a queued command\""]);
      expect(await typed.first().evaluate((line) => getComputedStyle(line, "::before").content)).toBe('"$ "');

      // Assert: colours for the codes, and no code left as text.
      await expect(output.locator(".terminal-green")).toHaveText("✓");
      await expect(output.locator(".terminal-red")).toHaveText("✘");
      await expect(output).not.toContainText("[3");
      await expect(output.locator(".terminal-path")).toHaveText(TRACE);

      // Assert: the error reads as one, and the command's end is its own line.
      await expect(output.locator(".terminal-line", { hasText: "expect(locator)" })).toHaveClass(/\berror\b/);
      await expect(row.locator(".terminal-result")).toHaveText("Failed");
      await expect(row.locator(".terminal-result")).toHaveClass(/\berror\b/);
    });

    test("a long line wraps at its words and a path's separators, never inside a word, and a word too long for the card scrolls sideways", async ({ page }) => {
      // Arrange and Act
      const row = await openRow(page, "npx playwright test");
      const output = row.locator(".terminal-output");
      const long = output.locator(".terminal-line", { hasText: "expect(locator)" });

      // Assert: the long line takes more rows than a short one, inside the card.
      await expect(long).toBeVisible();
      expect((await long.boundingBox())!.height, "the long line wraps").toBeGreaterThan((await output.locator(".terminal-line").first().boundingBox())!.height * 1.5);
      expect(await output.evaluate((element) => element.scrollWidth <= element.clientWidth + 1), "nothing scrolls sideways").toBe(true);
      expect(await foldedWords(output.locator(".terminal-line"))).toEqual([]);
      expect(await foldedWords(row.locator(".terminal-command .terminal-line"))).toEqual([]);

      // Act: the row whose output is one word with no separator.
      const digest = messages(page).locator("details.message-call", { hasText: "cat digest.txt" });
      await digest.locator("> summary").click();
      const word = digest.locator(".terminal-output");

      // Assert: it stays one row and scrolls inside its card.
      await expect(word).toHaveText(DIGEST);
      expect(await word.evaluate((element) => element.scrollWidth > element.clientWidth), "the word scrolls sideways").toBe(true);
      expect(await word.locator(".terminal-line").evaluate((line) => line.getBoundingClientRect().height <= parseFloat(getComputedStyle(line).lineHeight) + 1), "the word is on one row").toBe(true);
      expect(await fitsWidth(page)).toBe(true);
    });

    test("Copy is its icon alone, keeps its name for a reader, and copies the whole text", async ({ page, context }) => {
      // Arrange
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      const row = await openRow(page, "npx playwright test");
      const copy = row.getByRole("button", { name: "Copy output", exact: true });

      // Assert: no word beside the icon.
      await expect(copy).toHaveText("");
      await expect(copy.locator("svg")).toBeVisible();

      // Act
      await copy.click();

      // Assert: every line, the ones scrolled out of view too, with no colour code.
      await expect.poll(() => clipboard(page)).toBe(PLAIN);
      await expect(row.getByRole("button", { name: "Copied", exact: true })).toBeVisible();

      // Act
      await row.getByRole("button", { name: "Copy command", exact: true }).click();

      // Assert
      await expect.poll(() => clipboard(page)).toBe(COMMAND);

      // Act: a row whose output stands alone, as a Read row's does.
      const read = messages(page).locator("details.message-call", { hasText: "notes.md" });
      await read.locator("> summary").click();
      await read.getByRole("button", { name: "Copy", exact: true }).click();

      // Assert: its one Copy takes every line, the ones scrolled out of view too.
      await expect.poll(() => clipboard(page)).toBe(NOTES);
    });

    test("Full screen fills the window with the whole text, and Escape or its button leaves it and nothing else", async ({ page }, testInfo) => {
      // Arrange
      const row = await openRow(page, "npx playwright test");
      const full = row.getByRole("button", { name: "Full screen", exact: true });
      await expect(full).toBeVisible();

      // Act
      await full.click();

      // Assert: the window, whole.
      expect(await over(page).boundingBox(), "it fills the window").toEqual({ x: 0, y: 0, width, height });
      const card = (await over(page).locator(".terminal-block").boundingBox())!;
      expect(card.width, "the card is as wide as the window leaves").toBeGreaterThan(width - 32);
      expect(card.height, "the card is as tall as the window leaves").toBeGreaterThan(height - 32);
      await expect(over(page).locator(".terminal-command")).toContainText("npx playwright test");
      await expect(over(page).locator(".terminal-output")).toContainText("1 failed");
      expect(await over(page).locator(".terminal-command").evaluate((command) => command.scrollHeight <= command.clientHeight), "a command of a few lines shows whole, with no scroll of its own").toBe(true);
      expect(await foldedWords(over(page).locator(".terminal-line"))).toEqual([]);
      await page.screenshot({ path: testInfo.outputPath(`full-${label}.png`) });

      // Act
      await page.keyboard.press("Escape");

      // Assert: only the full screen went, and the keyboard is back on its button.
      await expect(over(page)).toHaveCount(0);
      await expect(page.locator("#panel-title")).toHaveText(NAME);
      await expect(row.locator(".terminal-block")).toBeVisible();
      await expect(full).toBeFocused();

      // Act and Assert: its own button leaves it too.
      await full.click();
      await over(page).getByRole("button", { name: "Exit full screen", exact: true }).click();
      await expect(over(page)).toHaveCount(0);
    });

    test("a Command Center command reads as a chat row's does: its lines, what its terminal showed, and how it ended", async ({ page, context }, testInfo) => {
      // Arrange
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      await page.route("**/api/actions", async (route) => {
        const sent: Record<string, unknown> = route.request().postDataJSON();
        await route.fulfill({ json: { id: String(sent.id), kind: "run", run_id: "publish-v0.5.2", status: "queued" } });
      });
      await page.goto("/tests/fixtures/terminal-text.html");
      await page.waitForFunction(() => "terminalStep" in window);
      await openItem(page, "Publish release v0.5.2");
      const card = page.locator("dialog.question-modal .run-card");
      const block = card.locator(".terminal-block");

      // Assert: before it runs, the command alone, each line under the prompt's mark.
      await expect(block.locator(".terminal-command .terminal-line")).toHaveText(["gh release create v0.5.2 `", "--notes-file notes.md `", "--title \"v0.5.2\"", "if ($LASTEXITCODE) { exit $LASTEXITCODE }"]);
      expect(await block.locator(".terminal-command .terminal-line").first().evaluate((line) => getComputedStyle(line, "::before").content)).toBe('"> "');
      await expect(block.locator(".terminal-output, .terminal-result")).toHaveCount(0);
      await page.screenshot({ path: testInfo.outputPath(`run-ready-${label}.png`) });

      // Act: full screen from the command's own caption, with nothing printed yet.
      await block.getByRole("button", { name: "Full screen", exact: true }).click();

      // Assert: the command whole, with all of the card's room and not only
      // the two fifths it takes above what a command printed.
      await expect(over(page).locator(".terminal-command .terminal-line")).toHaveText(["gh release create v0.5.2 `", "--notes-file notes.md `", "--title \"v0.5.2\"", "if ($LASTEXITCODE) { exit $LASTEXITCODE }"]);
      const alone = over(page).locator(".terminal-command");
      expect(await alone.evaluate((command) => command.scrollHeight <= command.clientHeight), "the command shows whole").toBe(true);
      expect((await alone.boundingBox())!.height, "the command takes the card's room").toBeGreaterThan((await over(page).boundingBox())!.height * 0.4);
      await page.keyboard.press("Escape");
      await expect(over(page)).toHaveCount(0);

      // Act: he runs it, and it fails.
      await card.getByRole("button", { name: "Run in PowerShell" }).click();
      await page.evaluate(() => (window as unknown as { terminalStep: (name: string) => void }).terminalStep("failed"));
      await expect(page.locator("main")).toHaveAttribute("data-step", "failed");

      // Assert: what its terminal showed, read as terminal text, and how it
      // ended, with no exit code, which History keeps.
      await expect(block.locator(".terminal-line.warning")).toContainText("notes.md has no heading");
      await expect(block.locator(".terminal-line.error")).toHaveText("error: release v0.5.2 already exists");
      await expect(block.getByRole("link", { name: "https://github.com/acme/site/releases/tag/v0.5.2", exact: true })).toHaveAttribute("target", "_blank");
      await expect(block.locator(".terminal-path")).toContainText("site-v0.5.2-windows-x64-installer");
      await expect(block.locator(".terminal-result")).toHaveText("Failed");
      await expect(block.locator(".terminal-result")).toHaveClass(/\berror\b/);
      await expect(card).not.toContainText(/exit code/i);
      expect(await foldedWords(block.locator(".terminal-line"))).toEqual([]);
      expect(await block.locator(".terminal-output").evaluate((element) => element.scrollWidth <= element.clientWidth + 1), "nothing scrolls sideways").toBe(true);
      expect(await fitsWidth(page)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`run-failed-${label}.png`) });

      // Act
      await block.getByRole("button", { name: "Copy command", exact: true }).click();

      // Assert
      await expect.poll(() => clipboard(page)).toBe("gh release create v0.5.2 `\n    --notes-file notes.md `\n    --title \"v0.5.2\"\nif ($LASTEXITCODE) { exit $LASTEXITCODE }");

      // Act: full screen from inside the Command Center, then Escape.
      await block.getByRole("button", { name: "Full screen", exact: true }).click();
      expect(await over(page).boundingBox(), "it fills the window").toEqual({ x: 0, y: 0, width, height });
      await page.keyboard.press("Escape");

      // Assert: Escape left the full screen and the Command Center stayed open.
      await expect(over(page)).toHaveCount(0);
      await expect(card).toBeVisible();
    });

    // He could not tell whether to click the radio buttons or the prompt the
    // question quoted (2026-11-13: "what do you click the radio button or teh
    // terminal ?"), so the prompt reads as its terminal showed it and the
    // card's radios are the one place to choose.
    test("a question that quotes a prompt draws it as terminal text, each line as written and none as a choice, beside the card's own choices", async ({ page }, testInfo) => {
      // Arrange
      await page.goto("/tests/fixtures/terminal-text.html");
      await page.waitForFunction(() => "terminalStep" in window);

      // Act
      await openItem(page, "Should the release be replaced?");
      const card = page.locator("dialog.question-modal .question-card");
      const lines = card.locator(".terminal-block .terminal-line");

      // Assert
      await expect(lines).toHaveText(["Release v0.5.2 already exists. Replace it?", "❯ 1. Yes, replace it", "2. No, keep the old one"]);
      await expect(card.locator(".terminal-block > figcaption")).toHaveText(["output"]);
      await expect(card.locator(".terminal-command")).toHaveCount(0);
      const looks = await lines.evaluateAll((all) => all.map((line) => getComputedStyle(line).color + " " + getComputedStyle(line).fontWeight));
      expect(new Set(looks).size, "no line of the prompt looks picked: " + looks.join(", ")).toBe(1);
      expect(await foldedWords(lines)).toEqual([]);
      await expect(card.locator(".question-decision")).toContainText("Should the release be replaced? The publish command stopped at this prompt:");
      await expect(card.locator(".question-decision")).not.toContainText("```");
      await expect(card.getByRole("radio")).toHaveCount(3);
      expect(await fitsWidth(page)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`question-${label}.png`) });
    });

    // A question can fence a command it would run, which reads as a command
    // under its shell's name, where what a command printed reads as output,
    // and a local path either names is a link, as one in the question's words
    // is: the whole path the card names, in the type of the line it is on,
    // wrapping only after its separators.
    test("a question's fence that names a shell reads as a command, its other fences as output, and a local path in either is a link", async ({ page, context }) => {
      // Arrange
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      await page.goto("/tests/fixtures/terminal-text.html");
      await page.waitForFunction(() => "terminalStep" in window);

      // Act
      await openItem(page, "Should the old build be removed?");
      const card = page.locator("dialog.question-modal .question-card");
      const command = card.locator(".question-decision .terminal-block");

      // Assert: the command under its shell's name and the prompt's mark, with
      // no output.
      await expect(command.locator("figcaption")).toHaveText(["powershell"]);
      await expect(command.locator(".terminal-command .terminal-line")).toHaveText(["Remove-Item C:\\work\\old -Recurse"]);
      expect(await command.locator(".terminal-line").first().evaluate((line) => getComputedStyle(line, "::before").content)).toBe('"> "');
      await expect(command.locator(".terminal-output")).toHaveCount(0);
      await expect(command.getByRole("button", { name: "Copy the path C:\\work\\old", exact: true })).toBeVisible();

      // Act
      const details = card.locator(".question-details");
      await details.locator("> summary").click();
      const evidence = details.locator(".question-evidence .terminal-block");

      // Assert: what the build printed as output, with no fence left as text,
      // and each path in it a link to the whole path.
      await expect(evidence.locator(".terminal-line")).toHaveText(["error: cannot open C:\\work\\old\\config.json", "log kept in " + KEPT, "wrote " + BUILT]);
      await expect(evidence.locator("figcaption")).toHaveText(["output"]);
      await expect(details.locator(".question-evidence")).not.toContainText("```");
      await expect(evidence.getByRole("button", { name: "Copy the path C:\\work\\old\\config.json", exact: true })).toBeVisible();

      // Assert: the long path wraps after its separators, never inside a
      // folder's name, in the size of the line it is on.
      expect(await foldedWords(card.locator(".terminal-block .terminal-line"))).toEqual([]);
      const built = evidence.getByRole("button", { name: "Copy the path " + BUILT, exact: true });
      const [linkSize, lineSize] = await built.evaluate((link) => [getComputedStyle(link).fontSize, getComputedStyle(link.closest(".terminal-line")!).fontSize]);
      expect(linkSize, "the link's type is its line's").toBe(lineSize);
      expect(await fitsWidth(page)).toBe(true);

      // Act
      await evidence.getByRole("button", { name: "Copy the path " + KEPT, exact: true }).click();

      // Assert: the link names the path whole, a tilde, an ampersand, a
      // bracket and an accented letter included, and copies it in a browser.
      await expect.poll(() => clipboard(page)).toBe(KEPT);
    });
  });
}

// A path link in a block a question fences wraps as the line's words do: a
// button lays out as one box, which a long path cannot wrap out of, so on a
// phone its later rows would start where a line starts, reading as new lines,
// and after words it would start a row of its own. It flows with its line
// instead, and still takes focus and copies its path with Enter or Space.
test("a long path link in a question's block flows with its line, its later rows set in, and Enter or Space copies it", async ({ page, context }) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 844 });
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/tests/fixtures/terminal-text.html");
  await page.waitForFunction(() => "terminalStep" in window);
  await openItem(page, "Should the old build be removed?");
  const details = page.locator("dialog.question-modal .question-card .question-details");
  await details.locator("> summary").click();
  const evidence = details.locator(".question-evidence .terminal-block");
  const built = evidence.getByRole("button", { name: "Copy the path " + BUILT, exact: true });

  // Act
  const layout = await built.evaluate((link) => {
    const line = link.closest(".terminal-line")!;
    const words = document.createRange();
    words.selectNodeContents(line);
    return {
      wordsTop: words.getClientRects()[0].top,
      indent: line.getBoundingClientRect().left + parseFloat(getComputedStyle(line).paddingLeft),
      rows: Array.from(link.getClientRects(), (rect) => ({ top: rect.top, left: rect.left })),
    };
  });

  // Assert: the link starts on the row of the words before it and goes on over
  // more rows, each set in under the line's hanging indent.
  expect(layout.rows.length, "the link wraps over rows of its line").toBeGreaterThan(1);
  expect(Math.abs(layout.rows[0].top - layout.wordsTop), "the link starts on the row of wrote").toBeLessThan(2);
  for (const row of layout.rows.slice(1)) expect(Math.abs(row.left - layout.indent), "a later row of the link sets in").toBeLessThan(1);
  expect(await evidence.locator(".terminal-output").evaluate((element) => element.scrollWidth <= element.clientWidth + 1), "nothing scrolls sideways").toBe(true);

  // Act
  await built.focus();
  await page.keyboard.press("Enter");

  // Assert
  await expect.poll(() => clipboard(page)).toBe(BUILT);

  // Act
  await page.evaluate(() => navigator.clipboard.writeText(""));
  await page.keyboard.press("Space");

  // Assert
  await expect.poll(() => clipboard(page)).toBe(BUILT);
});

// fixtureCommand opens the Command Center item named on the fixture page and
// gives the command its card draws.
async function fixtureCommand(page: Page, item: string, card: string) {
  await page.goto("/tests/fixtures/terminal-text.html");
  await page.waitForFunction(() => "terminalStep" in window);
  await openItem(page, item);
  return page.locator("dialog.question-modal " + card + " .terminal-command");
}

// A web address in a command's line is a link that reads as written, as one
// in what it printed is, wherever a command is drawn (the Overlord,
// 2026-11-11: "Should be clickable links or buttons"; #2360), and a local
// path beside it in a question's fence is still a link to that path.
const FETCHES = [
  { place: "a chat row's command", line: FETCH, address: INSTALL, pathLinks: 0, command: async (page: Page) => (await openRow(page, "curl -fsSL")).locator(".terminal-command") },
  { place: "a Command Center command", line: "Invoke-WebRequest " + INSTALLER + " -OutFile install.ps1", address: INSTALLER, pathLinks: 0, command: (page: Page) => fixtureCommand(page, "Fetch the installer", ".run-card") },
  { place: "a question's shell fence", line: "Invoke-WebRequest " + INSTALLER + " -OutFile " + SAVED, address: INSTALLER, pathLinks: 1, command: (page: Page) => fixtureCommand(page, "Should the installer be fetched again?", ".question-card .question-decision") },
];
for (const { place, line, address, pathLinks, command } of FETCHES) {
  test(`a web address in ${place} is a link that reads as written`, async ({ page }) => {
    // Arrange and Act
    const typed = (await command(page)).locator(".terminal-line");

    // Assert
    await expect(typed).toHaveText([line]);
    const link = typed.locator("a.message-link");
    await expect(link).toHaveText(address);
    await expect(link).toHaveAttribute("href", address);
    await expect(link).toHaveAttribute("target", "_blank");
    await expect(typed.getByRole("button", { name: "Copy the path " + SAVED, exact: true })).toHaveCount(pathLinks);
  });
}

// Each way a command row ends reads under that row's own mark, so a command
// denied at a permission prompt never reads as one paused: Completed's check,
// Running's clock, Failed's warning, Denied's lock and Interrupted's pause.
test("an open command row ends in how it ended, in that ending's colour and under the row's own mark", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), undefined, ROWS);
  await openGoblin(page);
  const marks: string[] = [];

  for (const { command, text, tone } of ENDINGS) {
    // Act
    const row = messages(page).locator("details.message-call", { hasText: command });
    await row.locator("> summary").click();
    const result = row.locator(".terminal-result");

    // Assert
    await expect(result).toHaveText(text);
    await expect(result).toHaveClass(new RegExp("\\b" + tone + "\\b"));
    const mark = await result.locator("path").getAttribute("d");
    expect(mark, text + " is drawn with its row's own mark").toBe(await row.locator("> summary .call-mark path").getAttribute("d"));
    marks.push(mark ?? "");
  }
  expect(new Set(marks).size, "each ending has a mark of its own").toBe(ENDINGS.length);
});

// A command can fail without exiting, as one whose terminal closed before it
// finished: its block ends in Failed as its card does, with no exit code.
test("a Command Center command that failed with no exit code ends in Failed", async ({ page }) => {
  // Arrange
  await page.route("**/api/actions", async (route) => {
    const sent: Record<string, unknown> = route.request().postDataJSON();
    await route.fulfill({ json: { id: String(sent.id), kind: "run", run_id: "publish-v0.5.2", status: "queued" } });
  });
  await page.goto("/tests/fixtures/terminal-text.html");
  await page.waitForFunction(() => "terminalStep" in window);
  await openItem(page, "Publish release v0.5.2");
  const card = page.locator("dialog.question-modal .run-card");
  await card.getByRole("button", { name: "Run in PowerShell" }).click();

  // Act
  await page.evaluate(() => (window as unknown as { terminalStep: (name: string) => void }).terminalStep("unfinished"));
  await expect(page.locator("main")).toHaveAttribute("data-step", "unfinished");

  // Assert
  const result = card.locator(".terminal-block .terminal-result");
  await expect(result).toHaveText("Failed");
  await expect(result).toHaveClass(/\berror\b/);
  await expect(card.locator(".terminal-output")).toHaveCount(0);
  await expect(card).not.toContainText(/exit code/i);
});
