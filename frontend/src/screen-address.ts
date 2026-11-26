// The board's address of a goblin's Screen view at full size, in a tab of
// its own, is /?screen=<task>.
const SCREEN_PARAMETER = "screen";

// screenTaskOf is the task a page's address asks the Screen view of, empty
// for the board itself.
export function screenTaskOf(search: string): string {
  return new URLSearchParams(search).get(SCREEN_PARAMETER) || "";
}
