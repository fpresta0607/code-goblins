import { expect, test, type Page, type Route } from "./site";

// The Overlord, 2026-10-12 about 11:55Z: "when I have a queue and I go back to
// a queue message, I should be able to see the answer that I [chose]. To then
// either select a new answer, not to show the queue complete screen with no
// option". Going back in the Command Center's stack to a question he answered
// shows it with his answer checked and its other choices open, and choosing
// another sends the change once. Nothing he goes back to is a done card with
// nothing to pick. The very last screen, You're all done, offers no Back:
// "in the completed very last screen of command center we dont need to see a
// back button" (2026-10-12 about 14:00Z).
test.use({ locale: "en-US", timezoneId: "America/Chicago" });

const NOW = new Date("2026-10-12T12:00:00Z");
const STRIPE = "afk-left-20261011T042116.474143200Z";

// actions records what the board posts, and the supervisor queues each.
async function actions(page: Page) {
  const posted: Record<string, unknown>[] = [];
  await page.route("**/api/actions", async (route: Route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    posted.push(body);
    await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
  });
  return posted;
}

// goThrough opens the Command Center on everything waiting, as Go through them
// does, with the page's clock held still so each card moves on only when the
// test lets time pass.
async function goThrough(page: Page) {
  await page.clock.install({ time: NOW });
  await page.clock.pauseAt(new Date(NOW.getTime() + 60_000));
  await page.goto("/tests/fixtures/queue-back.html");
  await page.locator(".command-center-menu > summary").click();
  await page.locator(".command-center-updates").getByRole("button", { name: "Go through them" }).click();
  return page.getByRole("dialog");
}

// answer sends choice from the card on screen and lets its check show long
// enough for the next card to follow.
async function answer(page: Page, choice: string) {
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("radio", { name: choice }).check();
  await dialog.getByRole("button", { name: "Send decision" }).click();
  await page.clock.runFor(1000);
}

const kinds = (posted: Record<string, unknown>[]) => posted.map((body) => [body.kind, body.question_id, body.text]);

for (const size of [{ name: "his window", viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 }, { name: "a phone", viewport: { width: 390, height: 844 }, deviceScaleFactor: 3 }]) {
  test.describe(`at ${size.name}'s size`, () => {
    test.use({ viewport: size.viewport, deviceScaleFactor: size.deviceScaleFactor });

    test("going back to a question he answered shows his answer checked with the other choices open, never a done card", async ({ page }, testInfo) => {
      // Arrange: he answers the first, and the board has not heard back yet.
      const posted = await actions(page);
      const dialog = await goThrough(page);
      await answer(page, "Switch them off now");
      await expect(dialog.locator(".stack-count")).toHaveText("2 of 3");

      // Act
      await dialog.getByRole("button", { name: "Back" }).click();
      await page.clock.runFor(1000);
      await dialog.locator(".card-stage").screenshot({ path: testInfo.outputPath(`back-${size.viewport.width}.png`) });

      // Assert
      await expect(dialog.locator(".stack-count")).toHaveText("1 of 3");
      await expect(dialog.locator(".done-card")).toHaveCount(0);
      await expect(dialog.locator(".question-body")).toContainText("Switch off the 136 live Stripe pilot links?");
      await expect(dialog.getByRole("radio", { name: "Switch them off now" })).toBeChecked();
      await expect(dialog.getByRole("radio", { name: "Leave them on" })).toBeEnabled();
      await expect(dialog.getByRole("radio", { name: "Leave them on" })).not.toBeChecked();
      await expect(dialog.locator(".answered-by")).toHaveText(/^You chose this/);
      await expect(dialog.getByRole("button", { name: "Change answer" })).toBeDisabled();
      await expect(dialog.getByRole("button", { name: "Skip" })).toBeEnabled();
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
      expect(kinds(posted)).toEqual([["cfo_answer", STRIPE, "Switch them off now"]]);
    });
  });
}

test("choosing another answer there sends the change once, and Skip goes on to what still waits", async ({ page }) => {
  // Arrange
  const posted = await actions(page);
  const dialog = await goThrough(page);
  await answer(page, "Switch them off now");
  await page.evaluate((id) => window.answerLands?.(id, "afk-left-20261011T042116.474143200Z", "Switch them off now"), String(posted[0].id));
  await dialog.getByRole("button", { name: "Back" }).click();
  await dialog.getByRole("radio", { name: "Leave them on" }).check();

  // Act
  await dialog.getByRole("button", { name: "Change answer" }).dblclick();
  await page.clock.runFor(2000);

  // Assert
  await expect(dialog.getByRole("radio", { name: "Leave them on" })).toBeChecked();
  await expect(dialog.locator(".answered-by")).toHaveText(/^You chose this/);
  await expect(dialog.getByRole("button", { name: "Change answer" })).toBeDisabled();
  expect(kinds(posted)).toEqual([["cfo_answer", STRIPE, "Switch them off now"], ["answer_change", STRIPE, "Leave them on"]]);
  expect(posted[1]).toMatchObject({ generation: "f".repeat(64), answer_kind: "option" });
  await dialog.getByRole("button", { name: "Skip" }).click();
  await expect(dialog.locator(".stack-count")).toHaveText("2 of 3");
  await expect(dialog.getByRole("button", { name: "Send decision" })).toBeVisible();
});

test("You're all done after the last answer shows no Back, and the Command Center closes by itself", async ({ page }, testInfo) => {
  // Arrange
  const posted = await actions(page);
  const dialog = await goThrough(page);
  await answer(page, "Switch them off now");
  await answer(page, "Narrow it now");

  // Act
  await answer(page, "Push the tag");
  await expect(dialog.locator(".done-card")).toContainText("You're all done");
  await dialog.locator(".card-stage").screenshot({ path: testInfo.outputPath("all-done.png") });
  const buttons = await dialog.locator(".done-card").getByRole("button").count();
  await page.clock.runFor(3000);

  // Assert
  expect(buttons).toBe(0);
  await expect(dialog).toBeHidden();
  expect(kinds(posted).map(([kind]) => kind)).toEqual(["cfo_answer", "cfo_answer", "cfo_answer"]);
});
