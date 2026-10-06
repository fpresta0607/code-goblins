import type { Comeback } from "./types.ts";

// ComebackLine is the board's one line about the last restart: text says what
// came back, detail what to know beside it, and isDone that nothing waits to
// come back any more, so the line can be dismissed.
export interface ComebackLine { text: string; detail: string; isDone: boolean }

const goblins = (count: number) => count + (count === 1 ? " goblin" : " goblins");

// names joins ids as "a", "a and b" or "a, b and c".
const names = (ids: string[]) => ids.length < 2 ? ids.join("") : ids.slice(0, -1).join(", ") + " and " + ids[ids.length - 1];

// comebackLine is what the board says while the supervisor brings the CFO and
// the goblins back after a restart or sign-out, and once it has: what is
// back, what waits, and what did not come back and where it says why. A
// restart that brought nothing back says nothing.
export function comebackLine(comeback: Comeback | undefined): ComebackLine | null {
  if (!comeback || !comeback.cfo && comeback.goblins.length === 0) return null;
  const { cfo } = comeback;
  const total = comeback.goblins.length;
  const back = comeback.goblins.filter((entry) => entry.state === "back").length;
  const stopped = comeback.goblins.filter((entry) => entry.state === "stopped").map((entry) => entry.id);
  const isWaiting = cfo?.state === "waiting" || comeback.goblins.some((entry) => entry.state === "waiting");
  if (isWaiting) {
    const parts = [];
    if (cfo) parts.push(cfo.state === "back" ? "the CFO is back" : cfo.state === "waiting" ? "the CFO comes back first" : "the CFO did not come back");
    if (total > 0) parts.push(back + " of " + goblins(total) + (back === 1 || total === 1 ? " is back" : " are back"));
    return { text: "Coming back after a restart: " + parts.join(", ") + ".", detail: "The next one starts when memory allows.", isDone: false };
  }
  const resumed = [];
  if (cfo?.state === "back") resumed.push("the CFO");
  if (total > 0) resumed.push(back === total ? goblins(total) : back + " of " + goblins(total));
  const details = [];
  if (cfo?.state === "stopped") details.push("The CFO did not come back: " + cfo.reason.replace(/[. ]+$/, "") + ".");
  if (stopped.length > 0) details.push(names(stopped) + " did not come back; " + (stopped.length === 1 ? "its card says" : "their cards say") + " why.");
  return { text: resumed.length ? "Resumed " + resumed.join(" and ") + " after a restart." : "Nothing came back after a restart.", detail: details.join(" "), isDone: true };
}
