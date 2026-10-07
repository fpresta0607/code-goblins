import { doneCards, expect, openItem, recordDoneCards, test } from "./site";

// The Overlord, 2026-09-29: "You can't close a command card if you answered
// it". He answered the CFO's question in chat and its card stayed under
// Waiting on you. The CFO's record of that answer finishes the open card as
// his answer in chat, and he can dismiss a question he answered elsewhere or
// no longer needs from the card himself.
test("a question he answered in chat finishes its open card, recorded by the CFO, and moves on", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/answer-elsewhere.html");
  const dialog = page.getByRole("dialog");
  await openItem(page, "May I stop the 4 stray Herdr panes left from yesterday?");
  await expect(dialog.getByText("May I stop the 4 stray Herdr panes left from yesterday?")).toBeVisible();

  await recordDoneCards(page);

  // Act
  await page.evaluate(() => window.recordInChat?.());

  // Assert
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Answered/));
  for (const answered of (await doneCards(page)).filter((text) => text.startsWith("Answered"))) expect(answered).toContain("You answered in chat · recorded by the CFO");
  await expect(dialog.getByText("May I build the layout switch as drawn?")).toBeVisible();
});

test("he dismisses a question he no longer needs from its card", async ({ page }) => {
  // Arrange
  const posted: Record<string, unknown>[] = [];
  await page.route("**/api/actions", async (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    posted.push(body);
    await route.fulfill({ status: 202, json: { id: body.id, kind: body.kind, status: "queued", question_id: body.question_id, generation: body.generation } });
  });
  await page.goto("/tests/fixtures/answer-elsewhere.html");
  const dialog = page.getByRole("dialog");
  await openItem(page, "May I stop the 4 stray Herdr panes left from yesterday?");
  const dismiss = dialog.getByRole("button", { name: "Dismiss this question" });
  await expect(dismiss).toHaveAttribute("data-tip", "Dismiss: answered elsewhere or no longer needed");
  await recordDoneCards(page);

  // Act
  await dismiss.click();

  // Assert
  await expect.poll(() => posted.map((body) => [body.kind, body.question_id, body.generation])).toEqual([["question_clear", "herdr-strays-20260929", "c".repeat(64)]]);
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Dismissed/));
  await expect(dialog.getByText("May I build the layout switch as drawn?")).toBeVisible();
});
