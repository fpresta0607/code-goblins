import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-09, of Bernie's paused panel, his message "i can
// message?" marked Kept for resume: "why is there no delete button for paused
// queued messages? nor the same send button as Scrawl or microphone
// dictation". The box under a paused goblin sends with a button, dictates
// through the terminals' own dictation while its microphone is held, and
// deletes a message a resume still carries.

// Chromium's fake microphone (periodic beeps) stands in for the Overlord's.
test.use({
  timezoneId: "UTC",
  viewport: { width: 1440, height: 900 },
  permissions: ["microphone"],
  launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] },
});

const PAUSED_AT = "2026-10-09T06:11:00Z";

// Bernie paused by the Overlord, with the messages his resume note will
// carry, one typed into his terminal before he paused, and the action that
// kept "i can message?", which the box does not list a second time.
function bernie(keptMessages: string[]) {
  return {
    healthy: true, instance: "message-box-proof", revision: 3 - keptMessages.length, cfo_runs: true,
    tasks: [{
      id: "bernie", title: "Land the merge train", goblin_name: "Bernie", goblin_title: "Merge Maestro", project: "code-goblins", backend: "native", harness: "claude",
      generation: "s1", session: "claude/bernie", phase: "paused", verified: false, at: PAUSED_AT, report: "working",
      lifecycle: { phase: "paused", action: "pause", at: PAUSED_AT, kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason: "overlord", until: "", at: PAUSED_AT }, kept_messages: keptMessages },
    }],
    actions: [
      { id: "typed", kind: "message", task_id: "bernie", generation: "s1", status: "succeeded", message: "Accepted by the goblin in its native terminal.", text: "merge 512 first", updated_at: "2026-10-09T05:50:00Z" },
      { id: "kept", kind: "message", task_id: "bernie", generation: "s1", status: "succeeded", message: "Kept for its resume.", text: "i can message?", updated_at: "2026-10-09T12:04:00Z" },
    ],
  };
}

// No CFO runs: its terminal ended, as it does when he closes it and for a
// moment while it restarts.
const CLOSED_CFO = { healthy: true, instance: "message-box-proof", revision: 1, cfo_runs: false, cfo_closed: true, tasks: [], actions: [] };

// No CFO runs in a home that has had none, whose board he opened without one.
const NO_CFO = { ...CLOSED_CFO, cfo_closed: false };
// What its first-run page reads of the machine before it offers the board.
const SETUP = { home: "C:\\Users\\franco\\AppData\\Local\\CodeGoblins", agent: "claude", projects_root: "", checkouts: [], agents: [{ id: "claude", name: "Claude Code", recommended: true, note: "the best experience", installed: true, sign_in: "signed_in" }], cfo_runs: false };

// open serves the real board its snapshots through a stand-in event stream,
// keeps each action the board posts and accepts it, and has the supervisor's
// speech model hear "rebase onto main" in every sound. publish hands the
// board its next snapshot.
async function open(page: Page, first: object) {
  const posted: { token: string | null; body: Record<string, unknown> }[] = [];
  const heard: { token: string | null; type: string | null }[] = [];
  await page.addInitScript((snapshot) => {
    localStorage.setItem("cfo-first-open", "shown");
    class SnapshotSource extends EventTarget {
      private publish = (event: Event) => {
        if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(event.detail) }));
      };
      constructor() {
        super();
        window.addEventListener("fixture-snapshot", this.publish);
        queueMicrotask(() => this.publish(new CustomEvent("fixture-snapshot", { detail: snapshot })));
      }
      close() { window.removeEventListener("fixture-snapshot", this.publish); }
    }
    Object.defineProperty(window, "EventSource", { value: SnapshotSource });
  }, first);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.route("**/api/setup?root=*", (route) => route.fulfill({ json: SETUP }));
  await page.route("**/api/actions", async (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    posted.push({ token: await route.request().headerValue("X-CFO-Token"), body });
    return route.fulfill({ status: 202, json: { ...body, status: body.kind === "message_withdraw" ? "succeeded" : "queued" } });
  });
  await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
  await page.route("**/api/dictation", async (route) => {
    if (route.request().method() === "GET") return route.fulfill({ json: { engine: "test-model", state: "ready" } });
    heard.push({ token: await route.request().headerValue("X-CFO-Token"), type: await route.request().headerValue("Content-Type") });
    return route.fulfill({ json: { text: "rebase onto main", engine: "test-model" } });
  });
  await page.goto("/");
  const publish = (next: object) => page.evaluate((detail) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail })), next);
  return { posted, heard, publish };
}

async function openBernie(page: Page, first: object) {
  const opened = await open(page, first);
  await page.locator(".task-card").filter({ hasText: "Bernie" }).click();
  return { ...opened, box: page.getByRole("region", { name: "Messages to Bernie - Merge Maestro" }) };
}

test("Send sends what he wrote to a paused goblin, as Enter does", async ({ page }) => {
  // Arrange
  const { box, posted } = await openBernie(page, bernie(["i can message?"]));
  const draft = box.getByRole("textbox", { name: "Message Bernie - Merge Maestro" });
  const send = box.getByRole("button", { name: "Send", exact: true });
  await expect(send).toBeDisabled();

  // Act
  await draft.fill("rebase first");
  await send.click();

  // Assert
  await expect(draft).toHaveValue("");
  expect(posted).toEqual([{ token: "message-box-proof", body: { id: expect.any(String), kind: "message", task_id: "bernie", generation: "", text: "rebase first" } }]);
  await expect(send).toHaveClass(/primary send-decision/);
});

test("holding the microphone dictates into the box for a paused goblin, through the supervisor's speech model", async ({ page }) => {
  // Arrange
  const { box, heard, posted } = await openBernie(page, bernie(["i can message?"]));
  const draft = box.getByRole("textbox", { name: "Message Bernie - Merge Maestro" });
  const microphone = box.getByRole("button", { name: "Hold to dictate" });
  await draft.fill("please");

  // Act
  await microphone.hover();
  await page.mouse.down();
  await expect(box.getByRole("button", { name: "Listening" })).toHaveClass(/recording/);
  await page.waitForTimeout(1200);
  await page.mouse.up();

  // Assert
  await expect(draft).toHaveValue("please rebase onto main");
  await expect(box.getByRole("button", { name: "Hold to dictate" })).not.toHaveClass(/recording/);
  expect(heard).toEqual([{ token: "message-box-proof", type: "audio/wav" }]);
  expect(posted, "dictating sends nothing by itself").toEqual([]);
});

test("a message his resume will carry has a delete button, which withdraws it, and one already delivered has none", async ({ page }) => {
  // Arrange
  const { box, posted, publish } = await openBernie(page, bernie(["i can message?"]));
  const kept = box.getByRole("listitem").filter({ hasText: "i can message?" });
  const typed = box.getByRole("listitem").filter({ hasText: "merge 512 first" });
  await expect(box.getByRole("listitem")).toHaveCount(2);
  await expect(kept).toContainText("Kept for resume");
  await expect(typed).toContainText("Sent");
  await expect(typed.getByRole("button")).toHaveCount(0);

  // Act
  await kept.getByRole("button", { name: "Delete: i can message?" }).click();
  await expect.poll(() => posted.length).toBe(1);
  await publish(bernie([]));

  // Assert
  expect(posted).toEqual([{ token: "message-box-proof", body: { id: expect.any(String), kind: "message_withdraw", task_id: "bernie", generation: "", text: "i can message?" } }]);
  await expect(kept).toHaveCount(0);
  await expect(box.getByRole("listitem")).toHaveText([/merge 512 first/]);
});

// The Overlord, 2026-10-09, after a restart of the CFO showed a box to send
// it a message: "when cfo is off no message box should exist". The CFO's
// slot says why it has no terminal and offers nothing to write in.
for (const { state, first, says } of [
  { state: "it is closed, as it is for a moment while it restarts", first: CLOSED_CFO, says: "The CFO is closed." },
  { state: "none runs", first: NO_CFO, says: "No CFO is running." },
]) {
  test(`the CFO's slot has no message box while ${state}`, async ({ page }) => {
    // Arrange
    await open(page, first);
    if (!first.cfo_closed) await page.getByRole("button", { name: "Open the board without a CFO" }).click();

    // Act: Orchestration opens on the CFO's terminal.
    await page.getByRole("button", { name: "Orchestration", exact: true }).click();

    // Assert
    const deck = page.locator(".terminal-deck");
    await expect(deck).toContainText(says);
    await expect(page.getByRole("region", { name: /^Messages to/ })).toHaveCount(0);
    await expect(deck.getByRole("textbox")).toHaveCount(0);
  });
}
