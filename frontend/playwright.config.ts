import { defineConfig } from "@playwright/test";
import { ORIGIN } from "./tests/site.ts";

// The terminal specs whose terminals a fixture page draws, each of which runs
// a second time with its terminal stepped into a Messages view's place, in
// the stage the board draws it in (tests/fixtures/stepped-in.tsx), so the
// terminal stepping in keeps every checklist item it carries. The terminal
// specs that load the whole board step it in themselves.
const STEPPED_IN = ["terminal-keys", "terminal-input", "terminal-held", "terminal-harness-screens", "terminal-scroll", "terminal-zoom", "terminal-geometry", "voice-bubble",
  "dictated-input", "touch-dictation", "phone-paste", "terminal-swipe", "phone-terminal-tools"].map((name) => `**/${name}.spec.ts`);

// The WebKit spec is a project of its own, so the browser job's Chromium
// shards never install WebKit and the Media Foundation it needs on a Windows
// runner: that install waited 29 minutes on Windows Update on 2026-10-22, in
// every shard, though only one shard runs this file (#1406).
const IPHONE = ["**/iphone-terminal.spec.ts", "**/iphone-chat.spec.ts"];

export default defineConfig<{ isSteppedIn: boolean }>({
  testDir: "./tests",
  workers: 1,
  // In CI a test that fails runs once more, since one can fail by chance on a
  // loaded runner, and tests/failed-once.ts names each test that passed only
  // then, so none passes unseen. A test that fails twice fails the run. At a
  // desk a failure is the answer wanted, so nothing runs again.
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["dot"], ["./tests/failed-once.ts"]] : "list",
  // A test that never finishes must fail a shard rather than hold the job to
  // its own limit, and every merge train waiting on the run with it (#1406).
  // In CI the run ends after 20 minutes, a little over twice a shard's normal
  // 8 to 12, so the job's rerun of its failed checks takes over. At a desk a
  // whole suite runs on one worker and may take longer than a shard, so only
  // CI is bounded.
  globalTimeout: process.env.CI ? 20 * 60_000 : 0,
  // The tests load the board and their fixture pages from a build, which
  // tests/site.ts answers from files: there is no web server, and baseURL is
  // only the origin the pages are given, on a port nothing is fetched from.
  // Then the run opens the system fonts the terminal draws harness screens
  // in and an unstyled page is drawn in, so no test's first screen or page
  // waits while a runner reads their files.
  globalSetup: ["./tests/build-site.ts", "./tests/system-fonts.ts"],
  use: {
    baseURL: ORIGIN,
    viewport: { width: 1440, height: 1200 },
    launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"] },
    // A failed test keeps its trace in test-results beside its page
    // snapshot, which CI uploads: of a test that failed twice, and of the
    // first try of one that then passed.
    trace: "retain-on-failure",
  },
  projects: [
    { name: "board", testIgnore: IPHONE },
    { name: "stepped-in", testMatch: STEPPED_IN, use: { isSteppedIn: true } },
    { name: "iphone", testMatch: IPHONE },
  ],
});
