import { expect, test, type Locator, type Page } from "./site";
import { open, openGoblin, snapshot } from "./session-board";

// The Overlord, 2026-10-28, from his phone: "mobile view is messy", with a
// picture of one PowerShell tool row printing its whole command wrapped over
// 14 lines, then "More menus and openable functionality ... And
// reformatting, pull with a lot of text need to be cleaned." A tool row is one
// line that opens to its whole command and output; a long block starts folded
// and opens on tap; code and commands sit in cards whose long lines scroll
// sideways; and nothing makes the conversation wider than the screen. The
// supervisor is played by tests/session-board.ts.

// The command in his 01:52Z picture.
const COMMAND = String.raw`Get-ChildItem C:\dev\code-goblins\state -File | Where-Object { $_.Length -lt 4096 -and $_.LastWriteTime -gt (Get-Date).AddMinutes(-10) } | Select-Object Name, LastWriteTime, Length; try { $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 15 "http://127.0.0.1:64610/healthz"; "health 64610: $($r.StatusCode) $($r.Content)" } catch { "health 64610 error: $($_.Exception.Message)" }`;
const OUTPUT = "Name                                   LastWriteTime          Length\n----                                   -------------          ------\nsupervisor-heartbeat-cg-chat-agentic-ui.json 10/27/2026 8:49:58 PM 812\nhealth 64610: 200 ok";
const PATH = String.raw`C:\dev\code-goblins\state\tasktmp\cg-chat-agentic-ui-h4\runtime-capsule-with-a-very-long-name-that-never-breaks.md`;
const URL = "https://github.com/fpresta0607/code-goblins/actions/runs/123456789012/job/987654321098?check_suite_focus=true&pr=1579&very_long_parameter=" + "x".repeat(60);
const CODE = "npm publish --otp=YOUR_6_DIGIT_CODE --access public --registry https://registry.npmjs.org/ --tag next-release-candidate";
const PASTE = Array.from({ length: 14 }, (_, at) => `Line ${at + 1} of the log he pasted, ${at % 2 ? PATH : "with words enough to wrap on a phone"}`).join("\n");
const THOUGHT = "**Checking the board's health** before the next step.\n\nThe heartbeat file is fresh, so the supervisor is alive. " + "It reads well. ".repeat(40);

const at = (minute: number) => new Date(Date.parse("2026-10-27T20:48:00Z") + minute * 60_000).toISOString();
const call = (tool: string, subject: string, result: string) => ({ kind: "call", tool, subject, state: "done", result });
const TALK = { messages: [
  { id: "p0", seq: 0, at: at(0), kind: "prompt", source: "overlord", text: PASTE },
  { id: "r1", seq: 1, at: at(1), kind: "reply", blocks: [
    { kind: "thinking", text: THOUGHT },
    call("PowerShell", COMMAND, OUTPUT),
    call("Read", "src/touch.ts", "1\texport const swipe = true;"),
    { kind: "text", text: `The state lives at ${PATH} and the run is at ${URL}.\n\nTo publish:\n\n\`\`\`bash\n${CODE}\n\`\`\`` },
  ] },
  { id: "p2", seq: 2, at: at(2), kind: "prompt", source: "overlord", text: "Working?" },
  { id: "r3", seq: 3, at: at(3), kind: "reply", blocks: [{ kind: "text", text: "Yes, the board is healthy." }] },
], more: false };

const log = (page: Page) => page.getByRole("log");
const row = (page: Page, id: string) => log(page).locator(`[data-message="${id}"]`);
const toolRow = (page: Page, tool: string) => row(page, "r1").locator(".disclosure.message-call").filter({ hasText: tool });
const height = async (locator: Locator) => (await locator.boundingBox())!.height;

async function openTalk(page: Page, width: number) {
  await page.setViewportSize({ width, height: 852 });
  await open(page, snapshot({}, { phase: "settled" }), "", TALK);
  await openGoblin(page);
  await expect(row(page, "r3")).toBeVisible();
}

for (const width of [360, 393, 430, 1440]) {
  test(`at ${width} px a tool row with a long command is one line, and a tap opens it to the whole command and its output`, async ({ page }) => {
    // Arrange
    await openTalk(page, width);
    const command = toolRow(page, "Get-ChildItem");
    const summary = command.locator("summary");

    // Assert: as tall as a short row, its command cut on that one line.
    expect(Math.abs(await height(summary) - await height(toolRow(page, "src/touch.ts").locator("summary"))), "as tall as a short row").toBeLessThanOrEqual(1);
    await expect(summary.locator(".call-subject")).toHaveCSS("text-overflow", "ellipsis");
    expect(await summary.locator(".call-subject").evaluate((element) => element.scrollWidth > element.clientWidth), "the command is cut").toBe(true);
    await expect(command.locator(".call-result")).toHaveCount(0);

    // Act
    await summary.click();

    // Assert: the whole command in its card, then what it printed.
    const card = command.locator(".message-code").first();
    await expect(card.locator("figcaption")).toContainText("powershell");
    await expect(card.locator("pre")).toHaveText(COMMAND);
    await expect(command.locator(".call-result")).toHaveText(OUTPUT);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
  });
}

test("Enter on a tool row opens it and Enter again folds it", async ({ page }) => {
  // Arrange
  await openTalk(page, 393);
  const command = toolRow(page, "Get-ChildItem");

  // Act
  await command.locator("summary").focus();
  await page.keyboard.press("Enter");

  // Assert
  await expect(command.locator(".message-code pre").first()).toHaveText(COMMAND);

  // Act
  await page.keyboard.press("Enter");

  // Assert
  await expect(command.locator(".message-code")).toHaveCount(0);
});

for (const width of [360, 393, 430]) {
  test(`at ${width} px tool output, a long message of his and thinking start folded and open on tap`, async ({ page }) => {
    // Arrange
    await openTalk(page, width);
    const bubble = row(page, "p0").locator(".message-bubble");
    const more = bubble.getByRole("button", { name: "Show more" });
    const thinking = row(page, "r1").locator(".message-thinking");

    // Assert: each folded.
    await expect(log(page).locator(".call-result")).toHaveCount(0);
    await expect(thinking.locator(".disclosure-content")).toHaveCount(0);
    expect(await height(thinking.locator("summary")), "thinking is one line").toBeLessThan(40);
    await expect(bubble.locator(".long-text")).toHaveClass(/folded/);
    await expect(more).toHaveAttribute("aria-expanded", "false");
    await expect(row(page, "p2").getByRole("button", { name: "Show more" }), "a short message is as it was").toHaveCount(0);
    const folded = await height(bubble);

    // Act
    await toolRow(page, "src/touch.ts").locator("summary").click();
    await thinking.locator("summary").click();
    await more.click();

    // Assert: each open, and Show more opened the message, not its row.
    await expect(toolRow(page, "src/touch.ts").locator(".call-result")).toHaveText("1\texport const swipe = true;");
    await expect(thinking.locator(".message-words")).toContainText("The heartbeat file is fresh");
    await expect(bubble.locator(".long-text")).not.toHaveClass(/folded/);
    await expect(bubble.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");
    await expect(bubble).toContainText("Line 14 of the log he pasted");
    expect(await height(bubble), "the message opened").toBeGreaterThan(folded * 2);
    await expect(row(page, "p0")).not.toHaveClass(/actions-open/);

    // Act
    await bubble.getByRole("button", { name: "Show less" }).click();

    // Assert
    await expect(bubble.locator(".long-text")).toHaveClass(/folded/);
  });
}

test("a long message he opened stays open after its row leaves the window and comes back", async ({ page }) => {
  // Arrange: a conversation long enough that his first message leaves the
  // page as he reads at its end.
  const filler = Array.from({ length: 200 }, (_, seq) => ({ id: "f" + seq, seq: seq + 1, at: at(seq + 1), kind: "reply", blocks: [{ kind: "text", text: "Answer " + seq }] }));
  await page.setViewportSize({ width: 393, height: 852 });
  await open(page, snapshot({}, { phase: "settled" }), "", { messages: [{ id: "p0", seq: 0, at: at(0), kind: "prompt", source: "overlord", text: PASTE }, ...filler], more: false });
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 199");
  await log(page).evaluate((element) => { element.scrollTop = 0; });
  await row(page, "p0").getByRole("button", { name: "Show more" }).click();
  await expect(row(page, "p0").locator(".long-text")).not.toHaveClass(/folded/);

  // Act: read at the end, so the row leaves the page, then come back.
  await log(page).evaluate((element) => { element.scrollTop = element.scrollHeight; });
  await expect(row(page, "p0")).toHaveCount(0);
  await expect.poll(() => log(page).evaluate((element) => { element.scrollTop = 0; return element.querySelector('[data-message="p0"]') !== null; })).toBe(true);

  // Assert
  await expect(row(page, "p0").locator(".long-text")).not.toHaveClass(/folded/);
  await expect(row(page, "p0").getByRole("button", { name: "Show less" })).toBeVisible();
});

for (const width of [360, 393, 430]) {
  test(`at ${width} px a code card scrolls its long line inside itself, and Expand opens it whole`, async ({ page }) => {
    // Arrange
    await openTalk(page, width);
    const card = row(page, "r1").locator(".message-words .message-code");
    const text = card.locator("pre");

    // Assert: the line scrolls inside the card, which keeps the column's width.
    await expect(card.locator("figcaption")).toContainText("bash");
    await expect(card.getByRole("button", { name: "Copy" })).toBeVisible();
    await expect(text).toHaveCSS("white-space", "pre");
    expect(await text.evaluate((element) => element.scrollWidth > element.clientWidth), "the long line scrolls").toBe(true);
    expect((await card.boundingBox())!.width, "the card fits the column").toBeLessThanOrEqual((await row(page, "r1").boundingBox())!.width + 1);

    // Act
    await text.evaluate((element) => { element.scrollLeft = element.scrollWidth; });
    await card.getByRole("button", { name: "Expand" }).click();

    // Assert: whole, with nothing left to scroll.
    await expect(card.getByRole("button", { name: "Collapse" })).toHaveAttribute("aria-expanded", "true");
    expect(await text.evaluate((element) => element.scrollWidth <= element.clientWidth + 1), "every line wraps").toBe(true);
    await expect(text).toHaveText(CODE);
  });
}

for (const width of [360, 393, 430]) {
  test(`at ${width} px a long path, URL and command never make the conversation wider than the screen`, async ({ page }) => {
    // Arrange
    await openTalk(page, width);

    // Act: open everything that opens.
    await toolRow(page, "Get-ChildItem").locator("summary").click();
    await row(page, "p0").getByRole("button", { name: "Show more" }).click();
    await row(page, "r1").locator(".message-thinking summary").click();

    // Assert
    await expect(toolRow(page, "Get-ChildItem").locator(".call-result")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
    const widths = await log(page).evaluate((element) => ({
      list: element.clientWidth, overflow: element.scrollWidth - element.clientWidth,
      wider: [...element.querySelectorAll("[data-message]")].filter((message) => message.getBoundingClientRect().right > element.getBoundingClientRect().right + 1 || message.scrollWidth > message.clientWidth + 1).map((message) => message.getAttribute("data-message")),
    }));
    expect(widths.overflow, "the conversation does not scroll sideways").toBeLessThanOrEqual(0);
    expect(widths.wider, "no message is wider than the conversation").toEqual([]);
  });
}

test("the 01:52Z conversation at 393 px", async ({ page }, testInfo) => {
  // Arrange
  await openTalk(page, 393);

  const picture = async (name: string) => {
    await page.screenshot({ path: testInfo.outputPath(name + ".png") });
    await testInfo.attach(name, { path: testInfo.outputPath(name + ".png"), contentType: "image/png" });
  };

  // Act: his message at the top of the screen, the reply under it.
  await row(page, "p0").evaluate((element) => element.scrollIntoView({ block: "start" }));

  // Assert: the pictures, for the report, folded and with the row opened.
  await expect(toolRow(page, "Get-ChildItem")).toBeInViewport();
  await picture("phone-text-393");
  await toolRow(page, "Get-ChildItem").locator("summary").click();
  await toolRow(page, "Get-ChildItem").evaluate((element) => element.scrollIntoView({ block: "start" }));
  await expect(toolRow(page, "Get-ChildItem").locator(".call-result")).toBeInViewport();
  await picture("phone-text-393-opened");
});
