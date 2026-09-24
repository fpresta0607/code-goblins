import { useEffect, useRef, useState } from "react";
import { message, request } from "./api";
import { parseAction, type Action, type BoardActivity, type Snapshot } from "./types";
import { submissionFor, type Submission } from "./feedback";
import { PresentationNotices } from "./PresentationNotices";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { questionAnswer, questionChoices, questionSelection } from "./questionChoices";
import { personaFor } from "./workflow";

interface Draft { selection: string; written: string; submission: Submission | null; sending: boolean; error: string; receipt?: Action }
export function Questions({ snapshot, connected, presentations }: { snapshot: Snapshot; connected: boolean; presentations:BoardActivity[] }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const [dismissed, setDismissed] = useState<Set<string>>(new Set());
  const [selected, setSelected] = useState("");
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const questions = snapshot.questions || [];
  const question = questions.find((q) => q.id === selected) || questions.find((q) => q.status === "pending" && !dismissed.has(q.id));
  const id = question?.id;
  useEffect(() => {
    const element = dialog.current;
    if (id && element && !element.open) { returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; element.showModal(); }
    if (!id && element?.open) { element.close(); returnFocus.current?.focus(); }
  }, [id]);
  const dismiss = () => { if (question) setDismissed((prior) => new Set([...prior, question.id])); setSelected(""); };
  const draft = id ? drafts[id] : undefined;
  const outcome = draft?.submission ? snapshot.actions.find((a) => a.id === draft.submission?.id) || draft.receipt : undefined;
  const pending = question?.status === "pending" && !outcome;
  const displayed = question ? questionSelection(question, draft, outcome) : {selection:"", written:""};
  const update = (id: string, changes: Partial<Draft>) => setDrafts((prior) => ({ ...prior, [id]: { ...(prior[id] || { selection: "", written: "", submission: null, sending: false, error: "" }), ...changes } }));
  const payload = question && draft ? questionAnswer(question, draft.selection, draft.written) : null;
  const send = async () => {
    if (!question || !payload || draft?.sending || !pending) return;
    const submission = submissionFor(JSON.stringify(payload), draft?.submission || null, () => crypto.randomUUID());
    update(question.id, { submission, sending: true, error: "" });
    setSelected(question.id);
    try {
      const receipt = parseAction(await request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ id: submission.id, ...payload }) }));
      update(question.id, { receipt, sending: false });
    } catch (e: unknown) { update(question.id, { error: message(e), sending: false }); }
  };
  const waiting = questions.filter((q) => q.status === "pending").length;
  const asker = question?.task || "the CFO";
  return <>
    <details className="command-center-menu"><summary className="icon-button" data-tip="Command Center" data-tip-align="end" aria-label={"Command Center" + (waiting ? ", " + waiting + " waiting on you" : "")}><Icon name="command-center" />{waiting > 0 && <span className="count-badge" aria-hidden="true">{waiting}</span>}</summary><div className="command-center-updates">
      <h2>Supreme Overlord Command Center</h2>
      {questions.length > 0 && <button onClick={() => setSelected(questions.find(q=>q.status==="pending")?.id || questions[questions.length-1].id)}>{waiting ? "Answer pending question" : "View last decision"}</button>}
      <PresentationNotices snapshot={snapshot} presentations={presentations} />
    </div></details>
    <dialog ref={dialog} className="question-modal" aria-labelledby="command-center-heading" aria-describedby="question-heading" onCancel={(event) => { event.preventDefault(); dismiss(); }} onKeyDown={(event) => event.stopPropagation()}>
      {question && <form onSubmit={(event) => { event.preventDefault(); void send(); }}>
        <button type="button" className="question-close icon-button" aria-label="Dismiss question for now" data-tip="Later" data-tip-align="end" onClick={dismiss}><Icon name="close" /></button>
        <header className="command-center-heading"><Avatar persona={question.task ? personaFor(snapshot.tasks.find((task) => task.id === question.task)) : "cfo"} /><div><h2 id="command-center-heading">Supreme Overlord<span>Command Center</span></h2><p><span className="status-dot" />{pending ? (question.task || "CFO") + " needs your decision" : question.answer_id || outcome ? "Your decision" : "Question closed"}</p></div></header>
        <h3 id="question-heading">{question.text}</h3>
        <fieldset disabled={!pending || draft?.sending}><legend className="sr-only">Choose your answer</legend>
          {questionChoices(question).map((option) => <label className="question-choice" key={option.value}>
            <input type="radio" name="answer" checked={displayed.selection === "option:" + option.value} onChange={() => update(question.id, { selection: "option:" + option.value, error: "", receipt: undefined })} />
            <span className="question-option"><span>{option.label}. {option.value}</span>{option.recommended && <span className="recommendation">Recommended</span>}
              {option.image && <span className="question-image"><img src={option.image} alt={"Option " + option.label + ": " + option.value} /><a className="icon-button" href={option.image} target="_blank" rel="noreferrer" aria-label={"Open option " + option.label + " full size"} data-tip="Open full size" data-tip-align="end"><Icon name="external" /></a></span>}</span>
          </label>)}
          <label className="question-choice"><input type="radio" name="answer" checked={displayed.selection === "other"} onChange={() => update(question.id, { selection: "other", error: "", receipt: undefined })} /><span className="question-option"><span>Other</span><small>Write your own answer.</small></span></label>
          {displayed.selection === "other" && <label className="written-answer"><span className="sr-only">Your written answer</span><textarea rows={3} maxLength={4000} placeholder={"Tell " + asker + " what you prefer..."} value={displayed.written} onChange={(event) => update(question.id, { written: event.target.value, error: "", receipt: undefined })} /></label>}
        </fieldset>
        {draft?.error && !outcome && <p className="error-box" role="alert">{draft.error} An unchanged retry keeps its request identity.</p>}
        {(outcome || question.status !== "pending") && <p className="question-outcome" role="status">{outcome?.message || question.message || (question.status === "queued" ? "Answer queued for " + asker + "." : "Answer recorded.")}</p>}
        <div className="question-footer"><button type="button" onClick={dismiss}>{pending ? "Later" : "Close"}</button>{pending && <button className="primary" type="submit" disabled={!connected || !payload || draft?.sending}>{draft?.sending ? "Sending..." : "Send decision"}</button>}</div>
      </form>}
    </dialog>
  </>;
}
