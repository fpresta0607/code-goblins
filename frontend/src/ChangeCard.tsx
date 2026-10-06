import type { Question, Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { canChange, chosenOption } from "./commandQueue";
import { questionAnswer, questionChoices } from "./questionChoices";
import { messageElements } from "./messageText";
import { copyValues } from "./CopyValue";
import { personaFor } from "./workflow";
import type { Draft } from "./QuestionCard";

// A goblin's question the CFO answered, opened so the Overlord can change the
// answer to his own: its choices with the CFO's marked, and Change to my
// answer while the goblin has reported nothing since. Once the goblin has
// moved on it says the CFO's answer stands.
export function ChangeCard({ question, snapshot, connected, draft, onDraft, onChange, onClose }: {
  question: Question; snapshot: Snapshot; connected: boolean; draft: Draft;
  onDraft: (changes: Partial<Draft>) => void; onChange: () => void; onClose: () => void;
}) {
  const task = snapshot.tasks.find((candidate) => candidate.id === question.task);
  const asker = task?.title || question.task;
  const cfoChoice = chosenOption(question);
  const outcome = draft.submission ? snapshot.actions.find((action) => action.id === draft.submission?.id) || draft.receipt : undefined;
  const isChangeable = canChange(question, snapshot);
  const isFailed = outcome ? outcome.status === "failed" || outcome.status === "uncertain" : !!draft.error;
  const payload = questionAnswer(question, draft.selection, draft.written);
  const isCFOChoice = payload?.answer_kind === "option" && payload.text === cfoChoice;
  const isGone = !task || task.generation !== question.generation;
  const status = !isChangeable && !draft.sending
    ? isGone ? "The goblin that asked has restarted or ended, so the CFO's answer stands." : asker + " has reported since the CFO answered, so the CFO's answer stands."
    : isFailed ? "Your change did not reach " + asker + "; the CFO's answer stands."
    : asker + " has not reported since the CFO answered, so your answer replaces the CFO's.";
  return <form className="question-card change-card" aria-labelledby={"change-" + question.id} onSubmit={(event) => { event.preventDefault(); onChange(); }}>
    <p className="asker"><Avatar persona={personaFor(task)} small /><span><strong>{asker}</strong> asks · answered by the CFO {age(question.answered_at)}</span></p>
    <div className="question-body" id={"change-" + question.id} tabIndex={-1}>{messageElements(question.text, copyValues)}</div>
    <fieldset disabled={!isChangeable || draft.sending}><legend className="sr-only">Choose your answer</legend>
      {questionChoices(question).map((option) => <label className="question-choice" key={option.value}>
        <input type="radio" name={"change-" + question.id} checked={draft.selection === "option:" + option.value} onChange={() => onDraft({ selection: "option:" + option.value, error: "", receipt: undefined })} />
        <span className="question-option"><span>{option.text}</span>{option.value === cfoChoice && <span className="cfo-answer">The CFO's answer</span>}</span>
      </label>)}
      <label className="question-choice"><input type="radio" name={"change-" + question.id} checked={draft.selection === "other"} onChange={() => onDraft({ selection: "other", error: "", receipt: undefined })} /><span className="question-option"><span>Other</span><small>Write your own answer.</small></span></label>
      {draft.selection === "other" && <label className="written-answer"><span className="sr-only">Your written answer</span><textarea rows={3} maxLength={4000} placeholder={"Tell " + asker + " what you prefer..."} value={draft.written} onChange={(event) => onDraft({ written: event.target.value, error: "", receipt: undefined })} /></label>}
    </fieldset>
    <p className="question-outcome answered-by" role="status"><Icon name={isChangeable && !isFailed ? "clock" : "check"} />{status}</p>
    <div className="card-actions">
      <button type="button" className="icon-button raised" aria-label="Close" data-tip="Close" data-tip-align="end" onClick={onClose}><Icon name="close" /></button>
      {isChangeable && <button className="primary send-decision" type="submit" disabled={!connected || !payload || isCFOChoice || draft.sending}><Icon name={draft.sending ? "clock" : "edit"} />{draft.sending ? "Changing" : "Change to my answer"}</button>}
    </div>
  </form>;
}
