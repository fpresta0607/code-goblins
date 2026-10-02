import { expect, test, type Page } from "@playwright/test";

// The Overlord, 2026-10-01, on answers to the CFO that had all arrived:
// "Delivery unconfirmed. Inspect the CFO queue before sending again: I get
// these command center blips and errors, fix". An answer typed for a CFO
// inside a turn is sent, then delivered, and never a warning. One that truly
// never arrives never brings its card back either ("no double fire or
// display"): its line in History says what to do in plain words.
async function answerTheCFO(page: Page): Promise<string> {
  let answer = "";
  await page.route("**/api/actions", async (route) => {
    answer = route.request().postDataJSON().id;
    await route.fulfill({ json: { id: answer, kind: "cfo_answer", question_id: "freeze-lift", status: "queued" } });
  });
  await page.goto("/tests/fixtures/answer-delivery.html");
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("radio", { name: /Lift it/ }).check();
  await dialog.getByRole("button", { name: "Send decision" }).click();
  await expect.poll(() => answer).not.toBe("");
  return answer;
}

async function history(page: Page) {
  await page.locator(".command-center-menu > summary").click();
  const list = page.locator(".inbox-history");
  if (!(await list.locator(".disclosure-content").count())) await list.locator("summary").click();
  return list;
}

test("an answer typed for a busy CFO reads sent, then delivered, and never warns", async ({ page }) => {
  // Arrange
  const answer = await answerTheCFO(page);
  const dialog = page.getByRole("dialog");

  // Act
  await page.evaluate((id) => window.settle?.("busy", id), answer);

  // Assert: his card finished and stays gone, with no warning anywhere.
  await expect(dialog).toBeHidden({ timeout: 10_000 });
  await page.waitForTimeout(1500);
  await expect(dialog).toBeHidden();
  await expect(page.getByText(/unconfirmed|Inspect the CFO queue|Not confirmed/)).toHaveCount(0);
  const list = await history(page);
  await expect(list).toContainText("You chose Lift it (not yet delivered to the CFO)");
  await expect(list.locator(".delivery.queued")).toHaveCount(1);

  // Act
  await page.evaluate((id) => window.settle?.("delivered", id), answer);

  // Assert
  await expect(list).toContainText("You chose Lift it");
  await expect(list).not.toContainText("not yet delivered");
  await expect(list.locator(".delivery.succeeded")).toHaveCount(1);
});

test("an answer the CFO never picks up keeps its card closed, and History says what to do", async ({ page }) => {
  // Arrange
  const answer = await answerTheCFO(page);
  const dialog = page.getByRole("dialog");
  await page.evaluate((id) => window.settle?.("busy", id), answer);
  await expect(dialog).toBeHidden({ timeout: 10_000 });

  // Act
  await page.evaluate((id) => window.settle?.("lost", id), answer);

  // Assert: the Command Center stays closed and nothing waits on him again.
  await page.waitForTimeout(1500);
  await expect(dialog).toBeHidden();
  await expect(page.getByLabel(/^Command Center$/)).toBeVisible();
  const list = await history(page);
  await expect(list).toContainText("Open its terminal and press Enter if your answer is waiting in its box");
  await expect(list.locator(".delivery.uncertain")).toHaveCount(1);
  await expect(page.getByText(/Inspect the CFO queue/)).toHaveCount(0);
});
