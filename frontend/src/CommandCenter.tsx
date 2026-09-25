import { useEffect, useRef, useState } from "react";
import { message, request } from "./api";
import { parseAction, type BoardActivity, type Question, type Review, type Snapshot } from "./types";
import { submissionFor } from "./feedback";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { settledIcon, settledItems, settledLabel, waitingItems, type Item } from "./commandQueue";
import { questionAnswer, questionChoices } from "./questionChoices";
import { personaFor } from "./workflow";
import { EMPTY_DRAFT, QuestionCard, type Draft } from "./QuestionCard";
import { ReviewCard, reviewImages } from "./ReviewCard";
import { ImageGallery } from "./ImageGallery";
import { Disclosure } from "./Disclosure";

export interface CommandFocus { key: string; at: number }

// The Supreme Overlord Command Center: an inbox of everything waiting on him,
// and a stack that shows one item at a time, a question or a review item. Each
// answer goes to its asker on its own, once; Later keeps an item in the stack;
// drafts survive closing, reconnecting and moving between cards. A new question
// opens the stack; a new review item waits in the inbox under the badge.
export function CommandCenter({ snapshot, connected, presentations, focus }: { snapshot: Snapshot; connected: boolean; presentations: BoardActivity[]; focus: CommandFocus | null }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const swipe = useRef<{ x: number; y: number } | null>(null);
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [open, setOpen] = useState(false);
  const [current, setCurrent] = useState("");
  const [kept, setKept] = useState<Set<string>>(new Set());
  const [announced, setAnnounced] = useState<Set<string>>(new Set());
  const [lastFocus, setLastFocus] = useState<CommandFocus | null>(focus);
  const [gallery, setGallery] = useState<number | null>(null);
  const [inbox, setInbox] = useState(false);
  const [background, setBackground] = useState<Set<string>>(new Set());
  const stack = waitingItems(snapshot, kept);
  const waiting = waitingItems(snapshot);
  const fresh = waiting.filter((item) => item.kind === "question" && !announced.has(item.key));
  if (fresh.length) {
    setAnnounced(new Set([...announced, ...fresh.map((item) => item.key)]));
    if (!open) { setOpen(true); setCurrent(fresh[0].key); setGallery(null); }
  }
  if (focus !== lastFocus) {
    setLastFocus(focus);
    if (focus) { setOpen(true); setCurrent(focus.key); setGallery(null); setInbox(false); }
  }
  const index = Math.max(0, stack.findIndex((item) => item.key === current));
  const item: Item | undefined = open ? stack[index] : undefined;
  // The card on screen stays in the stack while it is shown, so an item
  // answered or cleared elsewhere turns into its settled card instead of vanishing.
  if (item && !kept.has(item.key)) setKept(new Set([...kept, item.key]));
  const showing = !!item;
  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    if (showing && !element.open) {
      returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      element.showModal();
      // Start on the question itself, not the close button.
      element.querySelector<HTMLElement>(".question-card h3")?.focus();
    }
    if (!showing && element.open) { element.close(); returnFocus.current?.focus(); }
  }, [showing]);
  const isOpen = (candidate: Item) => candidate.kind === "question" ? candidate.question.status === "pending" : candidate.review.state === "open";
  const close = () => { setOpen(false); setKept(new Set()); setGallery(null); };
  const show = (key: string) => { setCurrent(key); setGallery(null); };
  const move = (step: number) => { const next = stack[index + step]; if (next) show(next.key); };
  const later = () => {
    const next = stack.slice(index + 1).find(isOpen) || stack.slice(0, index).find(isOpen);
    if (next) show(next.key); else close();
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
    } else if (draft.written.trim()) void post(target.key, { kind: "review_answer", review_id: target.review.id, generation: target.review.identity, text: draft.written });
  };
  const clear = (target: Review) => void post("review:" + target.id, { kind: "review_clear", review_id: target.id, generation: target.identity });
  const taskOf = (candidate: Item) => candidate.kind === "question" ? candidate.question.task : candidate.review.task;
  const askerOf = (candidate: Item) => taskOf(candidate) ? snapshot.tasks.find((task) => task.id === taskOf(candidate))?.title || taskOf(candidate) : "The CFO";
  const textOf = (candidate: Item) => candidate.kind === "question" ? candidate.question.text : candidate.review.title;
  const created = (candidate: Item) => candidate.kind === "question" ? candidate.question.created_at : candidate.review.created_at;
  const iconOf = (candidate: Item) => candidate.kind === "question" ? candidate.question.image_count ? "images" : "question" : candidate.review.image_count ? "images" : "comment";
  const pageFor = (candidate: Question) => presentations.find((event) => event.kind === "review" && (candidate.task ? event.task_id === candidate.task : !!event.cfo_identity));
  const images = !item ? [] : item.kind === "question"
    ? questionChoices(item.question).filter((choice) => choice.image).map((choice) => ({ src: choice.image, label: choice.label, value: choice.value }))
    : reviewImages(item.review).map((src, n) => ({ src, label: String(n + 1), value: "Image " + (n + 1) }));
  const notices = presentations.filter((event) => !background.has(event.id)).slice(-4).reverse();
  const settled = settledItems(snapshot).slice(0, 20);
  return <>
    <details className="command-center-menu" open={inbox} onToggle={(event) => setInbox(event.currentTarget.open)}>
      <summary className="icon-button" data-tip="Command Center" data-tip-align="end" aria-label={"Command Center" + (waiting.length ? ", " + waiting.length + " waiting on you" : "")}><Icon name="command-center" />{waiting.length > 0 && <span className="count-badge" aria-hidden="true">{waiting.length}</span>}</summary>
      <div className="command-center-updates">
        <h2><Avatar persona="cfo" small />Command Center</h2>
        <section aria-label="Waiting on you">
          <h3>Waiting on you <span className="column-count">{waiting.length}</span></h3>
          {waiting.length ? <ul className="inbox-list">{waiting.map((candidate) => <li key={candidate.key}>
            <Avatar persona={taskOf(candidate) ? personaFor(snapshot.tasks.find((task) => task.id === taskOf(candidate))) : "cfo"} small />
            <span className="inbox-text"><strong>{askerOf(candidate)}</strong>{textOf(candidate)}</span>
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
        {settled.length > 0 && <Disclosure kind="inbox-history" title={<>Answered <span className="column-count">{settled.length}</span></>}>
          <ul className="inbox-list">{settled.map((candidate) => {
            const mark = settledIcon(candidate, snapshot.actions);
            return <li key={candidate.key}>
              <span className={"delivery " + mark.tone}><Icon name={mark.icon} /></span>
              <span className="inbox-text"><strong>{askerOf(candidate)}</strong>{textOf(candidate)}<small>{settledLabel(candidate, snapshot.actions)}</small></span>
            </li>;
          })}</ul>
        </Disclosure>}
        <p className="muted">Everything stays here until you answer or clear it. Opening a page never pauses work.</p>
      </div>
    </details>
    <dialog ref={dialog} className="question-modal" aria-labelledby="command-center-heading"
      onCancel={(event) => { event.preventDefault(); if (gallery !== null) setGallery(null); else close(); }} onKeyDown={(event) => event.stopPropagation()}>
      {item && <>
        <header className="command-center-heading">
          <Avatar persona="cfo" />
          <h2 id="command-center-heading">Supreme Overlord<span>Command Center</span></h2>
          {stack.length > 1 && <span className="count-pill">{index + 1} of {stack.length}</span>}
          <button type="button" className="icon-button question-close" aria-label="Close the Command Center" data-tip="Close" data-tip-align="end" onClick={close}><Icon name="close" /></button>
        </header>
        {gallery !== null && images.length > 0
          ? <ImageGallery images={images} index={Math.min(gallery, images.length - 1)} lavish={item.kind === "question" ? pageFor(item.question)?.url : item.review.lavish} onIndex={setGallery} onClose={() => setGallery(null)}
            onChoose={item.kind === "question" && item.question.status === "pending" ? (value) => { update(item.key, { selection: "option:" + value, error: "", receipt: undefined }); setGallery(null); } : undefined} />
          : <div className={"card-stage" + (stack.length > 1 ? " stacked" : "")}
            onPointerDown={(event) => { if (event.pointerType !== "mouse") swipe.current = { x: event.clientX, y: event.clientY }; }}
            onPointerUp={(event) => {
              const start = swipe.current;
              swipe.current = null;
              if (!start) return;
              const dx = event.clientX - start.x;
              if (Math.abs(dx) > 70 && Math.abs(event.clientY - start.y) < 50) move(dx < 0 ? 1 : -1);
            }}>
            {item.kind === "question"
              ? <QuestionCard key={item.key} question={item.question} snapshot={snapshot} connected={connected} draft={drafts[item.key] || EMPTY_DRAFT} review={pageFor(item.question)}
                onDraft={(changes) => update(item.key, changes)} onSend={() => send(item)} onImage={setGallery} />
              : <ReviewCard key={item.key} review={item.review} snapshot={snapshot} connected={connected} draft={drafts[item.key] || EMPTY_DRAFT}
                onDraft={(changes) => update(item.key, changes)} onSend={() => send(item)} onClear={() => clear(item.review)} onImage={setGallery} />}
          </div>}
        {gallery === null && <footer className="question-footer">
          <button type="button" className="text-button" onClick={later}>{isOpen(item) ? "Later" : "Next"}</button>
          {stack.length > 1 && <div className="stack-nav">
            <button type="button" className="icon-button raised" disabled={index === 0} aria-label="Previous item" data-tip="Previous" onClick={() => move(-1)}><Icon name="back" /></button>
            {stack.length <= 8 && <span className="stack-dots" aria-hidden="true">{stack.map((candidate) => <span key={candidate.key} className={candidate.key === item.key ? "on" : ""} />)}</span>}
            <button type="button" className="icon-button raised" disabled={index === stack.length - 1} aria-label="Next item" data-tip="Next" data-tip-align="end" onClick={() => move(1)}><Icon name="next" /></button>
          </div>}
        </footer>}
      </>}
    </dialog>
  </>;
}
