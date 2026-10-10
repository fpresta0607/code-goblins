// windowMayMove says whether the supervisor may move the desktop window this
// page is in onto the program an update installed: it is the window, the
// board is connected, nothing he typed is unsent and no answer is in
// progress, and the window is in the tray. A window he has open stays put:
// moving it ends it and opens another in its place, and on 2026-10-09 his
// window was replaced that way ten minutes after an update, the time a
// shown window had to go untouched before it was moved.
export function windowMayMove({ inWindow, connected, answering, hidden }: { inWindow: boolean; connected: boolean; answering: boolean; hidden: boolean }): boolean {
  return inWindow && connected && !answering && hidden;
}
