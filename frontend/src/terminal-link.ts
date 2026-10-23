import { useSyncExternalStore } from "react";
import type { ScreenState } from "./terminalStream";

// A live terminal's link, which its HostTerminal offers by the terminal
// deck's key for it, so the Messages view's box types through the terminal's
// own view: what that view last knew of its connection and its program, and
// what the box does through it. The box never holds a view itself, so a
// reconnect, which replaces the view, changes nothing for it.
export interface TerminalLink {
  phase: "connecting" | "live" | "closed";
  // Whether the view is reconnecting.
  isReconnecting: boolean;
  // Whether typed keys sit unread in a busy program's input, why typing is
  // held, and what the view was last told of the harness's screen, null
  // until it is told.
  isUnread: boolean;
  held: string;
  screen: ScreenState | null;
  // send types text as one paste and presses Enter, and says whether it was
  // sent; escape presses Escape.
  send: (text: string) => boolean;
  escape: () => boolean;
}

const links = new Map<string, TerminalLink>();
const listeners = new Set<() => void>();

export function offerLink(key: string, link: TerminalLink | null): void {
  if (link) links.set(key, link);
  else links.delete(key);
  for (const listener of listeners) listener();
}

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
};

// useTerminalLink is the link of the terminal the deck keeps as key, while
// one is offered.
export function useTerminalLink(key: string): TerminalLink | undefined {
  return useSyncExternalStore(subscribe, () => links.get(key));
}
