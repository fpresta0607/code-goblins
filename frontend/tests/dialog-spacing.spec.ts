import { expect, holdStream, test, type Locator, type Page } from "./site";

// The Overlord, 2026-10-09 about 13:55Z, with a screenshot of Restart the
// CFO?: "spacing between description and box has no clean padding". Its
// description sat right against the "Its conversation is kept." note, while
// the note, the buttons and the line under them each kept their space from
// the next. Stop this task? and Remove this task? are drawn by the same class
// and sat the same way. The supervisor is played by one held snapshot.
const since = "2026-10-09T09:00:00Z";
const SNAPSHOT = {
  healthy: true, instance: "fixture", revision: 1, attention: [], afk: { state: "off" },
  cfo_runs: true, cfo_harness: "claude", cfo_terminal: "cfo", cfo_terminal_since: since, sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }],
  tasks: [
    { id: "working-one", title: "working-one", project: "code-goblins", phase: "working", verified: false, generation: "working-one-1", since },
    { id: "queued-one", title: "queued-one", project: "code-goblins", phase: "queued", verified: false, generation: "", brief: true, queue_revision: "q1", since },
  ],
};

async function open(page: Page) {
  await holdStream(page, SNAPSHOT);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".task-card").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

async function showTask(page: Page, title: string) {
  await expect(page.locator("#panel-title")).toHaveText(title);
  const pill = page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true });
  if (await pill.count()) await pill.click();
}

// Each confirmation the class draws, and the presses that open it.
const DIALOGS: [string, (page: Page) => Promise<void>][] = [
  ["Restart the CFO?", async (page) => {
    await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
    await showTask(page, "CFO");
    await page.locator(".panel-header").getByRole("button", { name: "Restart the CFO" }).click();
  }],
  ["Stop this task?", async (page) => {
    await page.locator(".task-card").filter({ hasText: "working-one" }).first().click();
    await showTask(page, "working-one");
    await page.locator(".goblin-panel").getByRole("button", { name: "Stop working-one" }).click();
  }],
  ["Remove this task?", async (page) => {
    await page.locator(".task-card").filter({ hasText: "queued-one" }).first().click();
    await showTask(page, "queued-one");
    await page.locator(".goblin-panel").getByRole("button", { name: "Remove queued-one" }).click();
  }],
];

// The space between each part of a dialog and the next, from its description
// down: its note, its buttons and, where it has one, the line under them.
const spaces = (dialog: Locator) => dialog.evaluate((element) => {
  const parts: Element[] = [];
  for (let part: Element | null = document.getElementById(element.getAttribute("aria-describedby")!); part; part = part.nextElementSibling) parts.push(part);
  const boxes = parts.map((part) => part.getBoundingClientRect());
  return boxes.slice(1).map((box, at) => Math.round(box.top - boxes[at].bottom));
});

for (const [size, viewport, scale] of [["the Overlord's window", { width: 1707, height: 960 }, 1.5], ["a phone", { width: 390, height: 844 }, 2]] as const) {
  test.describe(`at ${size}`, () => {
    test.use({ viewport, deviceScaleFactor: scale });

    for (const [name, press] of DIALOGS) {
      test(`${name} keeps the same clear space between its description, its note and its buttons`, async ({ page }) => {
        // Arrange
        await open(page);

        // Act
        await press(page);
        const dialog = page.getByRole("dialog", { name });
        await expect(dialog.locator(".preservation-notice")).toBeVisible();
        const between = await spaces(dialog);

        // Assert: the note is as far under the description as the buttons
        // are under the note, and so is every part after them.
        expect(between.length).toBeGreaterThanOrEqual(2);
        expect(between[0]).toBeGreaterThanOrEqual(16);
        expect(between).toEqual(between.map(() => between[1]));
      });
    }
  });
}
