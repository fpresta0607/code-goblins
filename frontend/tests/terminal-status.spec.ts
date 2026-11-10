import { expect, test } from "./site";
import { box, field, NAME, open, openGoblin, typed } from "./session-board";

// What Claude Code 2.1.322's terminal drew over its prompt line, read on a
// disposable session on 2026-11-09, and its turn row while it retried (#2236).
const STATUS = { gauge: "83% context used", notices: [{ text: "Auto-update failed" }, { text: "Run claude doctor", action: "/doctor" }] };
const RETRY = { reason: "529 Overloaded", attempt: 3, attempts: 10 };
const idle = { type: "screen", asking: false, open: false, working: false, submission: "" };
const statusOf = (page: Parameters<typeof box>[0]) => box(page).getByRole("status", { name: "Terminal status" });
const talkOf = (page: Parameters<typeof box>[0]) => page.getByRole("log", { name: "Messages of " + NAME });

test("the chat shows the terminal's context gauge and notices over the box, and drops them when the terminal does", async ({ page }) => {
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
