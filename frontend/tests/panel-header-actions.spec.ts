import { expect, holdStream, test, type Locator, type Page } from "./site";

// The Overlord, 2026-10-08 about 21:55Z, of Restart the CFO in the body of
// the CFO's panel: "what does restart the cfo do.. i want it in header of
// task panel please right below goblin".
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

for (const [size, viewport] of [["desktop", { width: 1280, height: 900 }], ["phone", { width: 390, height: 844 }]] as const) {
  test.describe(`at ${size} width`, () => {
    test.use({ viewport });

    test("Restart the CFO sits in the CFO's header right below its name, on one line, and the panel under the header has none", async ({ page }, testInfo) => {
      // Arrange
      await open(page, snapshot());
      const header = page.locator(".panel-header");
      const restart = header.getByRole("button", { name: "Restart the CFO" });

      // Act
      await header.screenshot({ path: testInfo.outputPath(`cfo-header-${size}.png`) });

      // Assert
      await expect(restart).toBeVisible();
      await expect(page.locator(".panel-task").getByRole("button", { name: /Restart/ })).toHaveCount(0);
      const name = await box(header.locator("#panel-title")), button = await box(restart), status = await box(header.locator(".panel-status"));
      expect(button.y).toBeGreaterThanOrEqual(name.y + name.height - 1);
      expect(button.y + button.height).toBeLessThanOrEqual(status.y + 1);
      expect(Math.abs(button.x - name.x)).toBeLessThan(2);
      expect(await lines(restart)).toBe(1);
      await expect(restart).toHaveAttribute("data-tip", "Starts the CFO again on its conversation, for a screen that froze.");
      expect(await pageScroll(page)).toBe(0);
    });

    test("Restart in the header still asks first, and Cancel restarts nothing", async ({ page }) => {
      // Arrange
      const asked: string[] = [];
      await open(page, snapshot());
      await page.route("**/api/cfo/restart", (route) => { asked.push(route.request().method()); return route.fulfill({ json: {} }); });

      // Act
      await page.locator(".panel-header").getByRole("button", { name: "Restart the CFO" }).click();
      const dialog = page.getByRole("dialog", { name: "Restart the CFO?" });
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Cancel" }).click();

      // Assert
      await expect(dialog).toHaveCount(0);
      expect(asked).toEqual([]);
    });

    test("the CFO task panel omits the empty connections prompt and machine settings", async ({ page }, testInfo) => {
      // Arrange
      const settingsRequests: string[] = [];
      page.on("request", (request) => {
        if (request.method() === "POST" && /\/api\/(start-at-login|dev-drive)$/.test(request.url())) settingsRequests.push(request.url());
      });
      await open(page, snapshot({ start_at_login: { on: size === "desktop" } }));

      // Act
      await page.locator(".panel-content > .disclosure.workspace-details > summary").click();
      await page.locator(".panel-content > .disclosure.connections > summary").click();

      // Assert
      await expect(page.getByText("CFO project root", { exact: true })).toBeVisible();
      await expect(page.getByRole("list", { name: "Harness and model" })).toContainText("Claude Code");
      await expect(page.getByText("Choose a goblin to check its connections.", { exact: true })).toHaveCount(0);
      await expect(page.getByText("Dev Drive", { exact: true })).toHaveCount(0);
      await expect(page.getByText("Start at login", { exact: true })).toHaveCount(0);
      expect(settingsRequests).toEqual([]);
      expect(await pageScroll(page)).toBe(0);
      await page.locator(".goblin-panel").screenshot({ path: testInfo.outputPath(`cfo-panel-trimmed-${size}.png`) });
    });

  });
}
