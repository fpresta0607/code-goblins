import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-02: "make sure updates for code goblins comes just as
// its own special overlord command". A new release reaches him as an item of
// its own in the Command Center, with its own look, one Update button, and
// its progress and result on the card; a slim banner points to it.

async function openTheItem(page: Page) {
  await page.goto("/tests/fixtures/update-item.html");
  await page.getByLabel("Command Center, 1 waiting on you").click();
  const row = page.locator(".inbox-list li").first();
  await expect(row).toHaveClass(/release-row/);
  await expect(row).toContainText("Code Goblins");
  await expect(row).toContainText("Update to v0.5.0 from v0.4.2");
  await row.getByRole("button").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.locator(".update-card")).toBeVisible();
  return dialog;
}

test("a new release is its own item: the versions, what is new, what the update checks, and one Update button", async ({ page }) => {
  // Arrange
  let sent: Record<string, unknown> | null = null;
  await page.route("**/api/actions", async (route) => {
    sent = route.request().postDataJSON();
    await route.fulfill({ json: { id: String(sent?.id), kind: "run", run_id: "update-v0.5.0-1", status: "queued" } });
  });

  // Act
  const dialog = await openTheItem(page);

  // Assert: an event, not a goblin's chore: no command, the release goblin.
  const card = dialog.locator(".update-card");
  await expect(card.getByRole("heading", { name: "Update Code Goblins" })).toBeVisible();
  await expect(card.locator(".update-versions")).toHaveAccessibleName("From v0.4.2 to v0.5.0");
  await expect(card.locator(".update-notes li")).toHaveText(["The installer sorts out every machine by itself", "AFK mode holds questions for you", "Goblins that stall wake the CFO"]);
  await expect(card.getByRole("link", { name: "What's new" })).toHaveAttribute("href", "https://github.com/fpresta0607/code-goblins/releases/tag/v0.5.0");
  await expect(card.locator(".update-trust")).toContainText("Unsigned release. Update installs it only when each file matches the release's SHA-256, such as cfo.exe 3f9a6c0e…5bc21e.");
  await expect(card.locator(".run-command")).toHaveCount(0);
  await expect(card.locator(".goblin-avatar.persona-releases")).toBeVisible();

  // Act
  await card.getByRole("button", { name: "Update" }).click();

  // Assert: the click names the stored item, never a command.
  await expect.poll(() => sent).not.toBeNull();
  expect(sent).toMatchObject({ kind: "run", run_id: "update-v0.5.0-1", generation: "u".repeat(64) });
  expect(sent).not.toHaveProperty("command");
});

test("Update follows the run step by step, and a rollback says why and offers the next try", async ({ page }) => {
  // Arrange
  await page.route("**/api/runs/update-v0.5.0-1/output", (route) => route.fulfill({ json: { state: "running",
    output: "[1/4] Download Code Goblins v0.5.0\n[2/4] Check the download\n      cfo.exe matches the release's SHA256SUMS: 3f9a\n[3/4] Install Code Goblins v0.5.0\nStopping the supervisor (pid 21116).\n" } }));
  const dialog = await openTheItem(page);
  const card = dialog.locator(".update-card");

  // Act
  await page.evaluate(() => window.updateStep("running"));

  // Assert
  await expect(card.getByRole("status")).toHaveText("Updating");
  await expect(card.locator(".update-steps li.done")).toHaveText(["Download Code Goblins v0.5.0", "Check each file's SHA-256"]);
  await expect(card.locator(".update-steps li.now")).toHaveText("Restart the board on v0.5.0");
  await expect(card.getByRole("button", { name: "Update" })).toHaveCount(0);

  // Act
  await page.evaluate(() => window.updateStep("rolledBack"));

  // Assert
  const back = dialog.locator(".update-card");
  await expect(back.getByRole("status")).toHaveText("Rolled back");
  await expect(back.locator(".update-result")).toHaveText("Code Goblins v0.4.2 serves again, and v0.5.0 was not installed; what it printed above says why.");
  await expect(back.locator(".update-steps li.failed")).toHaveText("Restart the board on v0.5.0");

  // Act
  await back.getByRole("button", { name: "Try again" }).click();

  // Assert: the board's next item for the same release, ready to update.
  await expect(dialog.locator(".update-card").getByRole("status")).toHaveText("Ready");
  await expect(dialog.locator(".update-card").getByRole("button", { name: "Update" })).toBeEnabled();
});

test("an update that installed says so, and its item moves to History", async ({ page }) => {
  // Arrange
  const dialog = await openTheItem(page);

  // Act
  await page.evaluate(() => window.updateStep("updated"));

  // Assert
  const card = dialog.locator(".update-card");
  await expect(card.getByRole("status")).toHaveText("Updated");
  await expect(card.locator(".update-result")).toHaveText("v0.5.0 runs. The board reloads on it now.");
  await page.getByRole("button", { name: "Close the Command Center" }).click();
  await page.getByLabel("Command Center").click();
  await page.getByText("History").click();
  await expect(page.locator(".inbox-list li").filter({ hasText: "Update to v0.5.0 from v0.4.2" })).toContainText("Updated v0.5.0");
});

test("the slim banner points to the item, and hides until the next version", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/update-item.html");
  const banner = page.locator(".release-banner");
  await expect(banner).toContainText("Code Goblins v0.5.0 is ready · you run v0.4.2");

  // Act
  await banner.getByRole("button", { name: "Open" }).click();

  // Assert
  await expect(page.getByRole("dialog").locator(".update-card")).toBeVisible();

  // Act
  await page.getByRole("button", { name: "Close the Command Center" }).click();
  await banner.getByRole("button", { name: "Hide until the next version" }).click();

  // Assert: hidden for this version, here and after a reload.
  await expect(banner).toHaveCount(0);
  await page.reload();
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toBeVisible();
  await expect(page.locator(".release-banner")).toHaveCount(0);
});

test("a board built from a clone gets the clone's steps instead of Update", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/update-item.html");

  // Act
  await page.evaluate(() => window.updateStep("source"));

  // Assert
  const banner = page.locator(".release-banner");
  await expect(banner).toContainText("Code Goblins v0.5.0 is out · this board was built from a clone: run git pull, then .\\install.cmd -Dev in the clone");
  await expect(banner.getByRole("button", { name: "Open" })).toHaveCount(0);
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toHaveCount(0);
});
