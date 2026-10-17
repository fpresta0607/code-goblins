import { expect, holdStream, test, type Locator, type Page } from "./site";

// The Overlord, 2026-10-08 about 21:55Z, of Restart the CFO in the body of
// the CFO's panel: "what does restart the cfo do.. i want it in header of
// task panel please right below goblin". On 2026-10-13 every harness control
// moved into a category of its own named Harness, the CFO's Restart with it.
// The supervisor is played here: one snapshot on a held stream.
const NOTE = "An optional drive that Defender scans in performance mode, so files open faster.";
const EXPLAIN = "A Dev Drive is a drive Windows 11 formats for developer work: Microsoft Defender keeps scanning it, in performance mode, so opening a file no longer waits for the scan. It is not a Defender exclusion.";
const LINE = "absent: this machine can have one (A Dev Drive is a drive Windows 11 formats for developer work: Microsoft Defender keeps scanning it, in performance mode, so opening a file no longer waits for the scan. It is not a Defender exclusion), and it is optional; Set up under Dev Drive in the board's Workspace panel puts each step in the Command Center";
const drive = (fields: Record<string, unknown> = {}) => ({ state: "absent", line: LINE, explain: EXPLAIN, note: NOTE, action: "set-up", ...fields });
const snapshot = (fields: Record<string, unknown> = {}) => ({
  healthy: true, instance: "fixture", revision: 1, attention: [], tasks: [], afk: { state: "off" },
  cfo_runs: true, cfo_harness: "claude", cfo_terminal: "cfo", cfo_terminal_since: "2026-10-08T09:00:00Z", sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }],
  start_at_login: { on: true, unavailable: "" }, dev_drive: drive(), ...fields,
});

async function open(page: Page, first: object) {
  await holdStream(page, first);
  await page.route("**/api/**", (route) => {
    if (new URL(route.request().url()).pathname === "/api/workspace") return route.fulfill({ json: { project: "", repository: "", root: "C:\\Users\\op\\AppData\\Local\\CodeGoblins", branch: "", harness: "claude", model: "Reported: claude-opus-5-5", notes: [] } });
    return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(page.locator("#panel-title")).toHaveText("CFO");
  await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
  await page.evaluate(() => document.fonts.ready);
}

const box = async (locator: Locator) => (await locator.boundingBox())!;
// How many lines a control's words take; a switch with none takes its one.
const lines = (locator: Locator) => locator.evaluate((element) => {
  const tops = new Set<number>();
  const words = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
  for (let text = words.nextNode(); text; text = words.nextNode()) {
    const range = document.createRange();
    range.selectNodeContents(text);
    for (const rect of range.getClientRects()) if (rect.width > 0) tops.add(Math.round(rect.top));
  }
  return Math.max(tops.size, 1);
});
const pageScroll = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

// The Overlord, 2026-10-17, with the CFO panel reading Session ended while a
// Claude Code CFO worked: the header reads the registered CFO's own session,
// and an older CFO's ended session never stands in for it, whether or not the
// registered CFO has reported its session yet.
test("the CFO's panel header reads the registered CFO, never an ended CFO session", async ({ page }) => {
  const endedPi = { id: "pi/old-cfo", native_id: "old-cfo", harness: "pi", role: "cfo", phase: "ended", model: "openrouter/deepseek/deepseek-v4.1-flash" };
  const live = { id: "claude/live-cfo", native_id: "live-cfo", harness: "claude", role: "cfo", phase: "active", model: "claude-opus-5-5" };
  for (const [sessions, said] of [[[endedPi, live], "Working"], [[endedPi], "Supervising"]] as const) {
    await open(page, snapshot({ cfo_session: "claude/live-cfo", sessions }));
    await expect(page.locator(".panel-status")).toHaveText(said);
    await expect(page.locator(".panel-status")).not.toHaveText(/Session ended/);
  }
});

for (const [size, viewport] of [["desktop", { width: 1280, height: 900 }], ["phone", { width: 390, height: 844 }]] as const) {
  test.describe(`at ${size} width`, () => {
    test.use({ viewport });

    test("Restart the CFO sits under Harness in the CFO's task panel, on one line with its harness, and the header has none", async ({ page }, testInfo) => {
      // Arrange
      await open(page, snapshot());
      const section = page.locator(".panel-content > .disclosure.harness");
      const restart = section.getByRole("button", { name: "Restart the CFO" });
      const selector = section.getByRole("button", { name: "Harness: Claude Code" });

      // Act
      await section.locator("> summary").click();
      await page.locator(".goblin-panel").screenshot({ path: testInfo.outputPath(`cfo-harness-${size}.png`) });

      // Assert
      await expect(restart).toBeVisible();
      await expect(page.locator(".panel-header").getByRole("button", { name: /Restart|^Harness:/ })).toHaveCount(0);
      const button = await box(restart);
      expect(await lines(restart)).toBe(1);
      await expect(restart).toHaveText("Restart");
      await expect(selector).toBeVisible();
      const choice = await box(selector);
      expect(Math.abs(button.y - choice.y)).toBeLessThan(2);
      expect(choice.x).toBeGreaterThanOrEqual(button.x + button.width);
      expect(choice.x + choice.width).toBeLessThanOrEqual(viewport.width);
      expect(await lines(selector)).toBe(1);
      expect(await pageScroll(page)).toBe(0);
    });

    test("the harness menu dismisses without closing the task panel and Restart sends the selection", async ({ page }) => {
      // Arrange
      const asked: { method: string; body: string | null }[] = [];
      await open(page, snapshot());
      await page.route("**/api/cfo/restart", (route) => { asked.push({ method: route.request().method(), body: route.request().postData() }); return route.fulfill({ json: {} }); });
      const section = page.locator(".panel-content > .disclosure.harness");
      await section.locator("> summary").click();
      const selector = section.getByRole("button", { name: "Harness: Claude Code" });

      // Act
      await selector.click();
      await page.keyboard.press("Escape");

      // Assert
      await expect(page.getByRole("menu", { name: "Harness" })).toHaveCount(0);
      await expect(selector).toBeFocused();
      await expect(page.locator("#panel-title")).toHaveText("CFO");
      expect(asked).toEqual([]);
      await selector.click();
      await page.getByRole("menuitemradio", { name: "Codex", exact: true }).click();
      expect(asked).toEqual([]);
      await section.getByRole("button", { name: "Restart the CFO" }).click();
      await expect.poll(() => asked).toEqual([{ method: "POST", body: JSON.stringify({ harness: "codex" }) }]);
      await expect(page.getByRole("dialog")).toHaveCount(0);
    });

    test("the compact terminal header has no restart controls", async ({ page }) => {
      // Arrange
      await open(page, snapshot());

      // Act
      await page.locator(".panel-pill").getByRole("button", { name: "Terminal", exact: true }).click();

      // Assert
      await expect(page.locator(".panel-header")).toHaveClass(/compact/);
      await expect(page.getByRole("button", { name: "Restart the CFO" })).toHaveCount(0);
      await expect(page.getByRole("button", { name: /^Harness:/ })).toHaveCount(0);
    });

    test("the CFO task panel omits the prompt and explanation while keeping Dev Drive actions", async ({ page }, testInfo) => {
      // Arrange
      const settingsRequests: string[] = [];
      page.on("request", (request) => {
        if (request.method() === "POST" && /\/api\/(start-at-login|dev-drive)$/.test(request.url())) settingsRequests.push(request.url());
      });
      await open(page, snapshot({ start_at_login: { on: size === "desktop" } }));

      // Act
      await page.locator(".panel-content > .disclosure.workspace-details > summary").click();
      await page.locator(".panel-content > .disclosure.harness > summary").click();

      // Assert
      await expect(page.getByText("CFO project root", { exact: true })).toBeVisible();
      await expect(page.locator(".panel-content > .disclosure.harness").getByRole("button", { name: "Harness: Claude Code" })).toBeVisible();
      await expect(page.locator(".panel-content > .disclosure.connections")).toHaveCount(0);
      await expect(page.getByText("Choose a goblin to check its connections.", { exact: true })).toHaveCount(0);
      await expect(page.getByText("Dev Drive", { exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: "Set up", exact: true })).toBeEnabled();
      await expect(page.getByText(NOTE, { exact: true })).toHaveCount(0);
      await expect(page.getByText(EXPLAIN, { exact: true })).toHaveCount(0);
      await expect(page.getByText("Start at login", { exact: true })).toHaveCount(0);
      expect(settingsRequests).toEqual([]);
      expect(await pageScroll(page)).toBe(0);
      await page.locator(".goblin-panel").screenshot({ path: testInfo.outputPath(`cfo-panel-trimmed-${size}.png`) });
    });

    for (const [action, label] of [["set-up", "Set up"], ["try-again", "Try again"], ["attach", "Attach"]] as const) {
      test(`the Dev Drive ${label} action stays available without explanatory copy`, async ({ page }) => {
        const requests: { token: string | null; body: string | null }[] = [];
        await open(page, snapshot({ dev_drive: drive({ action }) }));
        await page.route("**/api/dev-drive", async (route) => {
          requests.push({ token: await route.request().headerValue("X-CFO-Token"), body: route.request().postData() });
          await route.fulfill({ json: { wanted: true } });
        });
        await page.locator(".panel-content > .disclosure.workspace-details > summary").click();
        const row = page.locator(".dev-drive-setting");
        const button = row.getByRole("button", { name: label, exact: true });
        await expect(row.locator("p")).toHaveCount(0);
        const nameBox = await box(row.getByText("Dev Drive", { exact: true })), buttonBox = await box(button), rowBox = await box(row);
        expect(nameBox.y + nameBox.height / 2).toBeGreaterThan(buttonBox.y);
        expect(nameBox.y + nameBox.height / 2).toBeLessThan(buttonBox.y + buttonBox.height);
        expect(buttonBox.x + buttonBox.width).toBeLessThanOrEqual(rowBox.x + rowBox.width + 1);
        expect(await lines(button)).toBe(1);
        await button.click();
        await expect.poll(() => requests).toEqual([{ token: "fixture", body: '{"want":true}' }]);
        expect(await pageScroll(page)).toBe(0);
      });
    }

    test("Dev Drive keeps its waiting status without explanatory copy", async ({ page }) => {
      await open(page, snapshot({ dev_drive: drive({ waiting: "create", action: "" }) }));
      await page.locator(".panel-content > .disclosure.workspace-details > summary").click();
      const row = page.locator(".dev-drive-setting");
      await expect(row.getByRole("status")).toHaveText("Its next step waits for you in the Command Center.");
      await expect(row.getByRole("button")).toHaveCount(0);
      await expect(row).not.toContainText(NOTE);
    });

    test("Dev Drive keeps failed request feedback and allows retry", async ({ page }) => {
      await open(page, snapshot({ dev_drive: drive({ action: "try-again" }) }));
      await page.route("**/api/dev-drive", (route) => route.fulfill({ status: 409, json: { error: "Dev Drive setup is busy" } }));
      await page.locator(".panel-content > .disclosure.workspace-details > summary").click();
      const row = page.locator(".dev-drive-setting");
      await row.getByRole("button", { name: "Try again", exact: true }).click();
      await expect(row.locator(".click-feedback")).toHaveText("Dev Drive setup is busy.");
      await expect(row.getByRole("button", { name: "Try again", exact: true })).toBeEnabled();
    });

  });
}
