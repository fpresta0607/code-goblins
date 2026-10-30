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
      alive: ReturnType<typeof setInterval> | undefined;
      constructor(address: string) {
        super();
        if (new URL(address, location.href).pathname === "/api/messages") {
          queueMicrotask(() => this.dispatchEvent(new MessageEvent("page", { data: JSON.stringify({ messages: [
            { id: "typed", seq: 0, kind: "prompt", source: "overlord", text: "merge 512 first" },
          ], more: false }) })));
          // A message stream says alive every five seconds, as the
          // supervisor's does while it has nothing to send.
          this.alive = setInterval(() => this.dispatchEvent(new MessageEvent("alive", { data: "{}" })), 5000);
          return;
        }
        window.addEventListener("fixture-snapshot", this.publish);
        queueMicrotask(() => this.publish(new CustomEvent("fixture-snapshot", { detail: snapshot })));
      }
      close() { window.removeEventListener("fixture-snapshot", this.publish); clearInterval(this.alive); }
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
  await expect(send).toHaveClass(/composer-send/);
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

// hearSilence has the microphone hear silence, in which the speech model
// hears no words.
async function hearSilence(page: Page) {
  await page.route("**/api/dictation", (route) => route.request().method() === "GET"
    ? route.fulfill({ json: { engine: "test-model", state: "ready" } })
    : route.fulfill({ json: { text: "", engine: "test-model" } }));
  await page.evaluate(() => {
    navigator.mediaDevices.getUserMedia = async () => {
      const context = new AudioContext();
      await context.resume();
      return context.createMediaStreamDestination().stream;
    };
  });
}

// holdMicrophone holds the box's microphone for a moment and lets it go.
async function holdMicrophone(page: Page, box: ReturnType<Page["locator"]>) {
  await box.getByRole("button", { name: "Hold to dictate" }).hover();
  await page.mouse.down();
  await expect(box.getByRole("button", { name: "Listening" })).toHaveClass(/recording/);
  await page.waitForTimeout(1200);
  await page.mouse.up();
}

// The Overlord, 2026-10-29, of "Nothing was heard." as a line under the box:
// it should be "a little banner right above the chat input that is only
// temporary, goes away slightly and doesn't move the screen up".
test("a hold with nothing said in it says Nothing was heard over the box, for a moment, and moves nothing", async ({ page }) => {
  // Arrange: the microphone hears silence, and a screen reader listens to the
  // box's status line, there before anything is said in it.
  const { box } = await openBernie(page, bernie(["i can message?"]));
  await hearSilence(page);
  const draft = box.getByRole("textbox", { name: "Message Bernie - Merge Maestro" });
  await draft.fill("please");
  const before = await draft.boundingBox();
  await expect(box.getByRole("status")).toHaveText("");
  const spoken = await box.getByRole("status").elementHandle();

  // Act
  await holdMicrophone(page, box);

  // Assert: the banner says it over the box, the same status line says it to
  // a screen reader, the box stays where it was, and the banner goes by
  // itself.
  const banner = box.locator(".composer-banner");
  await expect(banner).toHaveText("Nothing was heard.");
  expect(await spoken.evaluate((line) => line.isConnected && line.textContent)).toBe("Nothing was heard.");
  expect(await draft.boundingBox()).toEqual(before);
  await expect(draft).toHaveValue("please");
  await expect(banner).toHaveCount(0, { timeout: 8000 });
});

// A refusal shows over the box until its banner goes, and leaves no trace
// there to hide the next notice behind.
for (const press of ["Send", "Cancel"] as const) {
  test(`after a refused ${press} has gone from over the box, a hold with nothing said in it still says Nothing was heard there`, async ({ page }) => {
    // Arrange: a message of his waits for Bernie, the board refuses whatever
    // he asks of it, and the microphone hears silence.
    const first = bernie(["i can message?"]);
    first.actions.push({ id: "waiting", kind: "message", task_id: "bernie", generation: "s1", status: "queued", message: "Kept until Bernie resumes.", text: "wait for me", updated_at: PAUSED_AT });
    const { box } = await openBernie(page, first);
    await page.route("**/api/actions", (route) => route.fulfill({ status: 503, json: { error: "The board is restarting." } }));
    await hearSilence(page);
    const draft = box.getByRole("textbox", { name: "Message Bernie - Merge Maestro" });
    const banner = box.locator(".composer-banner");
    await draft.fill("please");
    if (press === "Send") await box.getByRole("button", { name: "Send", exact: true }).click();
    else await page.locator(".queued-message.pane").filter({ hasText: "wait for me" }).getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(banner).toContainText("The board is restarting.");
    await expect(banner).toHaveCount(0, { timeout: 8000 });

    // Act
    await holdMicrophone(page, box);

    // Assert
    await expect(banner).toHaveText("Nothing was heard.");
    await expect(draft).toHaveValue("please");
  });
}

// Each hold says what it heard anew, even while the last hold's Nothing was
// heard still shows over the box.
test("a second hold with nothing said in it says Nothing was heard over the box again, in a banner of its own", async ({ page }) => {
  // Arrange: a first hold heard nothing, and says so over the box.
  const { box } = await openBernie(page, bernie(["i can message?"]));
  await hearSilence(page);
  const banner = box.locator(".composer-banner");
  await holdMicrophone(page, box);
  await expect(banner).toHaveText("Nothing was heard.");
  const first = await banner.elementHandle();

  // Act
  await holdMicrophone(page, box);

  // Assert: a new banner says it, rather than the first one going on.
  await expect(banner).toHaveText("Nothing was heard.");
  expect(await first.evaluate((element) => element.isConnected), "the first hold's banner has gone").toBe(false);
});

// The note of files being added takes its banner with it when they are in,
// and brings back no note that showed before them.
test("a file added while Nothing was heard shows does not bring that notice back over the box once it is in", async ({ page }) => {
  // Arrange: a hold heard nothing and says so over the box, and the board
  // keeps the next file only when the test lets it.
  const { box } = await openBernie(page, bernie(["i can message?"]));
  await hearSilence(page);
  let keep: () => Promise<void> = async () => { throw new Error("No file is being kept"); };
  await page.route("**/api/composer/file**", (route) => {
    keep = () => route.fulfill({ status: 201, json: { path: "C:/session-files/design.txt" } });
  });
  const banner = box.locator(".composer-banner");
  await holdMicrophone(page, box);
  await expect(banner).toHaveText("Nothing was heard.");
  await box.locator('input[type="file"]').setInputFiles({ name: "design.txt", mimeType: "text/plain", buffer: Buffer.from("Keep this") });
  await expect(banner).toHaveText("Adding the file…");

  // Act
  await keep();

  // Assert: the file is in and the status line says Nothing was heard again,
  // while no banner over the box says it a second time.
  await expect(box.getByRole("list", { name: "Attached files" })).toContainText("design.txt");
  await expect(box.getByRole("status")).toHaveText("Nothing was heard.");
  expect(await banner.count()).toBe(0);
});

for (const style of ["classic", "modern"]) {
  for (const hasHistory of [false, true]) {
    test(`${style}, history ${hasHistory}: a message his resume will carry has a delete button, which withdraws it, and one already delivered has none`, async ({ page }) => {
      // Arrange
      await page.addInitScript((style) => localStorage.setItem("cfo-chat-style", style), style);
      const first = bernie(["i can message?"]);
      if (!hasHistory) first.tasks[0].generation = "";
      const { box, posted, publish } = await openBernie(page, first);
      const kept = box.getByRole("listitem").filter({ hasText: "i can message?" });
      const typed = hasHistory ? page.locator(".message-prompt").filter({ hasText: "merge 512 first" }) : box.getByRole("listitem").filter({ hasText: "merge 512 first" });
      await expect(box.getByRole("listitem")).toHaveCount(hasHistory ? 1 : 2);
      await expect(kept).toContainText("Kept for resume");
      await expect(typed).toContainText("merge 512 first");
      await expect(typed.getByRole("button", { name: /Delete|Send now|Edit/ })).toHaveCount(0);

      // Act
      await kept.getByRole("button", { name: "Delete: i can message?" }).click();
      await expect.poll(() => posted.length).toBe(1);
      const next = bernie([]);
      if (!hasHistory) next.tasks[0].generation = "";
      await publish(next);

      // Assert
      expect(posted).toEqual([{ token: "message-box-proof", body: { id: expect.any(String), kind: "message_withdraw", task_id: "bernie", generation: "", text: "i can message?" } }]);
      await expect(kept).toHaveCount(0);
      await expect(box.getByRole("listitem")).toHaveCount(hasHistory ? 0 : 1);
      await expect(page.locator(".context-pane").getByText("merge 512 first", { exact: true })).toHaveCount(1);
    });
  }
}

// Two goblins whose Start waits its turn, both shown in the deck's one slot
// for a task with no terminal yet.
const waiting = (id: string, name: string) => ({
  id, title: "Wait for a turn to start", goblin_name: name, goblin_title: "Queue Keeper", project: "code-goblins", backend: "native", harness: "claude", phase: "queued", starting: true, verified: false, at: PAUSED_AT,
});
const BOTH_WAITING = { healthy: true, instance: "message-box-proof", revision: 1, cfo_runs: true, tasks: [waiting("ann", "Ann"), waiting("rex", "Rex")], actions: [] };
const waitingBox = (page: Page, name: string) => page.getByRole("region", { name: "Messages to " + name + " - Queue Keeper" });
const waitingDraft = (page: Page, name: string) => waitingBox(page, name).getByRole("textbox", { name: "Message " + name + " - Queue Keeper" });

test("two goblins waiting to start each keep their own draft, and Send goes to the one shown", async ({ page }) => {
  // Arrange
  const { posted } = await open(page, BOTH_WAITING);
  await page.locator(".task-card").filter({ hasText: "Ann" }).click();
  await waitingDraft(page, "Ann").fill("merge 512 first");

  // Act
  await page.locator(".task-card").filter({ hasText: "Rex" }).click();
  await expect(waitingDraft(page, "Rex")).toHaveValue("");
  await waitingDraft(page, "Rex").fill("rebase first");
  await waitingBox(page, "Rex").getByRole("button", { name: "Send", exact: true }).click();
  await page.locator(".task-card").filter({ hasText: "Ann" }).click();

  // Assert
  await expect(waitingDraft(page, "Ann")).toHaveValue("merge 512 first");
  await expect.poll(() => posted).toEqual([{ token: "message-box-proof", body: { id: expect.any(String), kind: "message", task_id: "rex", generation: "", text: "rebase first" } }]);
});

test("a message sent to a goblin waiting to start is not its draft again after another goblin is opened while it goes", async ({ page }) => {
  // Arrange: the supervisor takes the message only once Ann is open.
  const { posted } = await open(page, BOTH_WAITING);
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/actions", async (route) => {
    await held;
    await route.fallback();
  });
  await page.locator(".task-card").filter({ hasText: "Rex" }).click();
  await waitingDraft(page, "Rex").fill("rebase first");

  // Act
  await waitingBox(page, "Rex").getByRole("button", { name: "Send", exact: true }).click();
  await expect(waitingBox(page, "Rex").getByRole("button", { name: "Sending" })).toBeDisabled();
  await page.locator(".task-card").filter({ hasText: "Ann" }).click();
  await expect(waitingDraft(page, "Ann")).toHaveValue("");
  const answered = page.waitForResponse("**/api/actions");
  release();
  await (await answered).finished();
  await page.locator(".task-card").filter({ hasText: "Rex" }).click();

  // Assert
  await expect(waitingDraft(page, "Rex")).toHaveValue("");
  expect(posted).toEqual([{ token: "message-box-proof", body: { id: expect.any(String), kind: "message", task_id: "rex", generation: "", text: "rebase first" } }]);
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
