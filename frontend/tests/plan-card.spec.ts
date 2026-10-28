import { expect, test } from "./site";
import { at, open, openGoblin, snapshot } from "./session-board";

// A plan a call wrote, Claude Code's todo list or Codex's plan, shows in the
// reply as a checklist card, as the Claude desktop app shows one, not as one
// more tool row. The supervisor is played by tests/session-board.ts and
// reads the plan from the call (internal/fleettree/messages.go); every
// conversation is made up.

const PLAN = [
  { text: "Read the touch handlers", status: "completed" },
  { text: "Hand the swipe to the history", status: "in_progress" },
  { text: "Run the phone spec", status: "pending" },
];
const talk = (tool: string) => ({ messages: [
  { id: "m0", seq: 0, at, kind: "prompt", source: "overlord", text: "Fix the swipe and check the phone spec." },
  { id: "m1", seq: 1, at, kind: "reply", blocks: [
    { kind: "text", text: "Here is the plan." },
    { kind: "call", tool, subject: "", state: "done", plan: PLAN },
    { kind: "call", tool: "Read", subject: "frontend/src/terminalView.ts", state: "done" },
  ] },
], more: false });

for (const [harness, tool] of [["claude", "TodoWrite"], ["codex", "update_plan"]] as const) {
  test(`a ${harness} plan shows as a checklist card with each step's state`, async ({ page }) => {
    // Arrange
    await open(page, snapshot({}, { harness }), "", talk(tool));

    // Act
    await openGoblin(page);

    // Assert: one card, its steps in order with their states, and the next
    // call still a quiet row of its own.
    const plan = page.getByRole("region", { name: "Plan", exact: true });
    await expect(plan).toHaveCount(1);
    await expect(plan).toContainText("1 of 3 done");
    await expect(plan.getByRole("listitem")).toHaveText(["Read the touch handlersDone", "Hand the swipe to the historyIn progress", "Run the phone specTo do"]);
    await expect(plan.locator(".plan-step.in_progress .plan-text")).toHaveCSS("font-weight", "600");
    await expect(plan.locator(".plan-step.completed .plan-text")).toHaveCSS("text-decoration-line", "line-through");
    await expect(page.getByText("frontend/src/terminalView.ts", { exact: true })).toBeVisible();
  });
}

test("a plan call that failed is its row with Failed, not a card", async ({ page }) => {
  // Arrange: the supervisor serves a failed TodoWrite with no plan.
  await open(page, snapshot({}, { harness: "claude" }), "", { messages: [
    { id: "m0", seq: 0, at, kind: "prompt", source: "overlord", text: "Fix the swipe." },
    { id: "m1", seq: 1, at, kind: "reply", blocks: [
      { kind: "call", tool: "TodoWrite", subject: "", state: "failed", result: "InputValidationError: The required parameter `todos[0].activeForm` is missing" },
    ] },
  ], more: false });

  // Act
  await openGoblin(page);

  // Assert
  await expect(page.getByRole("region", { name: "Plan", exact: true })).toHaveCount(0);
  await expect(page.locator(".message-call").filter({ hasText: "Updated the plan" }).locator(".call-end")).toHaveText("Failed");
});
