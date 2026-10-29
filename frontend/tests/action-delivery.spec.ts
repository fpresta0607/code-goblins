import { expect, openTaskList, test, type Page } from "./site";

// #1611: a task's progress checks were 234 of the 256 actions every board's
// snapshot carried, though only the task's Details lists them. The snapshot
// now carries none, and Activity reads the task's newest actions when it
// opens, and again when the newest of them changes, never at every snapshot.
test.use({ timezoneId: "UTC", viewport: { width: 1440, height: 900 } });

const TITLE = "Probe the DNS";
const check = (id: string, at: string) => ({ id, kind: "evaluate", task_id: "cg-probe", generation: "g1", status: "succeeded", message: "", created_at: at, updated_at: at });
const FIRST = check("check-1", "2026-10-29T08:00:00Z");
const SECOND = { ...check("check-2", "2026-10-29T08:05:00Z"), status: "queued" };
const board = (revision: number, actionsAt: string) => ({
  healthy: true, instance: "board-0m", cfo_runs: true, cfo_terminal: "cfo", cfo_harness: "claude", revision,
  tasks: [{ id: "cg-probe", title: TITLE, project: "code-goblins", phase: "working", generation: "g1", verified: false, actions_at: actionsAt }],
  questions: [], reviews: [], runs: [], credentials: [], actions: [],
});

// The board gets its snapshots from the test alone, and reads the task's
// actions from what the supervisor holds at each read.
async function open(page: Page, held: { actions: object[] }) {
  const reads: string[] = [];
  await page.addInitScript(() => {
    class FixtureSource extends EventTarget {
      private publish = (event: Event) => {
        if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent(event.detail.type, { data: JSON.stringify(event.detail.data) }));
      };
      constructor() {
        super();
        window.addEventListener("fixture-stream", this.publish);
      }
      close() { window.removeEventListener("fixture-stream", this.publish); }
    }
    Object.defineProperty(window, "EventSource", { value: FixtureSource });
  });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.route("**/api/tasks/cg-probe/activity", (route) => route.fulfill({ json: ["2026-10-29T08:00:00Z working: probing the DNS"] }));
  await page.route("**/api/tasks/cg-probe/actions", (route) => {
    reads.push(new URL(route.request().url()).pathname);
    return route.fulfill({ json: held.actions });
  });
  await page.goto("/");
  return reads;
}
const send = (page: Page, data: object) => page.evaluate((detail) => { window.dispatchEvent(new CustomEvent("fixture-stream", { detail })); }, { type: "snapshot", data });
const delivery = (page: Page) => page.locator("#task-activity .action-list > li");

test("a task's actions are read when its Activity opens and again only when the newest of them changes", async ({ page }) => {
  // Arrange: its panel open on the task view, Activity closed.
  const held = { actions: [FIRST] };
  const reads = await open(page, held);
  await send(page, board(1, FIRST.updated_at));
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.locator(".task-card").filter({ has: page.locator(".card-title").filter({ hasText: TITLE }) }).first().click();
  await openTaskList(page);
  await expect(page.locator("#task-activity")).toBeVisible();
  expect(reads).toEqual([]);

  // Act: Activity opens.
  await page.locator("#task-activity > summary").click();

  // Assert
  await expect(delivery(page)).toHaveCount(1);
  await expect(delivery(page).first()).toContainText("Progress check");
  expect(reads).toHaveLength(1);

  // Act: a snapshot in which none of its actions changed, then one in which
  // a progress check was added.
  await send(page, board(2, FIRST.updated_at));
  held.actions = [FIRST, SECOND];
  await send(page, board(3, SECOND.updated_at));

  // Assert: the newest first, from a second read and no other.
  await expect(delivery(page)).toHaveCount(2);
  await expect(delivery(page).nth(0).getByRole("img")).toHaveAccessibleName("Queued");
  await expect(delivery(page).nth(1).getByRole("img")).toHaveAccessibleName("Done");
  expect(reads).toHaveLength(2);
});
