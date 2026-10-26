import { useCallback, useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { message as errorText, reportToCfo, request } from "./api";
import { copyText } from "./clipboard";
import { Icon } from "./Icon";
import { MessageRow } from "./message-row";
import { changeMessages, messageFontFor, olderMessages, pageMessages, parseMessages, parsePage, parseUsage, storedMessageFont, storeMessageFont, type MessagePage } from "./session-messages";
import { attachZoomGesture } from "./terminal-zoom";
import { string, type Action, type KeySpend, type SubscriptionUsage, type Task, type TreeNode } from "./types";
import { SessionFooter } from "./session-footer";
import { UsageBanner } from "./usage-banner";
import { sessionStats, usageBanner } from "./session-usage";
import { harnessInk } from "./harness-ink";
import { QueuedMessage } from "./queued-message";
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
const UNREAD = "The session's messages cannot be read";

// A Claude Code session's messages, read only: the supervisor's stream of
// them (/api/messages), drawn as the session's conversation. It sits in the
// box the session's terminal fills, so switching between the two never
// resizes the terminal, and stays mounted while its panel shows the session.
// The list scrolls as a page does, with the wheel, the page keys and
// Shift with them as in a terminal; reading up, nothing moves it, new
// messages and older ones read in above alike, and Jump to latest returns to
// the end, which then follows what arrives. Ctrl+Plus, Ctrl+Minus and Ctrl+0,
// or the right button with the wheel, size its text, kept in this browser.
// query names the session as /api/messages takes it (messagesQuery), name is
// whose replies they are, task is the goblin whose baby goblins its calls
// open, absent for the CFO, and box is the message box under the
// conversation.
export function MessagesView({ query, instance, name, task, harness, model, effort, memory, isWorking, onStop, workingSince, pending, subscriptions, spend, now, shown, visible, onChild, box }: {
  query: string; instance: string; name: string; task?: Task; harness: string; model: string; effort: string; memory: number; isWorking: boolean; onStop?: () => void;
  workingSince: string; pending: Action[];
  subscriptions?: SubscriptionUsage[]; spend?: KeySpend[]; now: number;
  shown: boolean; visible: boolean; onChild: (child: TreeNode, source: HTMLElement) => void; box?: ReactNode;
}) {
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

  // remember notes where he reads after he scrolls, and reads the older page
  // once he nears the top.
  const remember = () => {
    const box = list.current;
    if (!box || !shownValue.current) return;
    const isEnd = box.scrollHeight - box.scrollTop - box.clientHeight <= AT_END_PX;
    following.current = isEnd;
    setAtEnd(isEnd);
    const top = box.getBoundingClientRect().top;
    const first = [...box.querySelectorAll<HTMLElement>("[data-message]")].find((row) => row.getBoundingClientRect().bottom > top);
    anchor.current = first ? { id: first.dataset.message || "", offset: first.getBoundingClientRect().top - top } : null;
    if (box.scrollTop < OLDER_WITHIN_PX) readOlder();
  };

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
  // nothing either.
  useEffect(() => {
    const observer = new ResizeObserver(settle);
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
  useEffect(() => { onChildValue.current = onChild; }, [onChild]);
  const openChild = useCallback((child: TreeNode, source: HTMLElement) => onChildValue.current(child, source), []);
  const stats = sessionStats({ usage: held.usage, model, effort, memory });
  const reading = usageBanner(harness, subscriptions, spend);
  const lastSentAt = [...held.messages].reverse().find((message) => message.kind === "prompt" && message.source !== "notice")?.at ?? "";
  const lastReplyAt = [...held.messages].reverse().find((message) => message.kind === "reply")?.at ?? "";
  return <section className={"messages-view" + (box ? " has-box" : "")} hidden={!shown} aria-label="Messages" onKeyDown={keys}>
    <div className="conversation-frame">
      <div ref={list} className="conversation" role="log" aria-label={"Messages of " + name} tabIndex={0} style={{ fontSize: font }} onScroll={remember}>
        <div ref={items} className="conversation-items">
          {held.messages.map((message) => <MessageRow key={message.id} message={message}
            task={message.kind === "reply" && message.blocks.some((block) => block.kind === "call" && block.agent) ? task : undefined} picture={picture} onChild={openChild} />)}
          {pending.map((action) => <QueuedMessage key={"queued-" + action.id} action={action} instance={instance} name={name} variant="pane" />)}
        </div>
      </div>
      {phase === "connecting" && <div className="terminal-cover" role="status"><span className="terminal-spinner" aria-hidden="true" /><p>Connecting to the messages</p></div>}
      {phase === "live" && !held.messages.length && <p className="messages-empty" role="status">No messages yet</p>}
      {!isAtEnd && <button type="button" className="message-jump" onClick={jump}><Icon name="arrow-down" />Jump to latest</button>}
      {isCopied && <span className="conversation-copied" role="status">Copied</span>}
      {phase !== "closed" && (isReconnecting || !visible) && <span className="terminal-state terminal-reconnecting" role="status"><span className="status-dot" />Reconnecting</span>}
      {phase === "closed" && <div className="terminal-closed" role="status"><Icon name="comment" /><p>{closedWhy}</p>
        <button className="primary" disabled={!visible} onClick={() => { setPhase(held.messages.length ? "live" : "connecting"); setReconnecting(held.messages.length > 0); setAttempt((prior) => prior + 1); }}>Reconnect</button></div>}
    </div>
    {(box || reading.banner) && <div className="composer-dock">
      <SessionFooter ink={harnessInk(harness)} isWorking={isWorking} workingSince={workingSince} lastSentAt={lastSentAt} lastReplyAt={lastReplyAt} now={now} hasUsage={stats.hasUsage} why={reading.banner ? "" : reading.why} onStop={onStop} />
      <UsageBanner reading={reading} stats={stats} />
      {box}
    </div>}
  </section>;
}
