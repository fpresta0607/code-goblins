import { expect, test } from "./site";
import { box, field, NAME, open, openGoblin, snapshot, typed } from "./session-board";

// What Claude Code 2.1.322's terminal drew over its prompt line, read on a
// disposable session on 2026-11-09, and its turn row while it retried (#2236).
const STATUS = { gauge: "83% context used", notices: [{ text: "Auto-update failed", check: "update" }] };
const RETRY = { reason: "529 Overloaded", attempt: 3, attempts: 10 };
const idle = { type: "screen", asking: false, open: false, working: false, submission: "" };
const statusOf = (page: Parameters<typeof box>[0]) => box(page).getByRole("status", { name: "Terminal status" });
const talkOf = (page: Parameters<typeof box>[0]) => page.getByRole("log", { name: "Messages of " + NAME });

// #2294 (the Overlord, 2026-11-10): "2% until auto-compact" sat above the box
// he types in. The terminal draws it under its prompt, so the chat draws it
// under the box, clear of it, on a desktop and on a phone, idle and working.
const AUTO_COMPACT = { ...STATUS, gauge: "2% until auto-compact" };
for (const [where, width, height] of [["a desktop", 1440, 1200], ["a phone", 393, 852]] as const) {
  for (const state of ["idle", "working"] as const) {
    test(`the terminal's gauge sits under the box on ${where} while the session is ${state}, clear of the box and with no sideways scroll`, async ({ page }, testInfo) => {
      // Arrange
      await page.setViewportSize({ width, height });
      const relay = await open(page, snapshot({}, { phase: "idle" }));
      await openGoblin(page);
      await expect.poll(() => relay.views).toBeGreaterThan(0);

      // Act
      relay.say(state === "working" ? { ...idle, working: true, doing: "Rummaging", status: AUTO_COMPACT } : { ...idle, status: AUTO_COMPACT });

      // Assert: the session shows as it is, and the gauge is whole on the
      // screen, starting where the box ends or lower.
      await expect(box(page).locator(".composer-send")).toHaveAccessibleName(state === "working" ? "Stop" : "Send");
      await expect(talkOf(page).locator(".reply-working")).toHaveCount(state === "working" ? 1 : 0);
      await expect(statusOf(page)).toContainText("2% until auto-compact");
      await expect(statusOf(page)).toBeInViewport({ ratio: 1 });
      const gauge = (await statusOf(page).boundingBox())!;
      const input = (await box(page).locator(".composer-input").boundingBox())!;
      expect(gauge.y, "the gauge is under the box").toBeGreaterThanOrEqual(input.y + input.height);
      expect(gauge.x, "the gauge starts on the screen").toBeGreaterThanOrEqual(0);
      expect(gauge.x + gauge.width, "the gauge ends on the screen").toBeLessThanOrEqual(width);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
      const shot = testInfo.outputPath(`composer-${where.replace("a ", "")}-${state}.png`);
      await page.screenshot({ path: shot });
      await testInfo.attach(`composer-${where.replace("a ", "")}-${state}`, { path: shot, contentType: "image/png" });
    });
  }
}

test("the chat shows the terminal's context gauge and notices under the box, and drops them when the terminal does", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);

  // Act
  relay.say({ ...idle, status: STATUS });

  // Assert
  await expect(statusOf(page)).toContainText("83% context used");
  await expect(statusOf(page).locator(".terminal-notice")).toHaveText("Auto-update failed");
  await expect(statusOf(page).getByRole("button", { name: "Retry" })).toBeVisible();
  relay.say(idle);
  await expect(statusOf(page)).toHaveCount(0);
});

test("a retry the terminal shows reads Retrying with its attempt and failure, and gives way to the turn once a request goes through", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  const talk = talkOf(page);

  // Act
  relay.say({ ...idle, working: true, retry: RETRY });

  // Assert
  await expect(talk.getByRole("status", { name: "Retrying" })).toBeVisible();
  await expect(talk.locator(".reply-retry")).toHaveText("attempt 3 of 10 · 529 Overloaded");
  relay.say({ ...idle, working: true, retry: { ...RETRY, attempt: 4 } });
  await expect(talk.locator(".reply-retry")).toHaveText("attempt 4 of 10 · 529 Overloaded");
  relay.say({ ...idle, working: true, doing: "Cogitating" });
  await expect(talk.getByRole("status", { name: "Cogitating" })).toBeVisible();
  await expect(talk.locator(".reply-retry")).toHaveCount(0);
});

// The Overlord, 2026-11-10: "the Auto-update failed notice needs a reason and
// a Retry" (#2333). The terminal's own words for it tell him to run claude
// doctor, whose /doctor is a prompt that starts a turn, and the board typed
// it into the CEO's running turn three times that day. Retry has the
// supervisor run the harness's own update check outside the conversation
// instead: nothing is typed or queued for the session, even while a turn
// runs; the notice says the check is running, then why it failed; and it
// goes, with no restart, once the install is the newest.
for (const [where, width, height] of [["a phone", 393, 852], ["a desktop", 1440, 1200]] as const) {
  test(`on ${where}, the failed update notice offers Retry, which runs the check outside the conversation, says why it failed, and goes once the install is current`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height });
    const relay = await open(page);
    const checks: unknown[] = [], actions: unknown[] = [];
    await page.route("**/api/harness/update-check", (route) => {
      checks.push(route.request().postDataJSON());
      return route.fulfill({ status: 202, json: { running: true } });
    });
    await page.route("**/api/actions", (route) => {
      actions.push(route.request().postDataJSON());
      return route.fulfill({ status: 202, json: { status: "ok" } });
    });
    await openGoblin(page);
    await expect.poll(() => relay.views).toBeGreaterThan(0);
    relay.say({ ...idle, working: true, doing: "Cogitating", status: STATUS });
    await field(page).fill("half a thought");
    const retry = statusOf(page).getByRole("button", { name: "Retry" });
    const shoot = async (name: string) => {
      const shot = testInfo.outputPath(`update-notice-${where.replace("a ", "")}-${name}.png`);
      await page.screenshot({ path: shot });
      await testInfo.attach(`update-notice-${where.replace("a ", "")}-${name}`, { path: shot, contentType: "image/png" });
    };

    // Assert: the notice, with Retry and no command of the terminal's.
    await expect(statusOf(page).locator(".terminal-notice")).toHaveText("Auto-update failed");
    await expect(retry).toBeEnabled();
    await expect(statusOf(page)).not.toContainText("doctor");
    await shoot("offered");

    // Act: Retry, during the turn.
    await retry.click();

    // Assert: the check is asked of the supervisor, once, and nothing is
    // typed into the session or queued for it; his words stay in the box.
    await expect.poll(() => checks).toEqual([{ harness: "claude" }]);
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, update_checks: { claude: { running: true } } }));
    await expect(statusOf(page).getByRole("button", { name: "Checking" })).toBeDisabled();
    expect(typed(relay)).not.toContain("/");
    expect(actions).toEqual([]);
    await expect(field(page)).toHaveValue("half a thought");

    // Act: the check fails.
    const why = "Error: Failed to install update: EPERM, operation not permitted";
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 3, update_checks: { claude: { failed: true, result: why } } }));

    // Assert: the notice says why, whole on the screen, and offers Retry again.
    await expect(statusOf(page).locator(".terminal-notice")).toHaveText("Auto-update failed: " + why);
    await expect(retry).toBeEnabled();
    await expect(statusOf(page)).toBeInViewport({ ratio: 1 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
    await shoot("failed");

    // Act: a later check succeeds with the install the newest, while the
    // terminal still draws its notice.
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 4, update_checks: { claude: { current: true, result: "Claude Code is up to date (2.1.322)" } } }));

    // Assert: the notice is gone and the gauge stays, and with no gauge the
    // whole row goes.
    await expect(statusOf(page)).toHaveText("83% context used");
    await shoot("current");
    relay.say({ ...idle, status: { notices: STATUS.notices } });
    await expect(statusOf(page)).toHaveCount(0);
  });
}

test("a check that ended well over an install that is not the newest says what it said beside the notice, and a Retry the supervisor refuses says why over the box", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({ update_checks: { claude: { result: "Successfully updated" } } }));
  await page.route("**/api/harness/update-check", (route) => route.fulfill({ status: 409, json: { error: "claude has no update check the board can run" } }));
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  relay.say({ ...idle, status: STATUS });

  // Act
  await statusOf(page).getByRole("button", { name: "Retry" }).click();

  // Assert
  await expect(statusOf(page).locator(".terminal-notice")).toHaveText("Auto-update failed");
  await expect(statusOf(page).locator(".terminal-check-result")).toHaveText("Checked again: Successfully updated");
  await expect(box(page).locator(".composer-banner")).toHaveText("claude has no update check the board can run");
});
