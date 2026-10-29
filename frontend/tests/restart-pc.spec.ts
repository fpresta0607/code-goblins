import { expect, servePages, test, type Page } from "./site";

// The Overlord, 2026-10-18, after the kernel's pools held 11 GB four days into
// a boot: "the header shows the restart button in terminal cfo for safe pc
// restart". Past the line measured on this PC the line under the CFO's header
// row offers Restart PC on the board on the PC itself, never on his private
// board, as AFK mode's on. The supervisor is played here: snapshots on a stream the test moves on,
// and what the board sends is collected.
const GB = 2 ** 30;
const PRIVATE_BOARD = "https://my-desktop.example.ts.net:4311";
const machine = { available: 4.2 * GB, total: 32 * GB, commit_available: 18 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, pool_line: 7.5 * GB, pool_growth: [{ tag: "Toke", pool: "paged", grown: 3.2 * GB, holds: 3.5 * GB }] };
const PAST = { ...machine, paged_pool: 7.61 * GB, nonpaged_pool: 3.35 * GB };
const UNDER = { ...machine, paged_pool: 0.68 * GB, nonpaged_pool: 1.15 * GB };
const BOARD = { healthy: true, instance: "fixture", revision: 1, attention: [], tasks: [], afk: { state: "off" }, cfo_runs: true, cfo_harness: "claude", sessions: [{ id: "cfo-1", harness: "claude", role: "cfo" }] };

interface Sent { body: unknown; token: string }

// open shows the CFO's panel with memory, on the board on this PC or, with
// origin, as a phone sees the private board, and collects each press of
// Restart PC the board sends, which refusal refuses as the supervisor would.
// header is the header's row, and line the line under it.
async function open(page: Page, memory: object, { origin = "", refusal = "" } = {}) {
  const sent: Sent[] = [];
  await page.addInitScript((data) => {
    class LiveStream extends EventTarget {
      onerror: (() => void) | null = null;
      constructor() {
        super();
        Object.assign(window, { pushSnapshot: (next: string) => this.dispatchEvent(new MessageEvent("snapshot", { data: next })) });
        setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data })));
      }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: LiveStream });
  }, JSON.stringify({ ...BOARD, memory }));
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.route("**/api/pc/restart", (route) => {
    sent.push({ body: route.request().postDataJSON(), token: route.request().headers()["x-cfo-token"] });
    return refusal ? route.fulfill({ status: 403, json: { error: refusal } }) : route.fulfill({ status: 202, json: { state: "stopping" } });
  });
  if (origin) {
    await servePages(page.context(), origin);
    await page.goto(origin + "/");
  } else await page.goto("/");
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(page.locator(".panel-header #panel-title")).toHaveText("CFO");
  const push = (snapshot: object) => page.evaluate((data) => (window as unknown as { pushSnapshot: (data: string) => void }).pushSnapshot(data), JSON.stringify({ ...BOARD, revision: 2, ...snapshot }));
  return { sent, push, header: page.locator(".panel-header"), line: page.locator(".cfo-header-line") };
}

for (const [width, height] of [[1280, 800], [390, 844]]) {
  test.describe(`at ${width} px`, () => {
    test.use({ viewport: { width, height } });

    test("past the line the CFO's header offers Restart PC on a line under its row, asks once, and his yes asks the supervisor once", async ({ page }, testInfo) => {
      // Arrange
      const { sent, push, header, line } = await open(page, PAST);
      const restart = line.getByRole("button", { name: "Restart the PC" });

      // Assert: on a line of its own right under the header's row, at the
      // row's end, at every width, so the row keeps the AFK switch and Close.
      await expect(restart).toBeVisible();
      await page.locator(".goblin-panel").screenshot({ path: testInfo.outputPath(`cfo-header-restart-pc-${width}.png`) });
      const button = (await restart.boundingBox())!, row = (await header.boundingBox())!, corner = (await header.locator(".panel-corner").boundingBox())!;
      expect(button.y).toBeGreaterThanOrEqual(row.y + row.height - 0.5);
      expect(button.y - (row.y + row.height)).toBeLessThan(12);
      expect(Math.abs(button.x + button.width - (corner.x + corner.width))).toBeLessThanOrEqual(1);
      await expect(header.getByRole("switch", { name: "AFK mode" })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);

      // Act: he presses it, reads the question, and says yes.
      await restart.click();
      const dialog = page.getByRole("dialog", { name: "Restart the PC?" });
      await expect(dialog).toBeVisible();
      await dialog.screenshot({ path: testInfo.outputPath(`restart-pc-question-${width}.png`) });
      await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
      await expect(dialog).toContainText("The kernel's pools hold 11.0 GB, past the 7.5 GB line where free memory can no longer hold the fleet.");
      await expect(dialog.getByRole("listitem")).toHaveText([
        "The CFO is told first.",
        "Every working goblin writes its handoff and pauses until the PC has restarted.",
        "The heavy runs end, then Windows restarts. An app with unsaved work asks you first.",
        "After you sign in, the CFO and the goblins come back by themselves.",
      ]);
      await dialog.getByRole("button", { name: "Restart PC" }).click();

      // Assert: one request, and the header says the PC restarts once the
      // supervisor does.
      await expect(dialog).toBeHidden();
      await expect.poll(() => sent).toEqual([{ body: {}, token: "fixture" }]);
      await push({ memory: PAST, pc_restart: { state: "stopping", problem: "", at: "2026-10-18T22:30:00Z" } });
      const restarting = line.getByRole("button", { name: "Restarting the PC…" });
      await expect(restarting).toBeDisabled();
      await expect(line.getByRole("button", { name: "Restart the PC" })).toHaveCount(0);
    });
  });
}

test("Cancel restarts nothing", async ({ page }) => {
  // Arrange
  const { sent, line } = await open(page, PAST);

  // Act
  await line.getByRole("button", { name: "Restart the PC" }).click();
  await page.getByRole("dialog", { name: "Restart the PC?" }).getByRole("button", { name: "Cancel" }).click();

  // Assert
  await expect(page.getByRole("dialog", { name: "Restart the PC?" })).toBeHidden();
  expect(sent).toEqual([]);
});

test("under the line there is no Restart PC", async ({ page }) => {
  // Arrange and act
  const { header, line } = await open(page, UNDER);

  // Assert: and the line under the header's row takes no room.
  await expect(header.getByRole("switch", { name: "AFK mode" })).toBeVisible();
  await expect(line.getByRole("button", { name: "Restart the PC" })).toHaveCount(0);
  await expect(line).toBeHidden();
});

// A restart ends every program on the PC, so it is pressed on the PC: his
// private board shows no Restart PC however far past the line the pools are.
test("his private board never offers Restart PC", async ({ page }) => {
  // Arrange and act
  const { header, line, push } = await open(page, PAST, { origin: PRIVATE_BOARD });
  await push({ memory: PAST, pc_restart: { state: "stopped", problem: "not every working goblin paused: second-goblin (failed)", at: "2026-10-18T22:30:00Z" } });

  // Assert
  await expect(header.getByRole("switch", { name: "AFK mode" })).toBeVisible();
  await expect(line.getByRole("button", { name: /Restart the PC|Restarting/ })).toHaveCount(0);
  await expect(line.getByText(/The PC did not restart/)).toHaveCount(0);
});

// What a press could not do says so in a few words under the button that
// go by themselves, as Update's do, and the CFO is told why: the Overlord,
// 2026-10-08, "everything error wise goes to cfo". They lie over the
// conversation, so they neither widen the line nor move the conversation.
test("a press the supervisor refuses says so under the button and restarts nothing", async ({ page }) => {
  // Arrange
  const { sent, line } = await open(page, PAST, { refusal: "Press Restart PC in the Code Goblins window on this PC." });
  const before = (await line.boundingBox())!;

  // Act
  await line.getByRole("button", { name: "Restart the PC" }).click();
  const dialog = page.getByRole("dialog", { name: "Restart the PC?" });
  await dialog.getByRole("button", { name: "Restart PC" }).click();

  // Assert
  await expect(dialog).toBeHidden();
  await expect(line.locator(".notice")).toHaveText("Press Restart PC in the Code Goblins window on this PC.");
  await expect(line.getByRole("button", { name: "Restart the PC" })).toBeEnabled();
  expect(await line.boundingBox()).toEqual(before);
  expect((await line.locator(".notice").boundingBox())!.y).toBeGreaterThanOrEqual(before.y + before.height);
  expect(sent).toHaveLength(1);
});

test("a restart that stopped short says so under the button and offers Restart PC again", async ({ page }) => {
  // Arrange
  const { line, push } = await open(page, PAST);

  // Act
  await push({ memory: PAST, pc_restart: { state: "stopped", problem: "not every working goblin paused: second-goblin (failed)", at: "2026-10-18T22:30:00Z" } });

  // Assert
  await expect(line.locator(".notice")).toHaveText("The PC did not restart, and the CFO knows why.");
  await expect(line.getByRole("button", { name: "Restart the PC" })).toBeEnabled();
});
