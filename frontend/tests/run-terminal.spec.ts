import { doneCards, expect, openItem, recordDoneCards, test, type Page, type WebSocketRoute } from "./site";

// The Overlord, 2026-10-08, on a run card that said "It runs in its own
// window": "why can we not render terminal right in command center in place
// of the notification", then "i want powershell commands terminals to not
// open up the separate tab, stay in command center, print out and enter to be
// keyable from that window with cursor", and of its end: "it automatically
// enters at the end when it returns with the finished exit", "just have the
// same complete notification". Run turns the card into the command's own
// terminal; it completes by itself, Complete or Failed with the last line it
// printed, and History keeps the exit code.
const RUN = "Sign in to Fly so I can deploy";

// xterm draws in the DOM, where a test reads it, without WebGL. Chromium's
// fake microphone stands in for the Overlord's when a test dictates.
test.use({ launchOptions: { args: ["--disable-webgl", "--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] } });
const QUESTION = "May I merge the release train now?";

interface Wire { actions: Record<string, unknown>[]; inputs: Buffer[]; views: string[]; sockets: WebSocketRoute[] }

async function openRunCard(page: Page): Promise<Wire> {
  const wire: Wire = { actions: [], inputs: [], views: [], sockets: [] };
  await page.route("**/api/actions", async (route) => {
    const sent: Record<string, unknown> = route.request().postDataJSON();
    wire.actions.push(sent);
    await route.fulfill({ json: { id: String(sent.id), kind: String(sent.kind), run_id: "run-fly-signin-3", status: "queued" } });
  });
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    wire.views.push(socket.url());
    wire.sockets.push(socket);
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(Buffer.from("\x1b[2J\x1b[HWaiting for the sign-in in the browser\r\nYour fly.io email: "));
    socket.onMessage((data) => {
      if (typeof data !== "string") { wire.inputs.push(Buffer.from(data)); return; }
      const control: { type?: string; cols?: number; rows?: number } = JSON.parse(data);
      if (control.type === "resize") socket.send(JSON.stringify({ type: "size", cols: control.cols, rows: control.rows }));
    });
  });
  await page.goto("/tests/fixtures/run-terminal.html");
  await page.waitForFunction(() => "cardStep" in window);
  await openItem(page, RUN);
  await expect(page.locator("dialog.question-modal").getByRole("heading", { name: RUN })).toBeVisible();
  return wire;
}
async function step(page: Page, name: "running" | "elevating" | "complete" | "failed" | "stopped") {
  await page.evaluate((to) => (window as unknown as { cardStep: (name: string) => void }).cardStep(to), name);
  await expect(page.locator("main")).toHaveAttribute("data-step", name);
}
async function history(page: Page) {
  await page.locator(".command-center-menu > summary").click();
  const disclosure = page.locator(".inbox-history");
  await disclosure.locator("summary").click();
  return disclosure.locator(".inbox-list li");
}
const typed = (wire: Wire) => Buffer.concat(wire.inputs).toString("utf8");

test("Run turns the card into the command's terminal, which takes his keys and completes by itself", async ({ page }) => {
  // Arrange
  const wire = await openRunCard(page);
  const dialog = page.locator("dialog.question-modal");

  // Act: he presses Run, and the supervisor starts its terminal.
  await dialog.getByRole("button", { name: "Run in PowerShell" }).click();
  await step(page, "running");

  // Assert: the card is the command's live terminal, with the keyboard.
  await expect(dialog.getByRole("heading", { name: RUN })).toBeVisible();
  await expect(dialog.locator(".run-live .xterm-rows")).toContainText("Your fly.io email:");
  await expect(dialog.getByText("own window")).toHaveCount(0);
  expect(wire.views.map((url) => new URL(url).searchParams.get("run"))).toContain("run-fly-signin-3");
  await expect(dialog.getByRole("textbox", { name: "Terminal input" })).toBeFocused();

  // Act: he types his answer, presses Enter, then Escape.
  await page.keyboard.type("overlord@example.com");
  await page.keyboard.press("Enter");
  await page.keyboard.press("Escape");

  // Assert: every key reached the command, and Escape kept the Command Center open.
  await expect.poll(() => typed(wire)).toBe("overlord@example.com\r\x1b");
  await expect(dialog).toBeVisible();

  // Act: the command ends cleanly.
  await recordDoneCards(page);
  await step(page, "complete");

  // Assert: Complete, the next item, and the exit code in History.
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringContaining("Complete"));
  await expect(dialog).toContainText(QUESTION);
  expect((await doneCards(page)).join(" ")).not.toContain("exit");
  await dialog.getByRole("button", { name: "Close the Command Center" }).click();
  await expect((await history(page)).filter({ hasText: RUN })).toContainText("Complete · exit 0");
  expect(wire.actions).toMatchObject([{ kind: "run", run_id: "run-fly-signin-3", generation: "g".repeat(64) }]);
});

test("Stop ends the command from its card, and the card reads Stopped", async ({ page }) => {
  // Arrange
  const wire = await openRunCard(page);
  const dialog = page.locator("dialog.question-modal");
  await dialog.getByRole("button", { name: "Run in PowerShell" }).click();
  await step(page, "running");
  await expect(dialog.locator(".run-live .xterm-rows")).toContainText("Your fly.io email:");

  // Act
  await dialog.getByRole("button", { name: "Stop" }).click();
  await recordDoneCards(page);
  await step(page, "stopped");

  // Assert: Stop names the item, never a command, and the card reads Stopped.
  await expect.poll(() => wire.actions.length).toBe(2);
  expect(wire.actions[1]).toMatchObject({ kind: "run_stop", run_id: "run-fly-signin-3", generation: "g".repeat(64) });
  expect(wire.actions[1]).not.toHaveProperty("command");
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringContaining("Stopped"));
});

// A command's terminal that did not answer is named to the CFO by the
// command, never as "a goblin's terminal".
test("a command's terminal that did not answer is named to the CFO by its command", async ({ page }) => {
  // Arrange
  const reports: { where: string; text: string }[] = [];
  await page.route("**/api/cfo/report", (route) => { reports.push(route.request().postDataJSON() as { where: string; text: string }); return route.fulfill({ status: 202, json: { reported: true } }); });
  const wire = await openRunCard(page);
  const dialog = page.locator("dialog.question-modal");
  await dialog.getByRole("button", { name: "Run in PowerShell" }).click();
  await step(page, "running");
  await expect(dialog.locator(".run-live .xterm-rows")).toContainText("Your fly.io email:");

  // Act
  await wire.sockets.at(-1)?.close({ code: 1008, reason: "The terminal of this task did not answer." });

  // Assert
  await expect.poll(() => reports.map((report) => report.where)).toContain("the terminal of the command " + RUN);
  expect(reports.map((report) => report.where)).not.toContain("a goblin's terminal");
});

test("a command that failed says so in a plain sentence with its last line, and keeps what it showed", async ({ page }) => {
  // Arrange
  await openRunCard(page);
  const dialog = page.locator("dialog.question-modal");
  await dialog.getByRole("button", { name: "Run in PowerShell" }).click();
  await step(page, "running");

  // Act
  await step(page, "failed");
  await page.waitForTimeout(1500);

  // Assert
  await expect(dialog.getByRole("heading", { name: RUN })).toBeVisible();
  await expect(dialog.locator(".run-head .run-state")).toHaveText("Failed");
  await expect(dialog.locator(".run-failure")).toHaveText("Error: the sign-in was cancelled in the browser. The goblin that asked has its output and takes the next step.");
  await expect(dialog.locator(".run-output")).toContainText("Opening https://fly.io/app/auth/cli");
  await expect(dialog.locator(".run-live")).toHaveCount(0);
  await expect(dialog).not.toContainText("exit 1");
  await dialog.getByRole("button", { name: "Close the Command Center" }).click();
  await expect((await history(page)).filter({ hasText: RUN })).toContainText("Failed: Error: the sign-in was cancelled in the browser · exit 1");
});

test("an administrator's command waits on its card for Windows' prompt, with no window of its own", async ({ page }) => {
  // Arrange
  const wire = await openRunCard(page);
  const dialog = page.locator("dialog.question-modal");

  // Act
  await step(page, "elevating");

  // Assert
  await expect(dialog.getByRole("status").filter({ hasText: "Confirm the Windows prompt" })).toContainText("It runs here once you do");
  await expect(dialog.locator(".run-live")).toHaveCount(0);
  expect(wire.views).toEqual([]);
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true });

  test("the card's terminal fits the screen and takes the phone's keyboard", async ({ page }) => {
    // Arrange
    const wire = await openRunCard(page);
    const dialog = page.locator("dialog.question-modal");
    await dialog.getByRole("button", { name: "Run in PowerShell" }).tap();
    await step(page, "running");
    const terminal = dialog.locator(".run-live .host-terminal");
    await expect(dialog.locator(".run-live .xterm-rows")).toContainText("Your fly.io email:");

    // Act: he taps the terminal's text, and his phone's keyboard types as an
    // input method does, text at a time.
    await dialog.locator(".run-live .xterm-screen").tap({ position: { x: 12, y: 12 } });
    await expect(dialog.getByRole("textbox", { name: "Terminal input" })).toBeFocused();
    await page.keyboard.insertText("me");

    // Assert
    await expect.poll(() => typed(wire)).toBe("me");
    const box = await terminal.boundingBox();
    expect(box && box.x >= 0 && box.x + box.width <= 390).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  });
});

// The Overlord, 2026-10-12 about 12:25Z: "Whenever a terminal fails, in the
// command center, it shows the reconnect button when it shouldn't." A command
// that ended has nothing to reconnect to: its terminal says how it ended and
// offers no Reconnect, from the moment it ends and before the board hears of
// it, and so does a view opened once it has ended. A view the supervisor
// refused for any other reason still offers Reconnect.
for (const view of [
  { name: "a command that failed", close: { code: 1000, reason: "The terminal ended with exit code 3." }, isEnded: true },
  { name: "a command opened after it ended", close: { code: 1000, reason: "This command is not running." }, isEnded: true },
  { name: "a view the supervisor refused", close: { code: 1008, reason: "The terminal of this command did not answer." }, isEnded: false },
]) {
  test(`${view.name} ${view.isEnded ? "says how it ended and offers no Reconnect" : "still offers Reconnect"}`, async ({ page }) => {
    // Arrange
    const wire = await openRunCard(page);
    const dialog = page.locator("dialog.question-modal");
    await dialog.getByRole("button", { name: "Run in PowerShell" }).click();
    await step(page, "running");
    await expect(dialog.locator(".run-live .xterm-rows")).toContainText("Your fly.io email:");
    await expect(dialog.getByRole("textbox", { name: "Terminal input" })).toBeFocused();

    // Act: the view closes, and no snapshot follows yet.
    await wire.sockets.at(-1)!.close(view.close);

    // Assert
    const live = dialog.locator(".run-live");
    await expect(live.locator(".terminal-closed")).toBeVisible();
    await expect(live.getByRole("button", { name: "Reconnect" })).toHaveCount(view.isEnded ? 0 : 1);
    if (view.isEnded) await expect(live.locator(".terminal-closed")).toHaveText(view.close.reason);
  });
}

// The Overlord, the same minute: "you do have the microphone button that are
// running terminal. in the command center. Doesn't look like we ever need to
// use it, so just remove that microphone button in the command center for
// running terminals." A command's running terminal on its card has no
// microphone button. A goblin's own terminal keeps its, as the voice bubble's
// tests show.
for (const size of [{ name: "his window", viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 }, { name: "a phone", viewport: { width: 390, height: 844 }, deviceScaleFactor: 3 }]) {
  test.describe(`at ${size.name}'s size`, () => {
    test.use({ viewport: size.viewport, deviceScaleFactor: size.deviceScaleFactor });

    test("a running command's terminal has no microphone button, and a failed one no Reconnect", async ({ page }, testInfo) => {
      // Arrange
      const wire = await openRunCard(page);
      const dialog = page.locator("dialog.question-modal");
      await dialog.getByRole("button", { name: "Run in PowerShell" }).click();
      await step(page, "running");
      await expect(dialog.locator(".run-live .xterm-rows")).toContainText("Your fly.io email:");
      await expect(dialog.getByRole("textbox", { name: "Terminal input" })).toBeFocused();

      // Act
      await dialog.locator(".run-live").screenshot({ path: testInfo.outputPath(`running-${size.viewport.width}.png`) });
      const microphones = await dialog.locator(".run-live .voice-bubble").count();
      await wire.sockets.at(-1)!.close({ code: 1000, reason: "The terminal ended with exit code 3." });
      await expect(dialog.locator(".run-live .terminal-closed")).toBeVisible();
      await dialog.locator(".run-live").screenshot({ path: testInfo.outputPath(`ended-${size.viewport.width}.png`) });

      // Assert
      expect(microphones).toBe(0);
      await expect(dialog.getByRole("button", { name: "Recent messages" })).toHaveCount(0);
      await expect(dialog.getByRole("button", { name: "Reconnect" })).toHaveCount(0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
    });
  });
}

// Dictation by its key still works in a running command's terminal: the bars
// show over its corner while the keys are held, and what was said is typed
// into the command.
test.describe("dictating into a running command's terminal", () => {
  test.use({ permissions: ["microphone"] });

  test("the key still dictates there, with the bars showing only while it is held", async ({ page }) => {
    // Arrange
    await page.addInitScript(() => localStorage.setItem("cfo-voice-hint-v1", "dismissed"));
    await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
    await page.route("**/api/dictation", (route) => route.request().method() === "GET"
      ? route.fulfill({ json: { engine: "test-model", state: "ready" } })
      : route.fulfill({ json: { text: "overlord@example.com", engine: "test-model" } }));
    const wire = await openRunCard(page);
    const dialog = page.locator("dialog.question-modal");
    await dialog.getByRole("button", { name: "Run in PowerShell" }).click();
    await step(page, "running");
    await expect(dialog.getByRole("textbox", { name: "Terminal input" })).toBeFocused();
    const surface = dialog.locator(".run-live .terminal-surface");
    const before = await surface.boundingBox();

    // Act
    await page.keyboard.down("Control");
    await page.keyboard.down("Shift");
    await page.keyboard.down("Space");
    await expect(dialog.locator(".run-live .voice-bubble")).toHaveClass(/recording/);
    const held = await surface.boundingBox();
    await page.waitForTimeout(1200);
    await page.keyboard.up("Space");
    await page.keyboard.up("Shift");
    await page.keyboard.up("Control");

    // Assert: the bars cover the terminal's corner while held, so the
    // command's terminal keeps its size and is never resized by a dictation.
    expect(held).toEqual(before);
    await expect.poll(() => typed(wire)).toContain("overlord@example.com");
    await expect(dialog.locator(".run-live .voice-bubble")).toHaveCount(0);
  });
});
