import { useSyncExternalStore } from "react";
import type { ScreenState } from "./terminalStream";
import type { TypedState } from "./terminalView";

// A live terminal's link, which its HostTerminal offers by the terminal
// deck's key for it, so the Messages view's box types through the terminal's
// own view: what that view last knew of its connection and its program, and
// what the box does through it. The box never holds a view itself, so a
// reconnect, which replaces the view, changes nothing for it.
export interface TerminalLink {
  phase: "connecting" | "live" | "closed";
  // Whether the view is reconnecting.
  isReconnecting: boolean;
  // A recording the terminal's own microphone started, and what stops it and
  // what it last reported, so a box shown beside the terminal keeps the one
  // control for that recording and shows its words instead of a second layer.
  isDictating: boolean;
  stopDictation: () => void;
  feedback: string;
  // Whether typed keys sit unread in a busy program's input, why typing is
  // held, and what the view was last told of the harness's screen, null
  // until it is told.
  isUnread: boolean;
  held: string;
  screen: ScreenState | null;
  // Whether the view shows a session on stream-json pipes, which is held for
  // typing yet still stops a turn on Escape.
  isStream: boolean;
  // send types text as one paste and presses Enter, or as keys where isTyped,
  // and says whether it was sent, by its submission ID, or null; paste types
  // text as one paste with no Enter, and says whether it was sent; escape
  // presses Escape.
  send: (text: string, isTyped?: boolean) => string | null;
  paste: (text: string) => boolean;
  escape: () => boolean;
  control: (keys: string) => boolean;
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

// watchLink tells onChange the link of key, or undefined while none is
// offered, each time a link changes from now on, and returns what stops it.
export function watchLink(key: string, onChange: (link: TerminalLink | undefined) => void): () => void {
  return subscribe(() => onChange(links.get(key)));
}

// useTerminalLink is the link of the terminal the deck keeps as key, while
// one is offered.
export function useTerminalLink(key: string): TerminalLink | undefined {
  return useSyncExternalStore(subscribe, () => links.get(key));
}

// The message a link's send last typed as keys into the terminal the deck
// keeps as key, named by the submission send returned, with its words, and
// what became of it (TypedState), as any view of that terminal tells it: one
// a reconnect replaced, or one he left for another goblin, goes on with the
// Enter that waited. It holds the prompt line while its Enter waits, and once
// that Enter was pressed or left its words there, until the terminal's
// current view shows that line without words (clearTyped). It is kept here,
// beside the links, since a switch to another goblin takes the terminal and
// the box off the page, and a box drawn again knew nothing of it: it took the
// list under its words as his question, or typed onto them, and the closed
// view's answer reached no one (#2848's review).
export interface TypedMessage { submission: string; words: string; state: TypedState }

const typedMessages = new Map<string, TypedMessage>();
// What the box drawn for each key says of its message once the Enter left
// the words unsent or the relay never typed them, and the messages it has
// not said that of yet.
const tellers = new Map<string, (message: TypedMessage) => void>();
const untold = new WeakSet<TypedMessage>();

const tell = (key: string) => {
  const message = typedMessages.get(key);
  const teller = tellers.get(key);
  if (!message || !teller || !untold.has(message)) return;
  untold.delete(message);
  teller(message);
};

// recordTyped records what became of the message typed into key's terminal.
export function recordTyped(key: string, message: TypedMessage): void {
  typedMessages.set(key, message);
  if (message.state === "unsent" || message.state === "untyped") untold.add(message);
  for (const listener of listeners) listener();
  tell(key);
}

// clearTyped ends the message's hold on key's prompt line as the terminal's
// current view shows that line without words, once its Enter was pressed or
// left the words there.
export function clearTyped(key: string): void {
  const state = typedMessages.get(key)?.state;
  if (state !== "sent" && state !== "unsent") return;
  typedMessages.delete(key);
  for (const listener of listeners) listener();
}

// tellTyped has teller told of key's message once its Enter left the words
// unsent or the relay never typed them, at once where that was so before,
// each such message once, and returns what stops it.
export function tellTyped(key: string, teller: (message: TypedMessage) => void): () => void {
  tellers.set(key, teller);
  tell(key);
  return () => { if (tellers.get(key) === teller) tellers.delete(key); };
}

// useTypedMessage is the message last typed into the terminal the deck keeps
// as key, as recordTyped last recorded it, until clearTyped ends its hold.
export function useTypedMessage(key: string): TypedMessage | undefined {
  return useSyncExternalStore(subscribe, () => typedMessages.get(key));
}
