import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  workers: 1,
  // The tests load the board and their fixture pages from a build, which
  // tests/site.ts answers from files: there is no web server, and baseURL is
  // only the origin the pages are given.
  globalSetup: "./tests/build-site.ts",
  use: {
    baseURL: "http://127.0.0.1:5188",
    viewport: { width: 1440, height: 1200 },
    launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"] },
    // A failed test keeps its trace in test-results beside its page
    // snapshot, which CI uploads when the browser tests fail. Tests are not
    // retried, so a failure is kept rather than passed over.
    trace: "retain-on-failure",
  },
});
