import { expect, test, type Page } from "./site";

// One event, one alert: a goblin's question reached the board on 2026-10-01
// as two alerts that said the same thing, and opening one left the other.
const ASKS = "cg-board-theme asks: May I finish the remaining heavy steps now, or stay paused?";
const AT_QUESTION = "opened the Command Center at question:notify-cg-board-theme-3753";

async function open(page: Page) {
  await page.goto("/tests/fixtures/alert-once.html");
  await page.waitForFunction(() => "step" in window);
}
// Each step waits until the board has drawn its snapshot, so none is skipped.
async function step(page: Page, name: "working" | "asked" | "restarting" | "finished" | "shipped") {
  await page.evaluate((to) => (window as unknown as { step: (name: string) => void }).step(to), name);
  await expect(page.locator("main")).toHaveAttribute("data-step", name);
}
const toasts = (page: Page) => page.locator(".toasts .toast");
// A new question opens the Command Center on it by itself; he closes it to
// get back to the board, where the question's alert waits.
async function closeCommandCenter(page: Page) {
  await expect(page.locator("dialog.question-modal")).toContainText("May I finish the remaining heavy steps now, or stay paused?");
  await page.keyboard.press("Escape");
  await expect(page.locator("dialog.question-modal")).toBeHidden();
}

test("a goblin's question shows one alert, and opening it leaves no copy behind", async ({ page }) => {
  await open(page);
  await step(page, "asked");
  await expect(toasts(page)).toHaveCount(1);
  await expect(toasts(page)).toContainText(ASKS);
  await expect(toasts(page).locator(".dialogue-tab")).toHaveCount(0);
  await closeCommandCenter(page);
  await toasts(page).getByRole("button", { name: "Open Command Center" }).click();
  await expect(page.locator("output")).toHaveText(AT_QUESTION);
  await expect(page.locator("dialog.question-modal")).toContainText("May I finish the remaining heavy steps now, or stay paused?");
  await expect(page.locator(".toasts")).toHaveCount(0);
});

test("an alert he dismissed stays dismissed through a supervisor restart and a reload", async ({ page }) => {
  await open(page);
  await step(page, "asked");
  await expect(toasts(page)).toHaveCount(1);
  await closeCommandCenter(page);
  await toasts(page).getByRole("button", { name: /^Dismiss/ }).click();
  await expect(page.locator(".toasts")).toHaveCount(0);
  // The supervisor restarts, the goblin flickers back to work, and the same
  // question comes back in the next snapshot. The goblin's done news after it
  // proves the board took every snapshot in: an alert that came back would
  // show above it.
  for (const name of ["restarting", "working", "asked", "finished"] as const) await step(page, name);
  await expect(toasts(page).last()).toContainText("cg-board-theme finished: code-goblins #240 is ready.");
  await expect(toasts(page)).toHaveCount(1);
  // A reload sees the same question arrive again, then the goblin's next pull
  // request.
  await page.reload();
  await page.waitForFunction(() => "step" in window);
  for (const name of ["asked", "shipped"] as const) await step(page, name);
  await expect(toasts(page).last()).toContainText("cg-board-theme finished: code-goblins #241 is ready.");
  await expect(toasts(page)).toHaveCount(1);
});

test("clicking the Windows notification opens the alert's own item and leaves no copy on the board", async ({ page }) => {
  // A browser that allows notifications, with the board's window behind
  // another, records each notification it is asked to show.
  await page.addInitScript(() => {
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
    document.hasFocus = () => false;
  });
  await open(page);
  await step(page, "asked");
  await expect(toasts(page)).toHaveCount(1);
  const notes = () => page.evaluate(() => (window as unknown as { notes: { options: { body: string }; closed: boolean }[] }).notes.map((note) => ({ body: note.options.body, closed: note.closed })));
  expect(await notes()).toEqual([{ body: ASKS, closed: false }]);
  await page.evaluate(() => (window as unknown as { notes: { onclick: () => void }[] }).notes[0].onclick());
  await expect(page.locator("output")).toHaveText(AT_QUESTION);
  await expect(page.locator(".toasts")).toHaveCount(0);
  expect(await notes()).toEqual([{ body: ASKS, closed: true }]);
});
