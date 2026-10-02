import { expect, test, type Response } from "./site";

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
