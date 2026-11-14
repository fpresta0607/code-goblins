import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { message as errorText, reportToCeo, request } from "./api";
import { copyText } from "./clipboard";
import { conversationPictures } from "./conversation-pictures";
import { Icon } from "./Icon";
import { keptFileAddress } from "./kept-files";
import { MessageRow } from "./message-row";
import { MessageDocumentScope } from "./message-document-scope";
import { drawnSpans, ESTIMATED_ROW_PX, messageRowAt, messageWindow, OVERSCAN_PX } from "./message-window";
import { Notice } from "./notice";
import { PictureOpener } from "./picture-opener";
import { PictureViewer } from "./picture-viewer";
import { ReplyWorking } from "./reply-working";
import { changeMessages, conversationHead, isWrittenReply, messageFontFor, olderMessages, pageMessages, parseConversation, parseMessages, parsePage, parseSession, parseUsage, parseWaiting, storedMessageFont, storeMessageFont, turnEnds, viewStanding, type ConversationHead, type MessagePage, type SessionReport } from "./session-messages";
import { attachZoomGesture } from "./terminal-zoom";
import { object, parseAction, string, type Action, type KeySpend, type SubscriptionUsage, type Task, type TreeNode } from "./types";
import { useTerminalLink } from "./terminal-link";
import { usageReadings } from "./session-usage";
import { UsageBar } from "./usage-bar";
import { harnessInk } from "./harness-ink";
import { QueuedMessage } from "./queued-message";
import { isUndelivered, sameWords, UNDELIVERED_ROW, withTakenCommands, withUndelivered } from "./messages";
import { usePendingSends, type PendingSend } from "./pending-sends";
import { linkedText } from "./messageText";
import "./messages-view.css";

// A stream that keeps dropping stops retrying and says why.
const MAX_RETRIES = 5;
// He reads the newest messages again once he scrolls back this close to the
// list's end.
const AT_END_PX = 24;
// His scroll rests once this long passes with no scroll and nothing held
// down on the list.
const REST_MS = 250;
// How far Copied stands from the line of the selection it says it copied.
const COPIED_GAP = 6;
// The page of messages before the first one held is read once he reads up
// this close to the list's top, so it is there before he reaches it.
const OLDER_WITHIN_PX = 600;
// An older page that failed to arrive is asked for again on his next
// approach to the top once this long has passed since it failed.
const OLDER_RETRY_MS = 5000;
// Once this many older pages in a row fail to arrive, none is asked for
// until the stream reconnects.
const OLDER_TRIES = 3;
// A row with no folds open, shared so its props stay the same.
const NO_FOLDS: ReadonlySet<string> = new Set();
const UNREAD = "The session's messages cannot be read";
const RECENT_SESSIONS = 8;
const RECENT_MESSAGES = 50;
type PublishedMessages = MessagePage & { isWorking: boolean; said: string; task?: Task; sent: Action[]; pending: Action[]; sending: PendingSend[] };
// What the box under the conversation shows of the session: what it spent and
// the words he sent it.
type Composing = Pick<MessagePage, "usage"> & { history: string[] };

// A Claude Code session's messages, read only: the supervisor's stream of
// them (/api/messages), drawn as the session's conversation. It sits in the
// box the session's terminal fills, so switching between the two never
// resizes the terminal, and stays mounted while its panel shows the session.
// The list scrolls as a page does, with the wheel, the page keys and
// Shift with them as in a terminal; reading up, nothing moves it, new
// messages and older ones read in above alike, and Jump to latest returns to
// the end, which then follows what arrives. Ctrl+Plus, Ctrl+Minus and Ctrl+0,
// the right button with the wheel, or the wheel with Ctrl and Shift, size its
// text, kept in this browser.
// query names the session as /api/messages takes it (messagesQuery), name is
// whose replies they are, task is the goblin whose baby goblins its calls
// open, absent for the CEO, terminal is the key of the session's terminal,
// whose link reads what it is doing, sent is every message of his to the session the
// board knows, pending those of them that still wait, and box is the message
// box under the conversation, told whether the stream says a turn runs. onAskSlot takes the conversation's last row,
// where the box draws what the harness's own screen asks (#2223).
// ChatState is what keeps the conversation a view shows from being the
// session's own now, which its panel's header says (#2438): a dropped
// connection while it reconnects, and why the conversation drawn is stale or
// its stream cannot be kept, or nothing.
export interface ChatState { isReconnecting: boolean; trouble: string }
export const QUIET_CHAT: ChatState = { isReconnecting: false, trouble: "" };
export function MessagesView({ query, instance, pages, name, task, terminal, harness, model, effort, isCompacting, sent, pending, subscriptions, spend, shown, visible, onChild, onComposerDock, onAskSlot, onOpenCommand, onState, box }: {
  query: string; instance: string; name: string; task?: Task; terminal: string; harness: string; model: string; effort: string; isCompacting: boolean;
  pages: Map<string, MessagePage>;
  sent: Action[]; pending: Action[];
  subscriptions?: SubscriptionUsage[]; spend?: KeySpend[];
  shown: boolean; visible: boolean; onChild: (child: TreeNode, source: HTMLElement) => void; box?: (model: string, history: string[], effort: string, usage: ReactNode, onSend: () => void, isTurnRunning: boolean) => ReactNode;
  onComposerDock?: (node: HTMLDivElement | null) => void; onAskSlot?: (node: HTMLDivElement | null) => void;
  // onOpenCommand opens the Command Center on an item, as a block's Run does
  // on the run card it made.
  onOpenCommand?: (key: string) => void;
  // onState hears what keeps the conversation shown from being the session's
  // own now, which the panel's header says, or nothing.
  onState: (state: ChatState) => void;
}) {
  // What he sent that the board has not shown yet, drawn at once on its way.
  const sending = usePendingSends();
  const link = useTerminalLink(terminal);
  const said = link?.screen?.isWorking && link.screen.doing || "";
  const list = useRef<HTMLDivElement>(null);
  const items = useRef<HTMLDivElement>(null);
  const pageKey = instance + "\0" + query;
  const [held, setHeld] = useState<PublishedMessages>(() => ({ conversation: "", messages: [], more: false, waiting: [], ...pages.get(pageKey), isWorking: false, said, task, sent, pending, sending }));
  const heldValue = useRef(held);
  // The stream keeps one latest version while the displayed history stays
  // where he reads. Returning to the end publishes it once, without replay.
  const latest = useRef(held);
  // Where the latest version ends, so a view that holds it back knows it
  // draws less than it has.
  const [received, setReceived] = useState<ConversationHead>(() => conversationHead(held));
  // The box speaks for the session as it is now, however far up he reads, so
  // it follows the stream while the history he reads holds still.
  const [composing, setComposing] = useState<Composing>(() => composerOf({ history: [] }, held));
  const [phase, setPhase] = useState<"connecting" | "live" | "closed">("connecting");
  // What the fleet's stream says of the session's turn, which alone draws the
  // working indicator: never the board's snapshot of the terminal, which can
  // say a finished turn still works.
  const [report, setReport] = useState<SessionReport | null>(null);
  // Behind and working are read from the stream's word and the view's own
  // place: the conversation drawn against what the stream sent and what its
  // head names. The working indicator is that word as it is now, never kept
  // from when he left the end, so a turn that ended draws no indicator
  // wherever he reads. What the rows he reads show of their turn, its end, a
  // reply's Run and the working row's room, is published with the messages,
  // so what he reads up keeps still. A compaction is the session's own state
  // rather than a turn, and draws the one indicator too once the view is
  // live.
  const standing = viewStanding(report, conversationHead(held), received);
  const isAtWork = standing.isWorking || phase === "live" && isCompacting;
  // The box's Stop reads the same word of the stream, as the session is now
  // however far up he reads, so no Stop shows for a turn that ended, whatever
  // ended it (#2292).
  const isTurnRunning = report?.state === "running" || phase === "live" && isCompacting;
  const [isReconnecting, setReconnecting] = useState(false);
  const [closedWhy, setClosedWhy] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [font, setFont] = useState(storedMessageFont);
  // A selection he settles with a mouse or a key copies itself, and says
  // Copied right under where it ends, as a button says it on itself, never in
  // a notice over the conversation's top (#2438): copiedAt is where the
  // selection's last line lay, and copied the Copied placed by it.
  const [copiedAt, setCopiedAt] = useState<DOMRect | null>(null);
  const copied = useRef<HTMLDivElement>(null);
  const copiedTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const fontValue = useRef(font);
  const shownValue = useRef(shown);
  const [isAtEnd, setAtEnd] = useState(true);
  // Whether he reads the end of the list, which then follows new messages;
  // while he reads further up, anchor is the message at the list's top and
  // how far its top lies below the list's, which stays put, from the scroll
  // top observedTop the view last saw or put the list at.
  const following = useRef(true);
  // WebKit can clamp the old scroll top while Jump replaces windowed rows,
  // before layout settles at the newly measured end.
  const isJumping = useRef(false);
  // WebKit lays out a commit that takes rows from the list and restyles
  // anything under a size container in steps, and pulls the scroll top up on
  // the way by itself: a reply's first streamed line, which brought its usage
  // to the box, took a phone off the live end. So take draws each stream
  // update at once, where it just found his scroll, and no scroll position
  // the list finds while it draws is his.
  const isTaking = useRef(false);
  const anchor = useRef<{ id: string; offset: number } | null>(null);
  const observedTop = useRef<number | null>(null);
  // Where his last scroll left the list, apart from observedTop, as a streamed
  // line or a board snapshot can note his scroll before its event says so. It
  // takes what the list's own write at rest moves it by, which is no scroll of
  // his down.
  const scrolledTo = useRef(0);
  const reconcile = useRef<() => void>(() => {});
  const floor = useRef<HTMLDivElement>(null);
  // The room above the rows drawn stands for the rows it holds at the
  // window's heights. A row drawn into it, and while he scrolls anything else
  // that moves above him, takes its room from it, so the rows below stay
  // where he reads them and the view writes no scroll position: a browser
  // carrying his scroll on drops one written meanwhile, as Chromium does on
  // its way to where a key's or a wheel's smooth scroll or a dragged
  // scrollbar set out for, and the rows jump by what their estimates missed.
  // roomHeights is the window's heights the room stands for when it was last
  // laid out, roomShift how far the room stands from them, and roomTop the
  // message drawn first then. The room's style draws its window's heights
  // with the rows and takes its shift from --room-shift, and is never read
  // back, as Chromium gives it to six significant digits.
  const roomHeights = useRef(0);
  const roomShift = useRef(0);
  const roomTop = useRef("");
  const isScrolling = useRef(false);
  const isPressed = useRef(false);
  const isTouching = useRef(false);
  const restTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  // The page holds only the rows within reach of where he reads
  // (message-window.ts): rowHeights is each drawn row's measured height with
  // the gap after it, by its message's id, and view where he reads, kept only
  // when it moves which rows are drawn: the list's height, and the message at
  // its top then with how far that message's top lay below the list's, so an
  // older page read in above it draws the same rows, or the scroll top while
  // no message is at the top.
  const [rowHeights, setRowHeights] = useState<ReadonlyMap<string, number>>(() => new Map());
  const [view, setView] = useState({ top: 0, height: 0, anchor: "", offset: 0 });
  // The rows the page's selection starts and ends in while it starts in this
  // list, so the window keeps them drawn and a long selection copies whole. A
  // caret a press on a control leaves, as on a fold's line or a button, is no
  // place he reads from, so it keeps no row drawn.
  const [selected, setSelected] = useState<{ from: string; to: string; isCollapsed: boolean } | null>(null);
  useEffect(() => {
    const rowOf = (node: Node | null) => (node instanceof Element ? node : node?.parentElement)?.closest<HTMLElement>("[data-message]")?.dataset.message ?? "";
    const read = () => {
      const box = list.current, selection = document.getSelection();
      const from = selection?.anchorNode && box?.contains(selection.anchorNode) ? rowOf(selection.anchorNode) : "";
      const to = selection?.focusNode && box?.contains(selection.focusNode) ? rowOf(selection.focusNode) : "";
      const isCollapsed = !!selection?.isCollapsed;
      const anchor = selection?.anchorNode;
      const isOnControl = !!(anchor instanceof Element ? anchor : anchor?.parentElement)?.closest("summary, button, a, input, textarea");
      if (isCollapsed && isOnControl) { setSelected(null); return; }
      setSelected((prior) => !from ? null : prior?.from === from && prior.to === to && prior.isCollapsed === isCollapsed ? prior : { from, to, isCollapsed });
    };
    document.addEventListener("selectionchange", read);
    return () => document.removeEventListener("selectionchange", read);
  }, []);
  // The folds he opened, by message and by fold within it, kept here so a
  // row that leaves the window and comes back opens as he left it, and a row
  // is drawn again only when its own folds change.
  const [openFolds, setOpenFolds] = useState<ReadonlyMap<string, ReadonlySet<string>>>(() => new Map());
  // runCode asks the board to run a PowerShell block of a reply, named by
  // where it is in this session's conversation and never by its text, and
  // opens the run card whose terminal runs it.
  const openCommand = useRef(onOpenCommand);
  useLayoutEffect(() => { openCommand.current = onOpenCommand; });
  const runCode = useCallback(async (message: string, block: number, fence: number) => {
    const session = new URLSearchParams(query);
    const answer = object(await request("/api/code-run", undefined, {
      method: "POST", headers: { "Content-Type": "application/json", "X-CEO-Token": instance },
      body: JSON.stringify({ task: session.get("task") ?? "", generation: session.get("generation") ?? "", ceo: session.get("ceo") ?? "", message, block, fence }),
    }));
    openCommand.current?.("run:" + string(answer.run_id));
  }, [query, instance]);
  const foldRow = useCallback((message: string, fold: string, isOpen: boolean) => setOpenFolds((prior) => {
    const folds = new Set(prior.get(message));
    if (isOpen === folds.has(fold)) return prior;
    if (isOpen) folds.add(fold); else folds.delete(fold);
    const next = new Map(prior);
    if (folds.size) next.set(message, folds); else next.delete(message);
    return next;
  }), []);
  // His messages drawn where he sent them that he sent again or took off the
  // board, kept here so a row that leaves the window and comes back never
  // offers that press again (#2541).
  const [putAway, setPutAway] = useState<ReadonlySet<string>>(() => new Set());
  const putAwayRow = useCallback((id: string) => setPutAway((prior) => new Set(prior).add(id)), []);
  // The message whose row holds the keyboard, so the keyboard goes to the
  // list when that row leaves the window and the list's keys keep working.
  const focusedRow = useRef("");
  // Whether an older page is on its way, and how many failed to arrive in a
  // row and when the last did: his next approach to the top asks again once
  // OLDER_RETRY_MS have passed, until OLDER_TRIES have failed, which leaves
  // the rest unread until the stream's next page, the first a reconnect sends.
  const readingOlder = useRef<{ conversation: string; id: string; seq: number } | null>(null);
  const olderFailures = useRef({ count: 0, at: 0 });
  // An older page that arrives while he scrolls waits for his scroll to rest:
  // read in above him then, it would move the list by a scroll position his
  // scroll drops.
  const olderWaiting = useRef<(() => void) | null>(null);
  const onChildValue = useRef(onChild);
  const address = useCallback((extra: Record<string, string>) => {
    const params = new URLSearchParams(query);
    params.set("token", instance);
    for (const [key, value] of Object.entries(extra)) params.set(key, value);
    return "/api/messages?" + params.toString();
  }, [query, instance]);
  const where = "the messages of " + name;

  useEffect(() => {
    if (!visible) return;
    readingOlder.current = null;
    olderWaiting.current = null;
    const stream = new EventSource(address({ stream: "1" }));
    let failures = 0;
    // A stream that ends or drops is opened again, and says Reconnecting
    // until it does. Nothing infers that the conversation is stale from how
    // long the stream was quiet: the stream says itself what the session's
    // turn is and where its head is, and a view that has drawn less than the
    // head is behind.
    const close = (why: string) => {
      stream.close();
      setReconnecting(false);
      setClosedWhy(why);
      setPhase("closed");
      // The stream has ended, so nothing it last said of the turn is drawn: a
      // closed view offers the Reconnect, never an indicator for a turn that
      // is not running.
      setReport(null);
    };
    const take = (change: (prior: MessagePage) => MessagePage) => {
      failures = 0;
      const next = { ...latest.current, ...change(latest.current) };
      latest.current = next;
      // Reopening a session paints its last page while its fresh stream
      // connects. Keep data alone, bounded, with no inactive view or stream.
      pages.delete(pageKey);
      pages.set(pageKey, { conversation: next.conversation, messages: next.messages.slice(-RECENT_MESSAGES), more: next.more || next.messages.length > RECENT_MESSAGES, usage: next.usage, waiting: next.waiting });
      if (pages.size > RECENT_SESSIONS) pages.delete(pages.keys().next().value!);
      const reading = readingOlder.current;
      if (reading && (reading.conversation !== next.conversation || reading.id !== next.messages[0]?.id || reading.seq !== next.messages[0]?.seq)) {
        readingOlder.current = null;
        olderWaiting.current = null;
      }
      reconcile.current();
      const head = conversationHead(next);
      isTaking.current = true;
      try {
        flushSync(() => {
          if (following.current) setHeld(next);
          setReceived((prior) => prior.conversation === head.conversation && prior.newest === head.newest ? prior : head);
          setComposing((prior) => composerOf(prior, next));
          setReconnecting(false);
          setPhase("live");
        });
      } finally {
        isTaking.current = false;
      }
    };
    const invalid = () => {
      close(UNREAD);
      reportToCeo(where, "Invalid messages stream");
    };
    stream.addEventListener("page", (event: MessageEvent<string>) => {
      try {
        const page = parsePage(JSON.parse(event.data));
        olderFailures.current = { count: 0, at: 0 };
        // A page begins the state the session reports from, so no word of the
        // turn before it is drawn over it; its own session event follows.
        setReport(null);
        take((prior) => pageMessages(prior, page));
      } catch { invalid(); }
    });
    stream.addEventListener("messages", (event: MessageEvent<string>) => {
      try { const data = object(JSON.parse(event.data)); const conversation = parseConversation(data.conversation); const changed = parseMessages(data.messages); const waiting = parseWaiting(data.waiting); take((prior) => { if (prior.conversation !== conversation) throw new Error("Invalid conversation"); return { ...prior, messages: changeMessages(prior.messages, changed), waiting }; }); } catch { invalid(); }
    });
    stream.addEventListener("usage", (event: MessageEvent<string>) => {
      try { const data = object(JSON.parse(event.data)); const conversation = parseConversation(data.conversation); const usage = parseUsage(data.usage); take((prior) => { if (prior.conversation !== conversation) throw new Error("Invalid conversation"); return { ...prior, usage }; }); } catch { invalid(); }
    });
    stream.addEventListener("session", (event: MessageEvent<string>) => {
      try { setReport(parseSession(JSON.parse(event.data))); } catch { invalid(); }
    });
    stream.addEventListener("unavailable", (event: MessageEvent<string>) => {
      let why = "";
      try { why = string(JSON.parse(event.data)); } catch { /* the reason is the stream's own */ }
      close(why || UNREAD);
    });
    // A stream the supervisor refused is closed for good; one that dropped,
    // as each does after ten minutes, reconnects by itself. A drop says
    // nothing of the turn, so the view keeps its last word through it and
    // claims no turn end and no Send, but draws the working indicator only
    // while its stream is live and connected, as the turn may end meanwhile.
    // A view whose stream went away counts as reconnecting until its next
    // stream speaks.
    stream.onerror = () => {
      if (stream.readyState === EventSource.CLOSED || ++failures >= MAX_RETRIES) close(UNREAD);
      else setReconnecting(true);
    };
    return () => {
      if (stream.readyState !== EventSource.CLOSED) setReconnecting(true);
      stream.close();
    };
  }, [address, visible, attempt, where, pages, pageKey]);

  // settle keeps the list where he reads it after anything moved its
  // contents: at its end while he follows it, or else with the message at its
  // top where it was, by the room above the rows drawn while he scrolls and
  // by the scroll position once he rests.
  const settle = useCallback(() => {
    const box = list.current, rows = items.current;
    if (!box || !rows || !shownValue.current) return;
    const estimated = roomHeights.current, room = estimated + roomShift.current;
    const kept = anchor.current;
    const row = !following.current && kept && [...rows.querySelectorAll<HTMLElement>(":scope > [data-message]")].find((each) => each.dataset.message === kept.id);
    if (kept && row && observedTop.current !== null) {
      // How far the message he reads moved in the list's content since the
      // view noted it, whatever his scroll did meanwhile.
      const change = row.getBoundingClientRect().top - box.getBoundingClientRect().top + box.scrollTop - kept.offset - observedTop.current;
      if (isScrolling.current && estimated > 0 && room - change >= 0) {
        if (Math.abs(change) > .5) {
          roomShift.current -= change;
          rows.style.setProperty("--room-shift", roomShift.current + "px");
        }
      } else {
        // At rest the room gives back what it lent the rows drawn into it, so
        // it can lend the next scroll's, and once no row is left above it,
        // what it holds beyond them. The scroll position takes that and
        // whatever else moved, and the room keeps what the scroll position
        // cannot take, on its whole pixels or at its ends, until no row is
        // left above it: then it holds nothing at rest, and what the scroll
        // position could not take at the top moves the first message into
        // place, where he sees only the blank above it.
        const owed = isScrolling.current ? 0 : estimated > 0 ? Math.min(roomShift.current, 0) : roomShift.current;
        const moving = change - owed;
        let moved = 0;
        if (Math.abs(moving) > .5) {
          const top = box.scrollTop;
          box.scrollTop = top + moving;
          moved = box.scrollTop - top;
          observedTop.current += moved;
          scrolledTo.current += moved;
        }
        const shift = Math.max(0, room - change + moved) - estimated;
        if (!isScrolling.current && estimated === 0 && shift > 0) {
          anchor.current = { ...kept, offset: kept.offset - shift };
          roomShift.current = 0;
        } else roomShift.current = shift;
        rows.style.setProperty("--room-shift", roomShift.current + "px");
      }
    }
    reconcile.current();
    if (following.current) {
      roomShift.current = 0;
      rows.style.setProperty("--room-shift", "0px");
    }
    // Rows leaving the window go from the page before those taking their
    // place arrive, and WebKit can lay the list out between the two: near the
    // end, a list short of them pulls his scroll up to what is left, and keeps
    // it there once they arrive. The floor holds the list's height through
    // that, and takes its own here, before the follow reads the height.
    const { paddingTop, paddingBottom } = getComputedStyle(box);
    if (floor.current) floor.current.style.height = rows.getBoundingClientRect().height + parseFloat(paddingTop) + parseFloat(paddingBottom) + "px";
    if (following.current) {
      box.scrollTo({ top: box.scrollHeight, behavior: "instant" });
      observedTop.current = box.scrollTop;
      isJumping.current = false;
    }
  }, []);
  // restLater counts his scroll at rest once REST_MS pass with no scroll and
  // nothing held down on the list, a dragged scrollbar included, and settles
  // the list then. A finger is held until the last one lifts, though the
  // browser cancels its pointer once it takes the pan, and a mouse or pen
  // only by its main button, as a context menu that opens on a press takes
  // the release of any other, and only until a context menu opens or the
  // window loses its focus, since either takes the release of that one too.
  const restLater = useCallback(() => {
    clearTimeout(restTimer.current);
    restTimer.current = setTimeout(() => {
      if (isPressed.current || isTouching.current) return;
      isScrolling.current = false;
      const older = olderWaiting.current;
      olderWaiting.current = null;
      older?.();
      settle();
    }, REST_MS);
  }, [settle]);
  useEffect(() => {
    const releasePress = () => {
      if (!isPressed.current) return;
      isPressed.current = false;
      restLater();
    };
    const releaseTouch = (event: TouchEvent) => {
      if (!isTouching.current || event.touches.length) return;
      isTouching.current = false;
      restLater();
    };
    window.addEventListener("pointerup", releasePress);
    window.addEventListener("pointercancel", releasePress);
    window.addEventListener("contextmenu", releasePress, true);
    window.addEventListener("blur", releasePress);
    window.addEventListener("touchend", releaseTouch);
    window.addEventListener("touchcancel", releaseTouch);
    return () => {
      clearTimeout(restTimer.current);
      window.removeEventListener("pointerup", releasePress);
      window.removeEventListener("pointercancel", releasePress);
      window.removeEventListener("contextmenu", releasePress, true);
      window.removeEventListener("blur", releasePress);
      window.removeEventListener("touchend", releaseTouch);
      window.removeEventListener("touchcancel", releaseTouch);
    };
  }, [restLater]);

  const readOlder = useCallback(() => {
    const first = heldValue.current.messages[0];
    const failed = olderFailures.current;
    const conversation = heldValue.current.conversation;
    if (readingOlder.current || !conversation || conversation !== latest.current.conversation || !heldValue.current.more || !first || latest.current.messages[0]?.id !== first.id || latest.current.messages[0]?.seq !== first.seq || failed.count >= OLDER_TRIES || Date.now() - failed.at < OLDER_RETRY_MS) return;
    const reading = { conversation, id: first.id, seq: first.seq };
    readingOlder.current = reading;
    request(address({ before: String(first.seq), conversation })).then((value) => {
      if (readingOlder.current !== reading) return;
      const page = parsePage(value);
      olderFailures.current = { count: 0, at: 0 };
      const readIn = () => {
        if (readingOlder.current !== reading) return;
        readingOlder.current = null;
        // A page of a conversation the view no longer shows is dropped.
        if (page.conversation !== conversation || heldValue.current.conversation !== conversation || latest.current.conversation !== conversation || heldValue.current.messages[0]?.id !== first.id || heldValue.current.messages[0]?.seq !== first.seq || latest.current.messages[0]?.id !== first.id || latest.current.messages[0]?.seq !== first.seq) return;
        reconcile.current();
        const longer = { ...latest.current, ...olderMessages(latest.current, page) };
        latest.current = longer;
        setComposing((prior) => composerOf(prior, longer));
        setHeld((prior) => prior.conversation === conversation && latest.current.conversation === conversation && prior.messages[0]?.id === first.id && prior.messages[0]?.seq === first.seq ? { ...prior, ...olderMessages(prior, page) } : prior);
      };
      if (isScrolling.current) olderWaiting.current = readIn;
      else readIn();
    }).catch((error: unknown) => {
      if (readingOlder.current !== reading) return;
      readingOlder.current = null;
      olderFailures.current = { count: olderFailures.current.count + 1, at: Date.now() };
      reportToCeo(where, errorText(error));
    });
  }, [address, where]);

  // The rows drawn: those within reach of where he reads, or of the end while
  // he follows it, each row not measured yet taken at an estimate.
  // The rows are the session's messages, the commands the board saw its
  // harness take that the harness keeps no row of, and his messages whose
  // delivery ended with no take seen, each where he sent it (#2541).
  const taken = useMemo(() => withTakenCommands(held.messages, held.sent, harness), [held.messages, held.sent, harness]);
  // Once the session's record shows his words, written since the board took
  // them, its row is his message, and the board's copy goes, so it shows once
  // whichever of the two arrives first. Each row of his is one message, the
  // oldest the board took of the same words, settled or still waiting, so a
  // second message of the same words waits for its own row; a row the CEO or
  // the fleet wrote is never his. One the board settled had its row before it
  // settled, as the board settles a message only once it has seen the harness
  // take it, so a later row is never an older message's whose own row is not
  // loaded. A send the board has not taken yet names no time, so an earlier
  // message of the same words never hides it.
  const { read, undelivered } = useMemo(() => {
    const rows = ownPrompts(taken);
    const read = new Set<string>();
    const owners = held.sent.filter((action) => action.created_at && (action.status === "queued" || action.status === "running" || action.status === "succeeded"));
    for (const action of owners.sort((one, other) => Date.parse(one.created_at) - Date.parse(other.created_at))) {
      const row = rows.findIndex((each) => Date.parse(each.at) >= Date.parse(action.created_at) && (action.status !== "succeeded" || Date.parse(each.at) <= Date.parse(action.updated_at)) && sameWords(each.text, action.text));
      if (row < 0) continue;
      rows.splice(row, 1);
      read.add(action.id);
    }
    // His messages whose delivery ended with no take seen, failed or typed
    // and never seen taken, stay where he sent them with what happened and
    // Send again until something he sent after them lands, a message of the
    // board's whose row or take was seen, by when he sent it, or a row of his
    // no board message owns, so none reads as on its way for ever (#2242),
    // or until he sends one again or takes it off the board.
    const hasLandedSince = (sentAt: number) => rows.some((row) => Date.parse(row.at) > sentAt)
      || held.sent.some((other) => (other.status === "succeeded" || read.has(other.id)) && Date.parse(other.created_at) > sentAt);
    return { read, undelivered: held.sent.filter((action) => isUndelivered(action) && !putAway.has(action.id) && !hasLandedSince(Date.parse(action.created_at))) };
  }, [taken, held.sent, putAway]);
  const conversation = useMemo(() => withUndelivered(taken, undelivered, held.more), [taken, undelivered, held.more]);
  const rowIndices = useMemo(() => new Map(conversation.map((message, index) => [message.id, index])), [conversation]);
  const offsets = useMemo(() => {
    const offsets = [0];
    for (const message of conversation) offsets.push(offsets[offsets.length - 1] + (rowHeights.get(message.id) ?? ESTIMATED_ROW_PX));
    return offsets;
  }, [conversation, rowHeights]);
  const windowAt = (place: typeof view, isEnd: boolean) => {
    const anchorAt = rowIndices.get(place.anchor);
    const readTop = anchorAt === undefined ? place.top : offsets[anchorAt] - place.offset;
    return messageWindow(offsets, isEnd ? Infinity : readTop, place.height);
  };
  const drawn = windowAt(view, isAtEnd);
  const rowAt = (id: string) => rowIndices.get(id) ?? -1;
  const spans = drawnSpans(drawn, rowAt(selected?.from ?? ""), rowAt(selected?.to ?? ""), selected?.isCollapsed ?? true);
  const heightOf = (start: number, end: number) => offsets[end] - offsets[start];

  // The room above the rows drawn holds the window's heights of the rows it
  // stands for, never less than nothing. A row drawn into it from above lays
  // out at its own height where the room held the window's, an estimate until
  // the row is measured, so the room keeps the difference and nothing below
  // moves for it. The window's heights are drawn with the rows they stand for,
  // in the same commit, so nothing that lays the list out before this runs
  // finds rows gone from it without their room.
  const estimatedRoom = heightOf(0, spans[0].start);
  useLayoutEffect(() => {
    const rows = items.current;
    if (!rows) return;
    const drawnRows = [...rows.querySelectorAll<HTMLElement>(":scope > [data-message]")];
    const wasFirst = drawnRows.findIndex((row) => row.dataset.message === roomTop.current);
    if (!following.current && wasFirst > 0) {
      const gap = parseFloat(getComputedStyle(rows).rowGap) || 0;
      for (const row of drawnRows.slice(0, wasFirst)) {
        const at = rowIndices.get(row.dataset.message ?? ""), size = row.getBoundingClientRect().height;
        if (at !== undefined && size) roomShift.current += offsets[at + 1] - offsets[at] - size - gap;
      }
    }
    roomTop.current = drawnRows[0]?.dataset.message ?? "";
    roomHeights.current = estimatedRoom;
    roomShift.current = Math.max(roomShift.current, -estimatedRoom);
    rows.style.setProperty("--room-shift", roomShift.current + "px");
  }, [estimatedRoom, offsets, rowIndices]);

  // anchorOf finds the message at the list's top and how far above or below
  // that top it lies, which the window reads the rows from.
  const anchorOf = (box: HTMLElement) => {
    const top = box.getBoundingClientRect().top;
    const first = [...box.querySelectorAll<HTMLElement>("[data-message]")].find((row) => row.getBoundingClientRect().bottom > top);
    const firstTop = first?.getBoundingClientRect().top ?? Infinity;
    if (first && firstTop < top + box.clientHeight) return { id: first.dataset.message || "", offset: firstTop - top };
    // A jump past the rows drawn finds none of them in view, so the message
    // at the top is read from the rows' heights, where the window places
    // each row.
    const at = (items.current?.getBoundingClientRect().top ?? top) - top + roomShift.current;
    const index = Math.min(conversation.length - 1, messageRowAt(offsets, -at));
    const message = conversation[index];
    return message ? { id: message.id, offset: at + offsets[index] } : null;
  };
  const placeOf = (box: HTMLElement, kept: { id: string; offset: number } | null) => ({ top: box.scrollTop, height: box.clientHeight, anchor: kept?.id ?? "", offset: kept?.offset ?? 0 });
  // remember notes where he reads after he scrolls, draws the rows that come
  // within reach, and reads the older page once he nears the top.
  const rememberPosition = () => {
    const box = list.current;
    if (!box || !shownValue.current) return;
    const prior = observedTop.current ?? box.scrollTop;
    observedTop.current = box.scrollTop;
    // Any scroll of his up from the end leaves it at once, as a trackpad's
    // slow drag or a flick's first frames do, so the next streamed line never
    // pulls him back down; one that finds the list where it put itself is its
    // own and keeps it following, as Jump to latest's lands before the rows it
    // draws at the end are measured and those taller than their estimate push
    // the end on.
    // He comes back to the end only once what waits below it is drawn, as
    // readInBelow does.
    const distance = box.scrollHeight - box.scrollTop - box.clientHeight;
    const isEnd = (following.current || heldValue.current.messages === latest.current.messages)
      && (distance <= 1 || (following.current ? box.scrollTop >= prior : distance <= AT_END_PX && box.scrollTop > prior));
    following.current = isEnd;
    setAtEnd(isEnd);
    if (isEnd) setHeld(latest.current);
    anchor.current = anchorOf(box);
    const place = placeOf(box, anchor.current);
    const next = windowAt(place, isEnd);
    if (next.start !== drawn.start || next.end !== drawn.end || isEnd !== isAtEnd) setView(place);
  };
  // readInBelow draws what streamed while he read further up once his scroll
  // down brings the end he left within a screen, so the list goes on below
  // him. The rows drawn move to the message he reads first, as a scroll can
  // land past them, so what lies below is measured and not its estimate; that
  // message goes to the view alone, as settle pairs the anchor with the scroll
  // top noted beside it. He follows from that end only if the rest lies within
  // a screen too: from further off the list would leap by all that streamed
  // meanwhile, and he reads on down to it.
  const readInBelow = (box: HTMLElement) => {
    const distance = box.scrollHeight - box.scrollTop - box.clientHeight;
    const isDown = box.scrollTop > scrolledTo.current;
    scrolledTo.current = box.scrollTop;
    if (following.current || !isDown || distance > box.clientHeight || heldValue.current.messages === latest.current.messages) return;
    isTaking.current = true;
    try {
      flushSync(() => {
        setHeld(latest.current);
        setView(placeOf(box, anchorOf(box)));
      });
    } finally {
      isTaking.current = false;
    }
    following.current = distance <= AT_END_PX && box.scrollHeight - box.scrollTop - box.clientHeight <= Math.min(box.clientHeight, OVERSCAN_PX);
  };
  const remember = () => {
    isScrolling.current = true;
    restLater();
    const box = list.current;
    if (!box || !shownValue.current) return;
    readInBelow(box);
    rememberPosition();
    if (box.scrollTop - roomShift.current < OLDER_WITHIN_PX) readOlder();
  };

  useLayoutEffect(() => {
    reconcile.current = () => {
      const box = list.current;
      if (!isJumping.current && !isTaking.current && shown && shownValue.current && box && box.clientHeight && observedTop.current !== null && box.scrollTop !== observedTop.current) rememberPosition();
    };
  });
  useLayoutEffect(() => {
    // Each snapshot of the board draws the panel again with new lists of the
    // same messages, so the view publishes only what changed, or it would
    // draw itself a second time for every snapshot.
    const known = latest.current;
    if (known.isWorking === isAtWork && known.said === said && known.task === task && known.sending === sending
      && isSameList(known.sent, sent) && isSameList(known.pending, pending)) return;
    latest.current = { ...known, isWorking: isAtWork, said, task, sent, pending, sending };
    reconcile.current();
    if (following.current) setHeld(latest.current);
    else setHeld((prior) => {
      const localIDs = new Set([...prior.sending, ...sending].map((send) => send.id));
      const handedOff = sent.filter((action) => localIDs.has(action.id) && (action.status === "queued" || action.status === "running" || action.status === "succeeded") && !prior.pending.some((heldAction) => heldAction.id === action.id));
      return prior.sending === sending && !handedOff.length ? prior : { ...prior, sending, pending: [...prior.pending, ...handedOff] };
    });
  }, [isAtWork, said, phase, task, sent, pending, sending]);

  // Each drawn row is measured as it lays out and again whenever it changes,
  // as a picture loads or a call unfolds; a row the hidden view lays out at
  // no size keeps the height it had.
  useLayoutEffect(() => {
    const box = items.current;
    if (!box) return;
    const rows = [...box.querySelectorAll<HTMLElement>(":scope > [data-message]")];
    const measure = () => {
      const gap = parseFloat(getComputedStyle(box).rowGap) || 0;
      const sizes = rows.map((row) => ({ id: row.dataset.message, size: row.getBoundingClientRect().height }));
      setRowHeights((prior) => {
        let next: Map<string, number> | null = null;
        for (const { id, size } of sizes) {
          if (!id || !size || prior.get(id) === size + gap) continue;
          next ??= new Map(prior);
          next.set(id, size + gap);
        }
        return next ?? prior;
      });
    };
    measure();
    const observer = new ResizeObserver(measure);
    for (const row of rows) observer.observe(row);
    return () => observer.disconnect();
  }, [conversation, drawn.start, drawn.end, selected, openFolds, font, shown]);

  useLayoutEffect(() => {
    heldValue.current = held;
    settle();
    const box = list.current;
    if (box && shownValue.current && box.scrollTop - roomShift.current < OLDER_WITHIN_PX) readOlder();
  }, [held, rowHeights, drawn.start, drawn.end, isAtEnd, settle, readOlder]);
  useLayoutEffect(() => {
    shownValue.current = shown;
    if (shown && list.current) observedTop.current = list.current.scrollTop;
    settle();
  }, [shown, settle]);
  useLayoutEffect(() => {
    fontValue.current = font;
    settle();
  }, [font, settle]);
  // A picture that loads or a message unfolded above where he reads moves
  // nothing either, and a view that changes size draws the rows it reaches.
  useLayoutEffect(() => {
    const observer = new ResizeObserver(() => {
      settle();
      const box = list.current;
      if (box) setView((prior) => prior.height === box.clientHeight ? prior : { ...prior, height: box.clientHeight });
    });
    if (list.current) observer.observe(list.current);
    return () => observer.disconnect();
  }, [settle]);
  const zoom = useCallback((size: number) => {
    setFont(size);
    storeMessageFont(size);
  }, []);
  useEffect(() => {
    const box = list.current;
    if (!box) return;
    return attachZoomGesture(box, window, () => fontValue.current, zoom);
  }, [zoom]);

  const copySelection = useCallback(() => {
    const box = list.current;
    const selection = window.getSelection();
    if (!shownValue.current || !box || !selection || selection.isCollapsed || selection.rangeCount === 0) return;
    const text = selection.toString();
    const range = selection.getRangeAt(0);
    if (!text.trim() || !box.contains(range.commonAncestorContainer)) return;
    const end = [...range.getClientRects()].at(-1) ?? range.getBoundingClientRect();
    copyText(text).then(() => {
      clearTimeout(copiedTimer.current);
      setCopiedAt(end);
      copiedTimer.current = setTimeout(() => setCopiedAt(null), 1400);
    }, () => {});
  }, []);
  // Copied ends where the selection does, under its last line, or over it
  // where the frame has no room below, and inside the frame.
  useLayoutEffect(() => {
    const said = copied.current;
    const frame = said?.offsetParent;
    if (!said || !frame || !copiedAt) return;
    const room = frame.getBoundingClientRect();
    const under = copiedAt.bottom - room.top + COPIED_GAP;
    const top = under + said.offsetHeight <= room.height ? under : copiedAt.top - room.top - COPIED_GAP - said.offsetHeight;
    said.style.top = Math.min(Math.max(0, top), room.height - said.offsetHeight) + "px";
    said.style.left = Math.max(0, Math.min(copiedAt.right - room.left, room.width) - said.offsetWidth) + "px";
  }, [copiedAt]);
  useEffect(() => {
    let frame = 0;
    const settled = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(copySelection);
    };
    window.addEventListener("pointerup", settled);
    window.addEventListener("touchend", settled);
    window.addEventListener("keyup", settled);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("pointerup", settled);
      window.removeEventListener("touchend", settled);
      window.removeEventListener("keyup", settled);
    };
  }, [copySelection]);
  useEffect(() => () => clearTimeout(copiedTimer.current), []);
  // What keeps the conversation shown from being the session's own now goes
  // to the panel's header, never in a notice over the conversation (the
  // Overlord, 2026-11-12: "I hate all these alerts remove them!!", #2438): the
  // one Reconnecting of the pane, for its stream and for the terminal its box
  // types through, which is not drawn here (2026-11-04), and why the
  // conversation drawn is not the session's current one, which stays said
  // while it reconnects, so a drop moves nothing (2026-10-28). A view the
  // stream has run past says nothing, since Jump to latest shows wherever he
  // reads up and catches it up.
  const isDropped = shown && phase !== "closed" && (isReconnecting || !visible || !!link?.isReconnecting);
  const trouble = shown ? report?.stale || report?.problem || "" : "";
  useEffect(() => {
    onState({ isReconnecting: isDropped, trouble });
    return () => onState(QUIET_CHAT);
  }, [isDropped, trouble, onState]);
  const toEnd = useCallback((box: HTMLElement) => {
    isJumping.current = true;
    following.current = true;
    setAtEnd(true);
    setHeld(latest.current);
    box.scrollTo({ top: box.scrollHeight, behavior: "instant" });
    observedTop.current = box.scrollTop;
  }, []);
  const jump = () => {
    const box = list.current;
    if (!box) return;
    toEnd(box);
    box.focus({ preventScroll: true });
  };
  // A message he sends from the box shows from wherever he read, a few pixels
  // up included, where the list holds back all that arrives: the box would
  // empty with nothing drawn (the Overlord, 2026-11-05: "modern chat messages
  // disappear when he sends them"). A message sent from another screen moves
  // nothing, and the box keeps the keyboard.
  const [boxSends, setBoxSends] = useState(0);
  const sentFromBox = useCallback(() => setBoxSends((count) => count + 1), []);
  useLayoutEffect(() => {
    if (boxSends && list.current && !following.current) toEnd(list.current);
  }, [boxSends, toEnd]);
  // A screen of the harness's own that opens or changes in the conversation's
  // last row takes the list to its end, where it shows, so a key the empty box
  // hands the screen never answers a prompt he cannot see (#2223).
  useLayoutEffect(() => {
    const slot = items.current?.querySelector(":scope > .conversation-ask");
    if (!slot) return;
    const observer = new MutationObserver(() => { if (slot.childElementCount && list.current && !following.current) toEnd(list.current); });
    observer.observe(slot, { childList: true, subtree: true, characterData: true });
    return () => observer.disconnect();
  }, [onAskSlot, toEnd]);
  const keys = (event: KeyboardEvent<HTMLElement>) => {
    // Shift+Escape leaves the messages for the panel's first control, as it
    // leaves a terminal.
    if (event.shiftKey && event.key === "Escape") {
      event.preventDefault();
      event.currentTarget.closest(".context-pane")?.querySelector<HTMLButtonElement>(".panel-top button:not(:disabled)")?.focus();
      return;
    }
    const box = list.current;
    if (box && event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey && (event.key === "PageUp" || event.key === "PageDown")) {
      event.preventDefault();
      box.scrollBy({ top: (event.key === "PageUp" ? -1 : 1) * Math.max(box.clientHeight - 2 * font, font) });
      return;
    }
    const size = event.ctrlKey && !event.altKey && !event.metaKey ? messageFontFor(event.key, font) : null;
    if (size !== null) {
      event.preventDefault();
      zoom(size);
    }
  };
  const picture = useCallback((id: string) => address({ picture: id, conversation: held.conversation }), [address, held.conversation]);
  const taskID = task?.id;
  const keptPicture = useCallback((path: string) => keptFileAddress(instance, taskID, path), [instance, taskID]);
  // The picture he opened from a row, by the address the row draws it from,
  // shown in the one viewer of the conversation, which steps through every
  // picture of the messages the view holds, drawn in the window or not. The
  // set is read only while the viewer is open, so a streamed line draws
  // nothing for it.
  const [viewed, setViewed] = useState<string | null>(null);
  const isViewing = viewed !== null;
  const pictureSources = useMemo(() => isViewing ? conversationPictures(conversation, picture, keptPicture) : [], [isViewing, conversation, picture, keptPicture]);
  useEffect(() => { onChildValue.current = onChild; }, [onChild]);
  const taskValue = useRef(task);
  useLayoutEffect(() => { taskValue.current = task; }, [task]);
  const openChild = useCallback((child: TreeNode, source: HTMLElement) => {
    const current = taskValue.current?.tree?.children.find((each) => each.id === child.id);
    if (current) onChildValue.current(current, source);
  }, []);
  useLayoutEffect(() => {
    const box = list.current;
    if (!box || !focusedRow.current || document.activeElement !== document.body) return;
    focusedRow.current = "";
    box.focus({ preventScroll: true });
  }, [drawn.start, drawn.end]);
  useEffect(() => {
    const away = (event: PointerEvent) => { if (!list.current?.contains(event.target as Node)) focusedRow.current = ""; };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, []);
  const readings = usageReadings(harness, composing.usage, subscriptions, spend);
  const ends = turnEnds(conversation, held.isWorking);
  const reachesEnd = spans[spans.length - 1].end === conversation.length;
  const waiting = [...held.pending, ...held.sending.filter((send) => send.task === (held.task?.id ?? "") && !held.pending.some((action) => action.id === send.id)).map((send) =>
    parseAction({ id: send.id, kind: "message", task_id: send.task, text: send.text, status: "running", message: "Reaching the board.", send_now_why: "It is still reaching the board." }))];
  const queued = waiting.filter((action) => !read.has(action.id));
  // A prompt its harness holds that names the board message which typed it
  // is that message's one row, whichever of the two the board drew first,
  // its send still reaching the board included, since a send keeps the id of
  // its message; one that names none is a row of its own.
  const harnessActions = new Map<string, Action>();
  for (const prompt of held.waiting) {
    const row = prompt.action ? queued.findIndex((action) => action.id === prompt.action) : -1;
    if (row >= 0) harnessActions.set(prompt.id, queued.splice(row, 1)[0]);
  }
  const queuedIDs = [...held.waiting, ...queued].map((each) => each.id).join("\0");
  useLayoutEffect(() => {
    const observer = new ResizeObserver(settle);
    for (const row of items.current?.querySelectorAll<HTMLElement>(":scope > .reply-working, :scope > [data-queued], :scope > .conversation-ask") ?? []) observer.observe(row);
    settle();
    return () => observer.disconnect();
  }, [queuedIDs, held.isWorking, reachesEnd, phase, shown, settle]);
  return <MessageDocumentScope query={query} instance={instance}><section className={"messages-view" + (box ? " has-box" : "")} hidden={!shown} aria-label="Messages" onKeyDown={keys}>
    {/* Connecting is a plain line above the conversation, never a cover over
        the box he types into. */}
    {phase === "connecting" && <p className="terminal-connecting" role="status"><span className="terminal-spinner" aria-hidden="true" />Connecting to the messages</p>}
    <div className="conversation-frame">
      <div ref={list} className="conversation" role="log" aria-label={"Messages of " + name} tabIndex={0} style={{ fontSize: font }} onScroll={remember}
        onPointerDown={(event) => { if (event.pointerType !== "touch" && event.button === 0) isPressed.current = true; }}
        onTouchStart={() => { isTouching.current = true; }}
        onFocus={(event) => { focusedRow.current = event.target.closest<HTMLElement>("[data-message]")?.dataset.message ?? ""; }}
        onBlur={(event) => { if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget)) focusedRow.current = ""; }}>
        <div ref={floor} className="conversation-floor" />
        <div ref={items} className="conversation-items" style={{ paddingTop: `calc(${estimatedRoom}px + var(--room-shift, 0px))` }}>
          {/* One flat list keyed by message, so a row keeps its place in the
              page, its DOM and any selection in it as the window moves. */}
          <PictureOpener value={setViewed}>{spans.flatMap((span, at) => [
            ...at > 0 ? [<div key="room-between" className="conversation-room" style={{ height: heightOf(spans[at - 1].end, span.start) }} />] : [],
            ...conversation.slice(span.start, span.end).map((message) => {
              const ended = undelivered.find((action) => UNDELIVERED_ROW + action.id === message.id);
              return ended
                ? <QueuedMessage key={message.id} action={ended} liveAction={ended} instance={instance} name={name} variant="pane" row={message.id} onPutAway={putAwayRow} />
                : <MessageRow key={message.id} message={message} openFolds={openFolds.get(message.id) ?? NO_FOLDS} onFold={foldRow}
                  harness={harness} isTurnEnd={ends.has(message.id)} onRun={isWrittenReply(message, held.messages, held.isWorking) ? runCode : undefined}
                  task={message.kind === "reply" && message.blocks.some((block) => block.kind === "call" && block.agent) ? held.task : undefined} picture={picture} keptPicture={keptPicture} onChild={openChild} />;
            }),
          ])}</PictureOpener>
          <div className="conversation-room" style={{ height: heightOf(spans[spans.length - 1].end, conversation.length) }} />
          {/* While the view connects it shows nothing that waits on the
              session's reading, the dither included (the Overlord,
              2026-10-28: "connecting screens don't need to display the dither
              or usage unavailable"). */}
          {shown && held.isWorking && reachesEnd && <ReplyWorking said={held.said} ink={harnessInk(harness)} isCompacting={isCompacting} isHeld={!isAtWork || phase !== "live" || isReconnecting || !visible} retry={link?.screen?.retry} />}
          {/* His messages on their way, not yet in the session's record, at the
              conversation's end in its one scroll (the Overlord, 2026-10-29:
              "the double scrolls and having its own little scroll for queued
              messages doesn't really work"), so whatever the session prints
              comes before them and the list, following its end, keeps them
              right above the box. What the harness already holds was typed
              first, so it comes before what the board still holds. */}
          {held.waiting.map((prompt) => {
            const action = harnessActions.get(prompt.id);
            return action
              ? <QueuedMessage key={"queued-" + action.id} action={action} harnessState={prompt.sending ? "Sending" : "Queued"} liveAction={pending.find((each) => each.id === action.id) ?? null} instance={instance} name={name} variant="pane" />
              : <div key={"waiting-" + prompt.id} className={"queued-message pane " + prompt.source} data-queued={prompt.id}>
                <div className={"message-bubble queued-bubble" + (prompt.sending ? "" : " waiting")}>{linkedText(prompt.text)}</div>
                <p className="queued-state">{prompt.sending ? "Sending" : "Queued"}</p>
              </div>;
          })}
          {queued.map((action) => <QueuedMessage key={"queued-" + action.id} action={action} liveAction={pending.find((each) => each.id === action.id) ?? null} instance={instance} name={name} variant="pane" />)}
          {/* What the harness's own screen asks, a question, a permission prompt
              or a picker, is the conversation's last message, right above the
              box that answers it, and never a popup over the box (#2223). */}
          {onAskSlot && <div ref={onAskSlot} className="conversation-ask" />}
        </div>
      </div>
      {phase === "live" && !conversation.length && !queued.length && !held.waiting.length && <p className="messages-empty" role="status">No messages yet</p>}
      {/* A touch that stops the conversation gliding after a flick is one
          the browser gives no click, and the button shows the moment he
          leaves the end, while it glides, so a touch's lift takes him there
          as a click does (the Overlord, 2026-10-28, from his phone: "Jump to
          latest button mobile doesn't work"). */}
      {!isAtEnd && <button type="button" className="message-jump" onClick={jump} onPointerUp={(event) => { if (event.pointerType === "touch") jump(); }}><Icon name="arrow-down" />Jump to latest</button>}
      {copiedAt && <div ref={copied} className="selection-copied"><Notice>Copied</Notice></div>}
      {phase === "closed" && <Notice form="box" icon="comment" className="terminal-foot"
        actions={<button className="primary" disabled={!visible} onClick={() => { setPhase(held.messages.length ? "live" : "connecting"); setReconnecting(held.messages.length > 0); setAttempt((prior) => prior + 1); }}>Reconnect</button>}>{closedWhy}</Notice>}
    </div>
    {box && <div className="composer-dock">
      {onComposerDock && <div ref={onComposerDock} className="composer-slot" />}
      {/* The box draws the usage where its send arrow was (the Overlord,
          2026-10-28: "The usage circle goes in place of the arrow in the
          box"), only once the view is live and holds a real reading, never
          while it connects ("connecting screens don't need to display the
          dither or usage unavailable"). */}
      {box?.(model, composing.history, effort, phase === "live" && <UsageBar readings={readings} />, sentFromBox, isTurnRunning)}
    </div>}
    {viewed !== null && <PictureViewer sources={pictureSources} source={viewed} onSource={setViewed} onClose={() => setViewed(null)} />}
  </section></MessageDocumentScope>;
}

// ownPrompts is what he sent the session himself, oldest first, in the words
// he typed: Up in the box brings them back as the terminal's history does.
function ownPrompts(messages: MessagePage["messages"]): { at: string; text: string }[] {
  return messages.flatMap((message) => message.kind === "prompt" && message.text && (message.source === "overlord" || message.source === "slash" || message.source === "shell") ? [{ at: message.at, text: (message.source === "shell" ? "!" : "") + message.text }] : []);
}

// composerOf is what the box shows of page, prior itself while that is the
// same, so a streamed reply he reads past draws nothing.
function composerOf(prior: Composing, page: MessagePage): Composing {
  const history = ownPrompts(page.messages).map((row) => row.text);
  return prior.usage === page.usage && isSameList(prior.history, history) ? prior : { usage: page.usage, history };
}

// isSameList says two lists hold the same items in the same order.
function isSameList<T>(one: readonly T[], other: readonly T[]): boolean {
  return one.length === other.length && one.every((item, index) => item === other[index]);
}
