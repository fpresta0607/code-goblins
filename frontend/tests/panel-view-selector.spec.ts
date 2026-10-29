import { expect, test, type Page } from "./site";
import { open, openGoblin } from "./session-board";

// The Overlord, 2026-10-26: "i dont like how you remove the selector at the
// top between caht and task panel bring that back i dont like the drop down
// also". One segmented control switches a panel between its conversation and
// its task view, for the CFO and a goblin, at desktop and phone widths, and
// the keyboard reaches it. The Overlord, 2026-10-28: "chat and task doesn't
// need to be full width, it can fit inline with cfo and afk and just be the
// icons", so its two buttons are icons alone, named for a screen reader, with
// no words and no tip, in the panel header's own row.

const switcher = (page: Page) => page.getByRole("group", { name: "Panel view", exact: true });
const taskPanel = (page: Page) => page.locator(".goblin-panel > .panel-task");
const messages = (page: Page) => page.getByRole("region", { name: "Messages", exact: true });

async function openCfo(page: Page) {
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(page.locator("#panel-title")).toHaveText("CFO");
}

// The selector is two icons with no words and no tip, inside the panel's
// header on the same row as its picture, and the CFO's AFK switch shares that
// row, all inside the screen.
async function expectIconsInTheHeaderRow(page: Page, who: string) {
  const header = page.locator(".goblin-panel > .panel-header");
  const views = header.locator(":scope > .panel-view-switch");
  await expect(views).toBeVisible();
  for (const name of ["Chat", "Task"]) {
    const button = views.getByRole("button", { name, exact: true });
    await expect(button).toHaveText("");
    await expect(button.locator(".icon")).toBeVisible();
    await expect(button).not.toHaveAttribute("data-tip");
  }
  const picture = (await header.locator(".goblin-avatar").boundingBox())!;
  const onTheRow = async (part: string) => {
    const box = (await header.locator(part).boundingBox())!;
    const middle = box.y + box.height / 2;
    expect(middle, part + " on the picture's row").toBeGreaterThan(picture.y);
    expect(middle, part + " on the picture's row").toBeLessThan(picture.y + picture.height);
    expect(box.x + box.width, part + " inside the screen").toBeLessThanOrEqual(page.viewportSize()!.width);
  };
  await onTheRow(":scope > .panel-view-switch");
  if (who === "CFO") await onTheRow(".afk-header");
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
    await expectIconsInTheHeaderRow(page, who);
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

for (const width of [360, 393, 430]) {
  for (const who of ["goblin", "CFO"]) {
    test(`${who}'s panel on a ${width}px phone has the same selector, its icons in the header's one row`, async ({ page }, testInfo) => {
      // Arrange
      await page.setViewportSize({ width, height: 844 });
      await open(page);
      if (who === "goblin") await openGoblin(page);
      else await openCfo(page);
      const views = switcher(page);
      const chat = views.getByRole("button", { name: "Chat", exact: true });
      const task = views.getByRole("button", { name: "Task", exact: true });

      // Assert
      await expect(chat).toHaveAttribute("aria-pressed", "true");
      await expectIconsInTheHeaderRow(page, who);
      await expect(messages(page)).toBeVisible();

      // Act
      await task.focus();
      await page.keyboard.press("Space");

      // Assert: the task view keeps the selector on the header's row.
      await expect(task).toHaveAttribute("aria-pressed", "true");
      await expect(taskPanel(page)).toBeVisible();
      await expect(messages(page)).toBeHidden();
      await expectIconsInTheHeaderRow(page, who);
      await chat.click();
      await expect(messages(page)).toBeVisible();
      await testInfo.attach(`panel-phone-${who}-${width}.png`, { body: await page.screenshot(), contentType: "image/png" });
    });
  }
}

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
