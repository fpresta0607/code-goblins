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
  await expect(page.getByRole("group", { name: "CFO" }).getByRole("button", { name: /^Harness:/ })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "CFO panel" }).getByRole("button", { name: "Restart the CFO" })).toBeVisible();
});

test("choosing a harness sends nothing and Restart sends it directly", async ({ page }, testInfo) => {
  // Arrange
  const { requests, release } = await open(page, { status: 200, body: { restarted: true, resumed: true, session: "a1b2c3d4-session" } });
  await page.screenshot({ path: testInfo.outputPath("restart-cfo-bar.png") });
  const restart = page.getByRole("button", { name: "Restart the CFO" });
  const selector = page.getByRole("button", { name: "Harness: Claude Code" });
  await expect(selector).toBeVisible();
  await expect(restart).toHaveText("Restart");

  // Act
  await selector.click();
  const menu = page.getByRole("menu", { name: "Harness" });
  await expect(menu.getByRole("menuitemradio")).toHaveText(["Claude Code", "Codex", "pi"]);
  for (const name of ["Claude Code", "Codex", "pi"]) await expect(menu.getByRole("menuitemradio", { name, exact: true }).locator("svg")).toBeVisible();
  await menu.getByRole("menuitemradio", { name: "Codex", exact: true }).click();

  // Assert
  await expect(menu).toHaveCount(0);
  const selected = page.getByRole("button", { name: "Harness: Codex" });
  await expect(selected).toBeFocused();
  await expect(selected.locator(".mark svg")).toBeVisible();
  expect(requests).toHaveLength(0);
  await restart.click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(restart).toBeDisabled();
  await expect(selected).toBeDisabled();
  await expect.poll(() => requests).toEqual([{ token: "fixture", body: JSON.stringify({ harness: "codex" }) }]);
  release();
  await expect(restart).toBeEnabled();
  await expect(selected).toBeEnabled();
});

test("Enter on Restart sends the running harness once with the board token", async ({ page }) => {
  // Arrange
  const { requests, release } = await open(page, { status: 200, body: { restarted: true, resumed: true, session: "a1b2c3d4-session" } });
  const restart = page.getByRole("button", { name: "Restart the CFO" });
  await restart.focus();

  // Act
  await page.keyboard.press("Enter");

  // Assert: one request while it is under way, and nothing to report after.
  await expect(restart).toBeDisabled();
  await expect(page.getByRole("button", { name: "Harness: Claude Code" })).toBeDisabled();
  await page.keyboard.press("Enter");
  await expect.poll(() => requests).toEqual([{ token: "fixture", body: JSON.stringify({ harness: "claude" }) }]);
  release();
  await expect(page.getByRole("button", { name: "Restart the CFO" })).toBeEnabled();
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("a CFO that could not be restarted says so in a few words beside its Restart", async ({ page }) => {
  // Arrange
  const reason = "The chosen harness is not installed";
  const { release } = await open(page, { status: 500, body: { error: reason } });
  release();

  // Act
  await page.getByRole("button", { name: "Harness: Claude Code" }).click();
  await page.getByRole("menuitemradio", { name: "Codex", exact: true }).click();
  await page.getByRole("button", { name: "Restart the CFO" }).click();

  // Assert
  await expect(page.locator(".click-feedback")).toHaveText("The chosen harness is not installed.");
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Restart the CFO" })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Harness: Codex" })).toBeEnabled();
});

// The Overlord, 2026-10-09, after Restart refused on a conversation too large
// to resume, cut off as "Its last conversation is 33…": "this makes no
// sense". Restart always restarts the CFO, and when it starts on a new
// conversation the board says so in one plain line, which stays while the
// restarted CFO starts and its Restart is gone.
test("a CFO restarted on a new conversation says so in one plain line", async ({ page }, testInfo) => {
  // Arrange
  const { release } = await open(page, { status: 200, body: { restarted: true, resumed: false, session: "large-session" } }, "#comes-back-starting");
  release();

  // Act
  await page.getByRole("button", { name: "Restart the CFO" }).click();

  // Assert
  await expect(page.locator(".click-feedback")).toHaveText("The CFO restarted on a new conversation.");
  await expect(page.getByRole("button", { name: "Restart the CFO" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^Harness:/ })).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath("restart-cfo-new-conversation.png") });
});

test("a CFO still starting offers no Restart", async ({ page }) => {
  // Act
  await open(page, { status: 200, body: {} }, "#starting");

  // Assert
  await expect(page.getByRole("group", { name: "CFO" })).toContainText("Starting: answer anything it asks in its terminal.");
  await expect(page.getByRole("button", { name: "Restart the CFO" })).toHaveCount(0);
});

for (const [harness, name, width] of [["codex", "Codex", 1280], ["pi", "pi", 390]] as const) {
  test(`Restart can choose ${harness} at ${width}px and starts it fresh with the board token`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 844 });
    const { requests, release } = await open(page, { status: 200, body: { restarted: true, resumed: false } });
    await page.getByRole("button", { name: "Harness: Claude Code" }).click();
    const menu = page.getByRole("menu", { name: "Harness" });
    await expect(menu.getByRole("menuitemradio", { name: "Claude Code", exact: true })).toHaveAttribute("aria-checked", "true");
    await menu.screenshot({ path: testInfo.outputPath(`restart-options-${harness}.png`) });
    const bounds = await menu.boundingBox();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width);

    // Act
    await menu.getByRole("menuitemradio", { name, exact: true }).click();

    // Assert
    const controls = page.locator(".cfo-restart");
    await expect(controls).toHaveText(`Restart${name}`);
    await controls.screenshot({ path: testInfo.outputPath(`restart-as-${harness}.png`) });
    expect(await controls.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    expect(requests).toEqual([]);
    await page.getByRole("button", { name: "Restart the CFO" }).click();
    await expect.poll(() => requests).toEqual([{ token: "fixture", body: JSON.stringify({ harness }) }]);
    release();
    await expect(page.locator(".click-feedback")).toHaveText("The CFO restarted on a new conversation.");
  });
}

test("the harness menu supports keyboard selection and Escape returns focus", async ({ page }) => {
  // Arrange
  const { requests } = await open(page, { status: 200, body: {} });
  const selector = page.getByRole("button", { name: "Harness: Claude Code" });
  await selector.focus();
  await page.keyboard.press("ArrowDown");
  await expect(page.getByRole("menuitemradio", { name: "Claude Code", exact: true })).toBeFocused();

  // Act
  await page.keyboard.press("ArrowDown");
  await expect(page.getByRole("menuitemradio", { name: "Codex", exact: true })).toBeFocused();
  await page.keyboard.press("Enter");

  // Assert
  const selected = page.getByRole("button", { name: "Harness: Codex" });
  await expect(selected).toBeFocused();
  await expect(page.getByRole("menu")).toHaveCount(0);
  await page.keyboard.press("Space");
  await expect(page.getByRole("menuitemradio", { name: "Codex", exact: true })).toBeFocused();
  await expect(page.getByRole("menuitemradio", { name: "Codex", exact: true })).toHaveAttribute("aria-checked", "true");
  await page.keyboard.press("Home");
  await expect(page.getByRole("menuitemradio", { name: "Claude Code", exact: true })).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(page.getByRole("menuitemradio", { name: "pi", exact: true })).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(page.getByRole("menuitemradio", { name: "Claude Code", exact: true })).toBeFocused();
  await page.keyboard.press("End");
  await expect(page.getByRole("menuitemradio", { name: "pi", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("menu")).toHaveCount(0);
  await expect(selected).toBeFocused();
  expect(requests).toEqual([]);
});

test("outside clicks and Tab dismiss the harness menu without sending a request", async ({ page }) => {
  // Arrange
  const { requests } = await open(page, { status: 200, body: {} });
  const selector = page.getByRole("button", { name: "Harness: Claude Code" });

  // Act
  await selector.click();
  await page.getByRole("heading", { name: "CFO", exact: true }).click();

  // Assert
  await expect(page.getByRole("menu")).toHaveCount(0);
  await selector.click();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("menu")).toHaveCount(0);
  expect(requests).toEqual([]);
});

for (const [harness, name] of [["claude", "Claude Code"], ["codex", "Codex"], ["pi", "pi"]]) {
  test(`Restart begins with the running ${harness} selected`, async ({ page }) => {
    // Arrange
    const { requests, release } = await open(page, { status: 200, body: { resumed: harness !== "pi" } }, `?harness=${harness}`);
    await expect(page.getByRole("button", { name: `Harness: ${name}` })).toBeVisible();

    // Act
    await page.getByRole("button", { name: "Restart the CFO" }).click();

    // Assert
    await expect.poll(() => requests).toEqual([{ token: "fixture", body: JSON.stringify({ harness }) }]);
    release();
    await expect(page.getByRole("button", { name: "Restart the CFO" })).toBeEnabled();
  });
}
