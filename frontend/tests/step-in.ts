import { expect, type Page } from "./site";

// The board-level terminal specs run twice: on a Codex session, whose
// Terminal view is its terminal, and stepped in, on a Claude Code session,
// whose Terminal view shows its messages and whose terminal steps into their
// place. RUNS names both and the harness each runs.
export const RUNS = [{ name: "under Terminal", harness: "codex", isSteppedIn: false }, { name: "stepped in", harness: "claude", isSteppedIn: true }] as const;

// stepIn steps the open session's terminal into its messages' place, as the
// amber card's Answer in the terminal does once the relay, which say speaks
// for, tells the view the harness asks at a screen of its own.
export async function stepIn(page: Page, say: (message: object) => void): Promise<void> {
  say({ type: "screen", asking: true, open: true });
  await page.locator(".composer-asking").getByRole("button", { name: "Answer in the terminal" }).click();
  await expect(page.getByRole("button", { name: "Back to messages" })).toBeVisible();
}
