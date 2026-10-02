import { expect, test, type Page } from "@playwright/test";

// The Overlord, 2026-10-02, on a goblin's waiting card whose command sat in a
// paragraph: "this should be copy and paste?", then "sorry not copy and paste
// but run in powershell button". A goblin's command is a run card named for
// the goblin: the exact command, a copy button as the fallback, and one button
// that runs it in a window he can use.
const GOBLIN = "cg-goblins-quickstart";
const WHY = "Sign in to GitHub so I can push the branch";

async function openItsCard(page: Page) {
  await page.goto("/tests/fixtures/goblin-run.html");
  await page.getByLabel("Command Center, 1 waiting on you").click();
  const row = page.locator(".inbox-list li").first();
  await expect(row).toContainText(GOBLIN);
  await expect(row).toContainText(WHY);
  await row.getByRole("button").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  return dialog;
}

test("a goblin's command is a run card named for the goblin, run with one click", async ({ page }) => {
  // Arrange
  let sent: Record<string, unknown> | null = null;
  await page.route("**/api/actions", async (route) => {
    sent = route.request().postDataJSON();
    await route.fulfill({ json: { id: String(sent?.id), kind: "run", run_id: "run-cg-goblins-quickstart-12", status: "queued" } });
  });

  // Act
  const dialog = await openItsCard(page);

  // Assert: who asks, why, the exact command with its copy button, one button.
  await expect(dialog.locator(".asker")).toContainText(GOBLIN + " asks you to run this");
  await expect(dialog.getByRole("heading", { name: WHY })).toBeVisible();
  await expect(dialog.locator(".run-command code")).toHaveText("gh auth login --web");
  await expect(dialog.getByRole("button", { name: "Copy command" })).toBeVisible();
  const run = dialog.getByRole("button", { name: "Run in PowerShell" });
  await expect(run).toBeEnabled();

  // Act
  await run.click();

  // Assert: the click names the stored item, never a command.
  await expect.poll(() => sent).not.toBeNull();
  expect(sent).toMatchObject({ kind: "run", run_id: "run-cg-goblins-quickstart-12" });
  expect(sent).not.toHaveProperty("command");
});

test("a command running in its own window shows its state, never an empty output box", async ({ page }) => {
  // Arrange
  const dialog = await openItsCard(page);

  // Act
  await page.evaluate(() => window.runStep("running"));

  // Assert
  await expect(dialog.getByRole("status")).toContainText("Running");
  await expect(dialog.locator(".run-terminal")).toHaveCount(0);
  await expect(dialog.getByText("It runs in its own window")).toBeVisible();

  // Act
  await page.evaluate(() => window.runStep("finished"));

  // Assert
  await expect(dialog.getByRole("status")).toContainText("Finished · exit 0");
  await expect(dialog.locator(".run-terminal")).toHaveCount(0);
});
