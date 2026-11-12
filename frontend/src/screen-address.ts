// The board's address of a goblin's Screen view at full size, in a tab of
// its own, which its card's Browser active opens.
const SCREEN_PARAMETER = "screen";

export function screenAddress(task: string): string {
  return "/?" + new URLSearchParams({ [SCREEN_PARAMETER]: task }).toString();
}

// screenTaskOf is the task a page's address asks the Screen view of, empty
// for the board itself.
export function screenTaskOf(search: string): string {
  return new URLSearchParams(search).get(SCREEN_PARAMETER) || "";
}
