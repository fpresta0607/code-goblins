import { devices, expect, openItem, test, type Page } from "./site";
import type { Recognizer } from "../src/dictation";

// The Overlord, 2026-10-21, on his phone: "the mobile dictation button ...
// should be a click and then click again and the message, instead of that
// whole shift control ... Can't hold on my phone screen." On a touch screen a
// terminal's microphone is tapped to start listening and tapped again to stop
// and send what was said, on the PC's own speech model.

const LAUNCH = { args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] };
test.use({ ...devices["Pixel 7"], launchOptions: LAUNCH, permissions: ["microphone"] });

const WORDS = "show me the open pull requests";

// openTerminal opens a backend terminal whose PC's speech model hears WORDS in
// every sound, and returns what reached its program and how many sounds the
// model was asked about.
async function openTerminal(page: Page, backend: "native" | "herdr", role = "goblin"): Promise<{ typed: () => string; sounds: () => number }> {
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
  await page.goto("/tests/fixtures/terminal-input.html?role=" + role + "#" + backend);
  await expect(page.getByText("Connecting", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
  return { typed: () => Buffer.concat(inputs).toString("utf8"), sounds: () => sounds };
}

for (const backend of ["native", "herdr"] as const) {
  test(`on a touch screen one tap starts dictating into a ${backend} terminal and a second sends the message`, async ({ page }) => {
    // Arrange: the PC's speech model hears WORDS in the sound.
    const terminal = await openTerminal(page, backend);

    // Act: one tap, a moment's speech, a second tap.
    await page.getByRole("button", { name: "Dictate", exact: true }).tap();
    const listening = page.getByRole("button", { name: "Stop and send", exact: true });
    await expect(listening).toHaveClass(/recording/);
    await expect(listening).toHaveAttribute("aria-pressed", "true");
    await page.waitForTimeout(1200);
    await listening.tap();

    // Assert: the words are typed once and entered, and the button is idle.
    await expect.poll(terminal.typed).toBe("\x1b[200~" + WORDS + "\x1b[201~\r");
    await expect(page.getByRole("button", { name: "Dictate", exact: true })).toHaveAttribute("aria-pressed", "false");
    expect(terminal.sounds()).toBeGreaterThan(0);
  });
}

// The Overlord, 2026-10-22: "it only records like the first few sentences
// when I'm using dictation on mobile", "It's like a timer or something". A
// dictation ends when the page is hidden, and speaking touches nothing, so
// the phone's own idle timer locked the screen in the middle of a long one.
// While the board listens it holds the screen awake, and lets go once it
// stops.
test("on a touch screen the screen is kept awake while a dictation listens, and let go when it stops", async ({ page }) => {
  // Arrange: the phone's wake lock, played by a record of its requests.
  await page.addInitScript(() => {
    const locks: { type: string; isReleased: boolean }[] = [];
    Object.assign(window, { locks });
    Object.defineProperty(navigator, "wakeLock", { value: { request: async (type: string) => {
      const lock = { type, isReleased: false };
      locks.push(lock);
      return { release: async () => { lock.isReleased = true; } };
    } } });
  });
  await openTerminal(page, "native");
  const locks = () => page.evaluate(() => (window as unknown as { locks: { type: string; isReleased: boolean }[] }).locks);

  // Act
  await page.getByRole("button", { name: "Dictate", exact: true }).tap();
  const listening = page.getByRole("button", { name: "Stop and send", exact: true });
  await expect(listening).toHaveAttribute("aria-pressed", "true");

  // Assert: held while it listens, and let go at the second tap.
  await expect.poll(locks).toEqual([{ type: "screen", isReleased: false }]);
  await listening.tap();
  await expect.poll(locks).toEqual([{ type: "screen", isReleased: true }]);
});

for (const target of ["cfo", "goblin", "answer"] as const) {
  test(`a minute of phone dictation reaches the ${target} box whole when browser recognition cannot restart`, async ({ page }, testInfo) => {
    // Arrange: the phone ends browser recognition after a pause and ignores
    // a restart. The recorder receives 65 seconds of non-sensitive audio,
    // advanced in chunks so the test needs no minute-long sleep.
    const startedAt = Date.parse("2026-10-13T14:30:00Z");
    await page.clock.setFixedTime(startedAt);
    await page.addInitScript(() => {
      localStorage.setItem("cfo-dictation-browser-v1", "on");
      const probe = { starts: 0, isRecording: false, say: () => {}, pause: () => {} };
      Object.assign(window, { phoneDictation: probe });
      class Recognition implements Recognizer {
        continuous = false;
        interimResults = false;
        lang = "";
        onstart: Recognizer["onstart"] = null;
        onresult: Recognizer["onresult"] = null;
        onerror: Recognizer["onerror"] = null;
        onend: Recognizer["onend"] = null;
        start() {
          if (++probe.starts > 1) return;
          this.onstart?.();
          probe.pause = () => {
            this.onresult?.({ resultIndex: 0, results: [[{ transcript: "first phrase only" }]] });
            this.onend?.();
            probe.pause = () => {};
          };
        }
        stop() { this.onend?.(); }
        abort() {}
      }
      Object.defineProperty(window, "SpeechRecognition", { value: Recognition });
      class CaptureNode extends GainNode {
        port: MessagePort;
        constructor(context: AudioContext) {
          super(context);
          const channel = new MessageChannel();
          this.port = channel.port1;
          channel.port2.onmessage = ({ data }) => {
            probe.isRecording = data !== "flush";
            if (!probe.isRecording) channel.port2.postMessage(null);
          };
          probe.say = () => {
            if (!probe.isRecording) return;
            const samples = new Float32Array(5 * context.sampleRate);
            for (let index = 0; index < 4.7 * context.sampleRate; index++) samples[index] = 0.1 * Math.sin(2 * Math.PI * 440 * index / context.sampleRate);
            channel.port2.postMessage(samples);
          };
        }
      }
      Object.defineProperty(window, "AudioWorkletNode", { value: CaptureNode });
    });
    const terminal = await openTerminal(page, "native", target);
    const phrases: string[] = [];
    let recordedSeconds = 0;
    await page.route("**/api/dictation", (route) => {
      if (route.request().method() === "GET") return route.fulfill({ json: { engine: "test-model", state: "ready" } });
      const sound = route.request().postDataBuffer()!;
      recordedSeconds += sound.readUInt32LE(40) / 2 / sound.readUInt32LE(24);
      const text = `phrase ${phrases.length + 1}`;
      phrases.push(text);
      return route.fulfill({ json: { text } });
    });
    if (target === "answer") {
      await page.goto("/tests/fixtures/answer-dictation.html");
      await openItem(page, "Should Corey and Densie work with the numbers alone?");
      await page.getByRole("radio", { name: /^Other/ }).check();
    }
    const probe = () => page.evaluate(() => {
      const { starts, isRecording } = (window as unknown as { phoneDictation: { starts: number; isRecording: boolean } }).phoneDictation;
      return { starts, isRecording };
    });

    // Act: speak across thirteen pauses before the second tap.
    await page.getByRole("button", { name: "Dictate", exact: true }).tap();
    await expect.poll(async () => { const state = await probe(); return state.starts > 0 || state.isRecording; }).toBe(true);
    for (let segment = 0; segment < 13; segment++) {
      await page.clock.setFixedTime(startedAt + (segment + 1) * 5000);
      await page.evaluate(() => {
        const speech = (window as unknown as { phoneDictation: { say: () => void; pause: () => void } }).phoneDictation;
        speech.say();
        speech.pause();
      });
    }
    await page.getByRole("button", { name: target === "answer" ? "Stop dictating" : "Stop and send", exact: true }).tap();

    // Assert: every piece reaches the local model and the destination once.
    const text = Array.from({ length: 13 }, (_, index) => `phrase ${index + 1}`).join(" ");
    if (target === "answer") await expect(page.getByRole("textbox", { name: "Your written answer" })).toHaveValue(text);
    else await expect.poll(terminal.typed).toBe("\x1b[200~" + text + "\x1b[201~\r");
    expect(recordedSeconds).toBeCloseTo(65, 1);
    expect(phrases).toHaveLength(13);
    expect((await probe()).starts).toBe(0);
    await expect(page.getByRole("button", { name: "Dictate", exact: true })).toHaveAttribute("aria-pressed", "false");
    await page.screenshot({ path: testInfo.outputPath(`phone-${target}-complete.png`) });
  });
}
