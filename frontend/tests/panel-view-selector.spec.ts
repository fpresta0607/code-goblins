import { expect, test, type Page } from "./site";
import { open, openGoblin } from "./session-board";

// The Overlord, 2026-10-26: "i dont like how you remove the selector at the
// top between caht and task panel bring that back i dont like the drop down
// also". One segmented control switches a panel between its conversation and
// its task view, for the CFO and a goblin, at desktop and phone widths, and
// the keyboard reaches it.

const switcher = (page: Page) => page.getByRole("group", { name: "Panel view", exact: true });
const taskPanel = (page: Page) => page.locator(".goblin-panel > .panel-task");
const messages = (page: Page) => page.getByRole("region", { name: "Messages", exact: true });

async function openCfo(page: Page) {
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(page.locator("#panel-title")).toHaveText("CFO");
}

for (const who of ["goblin", "CFO"]) {
  test(`${who}'s panel switches between its conversation and its task view, with no fold`, async ({ page }, testInfo) => {
    // Arrange
    await open(page);
    if (who === "goblin") await openGoblin(page);
    else await openCfo(page);

    // Assert the conversation is what opens, with no Task disclosure fold.
    const views = switcher(page);
    await expect(views).toBeVisible();
    const chat = views.getByRole("button", { name: "Chat", exact: true });
    const task = views.getByRole("button", { name: "Task", exact: true });
    await expect(chat).toHaveAttribute("aria-pressed", "true");
    await expect(task).toHaveAttribute("aria-pressed", "false");
    // Desktop reads the words.
    await expect(chat.locator(".view-switch-label")).toHaveText("Chat");
    await expect(task.locator(".view-switch-label")).toHaveText("Task");
    await expect(chat.locator(".view-switch-label")).toBeVisible();
    await expect(page.locator(".goblin-panel details > summary", { hasText: /^Task$/ })).toHaveCount(0);
    await expect(taskPanel(page)).toBeHidden();

    // Act: the pointer switches to the task view.
    await task.click();

    // Assert
    await expect(task).toHaveAttribute("aria-pressed", "true");
    await expect(taskPanel(page)).toBeVisible();
    await expect(messages(page)).toBeHidden();

    // Act: the keyboard, as any other native button, switches back.
    await chat.focus();
    await page.keyboard.press("Enter");

    // Assert
    await expect(chat).toHaveAttribute("aria-pressed", "true");
    await expect(messages(page)).toBeVisible();
    await expect(taskPanel(page)).toBeHidden();
    if (who === "goblin") await testInfo.attach("panel-desktop.png", { body: await page.screenshot(), contentType: "image/png" });
  });
}

test("a phone's panel has the same selector, between its conversation and its task view", async ({ page }, testInfo) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page);
  await openGoblin(page);
  const views = switcher(page);

  // Assert
  await expect(views).toBeVisible();
  const chat = views.getByRole("button", { name: "Chat", exact: true });
  const task = views.getByRole("button", { name: "Task", exact: true });
  await expect(chat).toHaveAttribute("aria-pressed", "true");
  // A phone shows the two icons alone, each still named for a screen reader.
  await expect(chat.locator(".view-switch-label")).toBeHidden();
  await expect(task.locator(".view-switch-label")).toBeHidden();
  await expect(chat.locator(".icon")).toBeVisible();
  await expect(task.locator(".icon")).toBeVisible();
  await expect(messages(page)).toBeVisible();

  // Act
  await task.focus();
  await page.keyboard.press("Space");

  // Assert
  await expect(task).toHaveAttribute("aria-pressed", "true");
  await expect(taskPanel(page)).toBeVisible();
  await expect(messages(page)).toBeHidden();
  await chat.click();
  await expect(messages(page)).toBeVisible();
  await testInfo.attach("panel-phone.png", { body: await page.screenshot(), contentType: "image/png" });
});

test("a maximized panel shows its conversation and its task list together, with no selector", async ({ page }) => {
  // Arrange
  await open(page);
  await openGoblin(page);

  // Act
  await page.getByRole("button", { name: "Maximize the panel", exact: true }).click();

  // Assert: both are visible, so there is nothing to switch.
  await expect(page.locator(".panel-side-task")).toBeVisible();
  await expect(messages(page)).toBeVisible();
  await expect(switcher(page)).toHaveCount(0);

  // Act
  await page.getByRole("button", { name: "Restore the panel", exact: true }).click();

  // Assert
  await expect(switcher(page)).toBeVisible();
});
