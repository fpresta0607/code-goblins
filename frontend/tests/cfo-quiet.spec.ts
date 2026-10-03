import { expect, test, type Page } from "./site";

const NOTICE = "The CFO has not answered 3 questions; the oldest has waited 11 minutes.";

async function step(page: Page, name: Parameters<typeof window.advance>[0]) {
  await page.evaluate((to) => window.advance(to), name);
  await expect(page.locator("main")).toHaveAttribute("data-step", name);
}

for (const configuration of [
  { name: "a browser tab", viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 },
  { name: "the desktop window", viewport: { width: 1707, height: 1000 }, deviceScaleFactor: 1.5 },
]) {
  test.describe(configuration.name, () => {
    test.use({ viewport: configuration.viewport, deviceScaleFactor: configuration.deviceScaleFactor });

    test("unanswered questions raise one claimed notice per stretch, opening the CFO's terminal", async ({ page, context }, testInfo) => {
      // Arrange
      const announced = new Set<string>();
      let claims = 0;
      await context.route("**/api/announce", async (route) => {
        const asked: { keys: string[]; news: string[] } = route.request().postDataJSON();
        const claimed = [...asked.keys, ...asked.news].filter((key) => !announced.has(key));
        for (const key of claimed) announced.add(key);
        claims += claimed.length;
        await route.fulfill({ json: { claimed } });
      });
      await page.goto("/tests/fixtures/announce-once.html");
      await page.waitForFunction(() => "advance" in window);

      // Act
      await step(page, "quietCFO");

      // Assert
      const notice = page.locator(".toasts .toast").filter({ hasText: NOTICE });
      await expect(notice).toBeVisible();
      expect(claims).toBe(1);
      await expect(page.getByLabel("Command Center", { exact: true })).toBeVisible();
      await expect(page.locator("dialog.question-modal")).toBeHidden();
      await testInfo.attach("cfo-quiet-notice", { body: await page.screenshot(), contentType: "image/png" });
      await notice.getByRole("button", { name: "Open the CFO's terminal", exact: true }).click();
      await expect(page.locator("output")).toHaveText("opened cfo");
      await step(page, "quietChanged");
      await page.reload();
      await page.waitForFunction(() => "advance" in window);
      await step(page, "quietChanged");
      await step(page, "restarting");
      await step(page, "quietCFO");
      const second = await context.newPage();
      await second.goto("/tests/fixtures/announce-once.html");
      await second.waitForFunction(() => "advance" in window);
      await step(second, "quietCFO");
      await step(second, "working");
      await step(page, "working");
      await step(page, "quietAgain");
      await expect(page.locator(".toasts .toast")).toHaveText(/The CFO has not answered 1 question; the oldest has waited 10 minutes/);
      expect(claims).toBe(2);
      await expect(second.locator(".toasts .toast")).toHaveCount(0);
      await second.close();
    });
  });
}
