import { expect, openItem, test } from "./site";

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test.describe(`${viewport.width}px question input`, () => {
    test.use({ viewport });

    test("choices show bold and code while submitting their original text", async ({ page }, testInfo) => {
      // Arrange
      const sent: Record<string, unknown>[] = [];
      await page.route("**/api/actions", async (route) => {
        const body = route.request().postDataJSON();
        sent.push(body);
        await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
      });
      await page.goto("/tests/fixtures/question-input.html");
      await openItem(page, "What should happen to incident 37780310?");
      const dialog = page.getByRole("dialog");
      const choices = dialog.locator(".question-choice");

      // Assert
      await expect(choices.first().locator("strong")).toHaveText("Mark incident 37780310 a false positive in GitGuardian and keep history");
      await expect(choices.nth(1).locator("code")).toHaveText("git status");
      await expect(dialog.locator(".recommendation")).toHaveText("Recommended");
      await expect(dialog.locator("fieldset")).not.toContainText("**");
      await expect(dialog.locator("fieldset")).not.toContainText("`");
      await expect(dialog.locator("fieldset")).not.toContainText("(Recommended)");
      await expect(dialog.getByRole("textbox", { name: "Your written answer" })).toHaveCount(0);
      await dialog.screenshot({ path: testInfo.outputPath("formatted-choices.png") });

      // Act
      await choices.first().getByRole("radio").check();
      await dialog.getByRole("button", { name: "Send decision" }).click();

      // Assert
      await expect.poll(() => sent.length).toBe(1);
      expect(sent[0]).toMatchObject({ question_id: "incident", answer_kind: "option", text: "**Mark incident 37780310 a false positive in GitGuardian and keep history**" });
      await expect(dialog.getByRole("textbox", { name: "Your written answer" })).toBeFocused();
      await dialog.getByRole("button", { name: "Back", exact: true }).click();
      await expect(choices.first().locator("strong")).toHaveText("Mark incident 37780310 a false positive in GitGuardian and keep history");
      await expect(choices.nth(1).locator("code")).toHaveText("git status");
    });

    test("a question without choices opens focused and sends the written answer", async ({ page }, testInfo) => {
      // Arrange
      const sent: Record<string, unknown>[] = [];
      await page.route("**/api/actions", async (route) => {
        const body = route.request().postDataJSON();
        sent.push(body);
        await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
      });
      await page.goto("/tests/fixtures/question-input.html");
      await openItem(page, "How should we handle the history?");
      const dialog = page.getByRole("dialog");
      const box = dialog.getByRole("textbox", { name: "Your written answer" });
      const send = dialog.getByRole("button", { name: "Send decision" });

      // Assert
      await expect(box).toBeVisible();
      await expect(box).toBeFocused();
      await expect(dialog.getByRole("radio", { name: /^Other/ })).toBeChecked();
      await expect(send).toBeDisabled();

      // Act
      await page.keyboard.type("   ");
      await expect(send).toBeDisabled();
      await page.keyboard.type("Keep the history");
      await expect(send).toBeEnabled();
      await page.keyboard.press("Escape");
      await openItem(page, "How should we handle the history?");
      await expect(box).toBeFocused();
      await expect(box).toHaveValue("   Keep the history");
      await dialog.screenshot({ path: testInfo.outputPath("written-answer.png") });
      await send.click();

      // Assert
      await expect.poll(() => sent.length).toBe(1);
      expect(sent[0]).toMatchObject({ question_id: "written", answer_kind: "other", text: "   Keep the history" });
    });
  });
}
