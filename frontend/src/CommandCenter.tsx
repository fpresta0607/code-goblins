import { useEffect, useLayoutEffect, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { announce, message, request } from "./api";
import { parseAction, string, type BoardActivity, type Question, type Review, type Run, type Snapshot } from "./types";
import { deliveryMark, submissionFor } from "./feedback";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { answerMark, answerReason, answeredElsewhere, asItems, canChange, cardKey, closedAt, closedElsewhere, holdsUnsent, isOpen, itemFor, nextOpenKey, notSent, openKeys, questionPage, sendState, settledIcon, reviewLine, settledItems, settledLabel, waitingItems, type Item } from "./commandQueue";
import { publishedAt, type Sent } from "./item-state";
import { RunCard } from "./RunCard";
import { CredentialCard } from "./credential-card";
import { credentialAsk } from "./credentials";
import { questionAnswer, questionChoices } from "./questionChoices";
import { plainMessage } from "./messageText";
import { personaFor } from "./workflow";
import { EMPTY_DRAFT, QuestionCard, type Draft } from "./QuestionCard";
import { ReviewCard, reviewImages } from "./ReviewCard";
import { ImageGallery } from "./ImageGallery";
import { Disclosure } from "./Disclosure";
import { countedTitle } from "./arrivals";
import { DoneCard } from "./DoneCard";
import { DocumentCard } from "./DocumentCard";
import { AnswerMark } from "./AnswerMark";
import { ChangeCard } from "./ChangeCard";

// A sent item's check shows this long before the next item.
const DONE_MS = 750;
// "You're all done" shows this long before the Command Center closes.
const ALL_DONE_MS = 1600;

// A focus opens its item; one without a key opens the first item waiting on
// the Overlord, or the list when nothing waits.
export interface CommandFocus { key: string; at: number }

// Whether the Overlord is typing somewhere on the board: in a text field, a
// comment box or a terminal. The Command Center never opens itself then; what
// is new waits under the badge with its alert (decision 3596).
const typing = () => {
  const active = document.activeElement;
  return active instanceof HTMLElement && (active.isContentEditable || active instanceof HTMLTextAreaElement
    || active instanceof HTMLInputElement && !["button", "checkbox", "radio", "submit", "reset", "range", "color", "file"].includes(active.type));
};

const outsideDialog = (event: MouseEvent<HTMLDialogElement>) => {
  const box = event.currentTarget.getBoundingClientRect();
  return event.target === event.currentTarget && (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom);
};

// The Supreme Overlord Command Center: an inbox of everything waiting on him,
// and a stack that shows one item at a time, a question or a review item. Each
// answer goes to its asker on its own, once; drafts survive closing,
// reconnecting and moving between cards. A new question opens the stack, once:
// the supervisor hands each question to the first tab that asks and remembers
// it, so no reload, other tab or supervisor restart opens it again. Any
// other new item waits in the inbox under the badge, the board's alerts
// announce every new item, and the tab's title counts what waits. The moment an answer is sent
// its check shows and the next open item follows while delivery goes on
// quietly; an item he acted on never comes back, so a delivery that fails
// later reads as its line in History. The
// last one ends on "You're all done" before the Command Center closes. It
// tells onUnsent whether any card keeps an answer not yet sent, and onSent
// what he sent for an item the moment he sends it, then null if the
// supervisor refused it.
export function CommandCenter({ snapshot, connected, presentations, focus, onUnsent, onSent }: { snapshot: Snapshot; connected: boolean; presentations: BoardActivity[]; focus: CommandFocus | null; onUnsent: (unsent: boolean) => void; onSent?: (key: string, sent: Sent | null) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const menu = useRef<HTMLDetailsElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const swipe = useRef<{ x: number; y: number } | null>(null);
  const pressedOutside = useRef(false);
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [publications, setPublications] = useState(() => new Map(asItems(snapshot).map((item) => [item.key, publishedAt(item)])));
  const latestPublications = useRef(publications);
  useLayoutEffect(() => { latestPublications.current = publications; }, [publications]);
  const [open, setOpen] = useState(false);
  const [current, setCurrent] = useState("");
  const [kept, setKept] = useState<Set<string>>(new Set());
  const asked = useRef(new Set<string>());
  const [arrived, setArrived] = useState<string[]>([]);
  const [lastFocus, setLastFocus] = useState<CommandFocus | null>(focus);
  const [gallery, setGallery] = useState<number | null>(null);
  const [inbox, setInbox] = useState(false);
  const [background, setBackground] = useState<Set<string>>(new Set());
  const [sent, setSent] = useState<Set<string>>(new Set());
  const [leaving, setLeaving] = useState("");
  const [allDone, setAllDone] = useState(false);
  // The question whose CFO answer he opened from History to change.
  const [changing, setChanging] = useState("");
  const changeQuestion = changing ? (snapshot.questions || []).find((question) => question.id === changing) : undefined;
  const stack = waitingItems(snapshot, kept);
  const waiting = waitingItems(snapshot);
  const index = Math.max(0, stack.findIndex((item) => item.key === current));
  const item: Item | undefined = open && !changeQuestion ? stack[index] : undefined;
  const draft = item ? drafts[item.key] : undefined;
  const outcome = draft?.submission ? snapshot.actions.find((action) => action.id === draft.submission?.id) || draft.receipt : undefined;
  const mark = outcome ? deliveryMark(outcome, item?.kind === "question" ? item.question.task : undefined) : undefined;
  const sending = draft ? sendState(draft, snapshot.actions) : undefined;
  // A change to the CFO's answer keeps its own draft, apart from the
  // question's, and is done once it is sent.
  const changeKey = (question: Question) => "change:" + question.id;
  const changeDraft = (question: Question, from?: Draft) => drafts[changeKey(question)] || { ...EMPTY_DRAFT, selection: from?.selection || "", written: from?.written || "" };
  const isChanged = (question: Question) => {
    const submission = drafts[changeKey(question)]?.submission;
    const change = submission ? snapshot.actions.find((action) => action.id === submission.id) || drafts[changeKey(question)]?.receipt : undefined;
    return change?.status === "succeeded" || !!change?.awaiting;
  };
  // His send met the CFO's answer at the same moment: the board refused his,
  // or the supervisor sent nothing because the CFO had answered.
  const crossed = item?.kind === "question" && item.question.answered_by === "cfo" && !item.question.answered_in && !!draft?.submission && (outcome ? outcome.status === "failed" : !!draft.error);
  const changedHere = item?.kind === "question" && isChanged(item.question);
  // What the Overlord sent from this card shows as done the moment he sends
  // it; trouble keeps the card itself on screen with what went wrong. A run
  // keeps its card, which shows the command's result. An item he answered
  // elsewhere, such as on its page, finishes the same way, whatever this card
  // tried meanwhile, and so does one that closed while this card sent nothing:
  // answered on another board, or taken back by its asker or the CFO.
  const closedBy = item && !draft?.submission ? closedElsewhere(item, snapshot.actions) : "";
  const elsewhere = !!item && (answeredElsewhere(item) || !!closedBy);
  const finishing = !!item && item.kind !== "run" && (elsewhere || changedHere || !!sending && !sending.failed);
  const done = finishing ? item.key : "";
  // Moving off a finishing card counts it as sent, so only a card on screen
  // from its Send to its delivery moves on by itself.
  const show = (key: string) => {
    const shown = cardKey(snapshot, key);
    if (finishing && item.key !== shown) setSent((prior) => new Set([...prior, item.key]));
    setCurrent(shown); setGallery(null); setAllDone(false);
  };
  // Every question still open is asked about once, one its page's card
  // carries too, so it never opens the Command Center when that card closes.
  // With AFK mode on when its answer comes, even an earlier claim opens nothing.
  const { instance } = snapshot;
  const afkState = useRef(snapshot.afk.state);
  useEffect(() => { afkState.current = snapshot.afk.state; });
  const pending = [...openKeys(snapshot)].filter((key) => key.startsWith("question:")).join("\n");
  useEffect(() => {
    const keys = pending ? pending.split("\n").filter((key) => !asked.current.has(key)) : [];
    if (!keys.length) return;
    for (const key of keys) asked.current.add(key);
    void announce(instance, keys.map((key) => "open:" + key)).then((claimed) => setArrived((prior) => [...prior, ...keys.filter((key) => afkState.current !== "on" && (claimed === null || claimed.includes("open:" + key)))]));
  }, [pending, instance]);
  if (arrived.length) {
    setArrived([]);
    const fresh = waiting.find((item) => arrived.includes(item.key));
    if (fresh && !open && !typing()) { setOpen(true); show(fresh.key); }
  }
  const unsent = holdsUnsent(drafts, snapshot);
  useEffect(() => onUnsent(unsent), [unsent, onUnsent]);
  const baseTitle = useRef(document.title);
  useEffect(() => { document.title = countedTitle(baseTitle.current, waiting.length); }, [waiting.length]);
  useEffect(() => () => { document.title = baseTitle.current; }, []);
  if (focus !== lastFocus) {
    setLastFocus(focus);
    // A focus on an item he already answered or cleared, as from a
    // notification that outlived it, opens the list, never another item.
    const key = focus?.key ? cardKey(snapshot, focus.key) : waiting[0]?.key;
    if (focus && key && stack.some((item) => item.key === key)) { setOpen(true); show(key); setInbox(false); }
    else if (focus) setInbox(true);
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
  // A send the board refused after its card moved on sent nothing: its item
  // waits again, with what went wrong on its row, and nothing opens for it.
  const refused = [...sent].filter((key) => !!drafts[key] && notSent(drafts[key], itemFor(snapshot, key), snapshot.actions));
  if (refused.length) {
    setSent(new Set([...sent].filter((key) => !refused.includes(key))));
    if (refused.includes(current)) setAllDone(false);
  }
  // Anything that opens while "You're all done" shows takes its place.
  if (allDone) {
    const resume = nextOpenKey(stack, current, sent);
    if (resume) show(resume);
  }
  // The card on screen stays in the stack while it is shown, so an item
  // answered or cleared elsewhere turns into its settled card instead of vanishing.
  if (item && !kept.has(item.key)) setKept(new Set([...kept, item.key]));
  const showing = !!item || !!changeQuestion;
  // A change made from History shows its check, then the Command Center closes.
  const isChangeDone = !!changeQuestion && isChanged(changeQuestion);
  useEffect(() => {
    if (!isChangeDone) return;
    const timer = setTimeout(() => setChanging(""), ALL_DONE_MS);
    return () => clearTimeout(timer);
  }, [isChangeDone]);
  const shownKey = item ? item.key + ":" + publishedAt(item) : "";
  useEffect(() => {
    if (!done || sent.has(done)) return;
    const timer = setTimeout(() => setLeaving(done), DONE_MS);
    return () => clearTimeout(timer);
  }, [done, sent]);
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
      // Start on the question itself, not the close button, with the
      // Command Center at its top however it was left.
      element.scrollTop = 0;
      element.querySelector<HTMLElement>(".question-card .question-body, .question-card h3")?.focus({ preventScroll: true });
    }
    if (!showing && element.open) { element.close(); returnFocus.current?.focus(); }
  }, [showing]);
  // Each item shows from its top; within one item the scroll stays where he
  // puts it.
  useEffect(() => { if (shownKey && dialog.current) dialog.current.scrollTop = 0; }, [shownKey]);
  const close = () => {
    if (finishing) setSent((prior) => new Set([...prior, item.key]));
    setOpen(false); setKept(new Set()); setGallery(null); setAllDone(false); setChanging("");
  };
  const move = (step: number) => { const next = stack[index + step]; if (next) show(next.key); };
  // Back and Next with the card's place in the stack lead the card's own
  // action row, whose right end holds its answer; closing keeps every item.
  const pager: ReactNode = stack.length > 1 ? <div className="stack-pager" role="group" aria-label="Move between items">
    <button type="button" className="icon-button raised" disabled={index === 0} aria-label="Previous item" data-tip="Previous" data-tip-align="start" onClick={() => move(-1)}><Icon name="back" /></button>
    <span className="stack-count">{index + 1} of {stack.length}</span>
    <button type="button" className="icon-button raised" disabled={index === stack.length - 1} aria-label="Next item" data-tip="Next" onClick={() => move(1)}><Icon name="next" /></button>
  </div> : null;
  const update = (key: string, changes: Partial<Draft>) => setDrafts((prior) => ({ ...prior, [key]: { ...(prior[key] || EMPTY_DRAFT), ...changes } }));
  // An unchanged payload keeps its request ID, so a retry after an ambiguous
  // failure can never deliver twice.
  const post = async (key: string, payload: Record<string, unknown>) => {
    const draft = drafts[key] || EMPTY_DRAFT;
    if (draft.sending || draft.submission && snapshot.actions.some((action) => action.id === draft.submission?.id)) return;
    const publication = publications.get(key);
    const submission = submissionFor(JSON.stringify(payload), draft.submission, () => crypto.randomUUID());
    update(key, { submission, sending: true, error: "" });
    setKept((prior) => new Set([...prior, key]));
    const item = itemFor(snapshot, key);
    if (item) onSent?.(key, { kind: string(payload.kind), id: submission.id, text: string(payload.text), answer_kind: string(payload.answer_kind), created_at: publishedAt(item) });
    try {
      const receipt = parseAction(await request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ id: submission.id, ...payload }) }));
      if (latestPublications.current.get(key) !== publication) return;
      update(key, { receipt, sending: false });
    } catch (error: unknown) {
      if (latestPublications.current.get(key) !== publication) return;
      update(key, { error: message(error), sending: false });
      onSent?.(key, null);
    }
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
  const dismiss = (target: Question) => void post("question:" + target.id, { kind: "question_clear", question_id: target.id, generation: target.identity });
  // A crossed card starts from his pick on the question's own card.
  const editChange = (target: Question, changes: Partial<Draft>, from?: Draft) => {
    const draft = changeDraft(target, from);
    update(changeKey(target), drafts[changeKey(target)] ? changes : { selection: draft.selection, written: draft.written, ...changes });
  };
  const change = (target: Question, from?: Draft) => {
    const draft = changeDraft(target, from);
    const payload = questionAnswer(target, draft.selection, draft.written);
    if (!payload) return;
    if (!drafts[changeKey(target)]) editChange(target, {}, from);
    void post(changeKey(target), { ...payload, kind: "answer_change" });
  };
  const taskOf = (candidate: Item) => candidate.kind === "question" ? candidate.question.task : candidate.kind === "review" ? candidate.review.task : candidate.kind === "credential" ? candidate.request.task : "";
  const askerOf = (candidate: Item) => taskOf(candidate) ? snapshot.tasks.find((task) => task.id === taskOf(candidate))?.title || taskOf(candidate) : "The CFO";
  const textOf = (candidate: Item) => candidate.kind === "question" ? plainMessage(candidate.question.text) : candidate.kind === "review" ? reviewLine(candidate.review) : candidate.kind === "credential" ? credentialAsk(candidate.request) : candidate.run.title;
  const created = (candidate: Item) => candidate.kind === "question" ? candidate.question.created_at : candidate.kind === "review" ? candidate.review.created_at : candidate.kind === "credential" ? candidate.request.created_at : candidate.run.created_at;
  const iconOf = (candidate: Item) => candidate.kind === "question" ? candidate.question.image_count ? "images" : "question" : candidate.kind === "review" ? candidate.review.document ? "file" : candidate.review.image_count ? "images" : "comment" : candidate.kind === "credential" ? "key" : "play";
  const pageFor = (candidate: Question) => questionPage(presentations, candidate);
  const images = !item || item.kind === "run" || item.kind === "credential" ? [] : item.kind === "question"
    ? questionChoices(item.question).filter((choice) => choice.image).map((choice) => ({ src: choice.image, value: choice.value, text: choice.text }))
    : reviewImages(item.review).map((src, n) => ({ src, value: "Image " + (n + 1), text: "Image " + (n + 1) }));
  const notices = presentations.filter((event) => !background.has(event.id)).slice(-4).reverse();
  const settled = settledItems(snapshot).slice(0, 20);
  // Reset before committing this render, including IDs absent between publications.
  // Registration changes identity but keeps created_at, so unfinished answers survive.
  const changed = asItems(snapshot).filter((candidate) => publications.get(candidate.key) !== publishedAt(candidate));
  if (changed.length) {
    const replaced = new Set(changed.filter((candidate) => publications.has(candidate.key)).map((candidate) => candidate.key));
    setPublications(new Map([...publications, ...changed.map((candidate): [string, string] => [candidate.key, publishedAt(candidate)])]));
    if (replaced.size) {
      setDrafts((prior) => Object.fromEntries(Object.entries(prior).filter(([key]) => !replaced.has(key))));
      setSent((prior) => new Set([...prior].filter((key) => !replaced.has(key))));
      setKept((prior) => new Set([...prior].filter((key) => !replaced.has(key))));
      if (replaced.has(current)) { setGallery(null); setAllDone(false); }
      if (replaced.has(leaving)) setLeaving("");
    }
  }
  return <>
    <details ref={menu} className="command-center-menu" open={inbox} onToggle={(event) => setInbox(event.currentTarget.open)}>
      <summary className="icon-button" data-tip="Command Center" data-tip-align="end" aria-label={"Command Center" + (waiting.length ? ", " + waiting.length + " waiting on you" : "")}><Icon name="command-center" />{waiting.length > 0 && <span className="count-badge" aria-hidden="true">{waiting.length}</span>}</summary>
      <div className="command-center-updates">
        <h2><Avatar persona="cfo" small />Command Center</h2>
        <section aria-label="Waiting on you">
          <h3>Waiting on you <span className="column-count">{waiting.length}</span></h3>
          {waiting.length ? <ul className="inbox-list">{waiting.map((candidate) => <li key={candidate.key}>
            <Avatar persona={taskOf(candidate) ? personaFor(snapshot.tasks.find((task) => task.id === taskOf(candidate))) : "cfo"} small />
            <span className="inbox-text"><strong>{askerOf(candidate)}</strong><span className="inbox-summary">{textOf(candidate)}</span>{!!drafts[candidate.key] && notSent(drafts[candidate.key], candidate, snapshot.actions) && <small>Not sent: {drafts[candidate.key].error}</small>}</span>
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
            const who = candidate.kind === "question" ? answerMark(candidate.question) : "";
            const reason = candidate.kind === "question" ? answerReason(candidate.question) : "";
            return <li key={candidate.key}>
              {who ? <AnswerMark who={who} /> : <span className={"delivery " + mark.tone}><Icon name={mark.icon} /></span>}
              <span className="inbox-text"><strong>{askerOf(candidate)}</strong><span className="inbox-summary">{textOf(candidate)}</span><small>{settledLabel(candidate, snapshot.actions)}</small>{reason && <small className="answer-reason">{reason}</small>}</span>
              <time>{age(closedAt(candidate))}</time>
              {candidate.kind === "question" && canChange(candidate.question, snapshot) && <button className="icon-button raised" aria-label={"Change the CFO's answer to " + askerOf(candidate)} data-tip="Change the CFO's answer" data-tip-align="end" onClick={() => { setInbox(false); setChanging(candidate.question.id); }}><Icon name="edit" /></button>}
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
      {(item || changeQuestion) && <>
        <header className="command-center-heading">
          <Avatar persona="cfo" />
          <h2 id="command-center-heading">Supreme Overlord<span>Command Center</span></h2>
          <button type="button" className="icon-button question-close" aria-label="Close the Command Center" data-tip="Close" data-tip-align="end" onClick={close}><Icon name="close" /></button>
        </header>
        {changeQuestion
          ? <div className="card-stage">
            {isChangeDone
              ? <DoneCard key={"changed:" + changeQuestion.id} heading="Changed to your answer" label={(snapshot.tasks.find((task) => task.id === changeQuestion.task)?.title || changeQuestion.task) + " is told the answer is yours: " + changeQuestion.answer} />
              : <ChangeCard key={"change:" + changeQuestion.id} question={changeQuestion} snapshot={snapshot} connected={connected} crossed={false} draft={changeDraft(changeQuestion)}
                onDraft={(changes) => editChange(changeQuestion, changes)} onChange={() => change(changeQuestion)} onClose={() => setChanging("")} />}
          </div>
        : !item ? null
        : gallery !== null && images.length > 0
          ? <ImageGallery images={images} index={Math.min(gallery, images.length - 1)} lavish={item.kind === "question" ? pageFor(item.question)?.url : item.kind === "review" ? item.review.lavish : undefined} onIndex={setGallery} onClose={() => setGallery(null)}
            onChoose={item.kind === "question" && item.question.status === "pending" ? (value) => { update(item.key, { selection: "option:" + value, error: "", receipt: undefined }); setGallery(null); } : undefined} />
          : <div className="card-stage"
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
              : changedHere && item.kind === "question"
              ? <DoneCard key={"changed:" + shownKey} heading="Changed to your answer" label={askerOf(item) + " is told the answer is yours: " + item.question.answer} pager={pager} />
              : crossed && item.kind === "question"
              ? <ChangeCard key={"crossed:" + shownKey} question={item.question} snapshot={snapshot} connected={connected} crossed draft={changeDraft(item.question, draft)}
                onDraft={(changes) => editChange(item.question, changes, draft)} onChange={() => change(item.question, draft)} onClose={() => setLeaving(item.key)} />
              : elsewhere
              ? <DoneCard key={shownKey} heading={closedBy || "Answered"} label={settledLabel(item, snapshot.actions)} pager={pager} />
              : finishing && sending
              ? <DoneCard key={shownKey} heading={sending.heading}
                label={sending.cleared ? sending.heading !== "Cleared" ? "It moves to your history." : "" : mark?.label !== sending.heading ? mark?.label || "" : ""} pager={pager} />
              : item.kind === "question"
              ? <QuestionCard key={shownKey} question={item.question} snapshot={snapshot} connected={connected} draft={drafts[item.key] || EMPTY_DRAFT} review={pageFor(item.question)}
                onDraft={(changes) => update(item.key, changes)} onSend={() => send(item)} onDismiss={() => dismiss(item.question)} onImage={setGallery} pager={pager} />
              : item.kind === "credential"
              ? <CredentialCard key={shownKey} request={item.request} snapshot={snapshot} connected={connected} pager={pager} />
              : item.kind === "run"
              ? <RunCard key={shownKey} run={item.run} connected={connected} sending={!!drafts[item.key]?.sending} error={drafts[item.key]?.error || ""} onRun={() => run(item.run)} pager={pager} />
              : item.review.document
              ? <DocumentCard key={shownKey} review={item.review} document={item.review.document} snapshot={snapshot} connected={connected} draft={drafts[item.key] || EMPTY_DRAFT} onOpened={(how) => clear(item.review, how)} onClear={() => clear(item.review)} pager={pager} />
              : <ReviewCard key={shownKey} review={item.review} snapshot={snapshot} connected={connected} draft={drafts[item.key] || EMPTY_DRAFT}
                onDraft={(changes) => update(item.key, changes)} onSend={() => send(item)} onClear={() => clear(item.review)} onOpen={show} onImage={setGallery} pager={pager} />}
          </div>}
      </>}
    </dialog>
  </>;
}
