import { useState } from "react";
import type { Action, BoardActivity, Question, Snapshot } from "./types";
import { deliveryMark, type Submission } from "./feedback";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { answeredBy, chosenOption } from "./commandQueue";
import { questionAnswer, questionChoices, questionSelection } from "./questionChoices";
import { personaFor } from "./workflow";

export interface Draft { selection: string; written: string; submission: Submission | null; sending: boolean; error: string; receipt?: Action }
export const EMPTY_DRAFT: Draft = { selection: "", written: "", submission: null, sending: false, error: "" };

// One question: who asks and how long they have waited, the choices with the
// recommendation first, Other for a written answer, and its own Send.
export function QuestionCard({ question, snapshot, connected, draft, review, onDraft, onSend, onImage }: {
  question: Question; snapshot: Snapshot; connected: boolean; draft: Draft; review?: BoardActivity;
  onDraft: (changes: Partial<Draft>) => void; onSend: () => void; onImage: (index: number) => void;
}) {
  const outcome = draft.submission ? snapshot.actions.find((action) => action.id === draft.submission?.id) || draft.receipt : undefined;
  const pending = question.status === "pending" && !outcome;
  // A closed question shows what was chosen, not radios to choose again.
  const closed = question.status !== "pending" && !outcome && !!question.answer_id;
  const chosen = chosenOption(question);
  const displayed = questionSelection(question, draft, outcome);
  const task = snapshot.tasks.find((candidate) => candidate.id === question.task);
  const asker = question.task ? task?.title || question.task : "The CFO";
  const payload = questionAnswer(question, draft.selection, draft.written);
  const choices = questionChoices(question);
  const images = choices.filter((choice) => choice.image);
  const mark = outcome ? deliveryMark(outcome) : undefined;
  // An image can vanish with its goblin's worktree; show that, not a broken image.
  const [missing, setMissing] = useState<Set<string>>(new Set());
  return <form className="question-card" aria-labelledby={"question-" + question.id} onSubmit={(event) => { event.preventDefault(); onSend(); }}>
    <p className="asker"><Avatar persona={question.task ? personaFor(task) : "cfo"} small /><span><strong>{asker}</strong> asks · {closed ? "asked " + age(question.created_at) : "waiting " + age(question.created_at).replace(/ ago$/, "")}</span></p>
    <h3 id={"question-" + question.id} tabIndex={-1}>{question.text}</h3>
    {images.length > 0 && <div className="question-thumbs" aria-label="Images for this question">
      {images.map((choice, index) => <button type="button" key={choice.value} aria-label={"View image for option " + choice.label + " full size"} onClick={() => onImage(index)}>
        {missing.has(choice.image) ? <span className="image-missing"><Icon name="images" /></span> : <img src={choice.image} alt="" onError={() => setMissing((prior) => new Set([...prior, choice.image]))} />}<span>{choice.label}</span>
      </button>)}
    </div>}
    <fieldset disabled={!pending || draft.sending}><legend className="sr-only">Choose your answer</legend>
      {choices.map((option) => <label className={"question-choice" + (closed ? (chosen === option.value ? " chosen" : " dimmed") : "")} key={option.value}>
        {closed ? <span className="choice-mark">{chosen === option.value && <Icon name="check" />}</span>
          : <input type="radio" name={"answer-" + question.id} checked={displayed.selection === "option:" + option.value} onChange={() => onDraft({ selection: "option:" + option.value, error: "", receipt: undefined })} />}
        <span className="question-option"><span>{option.label}. {option.value}</span>{option.recommended && <span className="recommendation">Recommended</span>}</span>
      </label>)}
      {closed ? question.answer_kind === "other" && <label className="question-choice chosen"><span className="choice-mark"><Icon name="check" /></span><span className="question-option"><span>Other</span><small>{question.answer}</small></span></label>
        : <label className="question-choice"><input type="radio" name={"answer-" + question.id} checked={displayed.selection === "other"} onChange={() => onDraft({ selection: "other", error: "", receipt: undefined })} /><span className="question-option"><span>Other</span><small>Write your own answer.</small></span></label>}
      {displayed.selection === "other" && <label className="written-answer"><span className="sr-only">Your written answer</span><textarea rows={3} maxLength={4000} placeholder={"Tell " + (question.task ? asker : "the CFO") + " what you prefer..."} value={displayed.written} onChange={(event) => onDraft({ written: event.target.value, error: "", receipt: undefined })} /></label>}
    </fieldset>
    {draft.error && !outcome && <p className="warning-text" role="alert">{draft.error} An unchanged retry keeps its request identity.</p>}
    {mark ? <p className={"question-outcome delivery " + outcome?.status} role="status"><Icon name={mark.icon} />{mark.label}</p>
      : question.status === "superseded" ? <p className="question-outcome" role="status">Superseded; the asker was replaced</p>
        : closed && <p className="question-outcome answered-by" role="status">{answeredBy(question)}{question.answered_at && " · " + age(question.answered_at)}</p>}
    <div className="card-actions">
      {review && <a className="icon-button raised pill-link" href={review.url} target="_blank" rel="noreferrer" aria-label="Annotate in Lavish" data-tip="Annotate in Lavish"><Icon name="external" /><span>Lavish</span></a>}
      {pending && <button className="primary send-decision" type="submit" disabled={!connected || !payload || draft.sending}><Icon name={draft.sending ? "clock" : "send"} />{draft.sending ? "Sending" : "Send decision"}</button>}
    </div>
  </form>;
}
