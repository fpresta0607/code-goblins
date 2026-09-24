import { useEffect, useRef, useState } from "react";
import { message, request } from "./api";
import { parseAction, type BoardActivity, type Question, type Snapshot } from "./types";
import { submissionFor } from "./feedback";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { answeredLabel, settledQuestions, waitingQuestions } from "./commandQueue";
import { questionAnswer, questionChoices } from "./questionChoices";
import { personaFor } from "./workflow";
import { EMPTY_DRAFT, QuestionCard, type Draft } from "./QuestionCard";
import { ImageGallery } from "./ImageGallery";
import { Disclosure } from "./Disclosure";

export interface CommandFocus { id: string; at: number }

// The Supreme Overlord Command Center: an inbox of everything waiting on him,
// and a stack that shows one question at a time. Each answer goes to its asker
// on its own, once; Later keeps a question in the stack; drafts survive
// closing, reconnecting and moving between cards.
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
  const stack = waitingQuestions(snapshot, kept);
  const waiting = waitingQuestions(snapshot);
  // A question the Overlord has not seen yet opens the stack at it.
  const fresh = waiting.filter((question) => !announced.has(question.id));
  if (fresh.length) {
    setAnnounced(new Set([...announced, ...fresh.map((question) => question.id)]));
    if (!open) { setOpen(true); setCurrent(fresh[0].id); setGallery(null); }
  }
  if (focus !== lastFocus) {
    setLastFocus(focus);
    if (focus) { setOpen(true); setCurrent(focus.id); setGallery(null); setInbox(false); }
  }
  const index = Math.max(0, stack.findIndex((question) => question.id === current));
  const question: Question | undefined = open ? stack[index] : undefined;
  // The card on screen stays in the stack while it is shown, so a question
  // answered elsewhere turns into its answered card instead of vanishing.
  if (question && !kept.has(question.id)) setKept(new Set([...kept, question.id]));
  const showing = !!question;
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
  const close = () => { setOpen(false); setKept(new Set()); setGallery(null); };
  const show = (id: string) => { setCurrent(id); setGallery(null); };
  const move = (step: number) => { const next = stack[index + step]; if (next) show(next.id); };
  const later = () => {
    const next = stack.slice(index + 1).find((candidate) => candidate.status === "pending") || stack.slice(0, index).find((candidate) => candidate.status === "pending");
    if (next) show(next.id); else close();
  };
  const update = (id: string, changes: Partial<Draft>) => setDrafts((prior) => ({ ...prior, [id]: { ...(prior[id] || EMPTY_DRAFT), ...changes } }));
  const send = async (target: Question) => {
    const draft = drafts[target.id] || EMPTY_DRAFT;
    const known = draft.submission && snapshot.actions.some((action) => action.id === draft.submission?.id);
    const payload = questionAnswer(target, draft.selection, draft.written);
    if (target.status !== "pending" || known || draft.sending || !payload) return;
    const submission = submissionFor(JSON.stringify(payload), draft.submission, () => crypto.randomUUID());
    update(target.id, { submission, sending: true, error: "" });
    setKept((prior) => new Set([...prior, target.id]));
    try {
      const receipt = parseAction(await request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ id: submission.id, ...payload }) }));
      update(target.id, { receipt, sending: false });
    } catch (error: unknown) { update(target.id, { error: message(error), sending: false }); }
  };
  const askerOf = (candidate: Question) => candidate.task ? snapshot.tasks.find((task) => task.id === candidate.task)?.title || candidate.task : "The CFO";
  const reviewFor = (candidate: Question) => presentations.find((event) => event.kind === "review" && (candidate.task ? event.task_id === candidate.task : !!event.cfo_identity));
  const images = question ? questionChoices(question).filter((choice) => choice.image).map((choice) => ({ src: choice.image, label: choice.label, value: choice.value })) : [];
  const notices = presentations.filter((event) => !background.has(event.id)).slice(-4).reverse();
  const settled = settledQuestions(snapshot).slice(0, 20);
  return <>
    <details className="command-center-menu" open={inbox} onToggle={(event) => setInbox(event.currentTarget.open)}>
      <summary className="icon-button" data-tip="Command Center" data-tip-align="end" aria-label={"Command Center" + (waiting.length ? ", " + waiting.length + " waiting on you" : "")}><Icon name="command-center" />{waiting.length > 0 && <span className="count-badge" aria-hidden="true">{waiting.length}</span>}</summary>
      <div className="command-center-updates">
        <h2><Avatar persona="cfo" small />Command Center</h2>
        <section aria-label="Waiting on you">
          <h3>Waiting on you <span className="column-count">{waiting.length}</span></h3>
          {waiting.length ? <ul className="inbox-list">{waiting.map((candidate) => <li key={candidate.id}>
            <Avatar persona={candidate.task ? personaFor(snapshot.tasks.find((task) => task.id === candidate.task)) : "cfo"} small />
            <span className="inbox-text"><strong>{askerOf(candidate)}</strong>{candidate.text}</span>
            <time>{age(candidate.created_at)}</time>
            <button className="icon-button raised" aria-label={"Answer " + askerOf(candidate) + ": " + candidate.text} data-tip="Answer" data-tip-align="end" onClick={() => { setInbox(false); setOpen(true); show(candidate.id); }}><Icon name={candidate.image_count ? "images" : "question"} /></button>
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
          <ul className="inbox-list">{settled.map((candidate) => <li key={candidate.id}>
            <span className="delivery succeeded"><Icon name={candidate.status === "superseded" ? "close" : "check-double"} /></span>
            <span className="inbox-text"><strong>{askerOf(candidate)}</strong>{candidate.text}<small>{answeredLabel(candidate)}</small></span>
          </li>)}</ul>
        </Disclosure>}
        <p className="muted">Questions stay here until you answer them. Opening a page never pauses work.</p>
      </div>
    </details>
    <dialog ref={dialog} className="question-modal" aria-labelledby="command-center-heading"
      onCancel={(event) => { event.preventDefault(); if (gallery !== null) setGallery(null); else close(); }} onKeyDown={(event) => event.stopPropagation()}>
      {question && <>
        <header className="command-center-heading">
          <Avatar persona="cfo" />
          <h2 id="command-center-heading">Supreme Overlord<span>Command Center</span></h2>
          {stack.length > 1 && <span className="count-pill">{index + 1} of {stack.length}</span>}
          <button type="button" className="icon-button question-close" aria-label="Close the Command Center" data-tip="Close" data-tip-align="end" onClick={close}><Icon name="close" /></button>
        </header>
        {gallery !== null && images.length > 0
          ? <ImageGallery images={images} index={Math.min(gallery, images.length - 1)} onIndex={setGallery} onClose={() => setGallery(null)}
            onChoose={question.status === "pending" ? (value) => { update(question.id, { selection: "option:" + value, error: "", receipt: undefined }); setGallery(null); } : undefined} />
          : <div className={"card-stage" + (stack.length > 1 ? " stacked" : "")}
            onPointerDown={(event) => { if (event.pointerType !== "mouse") swipe.current = { x: event.clientX, y: event.clientY }; }}
            onPointerUp={(event) => {
              const start = swipe.current;
              swipe.current = null;
              if (!start) return;
              const dx = event.clientX - start.x;
              if (Math.abs(dx) > 70 && Math.abs(event.clientY - start.y) < 50) move(dx < 0 ? 1 : -1);
            }}>
            <QuestionCard key={question.id} question={question} snapshot={snapshot} connected={connected} draft={drafts[question.id] || EMPTY_DRAFT} review={reviewFor(question)}
              onDraft={(changes) => update(question.id, changes)} onSend={() => void send(question)} onImage={setGallery} />
          </div>}
        {gallery === null && <footer className="question-footer">
          <button type="button" className="text-button" onClick={later}>{question.status === "pending" ? "Later" : "Next"}</button>
          {stack.length > 1 && <div className="stack-nav">
            <button type="button" className="icon-button raised" disabled={index === 0} aria-label="Previous question" data-tip="Previous" onClick={() => move(-1)}><Icon name="back" /></button>
            {stack.length <= 8 && <span className="stack-dots" aria-hidden="true">{stack.map((candidate) => <span key={candidate.id} className={candidate.id === question.id ? "on" : ""} />)}</span>}
            <button type="button" className="icon-button raised" disabled={index === stack.length - 1} aria-label="Next question" data-tip="Next" data-tip-align="end" onClick={() => move(1)}><Icon name="next" /></button>
          </div>}
        </footer>}
      </>}
    </dialog>
  </>;
}
