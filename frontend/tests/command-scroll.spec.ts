import { expect, test, type Locator } from "./site";

// The Overlord, 2026-09-30: "when command center notification opens make sure
// its scrolled to top by default". Each opening starts at the top of its item;
// within one visit the scroll stays where he puts it, and moving to another
// item shows that item from its top.
const scrollTop = (dialog: Locator) => dialog.evaluate((element) => element.scrollTop);

test.use({ viewport: { width: 1280, height: 720 } });

test("the Command Center opens at the top of its item every time", async ({ page }) => {
  // Arrange: new questions open the stack by themselves; he scrolls down the
  // first card and closes the Command Center.
  await page.goto("/tests/fixtures/command-scroll.html");
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("May I build the Paused divider as drawn?")).toBeVisible();
  expect(await dialog.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true);
  await dialog.evaluate((element) => element.scrollTo(0, element.scrollHeight));
  expect(await scrollTop(dialog)).toBeGreaterThan(0);
  await page.waitForTimeout(200);
  expect(await scrollTop(dialog)).toBeGreaterThan(0);

  // Act
  await dialog.getByRole("button", { name: "Close the Command Center" }).click();
  await expect(dialog).toBeHidden();
  await page.getByRole("button", { name: "Open", exact: true }).click();

  // Assert
  await expect(dialog.getByText("May I build the Paused divider as drawn?")).toBeVisible();
  await expect.poll(() => scrollTop(dialog)).toBe(0);
  await page.waitForTimeout(300);
  expect(await scrollTop(dialog)).toBe(0);
});

test("moving to the next item shows it from its top", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/command-scroll.html");
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("May I build the Paused divider as drawn?")).toBeVisible();
  await dialog.evaluate((element) => element.scrollTo(0, element.scrollHeight));

  // Act
  await dialog.getByRole("button", { name: "Next item" }).click();

  // Assert
  await expect(dialog.getByText("May I restyle the header as drawn?")).toBeVisible();
  await expect.poll(() => scrollTop(dialog)).toBe(0);
});
