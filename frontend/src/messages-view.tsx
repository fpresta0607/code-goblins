import { useCallback, useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { message as errorText, reportToCfo, request } from "./api";
import { copyText } from "./clipboard";
import { Icon } from "./Icon";
import { keptFileAddress } from "./kept-files";
import { MessageRow } from "./message-row";
import { drawnSpans, ESTIMATED_ROW_PX, messageWindow } from "./message-window";
import { takesMessages } from "./messages";
import { Notice } from "./notice";
import { ReplyWorking } from "./reply-working";
import { changeMessages, messageFontFor, olderMessages, pageMessages, parseMessages, parsePage, parseUsage, storedMessageFont, storeMessageFont, turnEnds, type MessagePage } from "./session-messages";
import { attachZoomGesture } from "./terminal-zoom";
import { parseAction, string, type Action, type KeySpend, type SubscriptionUsage, type Task, type TreeNode } from "./types";
import { usageReadings } from "./session-usage";
import { UsageBar } from "./usage-bar";
import { harnessInk } from "./harness-ink";
import { QueuedMessage } from "./queued-message";
import { usePendingSends } from "./pending-sends";
import "./messages-view.css";

// A stream that keeps dropping stops retrying and says why.
const MAX_RETRIES = 5;
// He reads the newest messages while the list's end is this close.
const AT_END_PX = 24;
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
// open, absent for the CFO, terminal is the key of the session's terminal,
// whose link stops a turn, and box is the message box under the
// conversation.
export function MessagesView({ query, instance, name, task, terminal, harness, model, effort, isWorking, pending, subscriptions, spend, shown, visible, onChild, onComposerDock, box }: {
  query: string; instance: string; name: string; task?: Task; terminal: string; harness: string; model: string; effort: string; isWorking: boolean;
  pending: Action[];
  subscriptions?: SubscriptionUsage[]; spend?: KeySpend[];
  shown: boolean; visible: boolean; onChild: (child: TreeNode, source: HTMLElement) => void; box?: (model: string, history: string[], effort: string) => ReactNode;
  onComposerDock?: (node: HTMLDivElement | null) => void;
}) {
  // What he sent that the board has not shown yet, drawn at once as queued.
  const sending = usePendingSends();
  const list = useRef<HTMLDivElement>(null);
  const items = useRef<HTMLDivElement>(null);
  const [held, setHeld] = useState<MessagePage>({ messages: [], more: false });
  const heldValue = useRef(held);
  const [phase, setPhase] = useState<"connecting" | "live" | "closed">("connecting");
  const [isReconnecting, setReconnecting] = useState(false);
  const [closedWhy, setClosedWhy] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [font, setFont] = useState(storedMessageFont);
  // A selection he settles with a mouse or a key copies itself, with the same
  // quiet Copied the copy button gives.
  const [isCopied, setCopied] = useState(false);
  const copiedTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const fontValue = useRef(font);
  const shownValue = useRef(shown);
  const [isAtEnd, setAtEnd] = useState(true);
  // Whether he reads the end of the list, which then follows new messages;
  // while he reads further up, anchor is the message at the list's top and
  // how far its top lies below the list's, which stays put.
  const following = useRef(true);
  const anchor = useRef<{ id: string; offset: number } | null>(null);
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
  const foldRow = useCallback((message: string, fold: string, isOpen: boolean) => setOpenFolds((prior) => {
    const folds = new Set(prior.get(message));
    if (isOpen === folds.has(fold)) return prior;
    if (isOpen) folds.add(fold); else folds.delete(fold);
    const next = new Map(prior);
    if (folds.size) next.set(message, folds); else next.delete(message);
    return next;
  }), []);
  // The message whose row holds the keyboard, so the keyboard goes to the
  // list when that row leaves the window and the list's keys keep working.
  const focusedRow = useRef("");
  // Whether an older page is on its way, and how many failed to arrive in a
  // row and when the last did: his next approach to the top asks again once
  // OLDER_RETRY_MS have passed, until OLDER_TRIES have failed, which leaves
  // the rest unread until the stream's next page, the first a reconnect sends.
  const readingOlder = useRef(false);
  const olderFailures = useRef({ count: 0, at: 0 });
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
    readingOlder.current = false;
    const stream = new EventSource(address({ stream: "1" }));
    let failures = 0;
    const close = (why: string) => {
      stream.close();
      setReconnecting(false);
      setClosedWhy(why);
      setPhase("closed");
    };
    const take = (change: (prior: MessagePage) => MessagePage) => {
      failures = 0;
      setHeld(change);
      setReconnecting(false);
      setPhase("live");
    };
    const invalid = () => {
      close(UNREAD);
      reportToCfo(where, "Invalid messages stream");
    };
    stream.addEventListener("page", (event: MessageEvent<string>) => {
      try { const page = parsePage(JSON.parse(event.data)); olderFailures.current = { count: 0, at: 0 }; take((prior) => pageMessages(prior, page)); } catch { invalid(); }
    });
    stream.addEventListener("messages", (event: MessageEvent<string>) => {
      try { const changed = parseMessages(JSON.parse(event.data)); take((prior) => ({ ...prior, messages: changeMessages(prior.messages, changed) })); } catch { invalid(); }
    });
    stream.addEventListener("usage", (event: MessageEvent<string>) => {
      try { const usage = parseUsage(JSON.parse(event.data)); take((prior) => usage ? { ...prior, usage } : prior); } catch { invalid(); }
    });
    stream.addEventListener("unavailable", (event: MessageEvent<string>) => {
      let why = "";
      try { why = string(JSON.parse(event.data)); } catch { /* the reason is the stream's own */ }
      close(why || UNREAD);
    });
    // A stream the supervisor refused is closed for good; one that dropped,
    // as each does after ten minutes, reconnects by itself.
    stream.onerror = () => {
      if (stream.readyState === EventSource.CLOSED || ++failures >= MAX_RETRIES) close(UNREAD);
      else setReconnecting(true);
    };
    return () => stream.close();
  }, [address, visible, attempt, where]);

  // settle keeps the list where he reads it after anything moved its
  // contents: at its end while he follows it, or else with the message at its
  // top where it was.
  const settle = useCallback(() => {
    const box = list.current;
    if (!box || !shownValue.current) return;
    if (following.current) { box.scrollTop = box.scrollHeight; return; }
    const kept = anchor.current;
    const row = kept && [...box.querySelectorAll<HTMLElement>("[data-message]")].find((each) => each.dataset.message === kept.id);
    if (kept && row) box.scrollTop += row.getBoundingClientRect().top - box.getBoundingClientRect().top - kept.offset;
  }, []);

  const readOlder = useCallback(() => {
    const first = heldValue.current.messages[0];
    const failed = olderFailures.current;
    if (readingOlder.current || !heldValue.current.more || !first || failed.count >= OLDER_TRIES || Date.now() - failed.at < OLDER_RETRY_MS) return;
    readingOlder.current = true;
    request(address({ before: String(first.seq) })).then((value) => {
      const page = parsePage(value);
      readingOlder.current = false;
      olderFailures.current = { count: 0, at: 0 };
      // A page of a conversation the view no longer shows is dropped.
      setHeld((prior) => prior.messages[0]?.id === first.id ? olderMessages(prior, page) : prior);
    }).catch((error: unknown) => {
      readingOlder.current = false;
      olderFailures.current = { count: olderFailures.current.count + 1, at: Date.now() };
      reportToCfo(where, errorText(error));
    });
  }, [address, where]);

  // The rows drawn: those within reach of where he reads, or of the end while
  // he follows it, each row not measured yet taken at an estimate.
  const heights = held.messages.map((message) => rowHeights.get(message.id) ?? ESTIMATED_ROW_PX);
  const windowAt = (place: typeof view, isEnd: boolean) => {
    const anchorAt = held.messages.findIndex((message) => message.id === place.anchor);
    const readTop = anchorAt < 0 ? place.top : heights.slice(0, anchorAt).reduce((sum, each) => sum + each, 0) - place.offset;
    return messageWindow(heights, isEnd ? Infinity : readTop, place.height);
  };
  const drawn = windowAt(view, isAtEnd);
  const rowAt = (id: string) => id ? held.messages.findIndex((message) => message.id === id) : -1;
  const spans = drawnSpans(drawn, rowAt(selected?.from ?? ""), rowAt(selected?.to ?? ""), selected?.isCollapsed ?? true);
  const heightOf = (start: number, end: number) => heights.slice(start, end).reduce((sum, each) => sum + each, 0);

  // remember notes where he reads after he scrolls, draws the rows that come
  // within reach, and reads the older page once he nears the top.
  const remember = () => {
    const box = list.current;
    if (!box || !shownValue.current) return;
    const isEnd = box.scrollHeight - box.scrollTop - box.clientHeight <= AT_END_PX;
    following.current = isEnd;
    setAtEnd(isEnd);
    const top = box.getBoundingClientRect().top;
    const first = [...box.querySelectorAll<HTMLElement>("[data-message]")].find((row) => row.getBoundingClientRect().bottom > top);
    const firstTop = first?.getBoundingClientRect().top ?? Infinity;
    if (first && firstTop < top + box.clientHeight) anchor.current = { id: first.dataset.message || "", offset: firstTop - top };
    else {
      // A jump past the rows drawn finds none of them in view, so the message
      // at the top is read from the rows' heights, where the window places
      // each row.
      let at = (items.current?.getBoundingClientRect().top ?? top) - top, index = 0;
      while (index < heights.length - 1 && at + heights[index] <= 0) at += heights[index++];
      const message = held.messages[index];
      anchor.current = message ? { id: message.id, offset: at } : null;
    }
    const place = { top: box.scrollTop, height: box.clientHeight, anchor: anchor.current?.id ?? "", offset: anchor.current?.offset ?? 0 };
    const next = windowAt(place, isEnd);
    if (next.start !== drawn.start || next.end !== drawn.end || isEnd !== isAtEnd) setView(place);
    if (box.scrollTop < OLDER_WITHIN_PX) readOlder();
  };

  // Each drawn row is measured as it lays out and again whenever it changes,
  // as a picture loads or a call unfolds; a row the hidden view lays out at
  // no size keeps the height it had.
  useLayoutEffect(() => {
    const box = items.current;
    if (!box) return;
    const observer = new ResizeObserver((entries) => {
      const gap = parseFloat(getComputedStyle(box).rowGap) || 0;
      setRowHeights((prior) => {
        let next: Map<string, number> | null = null;
        for (const entry of entries) {
          const id = entry.target instanceof HTMLElement ? entry.target.dataset.message : undefined;
          const size = entry.borderBoxSize[0]?.blockSize ?? 0;
          if (!id || !size || prior.get(id) === Math.round(size + gap)) continue;
          next ??= new Map(prior);
          next.set(id, Math.round(size + gap));
        }
        return next ?? prior;
      });
    });
    for (const row of box.querySelectorAll<HTMLElement>(":scope > [data-message]")) observer.observe(row);
    return () => observer.disconnect();
  }, [held, drawn.start, drawn.end]);

  useLayoutEffect(() => {
    heldValue.current = held;
    settle();
    const box = list.current;
    if (box && shownValue.current && box.scrollTop < OLDER_WITHIN_PX) readOlder();
  }, [held, settle, readOlder]);
  useLayoutEffect(() => {
    shownValue.current = shown;
    settle();
  }, [shown, settle]);
  useLayoutEffect(() => {
    fontValue.current = font;
    settle();
  }, [font, settle]);
  // A picture that loads or a message unfolded above where he reads moves
  // nothing either, and a view that changes size draws the rows it reaches.
  useEffect(() => {
    const observer = new ResizeObserver(() => {
      settle();
      const box = list.current;
      if (box) setView((prior) => prior.height === box.clientHeight ? prior : { ...prior, height: box.clientHeight });
    });
    for (const box of [list.current, items.current]) if (box) observer.observe(box);
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
    if (!text.trim() || !box.contains(selection.getRangeAt(0).commonAncestorContainer)) return;
    copyText(text).then(() => {
      clearTimeout(copiedTimer.current);
      setCopied(true);
      copiedTimer.current = setTimeout(() => setCopied(false), 1400);
    }, () => {});
  }, []);
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
  const jump = () => {
    const box = list.current;
    if (!box) return;
    following.current = true;
    setAtEnd(true);
    box.scrollTop = box.scrollHeight;
    box.focus({ preventScroll: true });
  };
  const keys = (event: KeyboardEvent<HTMLElement>) => {
    // Shift+Escape leaves the messages for the panel's top row, as it leaves a
    // terminal.
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
  const picture = useCallback((id: string) => address({ picture: id }), [address]);
  const taskID = task?.id;
  const keptPicture = useCallback((path: string) => keptFileAddress(instance, taskID, path), [instance, taskID]);
  useEffect(() => { onChildValue.current = onChild; }, [onChild]);
  const openChild = useCallback((child: TreeNode, source: HTMLElement) => onChildValue.current(child, source), []);
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
  const readings = usageReadings(harness, held.usage, subscriptions, spend);
  // What he sent the session himself, oldest first, which Up in the box
  // brings back as the terminal's history does.
  const history = held.messages.flatMap((message) => message.kind === "prompt" && message.text && (message.source === "overlord" || message.source === "slash" || message.source === "shell") ? [(message.source === "shell" ? "!" : "") + message.text] : []);
  const ends = turnEnds(held.messages, isWorking);
  const reachesEnd = spans[spans.length - 1].end === held.messages.length;
  const queued = [...pending, ...sending.filter((send) => send.task === (task?.id ?? "") && !pending.some((action) => action.id === send.id)).map((send) =>
    parseAction({ id: send.id, kind: "message", task_id: send.task, text: send.text, status: "queued", message: "Reaching the board.", send_now_why: "It is still reaching the board." }))];
  return <section className={"messages-view" + (box ? " has-box" : "")} hidden={!shown} aria-label="Messages" onKeyDown={keys}>
    {/* Connecting is a plain line above the conversation, never a cover over
        the box he types into. */}
    {phase === "connecting" && <p className="terminal-connecting" role="status"><span className="terminal-spinner" aria-hidden="true" />Connecting to the messages</p>}
    <div className="conversation-frame">
      <div ref={list} className="conversation" role="log" aria-label={"Messages of " + name} tabIndex={0} style={{ fontSize: font }} onScroll={remember}
        onFocus={(event) => { focusedRow.current = event.target.closest<HTMLElement>("[data-message]")?.dataset.message ?? ""; }}
        onBlur={(event) => { if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget)) focusedRow.current = ""; }}>
        <div ref={items} className="conversation-items" style={{ paddingTop: heightOf(0, spans[0].start) }}>
          {/* One flat list keyed by message, so a row keeps its place in the
              page, its DOM and any selection in it as the window moves. */}
          {spans.flatMap((span, at) => [
            ...at > 0 ? [<div key="room-between" className="conversation-room" style={{ height: heightOf(spans[at - 1].end, span.start) }} />] : [],
            ...held.messages.slice(span.start, span.end).map((message) => <MessageRow key={message.id} message={message} openFolds={openFolds.get(message.id) ?? NO_FOLDS} onFold={foldRow}
              harness={harness} isTurnEnd={ends.has(message.id)}
              task={message.kind === "reply" && message.blocks.some((block) => block.kind === "call" && block.agent) ? task : undefined} picture={picture} keptPicture={keptPicture} onChild={openChild} />),
          ])}
          <div className="conversation-room" style={{ height: heightOf(spans[spans.length - 1].end, held.messages.length) }} />
          {/* While the view connects it shows nothing that waits on the
              session's reading, the dither included (the Overlord,
              2026-10-28: "connecting screens don't need to display the dither
              or usage unavailable"). */}
          {phase === "live" && isWorking && reachesEnd && <ReplyWorking terminal={terminal} canStop={!(task && takesMessages(task))} ink={harnessInk(harness)} />}
        </div>
      </div>
      {phase === "live" && !held.messages.length && <p className="messages-empty" role="status">No messages yet</p>}
      {/* A touch that stops the conversation gliding after a flick is one
          the browser gives no click, and the button shows the moment he
          leaves the end, while it glides, so a touch's lift takes him there
          as a click does (the Overlord, 2026-10-28, from his phone: "Jump to
          latest button mobile doesn't work"). */}
      {!isAtEnd && <button type="button" className="message-jump" onClick={jump} onPointerUp={(event) => { if (event.pointerType === "touch") jump(); }}><Icon name="arrow-down" />Jump to latest</button>}
      {isCopied && <Notice className="terminal-corner">Copied</Notice>}
      {phase !== "closed" && (isReconnecting || !visible) && <Notice tone="warning" className="terminal-corner">Reconnecting</Notice>}
      {phase === "closed" && <Notice form="box" icon="comment" className="terminal-foot"
        actions={<button className="primary" disabled={!visible} onClick={() => { setPhase(held.messages.length ? "live" : "connecting"); setReconnecting(held.messages.length > 0); setAttempt((prior) => prior + 1); }}>Reconnect</button>}>{closedWhy}</Notice>}
    </div>
    {/* His queued messages stay pinned between the conversation and the box,
        out of the list that scrolls, so whatever the session prints after
        them and however far up he reads they stay in view with their Send
        now, Edit and Cancel (the Overlord, 2026-10-28: "it should always stay
        at the bottom between the agent's chat response and the chat box, so I
        could always see it and control it"). */}
    {queued.length > 0 && <div className="conversation-queue" role="group" aria-label="Queued messages" style={{ fontSize: font }}>
      {queued.map((action) => <QueuedMessage key={"queued-" + action.id} action={action} instance={instance} name={name} variant="pane" />)}
    </div>}
    {box && <div className="composer-dock">
      {/* The usage shows only once the view is live and holds a real
          reading, never while it connects (the Overlord, 2026-10-28:
          "connecting screens don't need to display the dither or usage
          unavailable"). */}
      {phase === "live" && <UsageBar readings={readings} />}
      {onComposerDock && <div ref={onComposerDock} className="composer-slot" />}
      {box?.(held.usage?.model || model, history, held.usage?.effort || effort)}
    </div>}
  </section>;
}
