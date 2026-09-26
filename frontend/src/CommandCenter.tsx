import { useEffect, useRef, useState, type MouseEvent } from "react";
import { message, request } from "./api";
import { parseAction, type BoardActivity, type Question, type Review, type Run, type Snapshot } from "./types";
import { deliveryMark, submissionFor } from "./feedback";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { isOpen, nextOpenKey, questionPage, settledIcon, settledItems, settledLabel, waitingItems, type Item } from "./commandQueue";
import { RunCard } from "./RunCard";
import { questionAnswer, questionChoices } from "./questionChoices";
import { plainMessage } from "./messageText";
import { personaFor } from "./workflow";
import { EMPTY_DRAFT, QuestionCard, type Draft } from "./QuestionCard";
import { ReviewCard, reviewImages } from "./ReviewCard";
import { ImageGallery } from "./ImageGallery";
import { Disclosure } from "./Disclosure";
import { bannerItems, countedTitle } from "./arrivals";
import { DoneCard } from "./DoneCard";
import { DocumentCard } from "./DocumentCard";

// A banner for a new item stays this long; the item stays under the badge.
const BANNER_MS = 8000;
// A delivered answer's check shows this long before the next item.
const DONE_MS = 1100;
// "You're all done" shows this long before the Command Center closes.
const ALL_DONE_MS = 1600;

export interface CommandFocus { key: string; at: number }

const outsideDialog = (event: MouseEvent<HTMLDialogElement>) => {
  const box = event.currentTarget.getBoundingClientRect();
  return event.target === event.currentTarget && (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom);
};

// The Supreme Overlord Command Center: an inbox of everything waiting on him,
// and a stack that shows one item at a time, a question or a review item. Each
// answer goes to its asker on its own, once; Later keeps an item in the stack;
// drafts survive closing, reconnecting and moving between cards. A new question
// opens the stack; any other new item is announced in a banner and waits in
// the inbox under the badge, and the tab's title counts what waits. Once an
// answer is delivered its check shows, the next open item follows, and the
// last one ends on "You're all done" before the Command Center closes.
export function CommandCenter({ snapshot, connected, presentations, focus }: { snapshot: Snapshot; connected: boolean; presentations: BoardActivity[]; focus: CommandFocus | null }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const menu = useRef<HTMLDetailsElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const swipe = useRef<{ x: number; y: number } | null>(null);
  const pressedOutside = useRef(false);
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [open, setOpen] = useState(false);
  const [current, setCurrent] = useState("");
  const [kept, setKept] = useState<Set<string>>(new Set());
  const [announced, setAnnounced] = useState<Set<string>>(new Set());
  const [lastFocus, setLastFocus] = useState<CommandFocus | null>(focus);
  const [gallery, setGallery] = useState<number | null>(null);
  const [inbox, setInbox] = useState(false);
  const [background, setBackground] = useState<Set<string>>(new Set());
  const [sent, setSent] = useState<Set<string>>(new Set());
  const [leaving, setLeaving] = useState("");
  const [allDone, setAllDone] = useState(false);
  const stack = waitingItems(snapshot, kept);
  const waiting = waitingItems(snapshot);
  const index = Math.max(0, stack.findIndex((item) => item.key === current));
  const item: Item | undefined = open ? stack[index] : undefined;
  const draft = item ? drafts[item.key] : undefined;
  const outcome = draft?.submission ? snapshot.actions.find((action) => action.id === draft.submission?.id) || draft.receipt : undefined;
  const mark = outcome ? deliveryMark(outcome, item?.kind === "question" ? item.question.task : undefined) : undefined;
  // What the Overlord sent from this card is on its way or delivered; trouble
  // keeps the card itself on screen with what went wrong. A run keeps its card,
  // which shows the command's result.
  const finishing = !!item && item.kind !== "run" && !!mark && !mark.trouble;
  const delivered = finishing && outcome?.status === "succeeded" ? item.key : "";
  // Moving off a finishing card counts it as sent, so only a card on screen
  // from its Send to its delivery moves on by itself.
  const show = (key: string) => {
    if (finishing && item.key !== key) setSent((prior) => new Set([...prior, item.key]));
    setCurrent(key); setGallery(null); setAllDone(false);
  };
  const fresh = waiting.filter((item) => item.kind === "question" && !announced.has(item.key));
  if (fresh.length) {
    setAnnounced(new Set([...announced, ...fresh.map((item) => item.key)]));
    if (!open) { setOpen(true); show(fresh[0].key); }
  }
  const [bannered, setBannered] = useState<Set<string>>(new Set());
  const [banner, setBanner] = useState<string[]>([]);
  const arriving = bannerItems(waiting, bannered);
  if (arriving.length) {
    setBannered(new Set([...bannered, ...arriving.map((item) => item.key)]));
    setBanner(arriving.map((item) => item.key));
  }
  useEffect(() => {
    if (!banner.length) return;
    const timer = setTimeout(() => setBanner([]), BANNER_MS);
    return () => clearTimeout(timer);
  }, [banner]);
  const baseTitle = useRef(document.title);
  useEffect(() => { document.title = countedTitle(baseTitle.current, waiting.length); }, [waiting.length]);
  useEffect(() => () => { document.title = baseTitle.current; }, []);
  const announcedNow = banner.map((key) => waiting.find((candidate) => candidate.key === key)).filter((candidate): candidate is Item => !!candidate);
  if (focus !== lastFocus) {
    setLastFocus(focus);
    if (focus) { setOpen(true); show(focus.key); setInbox(false); }
  }
  // A delivered item's check has shown long enough: on to the next open item,
  // or "You're all done" when nothing else waits.
  if (leaving) {
    const done = new Set([...sent, leaving]);
    const next = nextOpenKey(stack, leaving, done);
    setSent(done);
    setLeaving("");
    if (next) show(next); else setAllDone(true);
  }
  // Anything that opens while "You're all done" shows takes its place.
  if (allDone) {
    const resume = nextOpenKey(stack, current, sent);
    if (resume) show(resume);
  }
  // The card on screen stays in the stack while it is shown, so an item
  // answered or cleared elsewhere turns into its settled card instead of vanishing.
  if (item && !kept.has(item.key)) setKept(new Set([...kept, item.key]));
  const showing = !!item;
  useEffect(() => {
    if (!delivered || sent.has(delivered)) return;
    const timer = setTimeout(() => setLeaving(delivered), DONE_MS);
    return () => clearTimeout(timer);
  }, [delivered, sent]);
  useEffect(() => {
    if (!allDone) return;
    const timer = setTimeout(() => { setOpen(false); setKept(new Set()); setGallery(null); setAllDone(false); }, ALL_DONE_MS);
    return () => clearTimeout(timer);
  }, [allDone]);
  // Clicking anywhere outside the inbox closes it.
  useEffect(() => {
    if (!inbox) return;
    const outside = (event: PointerEvent) => { if (!(event.target instanceof Node && menu.current?.contains(event.target))) setInbox(false); };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [inbox]);
  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    if (showing && !element.open) {
      returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      element.showModal();
      // Start on the question itself, not the close button.
      element.querySelector<HTMLElement>(".question-card .question-body, .question-card h3")?.focus();
    }
    if (!showing && element.open) { element.close(); returnFocus.current?.focus(); }
  }, [showing]);
  const close = () => { setOpen(false); setKept(new Set()); setGallery(null); setAllDone(false); };
  const move = (step: number) => { const next = stack[index + step]; if (next) show(next.key); };
  const later = () => {
    const next = item && nextOpenKey(stack, item.key);
    if (next) show(next); else close();
  };
  const update = (key: string, changes: Partial<Draft>) => setDrafts((prior) => ({ ...prior, [key]: { ...(prior[key] || EMPTY_DRAFT), ...changes } }));
  // An unchanged payload keeps its request ID, so a retry after an ambiguous
  // failure can never deliver twice.
  const post = async (key: string, payload: Record<string, unknown>) => {
    const draft = drafts[key] || EMPTY_DRAFT;
    if (draft.sending || draft.submission && snapshot.actions.some((action) => action.id === draft.submission?.id)) return;
    const submission = submissionFor(JSON.stringify(payload), draft.submission, () => crypto.randomUUID());
    update(key, { submission, sending: true, error: "" });
    setKept((prior) => new Set([...prior, key]));
    try {
      const receipt = parseAction(await request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ id: submission.id, ...payload }) }));
      update(key, { receipt, sending: false });
    } catch (error: unknown) { update(key, { error: message(error), sending: false }); }
  };
  const send = (target: Item) => {
    const draft = drafts[target.key] || EMPTY_DRAFT;
    if (!isOpen(target)) return;
    if (target.kind === "question") {
      const payload = questionAnswer(target.question, draft.selection, draft.written);
      if (payload) void post(target.key, payload);
    } else if (target.kind === "review" && draft.written.trim()) void post(target.key, { kind: "review_answer", review_id: target.review.id, generation: target.review.identity, text: draft.written });
  };
  // Run names the stored item; the browser never sends command text.
  const run = (target: Run) => { if (target.state === "ready") void post("run:" + target.id, { kind: "run", run_id: target.id, generation: target.identity }); };
  // A document leaves the queue once he opens or downloads it, and says so.
  const clear = (target: Review, how?: "Opened" | "Downloaded") => void post("review:" + target.id, { kind: "review_clear", review_id: target.id, generation: target.identity, ...(how ? { text: how } : {}) });
  const taskOf = (candidate: Item) => candidate.kind === "question" ? candidate.question.task : candidate.kind === "review" ? candidate.review.task : "";
  const askerOf = (candidate: Item) => taskOf(candidate) ? snapshot.tasks.find((task) => task.id === taskOf(candidate))?.title || taskOf(candidate) : "The CFO";
  const textOf = (candidate: Item) => candidate.kind === "question" ? plainMessage(candidate.question.text) : candidate.kind === "review" ? candidate.review.title : candidate.run.title;
  const created = (candidate: Item) => candidate.kind === "question" ? candidate.question.created_at : candidate.kind === "review" ? candidate.review.created_at : candidate.run.created_at;
  const iconOf = (candidate: Item) => candidate.kind === "question" ? candidate.question.image_count ? "images" : "question" : candidate.kind === "review" ? candidate.review.document ? "file" : candidate.review.image_count ? "images" : "comment" : "play";
  const pageFor = (candidate: Question) => questionPage(presentations, candidate);
  const images = !item || item.kind === "run" ? [] : item.kind === "question"
    ? questionChoices(item.question).filter((choice) => choice.image).map((choice) => ({ src: choice.image, label: choice.label, value: choice.value, text: choice.text }))
    : reviewImages(item.review).map((src, n) => ({ src, label: String(n + 1), value: "Image " + (n + 1), text: "Image " + (n + 1) }));
  const notices = presentations.filter((event) => !background.has(event.id)).slice(-4).reverse();
  const settled = settledItems(snapshot).slice(0, 20);
  return <>
    <details ref={menu} className="command-center-menu" open={inbox} onToggle={(event) => setInbox(event.currentTarget.open)}>
      <summary className="icon-button" data-tip="Command Center" data-tip-align="end" aria-label={"Command Center" + (waiting.length ? ", " + waiting.length + " waiting on you" : "")}><Icon name="command-center" />{waiting.length > 0 && <span className="count-badge" aria-hidden="true">{waiting.length}</span>}</summary>
      <div className="command-center-updates">
        <h2><Avatar persona="cfo" small />Command Center</h2>
        <section aria-label="Waiting on you">
          <h3>Waiting on you <span className="column-count">{waiting.length}</span></h3>
          {waiting.length ? <ul className="inbox-list">{waiting.map((candidate) => <li key={candidate.key}>
            <Avatar persona={taskOf(candidate) ? personaFor(snapshot.tasks.find((task) => task.id === taskOf(candidate))) : "cfo"} small />
            <span className="inbox-text"><strong>{askerOf(candidate)}</strong><span className="inbox-summary">{textOf(candidate)}</span></span>
            <time>{age(created(candidate))}</time>
            <button className="icon-button raised" aria-label={"Answer " + askerOf(candidate) + ": " + textOf(candidate)} data-tip="Answer" data-tip-align="end" onClick={() => { setInbox(false); setOpen(true); show(candidate.key); }}><Icon name={iconOf(candidate)} /></button>
          </li>)}</ul> : <p className="muted">Nothing is waiting on you.</p>}
        </section>
        {notices.length > 0 && <section aria-label="Pages to look at">
          <h3>Pages</h3>
          <ul className="inbox-list">{notices.map((event) => <li key={event.id}>
            <span className="mark"><Icon name={event.kind === "review" ? "comment" : "browser-check"} /></span>
            <span className="inbox-text"><strong>{event.kind === "review" ? "Review ready" : "Browser walkthrough running"}</strong>{event.cfo_identity ? "CFO" : snapshot.tasks.find((task) => task.id === event.task_id)?.title || event.task_id}</span>
            <a className="icon-button raised" href={event.url} target="_blank" rel="noreferrer" aria-label={event.kind === "review" ? "Open review" : "Open page"} data-tip={event.kind === "review" ? "Open review" : "Open page"} data-tip-align="end"><Icon name="external" /></a>
            <button className="icon-button" aria-label="Keep in background" data-tip="Keep in background" data-tip-align="end" onClick={() => setBackground((prior) => new Set([...prior, event.id]))}><Icon name="minus" /></button>
          </li>)}</ul>
        </section>}
        {settled.length > 0 && <Disclosure kind="inbox-history" title={<>History <span className="column-count">{settled.length}</span></>}>
          <ul className="inbox-list">{settled.map((candidate) => {
            const mark = settledIcon(candidate, snapshot.actions);
            return <li key={candidate.key}>
              <span className={"delivery " + mark.tone}><Icon name={mark.icon} /></span>
              <span className="inbox-text"><strong>{askerOf(candidate)}</strong><span className="inbox-summary">{textOf(candidate)}</span><small>{settledLabel(candidate, snapshot.actions)}</small></span>
            </li>;
          })}</ul>
        </Disclosure>}
        <p className="muted">Everything stays here until you answer or clear it, or the goblin that asked moves past it. Opening a page never pauses work.</p>
      </div>
    </details>
    {/* A click on the dimmed board around the card lands on the dialog
        itself, outside its box, and closes the Command Center; a press that
        starts inside the card and ends there keeps it open. */}
    <dialog ref={dialog} className="question-modal" aria-labelledby="command-center-heading"
      onCancel={(event) => { event.preventDefault(); if (gallery !== null) setGallery(null); else close(); }} onKeyDown={(event) => event.stopPropagation()}
      onPointerDown={(event) => { pressedOutside.current = outsideDialog(event); }}
      onClick={(event) => { if (pressedOutside.current && outsideDialog(event)) close(); }}>
      {item && <>
        <header className="command-center-heading">
          <Avatar persona="cfo" />
          <h2 id="command-center-heading">Supreme Overlord<span>Command Center</span></h2>
          {stack.length > 1 && !allDone && <span className="count-pill">{index + 1} of {stack.length}</span>}
          <button type="button" className="icon-button question-close" aria-label="Close the Command Center" data-tip="Close" data-tip-align="end" onClick={close}><Icon name="close" /></button>
        </header>
        {gallery !== null && images.length > 0
          ? <ImageGallery images={images} index={Math.min(gallery, images.length - 1)} lavish={item.kind === "question" ? pageFor(item.question)?.url : item.kind === "review" ? item.review.lavish : undefined} onIndex={setGallery} onClose={() => setGallery(null)}
            onChoose={item.kind === "question" && item.question.status === "pending" ? (value) => { update(item.key, { selection: "option:" + value, error: "", receipt: undefined }); setGallery(null); } : undefined} />
          : <div className={"card-stage" + (stack.length > 1 && !allDone ? " stacked" : "")}
            onPointerDown={(event) => { if (event.pointerType !== "mouse") swipe.current = { x: event.clientX, y: event.clientY }; }}
            onPointerUp={(event) => {
              const start = swipe.current;
              swipe.current = null;
              if (!start) return;
              const dx = event.clientX - start.x;
              if (Math.abs(dx) > 70 && Math.abs(event.clientY - start.y) < 50) move(dx < 0 ? 1 : -1);
            }}>
            {allDone
              ? <div className="done-card" role="status"><Avatar persona="cfo" /><h3>You're all done</h3><p>Nothing else is waiting on you.</p></div>
              : finishing && outcome
              ? <DoneCard key={item.key} delivered={!!delivered}
                heading={outcome.kind === "review_clear" ? delivered ? outcome.text || "Cleared" : "Clearing" : delivered ? "Sent" : "Sending"}
                label={!delivered ? "" : outcome.kind !== "review_clear" ? mark?.label || "" : outcome.text ? "It moves to your history." : ""} />
              : item.kind === "question"
              ? <QuestionCard key={item.key} question={item.question} snapshot={snapshot} connected={connected} draft={drafts[item.key] || EMPTY_DRAFT} review={pageFor(item.question)}
                onDraft={(changes) => update(item.key, changes)} onSend={() => send(item)} onImage={setGallery} />
              : item.kind === "run"
              ? <RunCard key={item.key} run={item.run} connected={connected} sending={!!drafts[item.key]?.sending} error={drafts[item.key]?.error || ""} onRun={() => run(item.run)} />
              : item.review.document
              ? <DocumentCard key={item.key} review={item.review} document={item.review.document} snapshot={snapshot} connected={connected} onOpened={(how) => clear(item.review, how)} onClear={() => clear(item.review)} />
              : <ReviewCard key={item.key} review={item.review} snapshot={snapshot} connected={connected} draft={drafts[item.key] || EMPTY_DRAFT}
                onDraft={(changes) => update(item.key, changes)} onSend={() => send(item)} onClear={() => clear(item.review)} onImage={setGallery} />}
          </div>}
        {gallery === null && !allDone && <footer className="question-footer">
          {stack.length > 1 && <div className="stack-nav">
            <button type="button" className="icon-button raised" disabled={index === 0} aria-label="Previous item" data-tip="Previous" onClick={() => move(-1)}><Icon name="back" /></button>
            {stack.length <= 8 && <span className="stack-dots" aria-hidden="true">{stack.map((candidate) => <span key={candidate.key} className={candidate.key === item.key ? "on" : ""} />)}</span>}
            <button type="button" className="icon-button raised" disabled={index === stack.length - 1} aria-label="Next item" data-tip="Next" data-tip-align="end" onClick={() => move(1)}><Icon name="next" /></button>
          </div>}
          <button type="button" className="text-button later" onClick={later}>{isOpen(item) ? "Later" : "Next"}</button>
        </footer>}
      </>}
    </dialog>
    {announcedNow.length > 0 && !open && <aside className="arrival-banner" role="status" aria-label="New in the Command Center">
      <Avatar persona={taskOf(announcedNow[0]) ? personaFor(snapshot.tasks.find((task) => task.id === taskOf(announcedNow[0]))) : "cfo"} small />
      <span className="inbox-text"><strong>{askerOf(announcedNow[0])}</strong><span className="inbox-summary">{textOf(announcedNow[0])}{announcedNow.length > 1 ? " and " + (announcedNow.length - 1) + " more" : ""}</span></span>
      <button className="primary" onClick={() => { const key = announcedNow[0].key; setBanner([]); setInbox(false); setOpen(true); show(key); }}>Open</button>
      <button className="icon-button" aria-label="Dismiss" data-tip="Dismiss" data-tip-align="end" onClick={() => setBanner([])}><Icon name="close" /></button>
    </aside>}
  </>;
}
