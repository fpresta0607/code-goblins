import { expect, test } from "./site";
import { box, field, NAME, open, openGoblin, snapshot, typed } from "./session-board";

// What Claude Code 2.1.322's terminal drew over its prompt line, read on a
// disposable session on 2026-11-09, and its turn row while it retried (#2236).
const STATUS = { gauge: "83% context used", notices: [{ text: "Auto-update failed" }, { text: "Run claude doctor", action: "/doctor" }] };
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
  await expect(statusOf(page).getByRole("button", { name: "Run claude doctor" })).toBeVisible();
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

test("the doctor notice sends /doctor into a waiting session and leaves his words in the box", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  relay.say({ ...idle, status: STATUS });
  await field(page).fill("half a thought");

  // Act
  await statusOf(page).getByRole("button", { name: "Run claude doctor" }).click();

  // Assert
  await expect.poll(() => typed(relay)).toContain("/doctor");
  await expect(field(page)).toHaveValue("half a thought");
  await expect(statusOf(page).getByRole("button", { name: "Run claude doctor" })).toBeDisabled();
  relay.say({ ...idle, status: STATUS });
  await expect(statusOf(page).getByRole("button", { name: "Run claude doctor" })).toBeEnabled();
});

test("the doctor notice during a turn is held as his queued /doctor, not typed into the turn", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  const posts: { kind: string; text: string }[] = [];
  await page.route("**/api/actions", (route) => {
    const body = route.request().postDataJSON() as { kind: string; text?: string };
    posts.push({ kind: body.kind, text: body.text ?? "" });
    return route.fulfill({ status: 202, json: { status: "ok" } });
  });
  await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  relay.say({ ...idle, working: true, doing: "Cogitating", status: STATUS });
  await field(page).fill("half a thought");

  // Act
  await statusOf(page).getByRole("button", { name: "Run claude doctor" }).click();

  // Assert
  await expect.poll(() => posts).toEqual([{ kind: "message", text: "/doctor" }]);
  expect(typed(relay)).not.toContain("/doctor");
  await expect(field(page)).toHaveValue("half a thought");
});
