import { CFO_DOOR, expect, test } from "./site";
import { open, snapshot } from "./session-board";
import { COMMANDS, CONVERSATION } from "./folded-calls-fixture";

for (const width of [390, 1440]) {
  test(`a reply's five finished commands fold into one Ran 5 commands row at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1100 });
    await open(page, snapshot(), undefined, CONVERSATION);
    await page.getByRole("button", { name: CFO_DOOR }).first().click();
    const messages = page.getByRole("log", { name: "Messages of CFO" });
    await expect(messages.getByText("One test fails.")).toBeVisible();

    const five = messages.locator("details.message-call-group", { hasText: "Ran 5 commands" });
    await expect(five).toHaveCount(1);
    await expect(five.locator(".disclosure-content")).toHaveCount(0);
    await expect(messages.getByText("Checking the branch before the push.")).toBeVisible();
    await expect(messages.getByText("All five checks pass.")).toBeVisible();
    await expect(messages.locator("details.message-call-group", { hasText: "Read 2 files, ran 3 commands" })).toHaveCount(1);
    const running = messages.locator(".message-steps > details.message-call:not(.message-call-group)", { hasText: "npm run build" });
    await expect(running).toHaveCount(1);
    await expect(running.locator("summary")).toContainText("Running");
    await expect(messages.locator("details.message-call-group", { hasText: "Ran 2 commands" })).toHaveCount(2);
    await expect(messages.locator(".message-steps > details.message-call:not(.message-call-group)", { hasText: "npm test" }).locator("summary")).toContainText("Failed");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`after-${width}.png`) });

    await five.locator("> summary").click();
    const rows = five.locator(".disclosure-content details.message-call");
    await expect(rows).toHaveCount(COMMANDS.length);
    for (const [index, command] of COMMANDS.entries()) await expect(rows.nth(index).locator("summary")).toContainText(command);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`after-open-${width}.png`) });

    await five.locator("> summary").focus();
    await page.keyboard.press("Enter");
    await expect(five.locator(".disclosure-content")).toHaveCount(0);
  });
}

test("a folded run stays open as the running call beside it finishes and joins it", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  await open(page, snapshot(), undefined, CONVERSATION);
  await page.getByRole("button", { name: CFO_DOOR }).first().click();
  const messages = page.getByRole("log", { name: "Messages of CFO" });
  const fold = messages.locator("details.message-call-group").filter({ hasText: "Read 2 files" });
  await fold.locator("> summary").click();
  await expect(fold.locator(".disclosure-content details.message-call")).toHaveCount(5);

  const reply = CONVERSATION.messages[2] as { blocks: { state?: string; result?: string }[] };
  await page.evaluate((reply) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [reply] } } })), {
    ...reply, blocks: reply.blocks.map((block) => block.state === "running" ? { ...block, state: "done", result: "built" } : block),
  });

  const joined = messages.locator("details.message-call-group", { hasText: "Read 2 files, ran 4 commands" });
  await expect(joined).toHaveCount(1);
  await expect(joined.locator(".disclosure-content details.message-call")).toHaveCount(6);
  await expect(messages.locator(".message-steps > details.message-call:not(.message-call-group)", { hasText: "npm run build" })).toHaveCount(0);
});
