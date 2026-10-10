import { defineConfig } from "@playwright/test";
import { ORIGIN } from "./tests/site";

export default defineConfig({
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
  globalSetup: "./tests/build-site.ts",
  use: {
    baseURL: ORIGIN,
    viewport: { width: 1440, height: 1200 },
    launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"] },
    // A failed test keeps its trace in test-results beside its page
    // snapshot, which CI uploads: of a test that failed twice, and of the
    // first try of one that then passed.
    trace: "retain-on-failure",
  },
});
