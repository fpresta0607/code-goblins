import { useEffect, useState, type ReactNode } from "react";
import type { Review, Snapshot } from "./types";
import { deliveryMark } from "./feedback";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { reviewLine, settledIcon, settledLabel, waitReason, waitsOnOverlord, waitTarget, type Item } from "./commandQueue";
import { personaFor } from "./workflow";
import { inlineElements, leadAndRest, messageElements } from "./messageText";
import { copyValues } from "./CopyValue";
import type { Draft } from "./QuestionCard";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { goblinName } from "./task-words";

export const reviewImages = (review: Review) => Array.from({ length: review.image_count }, (_, n) => "/api/reviews/" + encodeURIComponent(review.id) + "/images/" + n);

// One review item, saying each thing once: who asks by the goblin's name and
// title, what to look at, its images and its page, then Clear to close it. A
// page the supervisor watches shows itself, live and scaled down, and is
// answered on the page, so the card has one button that opens it, no text box
// and no line that explains. A goblin waiting on the Overlord is a status with
// no text box that opens what it waits on (its page, question, file or link)
// from its one action button, with Dismiss beside it. Anything else takes a
// written answer that goes to the asker once.
export function ReviewCard({ review, snapshot, connected, draft, onDraft, onSend, onClear, onOpen, onImage, pager }: {
  review: Review; snapshot: Snapshot; connected: boolean; draft: Draft;
  onDraft: (changes: Partial<Draft>) => void; onSend: () => void; onClear: () => void; onOpen: (key: string) => void; onImage: (index: number) => void; pager?: ReactNode;
}) {
  const [feedback, showFeedback] = useClickFeedback();
  useEffect(() => { if (draft.error) showFeedback(draft.error); }, [draft.error, showFeedback]);
  const [missing, setMissing] = useState<Set<string>>(new Set());
  const outcome = draft.submission ? snapshot.actions.find((action) => action.id === draft.submission?.id) || draft.receipt : undefined;
  const pending = review.state === "open" && !outcome;
  const task = snapshot.tasks.find((candidate) => candidate.id === review.task);
  const asker = review.task ? task ? goblinName(task) : review.task : "The CFO";
  const images = reviewImages(review);
  const mark = outcome ? deliveryMark(outcome) : undefined;
  const item: Item = { kind: "review", key: "review:" + review.id, review };
  const settled = settledIcon(item, snapshot.actions);
  const status = waitsOnOverlord(review);
  const answersHere = pending && !review.watched && !status;
  const target = status ? waitTarget(review, snapshot) : null;
  // The goblin's own words, formatted: its lead line heads the card, and the
  // details, a table of values to enter, or a closing line follow it. Words
  // that open with a table have no lead line and are all body.
  const { lead, rest } = leadAndRest(status ? waitReason(review) : review.title);
  return <form className="question-card" aria-labelledby={"review-" + review.id} onSubmit={(event) => { event.preventDefault(); onSend(); }}>
    <p className="asker"><Avatar persona={review.task ? personaFor(task) : "cfo"} small /><span><strong>{asker}</strong> {status ? "is waiting on you" : "asks"} · waiting {age(review.created_at).replace(/ ago$/, "")}</span></p>
    {!lead ? <div className="question-body" id={"review-" + review.id} tabIndex={-1}>{messageElements(rest, copyValues)}</div>
      : <h3 id={"review-" + review.id} tabIndex={-1}>{inlineElements(lead, copyValues)}</h3>}
    {lead && rest && <div className="question-body">{messageElements(rest, copyValues)}</div>}
    {/* The page itself, live and scaled down, from its own file: a preview
        to look at, never a review window of its own. */}
    {review.lavish && review.watched && <div className="page-preview" aria-hidden="true">
      <iframe src={"/api/reviews/" + encodeURIComponent(review.id) + "/preview"} title="" tabIndex={-1} sandbox="allow-scripts" scrolling="no" loading="lazy" />
    </div>}
    {images.length > 0 && <div className="question-thumbs" aria-label="Images to review">
      {images.map((src, index) => <button type="button" key={src} aria-label={"View image " + (index + 1) + " of " + images.length + " full size"} onClick={() => onImage(index)}>
        {missing.has(src) ? <span className="image-missing"><Icon name="images" /></span> : <img src={src} alt="" onError={() => setMissing((prior) => new Set([...prior, src]))} />}<span>{index + 1}</span>
      </button>)}
    </div>}
    {answersHere && <label className="written-answer"><span className="sr-only">Your answer</span><textarea rows={3} maxLength={4000} placeholder={"Tell " + (review.task ? asker : "the CFO") + " what you think..."} value={draft.written} disabled={draft.sending} onChange={(event) => onDraft({ written: event.target.value, error: "", receipt: undefined })} /></label>}
    {review.state !== "open" ? <p className={"question-outcome delivery " + settled.tone} role="status"><Icon name={settled.icon} />{settledLabel(item, snapshot.actions)}</p>
      : mark && <p className={"question-outcome delivery " + outcome?.status} role="status"><Icon name={mark.icon} />{mark.label}</p>}
    <div className="card-actions">
      <ClickFeedback text={feedback} />
      {pager}
      {pending && !status && <button type="button" className="icon-button raised" disabled={!connected || draft.sending} aria-label="Clear this item without answering" data-tip="Clear" onClick={onClear}><Icon name="close" /></button>}
      {pending && status && <button type="button" className={target ? "status-dismiss" : "primary status-dismiss"} disabled={!connected || draft.sending} onClick={onClear}><Icon name="check" />Dismiss</button>}
      {pending && target?.kind === "item" && <button type="button" className="primary wait-open" onClick={() => onOpen(target.key)}><Icon name="next" />{target.label}</button>}
      {pending && target?.kind === "page" && <a className="primary button-link wait-open" href={target.url} target="_blank" rel="noreferrer"><Icon name="external" />{target.label}</a>}
      {review.lavish && !status && <a className="primary button-link open-page" href={review.lavish} target="_blank" rel="noreferrer" aria-label={"Open review: " + reviewLine(review)}><Icon name="external" />Open review</a>}
      {answersHere && <button className="primary send-decision" type="submit" disabled={!connected || !draft.written.trim() || draft.sending}><Icon name={draft.sending ? "clock" : "send"} />{draft.sending ? "Sending" : draft.error ? "Retry" : "Send answer"}</button>}
    </div>
  </form>;
}
