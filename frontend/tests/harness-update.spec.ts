import { expect, holdStream, type Locator, openTaskList, type Page, test } from "./site";

// The Overlord, 2026-10-07, looking at Claude Code's "Update installed ·
// Restart to update" in the CFO's terminal: "there should be an easy update
// button right on cfo header for claude code when needed ie by afk switch".
// The supervisor is played here: one snapshot on a held stream, and what the
// board sends is collected.
const goblin = (fields: Record<string, unknown> = {}) => ({ id: "nw-invoice-export", title: "Export invoices as CSV", project: "northwind-api", phase: "working", verified: false, generation: "nw-invoice-export-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", ...fields });
const snapshot = (fields: Record<string, unknown> = {}, task: Record<string, unknown> = {}) => ({ healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [], tasks: [goblin(task)], sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }], afk: { state: "off" }, ...fields });
const WAITING = { harness: "claude", line: "✓ Update installed · Restart to update", installed: "2026-10-07T14:10:37Z" };

interface Sent { path: string; body: unknown; token: string }

async function open(page: Page, first: object): Promise<Sent[]> {
  const sent: Sent[] = [];
  await holdStream(page, first);
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === "POST" && (path === "/api/cfo/update" || path === "/api/tasks/engine")) {
      sent.push({ path, body: request.postDataJSON(), token: request.headers()["x-cfo-token"] });
      return route.fulfill({ status: 202, json: { pending: true } });
    }
    return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".cfo-pin")).toBeVisible();
  return sent;
}

const header = (page: Page) => page.locator(".panel-header");
// The line under the CFO's header row, which holds its Update.
const line = (page: Page) => page.locator(".cfo-header-line");
const card = (page: Page) => page.locator(".task-card-shell").filter({ hasText: "Export invoices as CSV" });
const panel = (page: Page) => page.locator(".goblin-panel");

// A goblin's controls are in its panel, never on its card (the Overlord,
// 2026-10-16), so its card opens it, on its Task view.
async function openGoblinPanel(page: Page) {
  await card(page).locator(".task-card").click();
  await expect(header(page).locator("#panel-title")).toHaveText("Export invoices as CSV");
  await openTaskList(page);
}

async function openCfoPanel(page: Page) {
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(header(page).locator("#panel-title")).toHaveText("CFO");
}

for (const [width, height] of [[1280, 800], [390, 844]]) {
  test.describe(`at ${width} px`, () => {
    test.use({ viewport: { width, height } });

    test("the CFO's header offers Update on a line under its row while an update waits, and his press asks for it once", async ({ page }, testInfo) => {
      // Arrange
      const sent = await open(page, snapshot({ cfo_update: WAITING }));
      await openCfoPanel(page);
      const update = line(page).getByRole("button", { name: "Update Claude Code" });

      // Assert: on a line of its own right under the header's row, at the
      // row's end, at every width, so the row keeps the AFK switch and Close.
      await expect(update).toBeVisible();
      await panel(page).screenshot({ path: testInfo.outputPath(`cfo-header-update-${width}.png`) });
      const button = (await update.boundingBox())!, row = (await header(page).boundingBox())!, corner = (await header(page).locator(".panel-corner").boundingBox())!;
      expect(button.y).toBeGreaterThanOrEqual(row.y + row.height - 0.5);
      expect(button.y - (row.y + row.height)).toBeLessThan(12);
      expect(Math.abs(button.x + button.width - (corner.x + corner.width))).toBeLessThanOrEqual(1);
      await expect(header(page).getByRole("switch", { name: "AFK mode" })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);

      // Act
      await update.click();

      // Assert
      await expect.poll(() => sent).toEqual([{ path: "/api/cfo/update", body: {}, token: "fixture" }]);
    });

    test("a goblin's panel offers Update among its controls while an update waits, its card none, and his press asks for its own update once", async ({ page }, testInfo) => {
      // Arrange
      const sent = await open(page, snapshot({}, { harness_update: { harness: "claude", installed: "2026-10-07T14:10:37Z" } }));
      await expect(card(page)).toBeVisible();
      await expect(card(page).getByRole("button", { name: /^Update / })).toHaveCount(0);
      await openGoblinPanel(page);
      const update = panel(page).getByRole("button", { name: "Update Export invoices as CSV" });

      // Assert
      await expect(update).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
      await panel(page).screenshot({ path: testInfo.outputPath(`goblin-panel-update-${width}.png`) });

      // The Overlord, 2026-10-07: "update button on task cards is ugly". It
      // wears the look of the panel's other controls.
      const look = (button: Locator) => button.evaluate((element) => { const style = getComputedStyle(element); return [style.height, style.borderTopColor, style.color, style.boxShadow, style.backgroundColor].join(" | "); });
      expect(await look(update)).toBe(await look(panel(page).getByRole("button", { name: "Pause Export invoices as CSV" })));

      // Act
      await update.click();

      // Assert
      await expect.poll(() => sent).toEqual([{ path: "/api/tasks/engine", body: { task: "nw-invoice-export", generation: "nw-invoice-export-1", when: "update" }, token: "fixture" }]);
    });
  });
}

test.describe("once pressed", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  // The header is one row, so the button under it shows the press waits, and
  // the CFO's Task view says for what.
  test("the CFO's header shows the restart waits, its Task view says for the end of its turn, and a second press takes it back", async ({ page }, testInfo) => {
    // Arrange
    const sent = await open(page, snapshot({ cfo_update: { ...WAITING, pending: true } }));
    await openCfoPanel(page);

    // Assert
    await expect(line(page).getByRole("button", { name: "Cancel the Claude Code update" })).toHaveText("Cancel update");
    await expect(header(page).getByRole("status").filter({ hasText: "Restarts onto" })).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath("cfo-header-update-pending.png") });
    await header(page).getByRole("button", { name: "Task", exact: true }).click();
    await expect(panel(page).getByRole("status").filter({ hasText: "Restarts onto" })).toHaveText("Restarts onto the Claude Code update when its turn ends.");
    await header(page).getByRole("button", { name: "Chat", exact: true }).click();

    // Act
    await line(page).getByRole("button", { name: "Cancel the Claude Code update" }).click();

    // Assert
    await expect.poll(() => sent).toEqual([{ path: "/api/cfo/update", body: { cancel: true }, token: "fixture" }]);
  });

  test("a waiting update shows in a goblin's panel, never on its one-row card, and a second press there takes it back", async ({ page }, testInfo) => {
    // Arrange
    const sent = await open(page, snapshot({}, { harness_update: { harness: "claude", installed: "2026-10-07T14:10:37Z" }, pending_engine: { harness: "claude", model: "claude-opus-5-5", effort: "xhigh", when: "update" } }));

    // Assert
    await expect(card(page)).not.toContainText("Updates Claude Code");
    await expect(card(page).getByRole("button", { name: /^Cancel the update of / })).toHaveCount(0);
    await card(page).screenshot({ path: testInfo.outputPath("goblin-card-update-pending.png") });

    // Act
    await openGoblinPanel(page);
    await panel(page).getByRole("button", { name: "Cancel the update of Export invoices as CSV" }).click();

    // Assert
    await expect.poll(() => sent).toEqual([{ path: "/api/tasks/engine", body: { task: "nw-invoice-export", generation: "nw-invoice-export-1", when: "cancel" }, token: "fixture" }]);
  });

  test("with no update waiting, neither the CFO's header nor a goblin's card offers Update", async ({ page }) => {
    // Arrange
    await open(page, snapshot());
    await openCfoPanel(page);

    // Assert
    await expect(page.getByRole("button", { name: /^Update / })).toHaveCount(0);
  });
});
