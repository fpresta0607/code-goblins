import { expect, test, type Page } from "./site";

// The Overlord approved both designs on 2026-10-06 ("i trust you i like it"):
// History tells at a glance who answered each question, the crown for him,
// the CFO's face for the CFO and the CFO's face with a moon while he was
// away, with when and why; and his choice wins, from History's pencil on an
// answer the CFO gave while the goblin has reported nothing since, or from
// his own card when his send crossed the CFO's answer.
test.use({ locale: "en-US", timezoneId: "America/Chicago" });

const NOW = new Date("2026-10-06T02:20:00Z");

async function history(page: Page) {
  await page.clock.install({ time: NOW });
  await page.goto("/tests/fixtures/answer-history.html");
  // cg-board-theme's waiting question opens the Command Center by itself.
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Ship the dark theme first?")).toBeVisible();
  await dialog.getByRole("button", { name: "Close the Command Center" }).click();
  await page.locator(".command-center-menu > summary").click();
  const panel = page.locator(".command-center-updates");
  await panel.getByText("History").click();
  return panel.locator(".inbox-history li");
}

async function actions(page: Page) {
  const posted: Record<string, unknown>[] = [];
  await page.route("**/api/actions", async (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    posted.push(body);
    if (body.kind === "goblin_answer") return route.fulfill({ status: 409, json: { error: "an answer is already recorded for this question; inspect its outcome" } });
    await route.fulfill({ status: 202, json: { id: body.id, kind: body.kind, status: "queued", question_id: body.question_id, generation: body.generation, text: body.text, answer_kind: body.answer_kind } });
  });
  return posted;
}

for (const width of [1440, 390]) {
  test(`History says who answered, when and why, and offers a change only where it can land, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 1000 });

    // Act
    const rows = await history(page);

    // Assert
    await expect(rows).toHaveCount(4);
    const [gate, accent, stray, older] = [rows.nth(0), rows.nth(1), rows.nth(2), rows.nth(3)];
    await expect(gate.getByRole("img", { name: "The CFO answered", exact: true })).toBeVisible();
    await expect(gate).toContainText("The CFO chose Wait for the running test step to end");
    await expect(gate.locator(".answer-reason")).toHaveText("Reason: the running test step ends in 4 minutes");
    await expect(gate.locator("time")).toHaveText("2m ago");
    await expect(accent.getByRole("img", { name: "You answered" })).toBeVisible();
    await expect(accent.locator("time")).toHaveText("12m ago");
    await expect(stray.getByRole("img", { name: "The CFO answered while you were away" })).toBeVisible();
    await expect(older.getByRole("img", { name: "The CFO answered", exact: true })).toBeVisible();
    await expect(rows.getByRole("button", { name: /^Change the CFO's answer/ })).toHaveCount(1);
    await expect(gate.getByRole("button", { name: "Change the CFO's answer to cg-verify-fast" })).toHaveAttribute("data-tip", "Change the CFO's answer");
    const overflow = await page.locator(".command-center-updates").evaluate((element) => element.scrollWidth - element.clientWidth);
    expect(overflow).toBe(0);
    await page.locator(".command-center-updates").screenshot({ path: testInfo.outputPath(`history-${width}.png`) });
  });
}

test("he changes the CFO's answer from History, and the goblin is told it is his", async ({ page }, testInfo) => {
  // Arrange
  const posted = await actions(page);
  const rows = await history(page);
  await rows.nth(0).getByRole("button", { name: "Change the CFO's answer to cg-verify-fast" }).click();
  const dialog = page.getByRole("dialog");
  const change = dialog.getByRole("button", { name: "Change to my answer" });
  await expect(dialog.locator(".question-choice", { hasText: "Wait for the running test step to end" }).locator(".cfo-answer")).toHaveText("The CFO's answer");
  await expect(dialog.getByRole("status")).toHaveText("cg-verify-fast has not reported since the CFO answered, so your answer replaces the CFO's.");
  await dialog.getByRole("radio", { name: "Wait for the running test step to end" }).check();
  await expect(change).toBeDisabled();

  // Act
  await dialog.getByRole("radio", { name: "Start PR 2's gate now" }).check();
  await dialog.locator(".card-stage").screenshot({ path: testInfo.outputPath("change-card.png") });
  await change.click();
  await expect.poll(() => posted.length).toBe(1);
  await page.evaluate((id) => window.changeLands?.(id, "notify-cg-verify-fast-12", "Start PR 2's gate now"), String(posted[0].id));

  // Assert
  expect(posted.map((body) => [body.kind, body.question_id, body.generation, body.text, body.answer_kind])).toEqual([["answer_change", "notify-cg-verify-fast-12", "a".repeat(64), "Start PR 2's gate now", "option"]]);
  const done = dialog.locator(".done-card");
  await expect(done.getByRole("heading", { name: "Changed to your answer" })).toBeVisible();
  await expect(done).toContainText("cg-verify-fast is told the answer is yours: Start PR 2's gate now");
  await page.clock.runFor(2000);
  await expect(dialog).toBeHidden();
  await page.locator(".command-center-menu > summary").click();
  await expect(rows.nth(0).getByRole("img", { name: "You answered" })).toBeVisible();
  await expect(rows.nth(0).locator(".answer-reason")).toHaveText("Replaced the CFO's answer: Wait for the running test step to end");
});

for (const width of [1440, 390]) {
  test(`his send crossing the CFO's answer says so in plain words and lets him change it, never a Retry, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 1000 });
    const posted = await actions(page);
    await page.clock.install({ time: NOW });
    await page.goto("/tests/fixtures/answer-history.html");
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("radio", { name: "Ship dark first" }).check();

    // Act
    await dialog.getByRole("button", { name: "Send decision" }).click();
    await expect.poll(() => posted.filter((body) => body.kind === "goblin_answer").length).toBe(1);
    await page.evaluate(() => window.cfoAnswersFirst?.());

    // Assert
    await expect(dialog.getByRole("status")).toHaveText("The CFO answered this at the same moment: Ship both together.");
    await expect(dialog.getByRole("radio", { name: "Ship dark first" })).toBeChecked();
    await expect(dialog.locator(".question-choice", { hasText: "Ship both together" }).locator(".cfo-answer")).toHaveText("The CFO's answer");
    await expect(dialog.getByRole("button", { name: "Retry" })).toHaveCount(0);
    await expect(dialog).not.toContainText("request identity");
    expect(await dialog.evaluate((element) => element.scrollWidth - element.clientWidth)).toBe(0);
    await dialog.locator(".card-stage").screenshot({ path: testInfo.outputPath(`crossed-card-${width}.png`) });
    await dialog.getByRole("button", { name: "Change to my answer" }).click();
    await expect.poll(() => posted.filter((body) => body.kind === "answer_change").length).toBe(1);
    const changeID = String(posted.find((body) => body.kind === "answer_change")!.id);
    await page.evaluate((id) => window.changeLands?.(id, "notify-cg-board-theme-5", "Ship dark first"), changeID);
    await expect(dialog.locator(".done-card").getByRole("heading", { name: "Changed to your answer" })).toBeVisible();
  });
}

test("once the goblin reports again, the CFO's answer stands and the change is gone", async ({ page }) => {
  // Arrange
  const rows = await history(page);
  await rows.nth(0).getByRole("button", { name: "Change the CFO's answer to cg-verify-fast" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Change to my answer" })).toBeVisible();

  // Act
  await page.evaluate(() => window.goblinReports?.());

  // Assert
  await expect(dialog.getByRole("status")).toHaveText("cg-verify-fast has reported since the CFO answered, so the CFO's answer stands.");
  await expect(dialog.getByRole("button", { name: "Change to my answer" })).toHaveCount(0);
  await expect(dialog.getByRole("radio", { name: "Start PR 2's gate now" })).toBeDisabled();
});
