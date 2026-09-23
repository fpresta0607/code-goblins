import { useEffect, useRef, useState } from "react";
import { message, request } from "./api";
import { parseAction, type Action, type Snapshot } from "./types";
import { submissionFor, type Submission } from "./feedback";

interface Draft { text: string; submission: Submission | null; sending: boolean; error: string; receipt?: Action }
export function Questions({ snapshot, connected }: { snapshot: Snapshot; connected: boolean }) {
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
  const update = (id: string, changes: Partial<Draft>) => setDrafts((prior) => ({ ...prior, [id]: { ...(prior[id] || { text: "", submission: null, sending: false, error: "" }), ...changes } }));
  const send = async () => {
    if (!question || !draft?.text.trim() || draft.sending || !pending) return;
    const payload = JSON.stringify({ kind: "cfo_answer", question_id: question.id, generation: question.identity, text: draft.text });
    const submission = submissionFor(payload, draft.submission, () => crypto.randomUUID());
    update(question.id, { submission, sending: true, error: "" });
    setSelected(question.id);
    try {
      const receipt = parseAction(await request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ id: submission.id, kind: "cfo_answer", question_id: question.id, generation: question.identity, text: draft.text }) }));
      update(question.id, { receipt, sending: false });
    } catch (e: unknown) { update(question.id, { error: message(e), sending: false }); }
  };
  const waiting = questions.filter((q) => q.status === "pending").length;
  return <>
    {questions.length > 0 && <button onClick={() => setSelected(questions.find((q) => q.status === "pending")?.id || questions[questions.length - 1].id)}>Questions{waiting > 0 && " · " + waiting}</button>}
    <dialog ref={dialog} className="question-modal" aria-labelledby="question-heading" onCancel={(event) => { event.preventDefault(); dismiss(); }} onKeyDown={(event) => event.stopPropagation()}>
      {question && <form onSubmit={(event) => { event.preventDefault(); void send(); }}>
        <div className="question-heading"><span>From your CFO</span><button type="button" className="icon-button" aria-label="Dismiss question for now" onClick={dismiss}>×</button></div>
        <h2 id="question-heading">{question.text}</h2>
        {question.options.length ? <fieldset disabled={!pending || draft?.sending}><legend className="sr-only">Choose your answer</legend>
          {question.options.map((option) => <label className="question-choice" key={option}><input type="radio" name="answer" checked={draft?.text === option} onChange={() => update(question.id, { text: option, error: "", receipt: undefined })} /><span>{option}</span></label>)}
        </fieldset> : <label className="written-answer">Your answer<textarea autoFocus rows={4} maxLength={16000} disabled={!pending || draft?.sending} value={draft?.text || ""} onChange={(event) => update(question.id, { text: event.target.value, error: "", receipt: undefined })} /></label>}
        {draft?.error && !outcome && <p className="error-box" role="alert">{draft.error} An unchanged retry keeps its request identity.</p>}
        {(outcome || question.status !== "pending") && <p className="question-outcome" role="status">{outcome?.message || question.message || (question.status === "queued" ? "Answer queued for the CFO." : "Answer recorded.")}</p>}
        <div className="question-footer"><button type="button" onClick={dismiss}>{pending ? "Later" : "Close"}</button>{pending && <button className="primary" type="submit" disabled={!connected || !draft?.text.trim() || draft.sending}>{draft?.sending ? "Sending..." : "Send answer to CFO"}</button>}</div>
      </form>}
    </dialog>
  </>;
}
