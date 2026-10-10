import { expect, test } from "./site";

// A throwaway test, removed before this branch is ready. It fails on its
// first try and passes on its second, to prove on a real run that a browser
// test which fails once leaves its job green and is named on the test check.
test("fails once on purpose", async ({ page }, testInfo) => {
  await page.goto("/tests/fixtures/card-layout.html");
  expect(testInfo.retry).toBe(1);
});
