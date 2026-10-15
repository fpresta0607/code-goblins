import { useEffect, useLayoutEffect, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { message, request } from "./api";
import { object, parseAction, string, type BoardActivity, type BrowserSignIn, type Question, type Review, type Run, type Snapshot } from "./types";
import { deliveryMark, runMark, submissionFor } from "./feedback";
import { Avatar } from "./Avatar";
import { Chevron } from "./Chevron";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { answerMark, answerReason, answeredElsewhere, answeredHere, asItems, canChange, closedAt, closedElsewhere, holdsUnsent, isOpen, itemFor, nextOpenKey, notSent, questionPage, sendState, settledIcon, reviewLine, settledItems, settledLabel, waitingItems, type Item } from "./commandQueue";
import { publishedAt, type Sent } from "./item-state";
import { RunCard } from "./RunCard";
import { UpdateCard } from "./update-card";
import { CfoUnwokenCard } from "./cfo-unwoken-card";
import { CredentialCard } from "./credential-card";
import { credentialAsk, credentialFinish, onThisMachine } from "./credentials";
import { BrowserSignInCard } from "./browser-signin-card";
import { signInHeading, signInOutcome } from "./browser-signins";
import { questionAnswer, questionChoices, questionSelection } from "./questionChoices";
import { plainMessage } from "./messageText";
import { personaFor } from "./workflow";
import { EMPTY_DRAFT, QuestionCard, type Draft } from "./QuestionCard";
import { ReviewCard, reviewImages } from "./ReviewCard";
import { ImageGallery } from "./ImageGallery";
import { Disclosure } from "./Disclosure";
import { countedTitle } from "./arrivals";
import { DoneCard } from "./DoneCard";
import { AnswerMark } from "./AnswerMark";
import { ChangeCard } from "./ChangeCard";
import "./lantern.css";
import { goblinName } from "./task-words";

// A sent item's check shows this long before the next item.
const DONE_MS = 750;
// "You're all done" shows this long before the Command Center closes.
const ALL_DONE_MS = 1600;
// A change a dropped connection kept from the supervisor goes again this soon.
const RESEND_MS = 1000;

// A focus opens its item; one without a key opens the first item waiting on
// the Overlord, or the list when nothing waits. One with keys, as Go through
// them on the AFK report names what still waits from the stretch, steps
// through only those. One with change opens the answered question at that
// item key on its change card, as History's Change answer does, as Change on
// the AFK recap asks.
export interface CommandFocus { key: string; at: number; keys?: string[]; change?: string }
interface SignInAttempt { signin: BrowserSignIn; instance: string; isPending: boolean; error: string }

const outsideDialog = (event: MouseEvent<HTMLDialogElement>) => {
  const box = event.currentTarget.getBoundingClientRect();
  return event.target === event.currentTarget && (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom);
};

// The Supreme Overlord Command Center: an inbox of everything waiting on him,
// and a stack that shows one item at a time, a question or a review item. Each
// answer goes to its asker on its own, once; drafts survive closing,
// reconnecting and moving between cards. It never opens by itself: a new
// item waits in the inbox under the badge, the bar says so, and the tab's
// title counts what waits. The moment an answer is sent
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
  const [signInAttempts, setSignInAttempts] = useState<Record<string, SignInAttempt>>({});
  const activeSignInAttempts = useRef(signInAttempts);
  useLayoutEffect(() => { activeSignInAttempts.current = signInAttempts; }, [signInAttempts]);
  const [publications, setPublications] = useState(() => new Map(asItems(snapshot).map((item) => [item.key, publishedAt(item)])));
  const latestPublications = useRef(publications);
  useLayoutEffect(() => { latestPublications.current = publications; }, [publications]);
  const [open, setOpen] = useState(false);
  const [current, setCurrent] = useState("");
  const [kept, setKept] = useState<Set<string>>(new Set());
  const [lastFocus, setLastFocus] = useState<CommandFocus | null>(focus);
  const [gallery, setGallery] = useState<number | null>(null);
  const [inbox, setInbox] = useState(false);
  const [sent, setSent] = useState<Set<string>>(new Set());
  const [leaving, setLeaving] = useState("");
  const [allDone, setAllDone] = useState(false);
  // The answered question he opened from History, to see or change its answer.
  const [changing, setChanging] = useState("");
  // The History rows he unfolded, by item key: each row is one line until he
  // opens it.
  const [unfolded, setUnfolded] = useState<ReadonlySet<string>>(new Set());
  // The changes being sent now, by draft key, so a second press in the same
  // frame sends nothing more.
  const sendingChanges = useRef(new Set<string>());
  // The items a focus with keys named, the only ones the stack steps through,
  // in its order, until the Command Center closes.
  const [scope, setScope] = useState<string[] | null>(null);
  const changeQuestion = changing ? (snapshot.questions || []).find((question) => question.id === changing) : undefined;
  const unscoped = waitingItems(snapshot, kept);
  const stack = scope ? scope.flatMap((key) => unscoped.filter((candidate) => candidate.key === key)) : unscoped;
  const waiting = waitingItems(snapshot);
  const index = Math.max(0, stack.findIndex((item) => item.key === current));
  const item: Item | undefined = open && !changeQuestion ? stack[index] : undefined;
  const draft = item ? drafts[item.key] : undefined;
  const outcome = draft?.submission ? snapshot.actions.find((action) => action.id === draft.submission?.id) || draft.receipt : undefined;
  const mark = outcome ? deliveryMark(outcome, item?.kind === "question" ? item.question.task : undefined) : undefined;
  const sending = draft ? sendState(draft, snapshot.actions) : undefined;
  // A change of an answer keeps its own draft, apart from the question's.
  const changeKey = (question: Question) => "change:" + question.id;
  const changeDraft = (question: Question) => drafts[changeKey(question)] || EMPTY_DRAFT;
  // What the Overlord sent from this card shows as done the moment he sends
  // it; trouble keeps the card itself on screen with what went wrong. An item
  // he answered elsewhere, such as on its page, finishes the same way,
  // whatever this card tried meanwhile, and so does one that closed while this
  // card sent nothing: answered on another board, or taken back by its asker
  // or the CFO. A command finishes once it ends without trouble, by its exit
  // code whatever its output says, or is withdrawn; one that failed keeps its
  // card, with its output, and an update keeps its own card. A credential
  // request finishes once every value it asks for is stored, by its card or
  // its terminal.
  const closedBy = item && !draft?.submission ? closedElsewhere(item, snapshot.actions) : "";
  const elsewhere = !!item && (answeredElsewhere(item) || !!closedBy);
  const ranClean = item?.kind === "run" && !item.run.update && !isOpen(item) && !runMark(item.run).trouble;
  const credentialEnd = item?.kind === "credential" ? credentialFinish(item.request) : null;
  const stored = !!credentialEnd;
  const finishing = !!item && (item.kind === "run" ? ranClean : item.kind === "credential" ? stored : item.kind === "browser-signin" ? !isOpen(item) : elsewhere || !!sending && !sending.failed);
  const done = finishing ? item.key : "";
  // A question he answered and moved past opens again, when he goes back to
  // it, with his answer checked and its other choices open to change, never
  // as its done card.
  const answered = item?.kind === "question" && sent.has(item.key) ? answeredHere(item.question, draft || EMPTY_DRAFT) : undefined;
  const revisited = answered && canChange(answered) ? answered : undefined;
  // Moving off a finishing card counts it as sent, so only a card on screen
  // from its Send to its delivery moves on by itself.
  const show = (key: string) => {
    if (finishing && item.key !== key) setSent((prior) => new Set([...prior, item.key]));
    setCurrent(key); setGallery(null); setAllDone(false);
  };
  const unsent = holdsUnsent(drafts, snapshot);
  useEffect(() => onUnsent(unsent), [unsent, onUnsent]);
  // A presentation reaches him only when its goblin asks him to watch it; a
  // goblin's own test run stays off the Command Center.
  const watching = presentations.filter((event) => event.watch);
  const needing = waiting.length + watching.length;
  const baseTitle = useRef(document.title);
  useEffect(() => { document.title = countedTitle(baseTitle.current, needing); }, [needing]);
  useEffect(() => () => { document.title = baseTitle.current; }, []);
  if (focus !== lastFocus) {
    setLastFocus(focus);
    const answered = focus?.change ? itemFor(snapshot, focus.change) : undefined;
    // A focus on an item he already answered or cleared, as from a
    // notification that outlived it, opens the list, never another item.
    const key = focus?.key || waiting[0]?.key;
    setScope(focus?.keys ?? null);
    if (answered?.kind === "question" && canChange(answered.question)) { setInbox(false); setChanging(answered.question.id); }
    else if (focus && !focus.change && key && unscoped.some((item) => item.key === key)) { setOpen(true); show(key); setInbox(false); }
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
  const shownKey = item ? item.key + ":" + publishedAt(item) : "";
  useEffect(() => {
    if (!done || sent.has(done)) return;
    const timer = setTimeout(() => setLeaving(done), DONE_MS);
    return () => clearTimeout(timer);
  }, [done, sent]);
  useEffect(() => {
    if (!allDone) return;
    const timer = setTimeout(() => { setOpen(false); setKept(new Set()); setGallery(null); setAllDone(false); setScope(null); }, ALL_DONE_MS);
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
    setOpen(false); setKept(new Set()); setGallery(null); setAllDone(false); setChanging(""); setScope(null);
  };
  const move = (step: number) => { const next = stack[index + step]; if (next) show(next.key); };
  // Back and Next with the card's place in the stack lead the card's own
  // action row, whose right end holds its answer. Next leaves the item as it
  // is and shows the next, and closing keeps every item. Skip, which clears
  // an item, is the card's own, at its top.
  const pager: ReactNode = stack.length > 1 ? <div className="stack-pager" role="group" aria-label="Move between items">
    <button type="button" disabled={index === 0} onClick={() => move(-1)}>Back</button>
    <span className="stack-count">{index + 1} of {stack.length}</span>
    <button type="button" disabled={index === stack.length - 1} onClick={() => move(1)}>Next</button>
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
      const chosen = questionSelection(target.question, draft);
      const payload = questionAnswer(target.question, chosen.selection, chosen.written);
      if (payload) void post(target.key, payload);
    } else if (target.kind === "review" && draft.written.trim()) void post(target.key, { kind: "review_answer", review_id: target.review.id, generation: target.review.identity, text: draft.written });
  };
  // Run names the stored item; the browser never sends command text.
  const run = (target: Run) => { if (target.state === "ready") void post("run:" + target.id, { kind: "run", run_id: target.id, generation: target.identity }); };
  // Skip and Other close a command nobody ran in place of running it.
  const skipRun = (target: Run) => { if (target.state === "ready") void post("run:" + target.id, { kind: "run_skip", run_id: target.id, generation: target.identity }); };
  const answerRun = (target: Run, text: string) => { if (target.state === "ready" && text) void post("run:" + target.id, { kind: "run_answer", run_id: target.id, generation: target.identity, text }); };
  const openSignIn = async (signin: BrowserSignIn) => {
    if (!connected || !onThisMachine(window.location.hostname) || signin.state !== "ready" || activeSignInAttempts.current[signin.id]?.isPending) return;
    const attempt: SignInAttempt = { signin, instance: snapshot.instance, isPending: true, error: "" };
    activeSignInAttempts.current = { ...activeSignInAttempts.current, [signin.id]: attempt };
    setSignInAttempts((prior) => ({ ...prior, [signin.id]: attempt }));
    try {
      await request("/api/browser-signins/open", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ id: signin.id, generation: signin.generation }) });
    } catch (error: unknown) {
      setSignInAttempts((prior) => prior[signin.id] === attempt ? { ...prior, [signin.id]: { ...attempt, isPending: false, error: message(error) } } : prior);
    }
  };
  // An update that did not install offers one Try again, once the board has
  // published its item for the same release again: retryItem is that item,
  // which the press shows and starts at once.
  const retryItem = (target: Run) => target.state === "failed" ? (snapshot.runs || []).find((other) => other.id !== target.id && other.state === "ready" && other.update?.to === target.update?.to) : undefined;
  const clear = (target: Review) => void post("review:" + target.id, { kind: "review_clear", review_id: target.id, generation: target.identity });
  const dismiss = (target: Question) => void post("question:" + target.id, { kind: "question_clear", question_id: target.id, generation: target.identity });
  // His change of an answer shows as the answer on its card the moment he
  // makes it. It goes under one request ID until the supervisor has it, so a
  // double press, a second tab or a resend after a dropped connection sends
  // it once. A refusal puts the answer back as it stood and says why beside
  // the button.
  const sendChange = async (key: string, payload: Record<string, unknown>) => {
    if (sendingChanges.current.has(key)) return;
    sendingChanges.current.add(key);
    const draft = drafts[key] || EMPTY_DRAFT;
    const submission = submissionFor(JSON.stringify(payload), draft.submission, () => crypto.randomUUID());
    update(key, { submission, sending: true, error: "", unsent: false, sentAt: submission === draft.submission ? draft.sentAt : new Date().toISOString() });
    try {
      const receipt = parseAction(await request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ id: submission.id, ...payload }) }));
      update(key, { receipt, sending: false });
    } catch (error: unknown) {
      const isRefused = error instanceof Error && error.cause !== undefined;
      update(key, isRefused ? { selection: "", written: "", submission: null, receipt: undefined, sending: false, error: message(error) } : { sending: false, unsent: true });
    } finally {
      sendingChanges.current.delete(key);
    }
  };
  const change = (target: Question) => {
    const draft = changeDraft(target);
    const payload = questionAnswer(target, draft.selection, draft.written);
    if (payload) void sendChange(changeKey(target), { ...payload, kind: "answer_change" });
  };
  // Each change a dropped connection kept back, as its draft key and payload.
  const keptBack = Object.entries(drafts).flatMap(([key, draft]) => key.startsWith("change:") && draft.unsent && draft.submission ? [[key, draft.submission.payload]] : []);
  const resend = useRef(sendChange);
  useLayoutEffect(() => { resend.current = sendChange; });
  const keptBackKey = JSON.stringify(keptBack);
  useEffect(() => {
    const waiting: string[][] = JSON.parse(keptBackKey);
    if (!connected || !waiting.length) return;
    const timer = setTimeout(() => { for (const [key, payload] of waiting) void resend.current(key, object(JSON.parse(payload))); }, RESEND_MS);
    return () => clearTimeout(timer);
  }, [connected, keptBackKey]);
  const taskOf = (candidate: Item) => candidate.kind === "browser-signin" ? candidate.signin.task : candidate.kind === "question" ? candidate.question.task : candidate.kind === "review" ? candidate.review.task : candidate.kind === "credential" ? candidate.request.task : candidate.run.task;
  // An Update item is Code Goblins' own, not the CFO's or a goblin's.
  const release = (candidate: Item) => candidate.kind === "run" ? candidate.run.update : null;
  const askerOf = (candidate: Item) => {
    const goblin = snapshot.tasks.find((task) => task.id === taskOf(candidate));
    return release(candidate) ? "Code Goblins" : taskOf(candidate) ? goblin ? goblinName(goblin) : taskOf(candidate) : "The CFO";
  };
  const textOf = (candidate: Item) => candidate.kind === "browser-signin" ? signInHeading(candidate.signin) : candidate.kind === "question" ? plainMessage(candidate.question.text) : candidate.kind === "review" ? reviewLine(candidate.review) : candidate.kind === "credential" ? credentialAsk(candidate.request) : candidate.run.update ? "Update to " + candidate.run.update.to + " from " + candidate.run.update.from : candidate.run.title;
  const created = (candidate: Item) => candidate.kind === "browser-signin" ? candidate.signin.created_at : candidate.kind === "question" ? candidate.question.created_at : candidate.kind === "review" ? candidate.review.created_at : candidate.kind === "credential" ? candidate.request.created_at : candidate.run.created_at;
  const iconOf = (candidate: Item) => candidate.kind === "browser-signin" ? "lock" : candidate.kind === "question" ? candidate.question.image_count ? "images" : "question" : candidate.kind === "review" ? candidate.review.document ? "file" : candidate.review.image_count ? "images" : "comment" : candidate.kind === "credential" ? "key" : candidate.run.update ? "download" : candidate.run.cfo_unwoken ? "refresh" : "play";
  const pageFor = (candidate: Question) => questionPage(presentations, candidate);
  const images = !item || item.kind === "run" || item.kind === "credential" || item.kind === "browser-signin" ? [] : item.kind === "question"
    ? questionChoices(item.question).filter((choice) => choice.image).map((choice) => ({ src: choice.image, value: choice.value, text: choice.text }))
    : reviewImages(item.review).map((src, n) => ({ src, value: "Image " + (n + 1), text: "Image " + (n + 1) }));
  const presenter = (event: BoardActivity) => {
    const goblin = snapshot.tasks.find((task) => task.id === event.task_id);
    return event.cfo_identity ? "The CFO" : goblin ? goblinName(goblin) : event.task_id;
  };
  const settled = settledItems(snapshot).slice(0, 20);
  // An accepted click stays pending until the broker reports progress, even
  // when he leaves its card. A late refusal belongs only to that attempt.
  const staleSignIns = Object.entries(signInAttempts).filter(([id, attempt]) => {
    const latest = snapshot.browser_signins?.find((signin) => signin.id === id);
    return !latest || attempt.instance !== snapshot.instance || latest.generation !== attempt.signin.generation || latest.created_at !== attempt.signin.created_at || latest.state !== attempt.signin.state || latest.reason !== attempt.signin.reason;
  });
  if (staleSignIns.length) {
    const stale = new Set(staleSignIns.map(([id]) => id));
    setSignInAttempts(Object.fromEntries(Object.entries(signInAttempts).filter(([id]) => !stale.has(id))));
  }
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
      <summary className="icon-button" data-tip="Command Center" aria-label={"Command Center" + (needing ? ", " + needing + " waiting on you" : "")}><Icon name="command-center" />{needing > 0 && <span className="count-badge" aria-hidden="true">{needing}</span>}</summary>
      <div className="command-center-updates">
        <h2><Avatar persona="cfo" small />Command Center</h2>
        <section aria-label="Waiting on you">
          <h3>Waiting on you <span className="column-count">{needing}</span>{waiting.length > 1 && <button className="primary go-through" onClick={() => { setInbox(false); setScope(null); setOpen(true); show(waiting[0].key); }}>Go through them</button>}</h3>
          {needing ? <ul className="inbox-list waiting">{waiting.map((candidate) => <li key={candidate.key} className={release(candidate) ? "release-row" : undefined}>
            <Avatar persona={release(candidate) ? "releases" : taskOf(candidate) ? personaFor(snapshot.tasks.find((task) => task.id === taskOf(candidate))) : "cfo"} small />
            <span className="inbox-text"><strong>{askerOf(candidate)}</strong><span className="inbox-summary">{textOf(candidate)}</span></span>
            <time>{age(created(candidate))}</time>
            <button className="icon-button raised" aria-label={"Answer " + askerOf(candidate) + ": " + textOf(candidate)} data-tip="Answer" onClick={() => { setInbox(false); setOpen(true); show(candidate.key); }}><Icon name={iconOf(candidate)} /></button>
          </li>)}{watching.map((event) => <li key={"watch:" + event.id}>
            <Avatar persona={event.cfo_identity ? "cfo" : personaFor(snapshot.tasks.find((task) => task.id === event.task_id))} small />
            <span className="inbox-text"><strong>{presenter(event)}</strong><span className="inbox-summary">{event.watch}</span></span>
            <time>{age(event.at)}</time>
            <a className="icon-button raised" href={event.url} target="_blank" rel="noreferrer" aria-label={"Watch: " + event.watch} data-tip="Watch"><Icon name="watch" /></a>
          </li>)}</ul> : <p className="muted">Nothing is waiting on you.</p>}
        </section>
        {settled.length > 0 && <Disclosure kind="inbox-history" title={<>History <span className="column-count">{settled.length}</span></>}>
          <ul className="inbox-list">{settled.map((candidate) => {
            const mark = settledIcon(candidate, snapshot.actions);
            const who = candidate.kind === "question" ? answerMark(candidate.question) : "";
            const reason = candidate.kind === "question" ? answerReason(candidate.question) : "";
            const isUnfolded = unfolded.has(candidate.key);
            const detail = "history-detail-" + candidate.key;
            // A folded row names a goblin by its first name alone, which leaves
            // the line to what it was about.
            const goblin = snapshot.tasks.find((task) => task.id === taskOf(candidate));
            const asker = !isUnfolded && !release(candidate) && goblin?.goblin_name || askerOf(candidate);
            const unfold = () => setUnfolded((rows) => {
              const next = new Set(rows);
              if (!next.delete(candidate.key)) next.add(candidate.key);
              return next;
            });
            return <li key={candidate.key} className={isUnfolded ? "unfolded" : undefined}>
              {who ? <AnswerMark who={who} /> : <span className={"delivery " + mark.tone}><Icon name={mark.icon} /></span>}
              <span className="inbox-text">
                <span className="history-head"><strong>{asker}</strong><span className="inbox-summary">{textOf(candidate)}</span></span>
                <span className="history-detail" id={detail} hidden={!isUnfolded}><small>{settledLabel(candidate, snapshot.actions)}</small>{reason && <small className="answer-reason">{reason}</small>}</span>
              </span>
              <time>{age(closedAt(candidate))}</time>
              <span className="history-actions">
                <button className="icon-button history-open" aria-expanded={isUnfolded} aria-controls={detail} aria-label={"Open: " + askerOf(candidate) + ": " + textOf(candidate)} data-tip={isUnfolded ? "Fold" : "Open"} onClick={unfold}><Chevron collapsed={!isUnfolded} /></button>
                {candidate.kind === "question" && canChange(candidate.question) && <button className="icon-button raised" aria-label={"Change the answer to " + askerOf(candidate) + ": " + textOf(candidate)} data-tip="Change answer" onClick={() => { setInbox(false); setChanging(candidate.question.id); }}><Icon name="edit" /></button>}
              </span>
            </li>;
          })}</ul>
        </Disclosure>}
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
          <button type="button" className="icon-button question-close" aria-label="Close the Command Center" data-tip="Close" onClick={close}><Icon name="close" /></button>
        </header>
        {scope && <p className="from-afk">From your AFK stretch</p>}
        {changeQuestion
          ? <div className="card-stage">
            <ChangeCard key={"change:" + changeQuestion.id} question={changeQuestion} snapshot={snapshot} connected={connected} draft={changeDraft(changeQuestion)}
              onDraft={(changes) => update(changeKey(changeQuestion), changes)} onChange={() => change(changeQuestion)} onClose={() => setChanging("")} />
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
              : revisited
              ? <ChangeCard key={"change:" + revisited.id} question={revisited} snapshot={snapshot} connected={connected} draft={changeDraft(revisited)}
                onDraft={(changes) => update(changeKey(revisited), changes)} onChange={() => change(revisited)} onClose={close} pager={pager} />
              : elsewhere
              ? <DoneCard key={shownKey} heading={closedBy || "Answered"} label={settledLabel(item, snapshot.actions)} pager={pager} />
              : ranClean
              ? <DoneCard key={shownKey} heading={runMark(item.run).label} label={item.run.state === "stopped" ? "" : item.run.reason} pager={pager} />
              : item.kind === "credential" && credentialEnd
              ? <DoneCard key={shownKey} heading={credentialEnd.heading} label={credentialEnd.label} pager={pager} />
              : item.kind === "browser-signin" && item.signin.state === "signed_in"
              ? <DoneCard key={shownKey} heading="Signed in" label={signInOutcome(item.signin)} pager={pager} />
              : finishing && sending
              ? <DoneCard key={shownKey} heading={sending.heading}
                label={sending.cleared ? sending.heading !== "Cleared" ? "It moves to your history." : "" : mark?.label !== sending.heading ? mark?.label || "" : ""} pager={pager} />
              : item.kind === "question"
              ? <QuestionCard key={shownKey} question={item.question} snapshot={snapshot} connected={connected} draft={drafts[item.key] || EMPTY_DRAFT} review={pageFor(item.question)}
                onDraft={(changes) => update(item.key, changes)} onSend={() => send(item)} onDismiss={() => dismiss(item.question)} onImage={setGallery} pager={pager} />
              : item.kind === "credential"
              ? <CredentialCard key={shownKey} request={item.request} snapshot={snapshot} connected={connected} pager={pager} />
              : item.kind === "browser-signin"
              ? <BrowserSignInCard key={shownKey} signin={item.signin} snapshot={snapshot} connected={connected} isOpening={!!signInAttempts[item.signin.id]?.isPending} error={signInAttempts[item.signin.id]?.error || ""} onOpen={() => void openSignIn(item.signin)} pager={pager} />
              : item.kind === "run" && item.run.cfo_unwoken
              ? <CfoUnwokenCard key={shownKey} run={item.run} snapshot={snapshot} pager={pager} />
              : item.kind === "run" && item.run.update
              ? <UpdateCard key={shownKey} run={item.run} offer={item.run.update} connected={connected} sending={!!drafts[item.key]?.sending} error={drafts[item.key]?.error || ""} onRun={() => run(item.run)} onRetry={retryItem(item.run) ? () => { const again = retryItem(item.run); if (again) { show("run:" + again.id); run(again); } } : undefined} pager={pager} />
              : item.kind === "run"
              ? <RunCard key={shownKey} run={item.run} goblin={item.run.task ? snapshot.tasks.find((task) => task.id === item.run.task) : undefined} connected={connected} instance={snapshot.instance} sending={!!drafts[item.key]?.sending} error={drafts[item.key]?.error || ""} onRun={() => run(item.run)} onSkip={() => skipRun(item.run)} onAnswer={(text) => answerRun(item.run, text)} pager={pager} />
              : <ReviewCard key={shownKey} review={item.review} snapshot={snapshot} connected={connected} draft={drafts[item.key] || EMPTY_DRAFT}
                onDraft={(changes) => update(item.key, changes)} onSend={() => send(item)} onClear={() => clear(item.review)}
                onAnswer={(text) => void post(item.key, { kind: "review_answer", review_id: item.review.id, generation: item.review.identity, text })} onOpen={show} onImage={setGallery} pager={pager} />}
          </div>}
      </>}
    </dialog>
  </>;
}
