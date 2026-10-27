import { expect, type Page } from "./site";

// The board-level terminal specs run in Classic on Codex and stepped into
// Modern on Claude Code. Both harnesses also have a Modern conversation.
export const RUNS = [{ name: "under Terminal", harness: "codex", isSteppedIn: false }, { name: "stepped in", harness: "claude", isSteppedIn: true }] as const;

// stepIn steps the open session's terminal into its messages' place, as the
// open screen's Show the terminal does once the relay, which say speaks for,
// tells the view the harness asks at a screen of its own. The view may not
// have connected yet, so the relay says it again until the list shows.
export async function stepIn(page: Page, say: (message: object) => void): Promise<void> {
  const show = page.locator(".harness-panel").getByRole("button", { name: "Show the terminal" });
  await expect(async () => {
    say({ type: "screen", asking: true, open: true, working: false, submission: "", panel: { rows: ["Do you want to proceed?"], choices: [{ text: "Yes", number: 1, focused: true }, { text: "No", number: 2 }] } });
    await expect(show).toBeVisible({ timeout: 1000 });
  }).toPass({ timeout: 20_000 });
  await show.click();
  await expect(page.getByRole("button", { name: "Back to messages" })).toBeVisible();
}
