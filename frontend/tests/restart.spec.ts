import { expect, test } from "./site";

// The Overlord's brief, 2026-09-30, approved as shown on 2026-10-06: after a
// restart the board says plainly what is coming back and then what resumed, a
// goblin that did not come back says so on its card, and Start at login is a
// switch in the CFO's panel. Why it did not come back is the CFO's to hear
// (2026-10-08, "everything error wise goes to cfo"). The window's size is his: 1707 CSS pixels wide
// at device scale 1.5.
test.use({ viewport: { width: 1707, height: 900 }, deviceScaleFactor: 1.5 });

test("while the fleet comes back the line says what is back, and a waiting card that it waits for memory", async ({ page }, testInfo) => {
  // Act
  await page.goto("/tests/fixtures/restart.html?state=coming");

  // Assert
  const line = page.getByRole("status").filter({ hasText: "after a restart" });
  await expect(line).toHaveText("Coming back after a restart: the CFO is back, 1 of 6 goblins is back.");
  await expect(line.getByRole("button")).toHaveCount(0);
  await expect(page.getByText("Comes back after the restart when memory allows")).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("restart-coming.png") });
});

test("once all is back the line says what resumed, the card of one that did not come back says so, and the line stays dismissed", async ({ page }, testInfo) => {
  // Arrange
  await page.goto("/tests/fixtures/restart.html");
  const line = page.getByRole("status").filter({ hasText: "after a restart" });

  // Assert
  await expect(line).toContainText("Resumed the CFO and 5 of 6 goblins after a restart.");
  await expect(line).not.toContainText("cg-site-hero");
  await expect(page.getByText("Did not come back after the restart", { exact: true })).toBeVisible();
  await expect(page.getByText("Codex is not signed in")).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath("restart-resumed.png") });

  // Act
  await line.getByRole("button", { name: "Dismiss" }).click();
  await page.reload();

  // Assert
  await expect(page.getByText("Did not come back after the restart", { exact: true })).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "after a restart" })).toHaveCount(0);
});

test("Start at login turns off with the board's token, and its tip says what it does or why it cannot be set", async ({ page }) => {
  // Arrange
  const requests: { token: string | null; body: string | null }[] = [];
  await page.route("**/api/start-at-login", async (route) => {
    requests.push({ token: await route.request().headerValue("X-CFO-Token"), body: route.request().postData() });
    return route.fulfill({ status: 200, json: { on: false } });
  });
  await page.goto("/tests/fixtures/restart.html");
  const toggle = page.getByRole("switch", { name: "Start at login" });
  await expect(toggle).toHaveAttribute("aria-checked", "true");
  await expect(toggle).toHaveAttribute("data-tip", "Starts Code Goblins in the tray at sign-in and brings back the CFO and its goblins after a restart.");

  // Act
  await toggle.click();

  // Assert
  await expect.poll(() => requests).toEqual([{ token: "fixture", body: "{\"on\":false}" }]);

  // Act
  await page.goto("/tests/fixtures/restart.html?login=unavailable");

  // Assert
  await expect(page.getByRole("switch", { name: "Start at login" })).toBeDisabled();
  await expect(page.getByRole("switch", { name: "Start at login" })).toHaveAttribute("data-tip", /^Start at login opens the desktop app, which this home does not hold/);
});
