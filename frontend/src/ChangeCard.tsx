import { useEffect } from "react";
import type { Question, Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { AnswerMark } from "./AnswerMark";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { changeWarning, standingAnswer, standingWho } from "./commandQueue";
import { questionAnswer, questionChoices } from "./questionChoices";
import { messageElements } from "./messageText";
import { copyValues } from "./CopyValue";
import { personaFor } from "./workflow";
import type { Draft } from "./QuestionCard";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { goblinName } from "./task-words";

// An answered question, opened again from History: its choices with the
// answer checked, who chose it and when, and Change answer for another
// choice or a written answer, at any time and whoever gave the answer. His
// change is the answer the moment he makes it, while it goes on its way, and
// choosing the answer already chosen sends nothing. When the first answer did
// what cannot be undone by itself, one line says so.
export function ChangeCard({ question, snapshot, connected, draft, onDraft, onChange, onClose }: {
  question: Question; snapshot: Snapshot; connected: boolean; draft: Draft;
  onDraft: (changes: Partial<Draft>) => void; onChange: () => void; onClose: () => void;
}) {
  const [feedback, showFeedback] = useClickFeedback();
  useEffect(() => { if (draft.error) showFeedback(draft.error); }, [draft.error, showFeedback]);
  const task = snapshot.tasks.find((candidate) => candidate.id === question.task);
  const asker = question.task ? task ? goblinName(task) : question.task : "The CFO";
  const standing = standingAnswer(question, draft, snapshot.actions);
  const shown = draft.selection ? { selection: draft.selection, written: draft.written } : standing;
  const payload = questionAnswer(question, shown.selection, shown.written);
  const isStanding = shown.selection === standing.selection && (shown.selection !== "other" || shown.written.trim() === standing.written.trim());
  const who = standingWho(question, draft, snapshot.actions);
  const warning = changeWarning(question, snapshot);
  const pick = (selection: string) => onDraft({ selection, error: "" });
  return <form className="question-card change-card" aria-labelledby={"change-" + question.id} onSubmit={(event) => { event.preventDefault(); if (payload && !isStanding) onChange(); }}>
    <p className="asker"><Avatar persona={question.task ? personaFor(task) : "cfo"} small /><span><strong>{asker}</strong> asked · {age(question.created_at)}</span></p>
    <div className="question-body" id={"change-" + question.id} tabIndex={-1}>{messageElements(question.text, copyValues)}</div>
    <fieldset><legend className="sr-only">Choose your answer</legend>
      {questionChoices(question).map((option) => <label className="question-choice" key={option.value}>
        <input type="radio" name={"change-" + question.id} checked={shown.selection === "option:" + option.value} onChange={() => pick("option:" + option.value)} />
        <span className="question-option"><span>{option.text}</span>{option.recommended && <span className="recommendation">Recommended</span>}</span>
      </label>)}
      <label className="question-choice"><input type="radio" name={"change-" + question.id} checked={shown.selection === "other"} onChange={() => onDraft({ selection: "other", written: shown.selection === "other" ? shown.written : standing.written, error: "" })} /><span className="question-option"><span>Other</span><small>Write your own answer.</small></span></label>
      {shown.selection === "other" && <label className="written-answer"><span className="sr-only">Your written answer</span><textarea rows={3} maxLength={4000} placeholder={"Tell " + (question.task ? asker : "the CFO") + " what you prefer..."} value={shown.written} onChange={(event) => onDraft({ selection: "other", written: event.target.value, error: "" })} /></label>}
    </fieldset>
    <p className="question-outcome answered-by" role="status"><AnswerMark who={who.mark} />{who.label}{who.at && " · " + age(who.at)}</p>
    {warning && <p className="change-warning">{warning}</p>}
    <div className="card-actions">
      <ClickFeedback text={feedback} />
      <button type="button" className="icon-button raised" aria-label="Close" data-tip="Close" onClick={onClose}><Icon name="close" /></button>
      <button className="primary send-decision" type="submit" disabled={!connected || !payload || isStanding || draft.sending}><Icon name="edit" />Change answer</button>
    </div>
  </form>;
}
