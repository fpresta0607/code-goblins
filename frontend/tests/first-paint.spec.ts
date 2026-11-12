import { expect, holdStream, test, type Page } from "./site";

// The board's first paint (#1599). On main at 83505e5de a phone's first load
// fetched 4.7 MB: one 1.3 MB script that held the terminal's xterm, the
// Command Center, Orchestration and the diff view, and 3.1 MB of PNG
// pictures. The board fetches what its first screen draws, its pictures as
// WebP, and each of those parts the first time it is opened.
const FIRST_PAINT_BYTES = 1_500_000;
const LAZY = /\/assets\/(CommandCenter|Orchestration|Lineage|DiffView|HostTerminal)-[^/]*\.js$/;
const since = "2026-10-27T09:00:00Z";
const memory = { next: 5368709120, floor: 4294967296, total: 34359738368, available: 6442450944, commit_limit: 51539607552, commit_available: 21474836480, paged_pool: 536870912, nonpaged_pool: 322122547 };
const task = (id: string, title: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title, project: "code-goblins", phase, verified: false, generation: id + "-1", since, harness: "claude", ...fields });
// A goblin from each sheet of portraits: a debugger and a researcher are
// specialists from the two specialist sheets, a builder is on the personas'
// sheet, and a finished task is drawn as its finisher.
const SNAPSHOT = { healthy: true, instance: "fixture", ceo_runs: true, revision: 1, at: since, attention: [], memory, tasks: [
  task("crash", "Fix the crash when the board reconnects", "working"),
  task("cache", "Research why the cache misses", "working"),
  task("kanban", "Build the kanban board", "working"),
  task("queued-1", "Implement the queue's next card", "queued", { generation: "", brief: true, queue_revision: "q1" }),
  task("finished:done-1", "Ship the release notes", "done", { archived: true, merged: true, verified: true, generation: "", branch: "fix/done-1", at: since }),
] };

interface Fetched { url: string; bytes: number }

// fetches records every response the page is given from now on, and returns
// what it has recorded, with each body's size.
function fetches(page: Page): () => Promise<Fetched[]> {
  const bodies: Promise<Fetched>[] = [];
  page.on("response", (response) => {
    bodies.push(response.body().then((body) => ({ url: response.url(), bytes: body.length })));
  });
  return () => Promise.all(bodies);
}

const fetchedCode = async (fetched: () => Promise<Fetched[]>, name: string) => (await fetched()).filter((each) => new RegExp("/assets/" + name + "-[^/]*\\.js$").test(each.url));

// open loads the board, as a browser that has shown it before, and waits for
// its first screen to be drawn and the network to go quiet.
async function open(page: Page) {
  await page.addInitScript(() => localStorage.setItem("ceo-first-open", "shown"));
  await holdStream(page, SNAPSHOT);
  await page.goto("/");
  await expect(page.locator(".task-card").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  await page.waitForLoadState("networkidle");
}

test.describe("at a phone's width", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the board's first paint fetches under 1.5 MB and no code for what it does not show", async ({ page }) => {
    // Arrange
    const fetched = fetches(page);

    // Act
    await open(page);

    // Assert
    const responses = await fetched();
    const bytes = responses.reduce((sum, each) => sum + each.bytes, 0);
    const listing = responses.map((each) => each.bytes + " " + new URL(each.url).pathname).join("\n");
    expect(bytes, listing).toBeLessThan(FIRST_PAINT_BYTES);
    expect(responses.map((each) => new URL(each.url).pathname).filter((path) => LAZY.test(path))).toEqual([]);
  });
});

test("Orchestration and the Command Center arrive the first time each is opened", async ({ page }) => {
  // Arrange
  const fetched = fetches(page);
  await open(page);
  expect(await fetchedCode(fetched, "Orchestration")).toEqual([]);
  expect(await fetchedCode(fetched, "CommandCenter")).toEqual([]);

  // Act
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();

  // Assert
  await expect(page.locator(".flow-node").first()).toBeVisible();
  expect(await fetchedCode(fetched, "Orchestration")).toHaveLength(1);
  expect(await fetchedCode(fetched, "CommandCenter")).toEqual([]);

  // Act
  await page.locator(".command-center-menu > summary").click();

  // Assert
  await expect(page.locator(".command-center-updates").getByText("Nothing is waiting on you.")).toBeVisible();
  await expect(page.locator(".command-center-menu > summary")).toBeFocused();
  expect(await fetchedCode(fetched, "CommandCenter")).toHaveLength(1);
});
