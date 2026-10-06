import { expect, test, type Page } from "./site";

// The Overlord's requirement, 2026-10-01: "A home that has had a CFO never
// shows the first-run page again. While its CFO is closed the board says so
// in plain words with one Reopen action, which does what goblins does. No
// error naming a pid or telling him to run cfo register appears for a CFO he
// closed."
async function open(page: Page, answer: { status: number; body: Record<string, unknown> }) {
  const requests: { token: string | null; body: string | null }[] = [];
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/cfo/reopen", async (route) => {
    requests.push({ token: await route.request().headerValue("X-CFO-Token"), body: route.request().postData() });
    await held;
    return route.fulfill({ status: answer.status, json: answer.body });
  });
  await page.goto("/tests/fixtures/closed-cfo.html");
  await expect(page.getByRole("button", { name: "Reopen the CFO" })).toBeVisible();
  return { requests, release };
}

test.use({ viewport: { width: 1280, height: 420 } });

test("a closed CFO is said to be closed in plain words, with Reopen as its one action", async ({ page }, testInfo) => {
  // Arrange
  const { requests, release } = await open(page, { status: 200, body: { reopened: true } });
  const bar = page.getByRole("group", { name: "CFO" });
  const reopen = page.getByRole("button", { name: "Reopen the CFO" });

  // Assert: plain words, one action, and nothing about a pid or a command.
  await expect(bar).toContainText("The CFO is closed. Goblins keep running.");
  await expect(bar.getByRole("button")).toHaveCount(1);
  await expect(page.getByText(/pid|cfo register|No CFO is running|Start the CFO/)).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath("closed-cfo.png") });

  // Act
  await reopen.click();

  // Assert: one request, carrying the board's token, and no second while it
  // is under way.
  await expect(page.getByRole("button", { name: "Reopening the CFO…" })).toBeDisabled();
  expect(requests).toEqual([{ token: "fixture", body: "{}" }]);
  release();
  await expect(reopen).toBeEnabled();
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("a CFO that could not come back says why, and Reopen can be tried again", async ({ page }) => {
  // Arrange
  const reason = "the CFO could not be reopened: claude is not on PATH";
  const { requests, release } = await open(page, { status: 500, body: { error: reason } });
  release();

  // Act
  await page.getByRole("button", { name: "Reopen the CFO" }).click();

  // Assert
  await expect(page.getByRole("alert")).toHaveText(reason);
  await expect(page.getByRole("button", { name: "Reopen the CFO" })).toBeEnabled();
  expect(requests).toHaveLength(1);
});

// The Overlord works in the desktop window, the board in WebView2: maximized
// on his 2560 by 1600 screen at 150 percent it is 1707 CSS pixels wide at
// device scale 1.5. The bar uses nothing only a browser has, so it must only
// fit there: its words and Reopen on one line, nothing cut off or wrapped.
test.describe("at the desktop window's size", () => {
  test.use({ viewport: { width: 1707, height: 420 }, deviceScaleFactor: 1.5 });

  test("the closed CFO's bar shows whole on one line, with Reopen in view", async ({ page }, testInfo) => {
    // Arrange
    const { release } = await open(page, { status: 200, body: { reopened: true } });
    release();
    const bar = page.getByRole("group", { name: "CFO" });
    const reopen = page.getByRole("button", { name: "Reopen the CFO" });

    // Act
    const said = bar.getByText("The CFO is closed. Goblins keep running.");
    const [words, button] = [await said.boundingBox(), await reopen.boundingBox()];
    const sideways = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);

    // Assert
    await expect(reopen).toBeInViewport({ ratio: 1 });
    expect(sideways).toBe(false);
    expect(words!.height).toBeLessThan(button!.height);
    await page.screenshot({ path: testInfo.outputPath("closed-cfo-window.png") });
  });
});
