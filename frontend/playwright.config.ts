import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  workers: 1,
  use: {
    baseURL: "http://127.0.0.1:5188",
    viewport: { width: 1440, height: 1200 },
    launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"] },
    // A failed test keeps its trace in test-results beside its page
    // snapshot, which CI uploads when the browser tests fail. Tests are not
    // retried, so a failure is kept rather than passed over.
    trace: "retain-on-failure",
  },
  webServer: {
    command: "npm run dev -- --port 5188",
    url: "http://127.0.0.1:5188",
    reuseExistingServer: false,
  },
});
