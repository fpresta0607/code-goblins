import { defineConfig } from "@playwright/test";
import { ORIGIN } from "./tests/site";

// The terminal specs whose terminals a fixture page draws, each of which runs
// a second time with its terminal stepped into a Messages view's place, in
// the stage the board draws it in (tests/fixtures/stepped-in.tsx), so the
// terminal stepping in keeps every checklist item it carries. The terminal
// specs that load the whole board step it in themselves.
const STEPPED_IN = ["terminal-keys", "terminal-input", "terminal-harness-screens", "terminal-scroll", "terminal-zoom", "terminal-geometry", "voice-bubble",
  "dictated-input", "touch-dictation", "phone-paste", "terminal-swipe", "phone-terminal-tools"].map((name) => `**/${name}.spec.ts`);

export default defineConfig<{ isSteppedIn: boolean }>({
  testDir: "./tests",
  workers: 1,
  // In CI a test that fails runs once more, since one can fail by chance on a
  // loaded runner, and tests/failed-once.ts names each test that passed only
  // then, so none passes unseen. A test that fails twice fails the run. At a
  // desk a failure is the answer wanted, so nothing runs again.
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["dot"], ["./tests/failed-once.ts"]] : "list",
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
  projects: [{ name: "board" }, { name: "stepped-in", testMatch: STEPPED_IN, use: { isSteppedIn: true } }],
});
