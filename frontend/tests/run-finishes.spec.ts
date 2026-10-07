import { expect, openItem, test, type Page } from "./site";

// The Overlord, 2026-10-07: the CFO's command publish-v0.5.2 finished with
// exit 0 and its card stayed in the Command Center's stack instead of moving
// to History, as every other item that closes on screen does. A command that
// ends cleanly, and one the CFO withdraws, finish like any card: the check,
// then the next item, and History holds how it ended. A command that failed
// keeps its card, with its output, since that is trouble he may need to read.
// How a command ended is its exit code: output that ends in an error line
// with exit 0 is a clean end.
const RUN = "Publish release v0.5.2";
const QUESTION = "May I merge the release train now?";

async function runIt(page: Page) {
  await page.route("**/api/actions", async (route) => {
    const sent: Record<string, unknown> = route.request().postDataJSON();
    await route.fulfill({ json: { id: String(sent.id), kind: "run", run_id: "publish-v0.5.2", status: "queued" } });
  });
  await page.goto("/tests/fixtures/run-finishes.html");
  await page.waitForFunction(() => "runEnds" in window);
  await openItem(page, RUN);
  const dialog = page.locator("dialog.question-modal");
  await expect(dialog.getByRole("heading", { name: RUN })).toBeVisible();
  await dialog.getByRole("button", { name: "Run in PowerShell" }).click();
  await step(page, "running");
  await expect(dialog.getByRole("status")).toContainText("Running");
  return dialog;
}
async function step(page: Page, name: "running" | "finished" | "errorLine" | "failed" | "withdrawn") {
  await page.evaluate((to) => (window as unknown as { runEnds: (name: string) => void }).runEnds(to), name);
  await expect(page.locator("main")).toHaveAttribute("data-step", name);
}
async function history(page: Page) {
  await page.locator(".command-center-menu > summary").click();
  const disclosure = page.locator(".inbox-history");
  await disclosure.locator("summary").click();
  return disclosure.locator(".inbox-list li");
}

for (const [outcome, says] of [["finished", "Finished · exit 0"], ["errorLine", "Finished · exit 0"], ["withdrawn", "Withdrawn by the CFO"]] as const) {
  test(`a command that ends without trouble (${outcome}) finishes its card, the next item follows, and History says how it ended`, async ({ page }, testInfo) => {
    // Arrange
    const dialog = await runIt(page);

    // Act
    await step(page, outcome);

    // Assert
    await expect(dialog.locator(".done-card")).toContainText(says);
    await expect(dialog.getByRole("heading", { name: RUN })).toHaveCount(0);
    await expect(dialog).toContainText(QUESTION);
    await testInfo.attach("the next item after the command ended", { body: await page.screenshot(), contentType: "image/png" });
    await dialog.getByRole("button", { name: "Close the Command Center" }).click();
    await expect(dialog).toBeHidden();
    await expect((await history(page)).filter({ hasText: RUN })).toContainText(says);
  });
}

test("a command that failed keeps its card on screen with its output, and leaves the stack once he moves on", async ({ page }) => {
  // Arrange
  const dialog = await runIt(page);

  // Act
  await step(page, "failed");
  await page.waitForTimeout(1500);

  // Assert
  await expect(dialog.getByRole("heading", { name: RUN })).toBeVisible();
  await expect(dialog.getByRole("status")).toContainText("Failed · exit 2");
  await expect(dialog).toContainText("gh: release v0.5.2 already exists");
  await dialog.getByRole("button", { name: "Close the Command Center" }).click();
  await expect((await history(page)).filter({ hasText: RUN })).toContainText("Failed · exit 2");
  await openItem(page, QUESTION);
  await expect(dialog).toContainText(QUESTION);
  await expect(dialog.getByRole("group", { name: "Move between items" })).toHaveCount(0);
});
