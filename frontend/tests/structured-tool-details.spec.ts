import { CFO_DOOR, expect, test } from "./site";
import { at, open, snapshot } from "./session-board";

for (const width of [390, 1440]) {
  test(`CEO tool details preserve input, output and streaming identity at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    const prose = "The expression text(await ...) is JavaScript.";
    const input = 'text(await tools.exec_command({cmd:"git status"}));\ntext("second line");';
    const call = { kind: "call", call_id: "call-1", tool: "exec", subject: "exec", input, state: "running" };
    const reply = { id: "reply-1", seq: 1, at, kind: "reply", blocks: [call, { kind: "text", text: prose }] };
    await open(page, snapshot({ cfo_harness: "codex" }), undefined, { conversation: "fixture-conversation", more: false, messages: [{ id: "user-1", seq: 0, at, kind: "prompt", source: "overlord", text: prose }, reply] });
    await page.getByRole("button", { name: CFO_DOOR }).first().click();
    const messages = page.getByRole("log", { name: "Messages of CFO" });
    const detail = messages.locator("details.message-call");
    await expect(detail).toHaveCount(1);
    await expect(detail.locator("summary")).not.toContainText("text(await");
    await expect(detail.locator(".disclosure-content")).toHaveCount(0);
    await expect(messages.getByText(prose, { exact: true })).toHaveCount(2);

    await page.evaluate((reply) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [reply] } } })), {
      ...reply, blocks: [{ ...call, state: "failed", result: "Script failed\nError: command failed" }, { kind: "text", text: prose }],
    });

    await expect(detail).toHaveCount(1);
    await expect(detail.locator("summary")).toContainText("Failed");
    await detail.locator("summary").focus();
    await page.keyboard.press("Enter");
    await expect(detail.locator(".disclosure-content")).toContainText(input);
    await expect(detail.locator(".disclosure-content")).toContainText("Error: command failed");
    await expect(detail.locator("button button, summary button")).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}
