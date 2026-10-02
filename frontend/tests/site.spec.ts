import { expect, ORIGIN, test, type Response } from "./site";

// A page a test loads comes from files the test answers itself. Nothing is
// taken from a server, so a connection the machine refuses, which once left a
// page without one of its modules and so without anything on it, cannot fail a
// page load.
test("a page load takes nothing from a server", async ({ page }) => {
  const responses: Response[] = [];
  page.on("response", (response) => responses.push(response));

  await page.goto("/tests/fixtures/card-layout.html");
  await expect(page.locator(".task-card-shell").first()).toBeVisible();

  // The page, its script, its styles and more: the load is a real one.
  expect(responses.length).toBeGreaterThan(3);
  const fromServer: string[] = [];
  for (const response of responses) if (await response.serverAddr()) fromServer.push(response.url());
  expect(fromServer).toEqual([]);
});

// A browser context no test serves has nothing answering for its pages. Its
// page load is refused by the browser itself, so it fails there and then: it
// cannot pass by reaching a server that happens to listen on this machine.
test("a page nothing serves is refused, not fetched from this machine", async ({ browser }) => {
  const unserved = await browser.newContext();
  const page = await unserved.newPage();

  const failure = await page.goto(ORIGIN + "/tests/fixtures/card-layout.html").then(() => "loaded", (error: Error) => error.message);

  expect(failure).toContain("net::ERR_UNSAFE_PORT");
  await unserved.close();
});
