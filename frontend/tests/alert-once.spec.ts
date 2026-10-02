import { expect, test, type Page } from "./site";

// One item, one signal, and only what the CFO has for him. On 2026-10-02 two
// goblins' questions reached the Overlord 19 seconds apart, each as a toast,
// a Windows notification and the Command Center opening itself, and both of
// his answers bounced because the CFO had answered first. His ruling: "The
// Command Center should only give me questions that the CFO has for the
// Overlord, for me", and one item is one signal.
const CFO_ASKS = "The CFO asks: Merge pull request 240 now?";
const AT_QUESTION = "opened the Command Center at question:merge-240";

async function open(page: Page) {
  await page.goto("/tests/fixtures/alert-once.html");
  await page.waitForFunction(() => "step" in window);
}
// Each step waits until the board has drawn its snapshot, so none is skipped.
async function step(page: Page, name: "working" | "asked" | "cfoAsks" | "restarting" | "finished" | "shipped") {
  await page.evaluate((to) => (window as unknown as { step: (name: string) => void }).step(to), name);
  await expect(page.locator("main")).toHaveAttribute("data-step", name);
}
const toasts = (page: Page) => page.locator(".toasts .toast");
const dialog = (page: Page) => page.locator("dialog.question-modal");
const badge = (page: Page) => page.locator("summary.icon-button");
// A browser that allows notifications records each one it is asked to show;
// hidden says whether the board's tab is out of sight.
async function recordNotifications(page: Page, hidden: boolean) {
  await page.addInitScript((isHidden) => {
    class Note {
      static permission = "granted";
      static requestPermission = async () => "granted";
      closed = false;
      onclick: (() => void) | null = null;
      onclose: (() => void) | null = null;
      constructor(public title: string, public options: { body: string; tag: string }) { (window as unknown as { notes: Note[] }).notes.push(this); }
      close() { this.closed = true; this.onclose?.(); }
    }
    Object.assign(window, { notes: [], Notification: Note });
    Object.defineProperty(document, "hidden", { get: () => isHidden });
    document.hasFocus = () => false;
  }, hidden);
}
const notes = (page: Page) => page.evaluate(() => (window as unknown as { notes: { options: { body: string }; closed: boolean }[] }).notes.map((note) => ({ body: note.options.body, closed: note.closed })));

test("a goblin's question to the CFO shows him nothing: no toast, no card, no count, no notification", async ({ page }) => {
  // Arrange: a board out of sight, where every alert would notify.
  await recordNotifications(page, true);
  await open(page);

  // Act: the goblin asks, then finishes, which proves the board took the
  // question's snapshot in before anything is counted.
  await step(page, "asked");
  await step(page, "finished");

  // Assert
  await expect(toasts(page)).toHaveCount(1);
  await expect(toasts(page)).toContainText("cg-board-theme finished: code-goblins #240 is ready.");
  await expect(dialog(page)).toBeHidden();
  await expect(badge(page)).toHaveAccessibleName("Command Center");
  expect(await notes(page)).toEqual([{ body: "cg-board-theme finished: code-goblins #240 is ready.", closed: false }]);
});

test("the CFO's question is one signal on the board: it waits under the count, with no toast, and nothing opens by itself", async ({ page }) => {
  // Arrange
  await recordNotifications(page, false);
  await open(page);

  // Act
  await step(page, "cfoAsks");

  // Assert
  await expect(badge(page)).toHaveAccessibleName("Command Center, 1 waiting on you");
  await expect(page.locator(".toasts")).toHaveCount(0);
  await expect(dialog(page)).toBeHidden();
  expect(await notes(page)).toEqual([]);
});

test("news he dismissed stays dismissed through a supervisor restart and a reload", async ({ page }) => {
  await open(page);
  await step(page, "finished");
  await expect(toasts(page)).toHaveCount(1);
  await toasts(page).getByRole("button", { name: /^Dismiss/ }).click();
  await expect(page.locator(".toasts")).toHaveCount(0);
  // The supervisor restarts, the goblin flickers back to work, and the same
  // news comes back in the next snapshot. Its next pull request after it
  // proves the board took every snapshot in: news that came back would show
  // above it.
  for (const name of ["restarting", "working", "finished", "shipped"] as const) await step(page, name);
  await expect(toasts(page).last()).toContainText("cg-board-theme finished: code-goblins #241 is ready.");
  await expect(toasts(page)).toHaveCount(1);
  // A reload sees the same news arrive again and shows none of it.
  await page.reload();
  await page.waitForFunction(() => "step" in window);
  for (const name of ["finished", "shipped", "working"] as const) await step(page, name);
  await expect(page.locator(".toasts")).toHaveCount(0);
});

test("with the board out of sight an item raises a Windows notification, and clicking it opens that item", async ({ page }) => {
  // Arrange
  await recordNotifications(page, true);
  await open(page);

  // Act
  await step(page, "cfoAsks");

  // Assert: the notification is the signal; the board shows no toast.
  await expect.poll(() => notes(page)).toEqual([{ body: CFO_ASKS, closed: false }]);
  await expect(page.locator(".toasts")).toHaveCount(0);
  await page.evaluate(() => (window as unknown as { notes: { onclick: () => void }[] }).notes[0].onclick());
  await expect(page.locator("output")).toHaveText(AT_QUESTION);
  await expect(dialog(page)).toContainText("Merge pull request 240 now?");
  expect(await notes(page)).toEqual([{ body: CFO_ASKS, closed: true }]);
});
