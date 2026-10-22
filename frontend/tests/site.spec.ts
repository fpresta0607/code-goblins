import { expect, openBoardPastFirstRun, ORIGIN, roomForATest, test, type Response } from "./site";

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

// A test waits for 5 GB of free memory before it starts, so a run on a
// machine the fleet shares cannot take the memory the fleet needs.
const GB = 2 ** 30;
const quick = { limit: 200, pause: 5, ci: false };

test("a test starts at once while memory is free", async () => {
  let reads = 0;
  await roomForATest({ ...quick, free: () => { reads++; return 5 * GB; } });
  expect(reads).toBe(1);
});

test("a test waits while memory is short and starts once it is free", async () => {
  const readings = [4.9 * GB, 2 * GB, 4.99 * GB, 6 * GB];
  let reads = 0;
  await roomForATest({ ...quick, limit: 5_000, free: () => readings[reads++] });
  expect(reads).toBe(4);
});

test("a test that never gets its memory fails saying so", async () => {
  const failure = await roomForATest({ ...quick, free: () => 4.9 * GB }).then(() => "started", (error: Error) => error.message);
  expect(failure).toBe("Less than 5 GB of memory was free for 0.2 s, so this test did not start. Free some memory, or run one spec at a time.");
});

test("in CI, which runs nothing else, a test never waits", async () => {
  let reads = 0;
  await roomForATest({ ...quick, ci: true, free: () => { reads++; return 0; } });
  expect(reads).toBe(0);
});

// The wait is every test's, not only a function: a test given no memory does
// not start, which is this test failing as it is expected to.
test.describe("with no memory free", () => {
  test.use({ room: { ...quick, free: () => 0 } });
  test.fail("a test does not start", async () => {});
});

// A loaded runner delivered a first-run board's first snapshot only after the
// test had clicked Board, and the board's first-run page then took the place of
// the board the test had asked for (#1348). Here the snapshot comes once Board
// is clicked, or a second after the page asks for it while nothing clicks.
test("a first-run board whose first snapshot comes late still opens on the board", async ({ page }) => {
  // Arrange
  const snapshot = { instance: "late-fixture", revision: 1, healthy: true, example: true, cfo_runs: false, tasks: [{ id: "polish-settings", title: "Polish settings", project: "example", phase: "queued", brief: true, verified: false }] };
  let pressBoard!: () => void;
  const boardPressed = new Promise<void>((resolve) => { pressBoard = resolve; });
  await page.exposeFunction("pressedBoard", () => pressBoard());
  await page.addInitScript(() => addEventListener("click", (event) => {
    if ((event.target as Element).closest("button")?.textContent === "Board") (window as unknown as { pressedBoard: () => void }).pressedBoard();
  }, true));
  await page.route("**/api/**", async (route) => {
    if (new URL(route.request().url()).pathname !== "/api/events") return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    await Promise.race([boardPressed, new Promise((resolve) => setTimeout(resolve, 1000))]);
    await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` });
  });

  // Act
  await openBoardPastFirstRun(page);

  // Assert
  await expect(page.getByRole("region", { name: "Tasks", exact: true })).toContainText("Polish settings");
  await expect(page.getByRole("main", { name: "First run" })).toHaveCount(0);
});
