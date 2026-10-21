import { devices, expect, test } from "./site";

// The Overlord, 2026-10-21, on his phone: "the mobile dictation button ...
// should be a click and then click again and the message, instead of that
// whole shift control ... Can't hold on my phone screen." On a touch screen a
// terminal's microphone is tapped to start listening and tapped again to stop
// and send what was said, on the PC's own speech model.

const LAUNCH = { args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] };
test.use({ ...devices["Pixel 7"], launchOptions: LAUNCH, permissions: ["microphone"] });

const WORDS = "show me the open pull requests";

for (const backend of ["native", "herdr"] as const) {
  test(`on a touch screen one tap starts dictating into a ${backend} terminal and a second sends the message`, async ({ page }) => {
    // Arrange: the PC's speech model hears WORDS in the sound.
    const inputs: Buffer[] = [];
    let sounds = 0;
    await page.addInitScript(() => localStorage.setItem("cfo-voice-hint-v1", "dismissed"));
    await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
    await page.route("**/api/dictation", (route) => {
      if (route.request().method() === "GET") return route.fulfill({ json: { engine: "test-model", state: "ready" } });
      sounds++;
      return route.fulfill({ json: { text: WORDS, engine: "test-model" } });
    });
    if (backend === "native") {
      await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
        socket.send(JSON.stringify({ type: "history", bytes: 0 }));
        socket.send(Buffer.from("\x1b[2J\x1b[H$ \x1b[?2004h"));
        socket.onMessage((data) => { if (typeof data !== "string") inputs.push(data); });
      });
    } else {
      await page.addInitScript(() => {
        const originalFetch = window.fetch;
        window.fetch = (resource, options) => {
          if (resource !== "/api/terminal/stream") return originalFetch(resource, options);
          const body = JSON.parse(String(options?.body));
          const encoder = new TextEncoder();
          return Promise.resolve(new Response(new ReadableStream({ start(controller) {
            controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.ready", lease: body.control ? "sized" : "observed", identity: "proof" }) + "\n"));
            controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.frame", encoding: "ansi", seq: 1, full: true, width: 40, height: 20, bytes: btoa("\x1b[2J\x1b[H$ \x1b[?2004h") }) + "\n"));
            options?.signal?.addEventListener("abort", () => controller.close(), { once: true });
          } })));
        };
      });
      await page.route("**/api/terminal/input", async (route) => {
        const { command, seq } = route.request().postDataJSON();
        if (command.type === "terminal.input") inputs.push(Buffer.from(command.text));
        await route.fulfill({ json: { seq } });
      });
      await page.route("**/api/terminal/history", (route) => route.fulfill({ json: { text: "$", agent: "codex" } }));
    }
    await page.goto("/tests/fixtures/terminal-input.html#" + backend);
    await expect(page.getByText("Connecting", { exact: true })).toHaveCount(0);
    await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);

    // Act: one tap, a moment's speech, a second tap.
    await page.getByRole("button", { name: "Dictate", exact: true }).tap();
    const listening = page.getByRole("button", { name: "Stop and send", exact: true });
    await expect(listening).toHaveClass(/recording/);
    await expect(listening).toHaveAttribute("aria-pressed", "true");
    await page.waitForTimeout(1200);
    await listening.tap();

    // Assert: the words are typed once and entered, and the button is idle.
    await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe("\x1b[200~" + WORDS + "\x1b[201~\r");
    await expect(page.getByRole("button", { name: "Dictate", exact: true })).toHaveAttribute("aria-pressed", "false");
    expect(sounds).toBeGreaterThan(0);
  });
}
