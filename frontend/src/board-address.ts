// A board address names one place on the board (#2363): ?task=<id> a goblin's
// panel, and ?item=<key> one Command Center item by the key the board and its
// pushes give it, such as question:<id>. It is the board's own root with that
// one parameter, and it only opens: nothing it names is answered, started or
// stopped.
export interface BoardPlace { kind: "task" | "item"; id: string }

// OPEN_ADDRESS is the page event that hands the board an address to open in
// this page, with no reload, so what he was typing stays: a link in a
// conversation or a terminal raises it with the address.
export const OPEN_ADDRESS = "code-goblins-open-address";

// boardPlace is the place an address names on the board at origin, or null
// for any address that is not a board address.
export function boardPlace(address: string, origin: string): BoardPlace | null {
  if (!URL.canParse(address, origin)) return null;
  const url = new URL(address, origin);
  const names = [...url.searchParams.keys()];
  if (url.origin !== origin || url.pathname !== "/" || names.length !== 1) return null;
  const [kind] = names;
  const id = url.searchParams.get(kind) || "";
  return (kind === "task" || kind === "item") && id ? { kind, id } : null;
}

// openBoardAddress opens a board address in this page and says so; any other
// address, and every address where there is no page, it leaves to its caller.
export function openBoardAddress(address: string): boolean {
  if (typeof location === "undefined" || !boardPlace(address, location.origin)) return false;
  window.dispatchEvent(new CustomEvent(OPEN_ADDRESS, { detail: address }));
  return true;
}
