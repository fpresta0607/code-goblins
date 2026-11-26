import { CEO_DOOR, expect, test, type Page } from "./site";
import { box, field, NAME, open, openGoblin, pickStyle, snapshot, typed } from "./session-board";

// What Claude Code 2.1.322's terminal drew over its prompt line, read on a
// disposable session on 2026-11-09, and its turn row while it retried (#2236).
const STATUS = { gauge: "83% context used", notices: [{ text: "Auto-update failed", check: "update" }] };
const RETRY = { reason: "529 Overloaded", attempt: 3, attempts: 10 };
const idle = { type: "screen", asking: false, open: false, working: false, submission: "" };
const statusOf = (page: Page, name = NAME) => box(page, name).getByRole("status", { name: "Terminal status" });
const talkOf = (page: Page) => page.getByRole("log", { name: "Messages of " + NAME });
// The panel's header row, and Retry update with the words of the check it
// runs, on the line under that row (#2956).
const header = (page: Page) => page.locator(".context-pane .panel-header");
const retryOf = (page: Page) => page.locator(".context-pane").getByRole("button", { name: "Retry the Claude Code update" });
const checkOf = (page: Page) => page.locator(".context-pane").getByRole("status", { name: "Update check" });

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

// The Overlord, 2026-11-26: "run claude doctor should be a button like the
// update/restart in caht pane not below the prompt line in modern terminal"
// (#2956). The gauge and any other notice stay under the box, and a failed
// update is Retry update, on the line under the chat's header row.
test("the chat shows the terminal's gauge and other notices under the box and its failed update as Retry update under the header's row, and drops them all when the terminal does", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);

  // Act
  relay.say({ ...idle, status: { ...STATUS, notices: [...STATUS.notices, { text: "Low disk space", check: "" }] } });

  // Assert
  await expect(retryOf(page)).toHaveText("Retry update");
  await expect(checkOf(page)).toHaveText("Auto-update failed");
  await expect(statusOf(page).locator("span")).toHaveText(["83% context used", "Low disk space"]);
  await expect(statusOf(page).getByRole("button")).toHaveCount(0);
  relay.say(idle);
  await expect(statusOf(page)).toHaveCount(0);
  await expect(retryOf(page)).toHaveCount(0);
  await expect(checkOf(page)).toHaveCount(0);
});

test("a notice of the terminal's that is no failed update stays under the box, and the header offers no Retry update for it", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);

  // Act
  relay.say({ ...idle, status: { ...STATUS, notices: [{ text: "Low disk space", check: "" }] } });

  // Assert
  await expect(statusOf(page).locator("span")).toHaveText(["83% context used", "Low disk space"]);
  await expect(retryOf(page)).toHaveCount(0);
  await expect(checkOf(page)).toHaveCount(0);
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
// it into the CEO's running turn three times that day. Retry update has the
// supervisor run the harness's own update check outside the conversation
// instead: nothing is typed or queued for the session, even while a turn
// runs; its words say the check is running, then why it failed; and it goes,
// with no restart, once the install is the newest. It sits on a line of its
// own right under the header's row, at the row's end, as the CEO's Update
// does (#2956).
for (const [where, width, height] of [["a phone", 393, 852], ["a desktop", 1440, 1200]] as const) {
  test(`on ${where}, a failed update is Retry update under the chat's header row and never under the box; it runs the check outside the conversation, says why it failed, and goes once the install is current`, async ({ page }, testInfo) => {
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
    const retry = retryOf(page);
    const shoot = async (name: string) => {
      const shot = testInfo.outputPath(`update-check-${where.replace("a ", "")}-${name}.png`);
      await page.screenshot({ path: shot });
      await testInfo.attach(`update-check-${where.replace("a ", "")}-${name}`, { path: shot, contentType: "image/png" });
    };

    // Assert: Retry update, right under the header's row at its end, with
    // the notice's words and no command of the terminal's, and nothing of
    // the update under the box.
    await expect(retry).toBeEnabled();
    await expect(checkOf(page)).toHaveText("Auto-update failed");
    await expect(statusOf(page)).toHaveText("83% context used");
    await expect(page.locator(".context-pane")).not.toContainText("doctor");
    const button = (await retry.boundingBox())!, row = (await header(page).boundingBox())!, corner = (await header(page).locator(".panel-corner").boundingBox())!;
    expect(button.y, "under the header's row").toBeGreaterThanOrEqual(row.y + row.height - 0.5);
    expect(button.y - (row.y + row.height), "right under it").toBeLessThan(12);
    expect(Math.abs(button.x + button.width - (corner.x + corner.width)), "at the row's end").toBeLessThanOrEqual(1);
    await shoot("offered");

    // Act: Retry update, during the turn.
    await retry.click();

    // Assert: the check is asked of the supervisor, once, and nothing is
    // typed into the session or queued for it; his words stay in the box.
    await expect.poll(() => checks).toEqual([{ harness: "claude" }]);
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, update_checks: { claude: { running: true } } }));
    await expect(retry).toBeDisabled();
    await expect(retry).toHaveText("Checking…");
    expect(typed(relay)).not.toContain("/");
    expect(actions).toEqual([]);
    await expect(field(page)).toHaveValue("half a thought");

    // Act: the check fails.
    const why = "Error: Failed to install update: EPERM, operation not permitted";
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 3, update_checks: { claude: { failed: true, result: why } } }));

    // Assert: its words say why, whole on the screen, and Retry update is
    // offered again.
    await expect(checkOf(page)).toHaveText("Auto-update failed: " + why);
    await expect(retry).toBeEnabled();
    await expect(retry).toHaveText("Retry update");
    await expect(checkOf(page)).toBeInViewport({ ratio: 1 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
    await shoot("failed");

    // Act: a later check succeeds with the install the newest, while the
    // terminal still draws its notice.
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 4, update_checks: { claude: { current: true, result: "Claude Code is up to date (2.1.322)" } } }));

    // Assert: Retry update and its words are gone and the gauge stays, and
    // with no gauge the whole row under the box goes.
    await expect(retry).toHaveCount(0);
    await expect(checkOf(page)).toHaveCount(0);
    await expect(statusOf(page)).toHaveText("83% context used");
    await shoot("current");
    relay.say({ ...idle, status: { notices: STATUS.notices } });
    await expect(statusOf(page)).toHaveCount(0);
  });
}

test("a check that ended well over an install that is not the newest says what it said beside the notice's words, and a press the supervisor refuses says why under Retry update", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({ update_checks: { claude: { result: "Successfully updated" } } }));
  await page.route("**/api/harness/update-check", (route) => route.fulfill({ status: 409, json: { error: "claude has no update check the board can run" } }));
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  relay.say({ ...idle, status: STATUS });

  // Act
  await retryOf(page).click();

  // Assert
  await expect(checkOf(page)).toHaveText("Auto-update failed · Checked again: Successfully updated");
  await expect(page.locator(".context-pane").getByText("claude has no update check the board can run")).toBeVisible();
  await expect(box(page).locator(".composer-banner")).toHaveCount(0);
});

test("a goblin's Retry update checks the goblin's own harness, not the CEO's, and goes once that harness's install is current", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({ ceo_harness: "codex" }));
  const checks: unknown[] = [];
  await page.route("**/api/harness/update-check", (route) => {
    checks.push(route.request().postDataJSON());
    return route.fulfill({ status: 202, json: { running: true } });
  });
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  relay.say({ ...idle, status: STATUS });

  // Act
  await retryOf(page).click();

  // Assert
  await expect.poll(() => checks).toEqual([{ harness: "claude" }]);

  // Act: Claude Code's check finds its install the newest.
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2, ceo_harness: "codex", update_checks: { claude: { current: true, result: "Claude Code is up to date (2.1.322)" } } }));

  // Assert
  await expect(retryOf(page)).toHaveCount(0);
  await expect(checkOf(page)).toHaveCount(0);
});

test("the CEO's chat offers a failed update as Retry update beside its Update, under its header row, and nothing of it under the box", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({ ceo_update: { harness: "claude", line: "✓ Update installed · Restart to update", installed: "2026-10-07T14:10:37Z" } }));
  await page.locator(".ceo-pin").getByRole("button", { name: CEO_DOOR }).last().click();
  await expect(header(page).locator("#panel-title")).toHaveText("CEO");
  await expect.poll(() => relay.views).toBeGreaterThan(0);

  // Act
  relay.say({ ...idle, status: STATUS });

  // Assert
  const line = page.locator(".ceo-header-line");
  await expect(line.getByRole("button", { name: "Update Claude Code" })).toBeVisible();
  await expect(line.getByRole("button", { name: "Retry the Claude Code update" })).toBeVisible();
  await expect(line.getByRole("status", { name: "Update check" })).toHaveText("Auto-update failed");
  await expect(statusOf(page, "CEO")).toHaveText("83% context used");
});

test("the Terminal style leaves a failed update to the terminal, with no Retry update in the header, and Modern offers it again", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  relay.say({ ...idle, status: STATUS });
  await expect(retryOf(page)).toBeVisible();

  // Act
  await pickStyle(page, "Terminal");

  // Assert
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeHidden();
  await expect(retryOf(page)).toHaveCount(0);
  await expect(checkOf(page)).toHaveCount(0);
  await pickStyle(page, "Modern chat");
  await expect(retryOf(page)).toBeVisible();
});

for (const phase of ["paused", "resuming"] as const) {
  test(`a ${phase} goblin offers no Retry update, though its terminal said its update failed`, async ({ page }) => {
    // Arrange
    const relay = await open(page);
    await openGoblin(page);
    await expect.poll(() => relay.views).toBeGreaterThan(0);
    relay.say({ ...idle, status: STATUS });
    await expect(retryOf(page)).toBeVisible();

    // Act
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2 }, { phase }));

    // Assert
    await expect(retryOf(page)).toHaveCount(0);
    await expect(checkOf(page)).toHaveCount(0);
    await expect(header(page).locator("#panel-title")).toHaveText(NAME);
  });
}

test("a goblin's header never draws Retry update as it goes from pausing to paused, though its terminal said its update failed", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  relay.say({ ...idle, status: STATUS });
  await expect(retryOf(page)).toBeVisible();
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2 }, { phase: "pausing" }));
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeHidden();
  await expect(retryOf(page)).toHaveCount(0);
  await page.evaluate(() => {
    const added: string[] = [];
    Object.assign(window, { retryUpdatesAdded: added });
    const retry = 'button[aria-label="Retry the Claude Code update"]';
    new MutationObserver((records) => {
      for (const node of records.flatMap((record) => [...record.addedNodes])) {
        if (node instanceof Element && (node.matches(retry) || node.querySelector(retry))) added.push(node.textContent ?? "");
      }
    }).observe(document.body, { subtree: true, childList: true });
  });

  // Act
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 3 }, { phase: "paused" }));

  // Assert
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
  await expect(retryOf(page)).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { retryUpdatesAdded: string[] }).retryUpdatesAdded), "Retry update added on the way to paused").toEqual([]);
});
