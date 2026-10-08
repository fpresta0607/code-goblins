import { expect, holdStream, test, type Locator, type Page } from "./site";

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
const card = (page: Page) => page.locator(".task-card-shell").filter({ hasText: "Export invoices as CSV" });

async function openCfoPanel(page: Page) {
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(header(page).locator("#panel-title")).toHaveText("CFO");
}

for (const [width, height] of [[1280, 800], [390, 844]]) {
  test.describe(`at ${width} px`, () => {
    test.use({ viewport: { width, height } });

    test("the CFO's header offers Update beside the AFK switch while an update waits, and his press asks for it once", async ({ page }, testInfo) => {
      // Arrange
      const sent = await open(page, snapshot({ cfo_update: WAITING }));
      await openCfoPanel(page);
      const update = header(page).getByRole("button", { name: "Update Claude Code" });

      // Assert: beside the switch, on one row with it at every width.
      await expect(update).toBeVisible();
      await header(page).screenshot({ path: testInfo.outputPath(`cfo-header-update-${width}.png`) });
      await expect(update).toHaveAttribute("data-tip", "Claude Code was updated. Restart the CFO onto it at its next stopping point. Its conversation is kept.");
      const button = (await update.boundingBox())!, toggle = (await header(page).getByRole("switch", { name: "AFK mode" }).boundingBox())!;
      expect(Math.abs(button.y + button.height / 2 - (toggle.y + toggle.height / 2))).toBeLessThan(4);
      expect(toggle.x - (button.x + button.width)).toBeLessThan(32);
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);

      // Act
      await update.click();

      // Assert
      await expect.poll(() => sent).toEqual([{ path: "/api/cfo/update", body: {}, token: "fixture" }]);
    });

    test("a goblin's card offers Update among its controls while an update waits, and his press asks for its own update once", async ({ page }, testInfo) => {
      // Arrange
      const sent = await open(page, snapshot({}, { harness_update: { harness: "claude", installed: "2026-10-07T14:10:37Z" } }));
      const update = card(page).getByRole("button", { name: "Update Export invoices as CSV" });

      // Assert
      await expect(update).toBeVisible();
      await expect(update).toHaveAttribute("data-tip", "Claude Code was updated. Restart this goblin onto it at its next stopping point. Its conversation is kept.");
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
      await card(page).screenshot({ path: testInfo.outputPath(`goblin-card-update-${width}.png`) });

      // The Overlord, 2026-10-07: "update button on task cards is ugly". It
      // wears the look of the card's other controls.
      const look = (button: Locator) => button.evaluate((element) => { const style = getComputedStyle(element); return [style.width, style.borderTopColor, style.color, style.boxShadow, style.backgroundColor].join(" | "); });
      expect(await look(update)).toBe(await look(card(page).getByRole("button", { name: "Pause Export invoices as CSV" })));

      // Act
      await update.click();

      // Assert
      await expect.poll(() => sent).toEqual([{ path: "/api/tasks/engine", body: { task: "nw-invoice-export", generation: "nw-invoice-export-1", when: "update" }, token: "fixture" }]);
    });
  });
}

test.describe("once pressed", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("the CFO's header says the restart waits for the end of its turn, and a second press takes it back", async ({ page }, testInfo) => {
    // Arrange
    const sent = await open(page, snapshot({ cfo_update: { ...WAITING, pending: true } }));
    await openCfoPanel(page);

    // Assert
    await expect(header(page).getByRole("status")).toHaveText("Restarts onto the Claude Code update when its turn ends.");
    await page.screenshot({ path: testInfo.outputPath("cfo-header-update-pending.png") });

    // Act
    await header(page).getByRole("button", { name: "Cancel the Claude Code update" }).click();

    // Assert
    await expect.poll(() => sent).toEqual([{ path: "/api/cfo/update", body: { cancel: true }, token: "fixture" }]);
  });

  test("a goblin's card says it updates at its next stopping point, and a second press takes it back", async ({ page }, testInfo) => {
    // Arrange
    const sent = await open(page, snapshot({}, { harness_update: { harness: "claude", installed: "2026-10-07T14:10:37Z" }, pending_engine: { harness: "claude", model: "claude-opus-5-5", effort: "xhigh", when: "update" } }));

    // Assert
    await expect(card(page)).toContainText("Updates Claude Code at its next stopping point");
    await card(page).screenshot({ path: testInfo.outputPath("goblin-card-update-pending.png") });

    // Act
    await card(page).getByRole("button", { name: "Cancel the update of Export invoices as CSV" }).click();

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
