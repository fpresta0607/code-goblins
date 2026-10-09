// QUIET_MS is how long a shown window goes without a click or key on the
// board before it counts as idle.
export const QUIET_MS = 10 * 60_000;

// windowMayMove says whether the supervisor may move the desktop window this
// page is in onto the program an update installed: it is the window, the
// board is connected, nothing he typed is unsent and no answer is in
// progress, and the window is in the tray or untouched for QUIET_MS.
export function windowMayMove({ inWindow, connected, answering, hidden, quietFor }: { inWindow: boolean; connected: boolean; answering: boolean; hidden: boolean; quietFor: number }): boolean {
  return inWindow && connected && !answering && (hidden || quietFor >= QUIET_MS);
}
