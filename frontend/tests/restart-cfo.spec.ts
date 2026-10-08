import { expect, test, type Page } from "./site";

// The Overlord's requirement, 2026-09-29, after the CFO's screen froze while
// its session kept working: goblins resume restarts it in place on its
// conversation, "and the board offers the same as a Restart CFO action".
// On 2026-10-07, looking at it on the CFO's bar: "i dont like this restart the
// cfo button here", so it lives in the CFO's panel.
async function open(page: Page, answer: { status: number; body: Record<string, unknown> }, hash = "") {
  const requests: { token: string | null; body: string | null }[] = [];
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/cfo/restart", async (route) => {
    requests.push({ token: await route.request().headerValue("X-CFO-Token"), body: route.request().postData() });
    await held;
    return route.fulfill({ status: answer.status, json: answer.body });
  });
  await page.goto("/tests/fixtures/restart-cfo.html" + hash);
  await expect(page.getByRole("group", { name: "CFO" })).toBeVisible();
  return { requests, release };
}

test.use({ viewport: { width: 1280, height: 720 } });

test("the CFO's bar has no Restart, and the CFO's panel has it", async ({ page }) => {
  // Act
  await open(page, { status: 200, body: {} });

  // Assert
  await expect(page.getByRole("group", { name: "CFO" }).getByRole("button", { name: /Restart/ })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "CFO panel" }).getByRole("button", { name: "Restart the CFO" })).toBeVisible();
});

test("Restart asks first, and Cancel restarts nothing", async ({ page }, testInfo) => {
  // Arrange
  const { requests } = await open(page, { status: 200, body: { restarted: true, resumed: true, session: "a1b2c3d4-session" } });
  await page.screenshot({ path: testInfo.outputPath("restart-cfo-bar.png") });

  // Act
  await page.getByRole("button", { name: "Restart the CFO" }).click();
  const dialog = page.getByRole("dialog", { name: "Restart the CFO?" });

  // Assert: it says what a restart does, and its own action has the keyboard.
  await expect(dialog).toContainText("interrupts what it is doing now");
  await expect(dialog).toContainText("Its conversation is kept.");
  await expect(dialog.getByRole("button", { name: "Restart the CFO" })).toBeFocused();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  expect(requests).toHaveLength(0);
});

test("Enter in the dialog restarts the CFO once, with the board's token", async ({ page }, testInfo) => {
  // Arrange
  const { requests, release } = await open(page, { status: 200, body: { restarted: true, resumed: true, session: "a1b2c3d4-session" } });
  await page.getByRole("button", { name: "Restart the CFO" }).click();
  await page.screenshot({ path: testInfo.outputPath("restart-cfo-dialog.png") });

  // Act
  await page.keyboard.press("Enter");

  // Assert: one request while it is under way, and nothing to report after.
  await expect(page.getByRole("button", { name: "Restarting the CFO…" })).toBeDisabled();
  expect(requests).toEqual([{ token: "fixture", body: "{}" }]);
  release();
  await expect(page.getByRole("button", { name: "Restart the CFO" })).toBeEnabled();
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("a CFO that could not be restarted says so in a few words beside its Restart", async ({ page }) => {
  // Arrange
  const reason = "the CFO could not be restarted: pi has no way to resume a conversation, so the CFO is left running: close it and run goblins to start it on a new conversation";
  const { release } = await open(page, { status: 500, body: { error: reason } });
  release();

  // Act
  await page.getByRole("button", { name: "Restart the CFO" }).click();
  await page.getByRole("dialog", { name: "Restart the CFO?" }).getByRole("button", { name: "Restart the CFO" }).click();

  // Assert
  await expect(page.locator(".click-feedback")).toHaveText(/^The CFO could not be restarted: pi has no way to resume a…$/);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Restart the CFO" })).toBeEnabled();
});

test("a CFO still starting offers no Restart", async ({ page }) => {
  // Act
  await open(page, { status: 200, body: {} }, "#starting");

  // Assert
  await expect(page.getByRole("group", { name: "CFO" })).toContainText("Starting: answer anything it asks in its terminal.");
  await expect(page.getByRole("button", { name: "Restart the CFO" })).toHaveCount(0);
});
