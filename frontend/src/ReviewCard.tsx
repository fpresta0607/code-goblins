import { useState, type ReactNode } from "react";
import type { Review, Snapshot } from "./types";
import { deliveryMark } from "./feedback";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { settledIcon, settledLabel, waitReason, waitsOnOverlord, waitTarget, type Item } from "./commandQueue";
import { personaFor } from "./workflow";
import type { Draft } from "./QuestionCard";

export const reviewImages = (review: Review) => Array.from({ length: review.image_count }, (_, n) => "/api/reviews/" + encodeURIComponent(review.id) + "/images/" + n);

// One review item: what to look at and who asks, its images and its page,
// then Clear to close it. A page the supervisor watches is answered on the
// page itself, so its preview is the one way in and the card has no text box;
// a goblin waiting on the Overlord is a status with no text box that says
// what it waits on and opens it (its page, question, file or link), with
// Dismiss beside it; anything else takes a written answer that goes to the
// asker once.
export function ReviewCard({ review, snapshot, connected, draft, onDraft, onSend, onClear, onOpen, onImage, pager }: {
  review: Review; snapshot: Snapshot; connected: boolean; draft: Draft;
  onDraft: (changes: Partial<Draft>) => void; onSend: () => void; onClear: () => void; onOpen: (key: string) => void; onImage: (index: number) => void; pager?: ReactNode;
}) {
  const [missing, setMissing] = useState<Set<string>>(new Set());
  const outcome = draft.submission ? snapshot.actions.find((action) => action.id === draft.submission?.id) || draft.receipt : undefined;
  const pending = review.state === "open" && !outcome;
  const task = snapshot.tasks.find((candidate) => candidate.id === review.task);
  const asker = review.task ? task?.title || review.task : "The CFO";
  const images = reviewImages(review);
  const mark = outcome ? deliveryMark(outcome) : undefined;
  const item: Item = { kind: "review", key: "review:" + review.id, review };
  const settled = settledIcon(item, snapshot.actions);
  const status = waitsOnOverlord(review);
  const answersHere = pending && !review.watched && !status;
  const target = status ? waitTarget(review, snapshot) : null;
  return <form className="question-card" aria-labelledby={"review-" + review.id} onSubmit={(event) => { event.preventDefault(); onSend(); }}>
    <p className="asker"><Avatar persona={review.task ? personaFor(task) : "cfo"} small /><span><strong>{asker}</strong> {status ? "is waiting on you" : "asks"} · waiting {age(review.created_at).replace(/ ago$/, "")}</span></p>
    <h3 id={"review-" + review.id} tabIndex={-1}>{status ? waitReason(review) : review.title}</h3>
    {target && <p className="wait-target">{target.says}</p>}
    {review.lavish && !status && <a className="page-preview" href={review.lavish} target="_blank" rel="noreferrer" aria-label={"Open review: " + review.title}>
      <span className="page-shot" aria-hidden="true"><Icon name="comment" /><strong>{review.title}</strong><span>Review page</span></span>
      <span className="open-overlay"><Icon name="external" />Open review</span>
    </a>}
    {review.lavish && pending && review.watched && <p className="review-status">Answer on the page itself; this card finishes when you send or end the review there.</p>}
    {images.length > 0 && <div className="question-thumbs" aria-label="Images to review">
      {images.map((src, index) => <button type="button" key={src} aria-label={"View image " + (index + 1) + " of " + images.length + " full size"} onClick={() => onImage(index)}>
        {missing.has(src) ? <span className="image-missing"><Icon name="images" /></span> : <img src={src} alt="" onError={() => setMissing((prior) => new Set([...prior, src]))} />}<span>{index + 1}</span>
      </button>)}
    </div>}
    {answersHere && <label className="written-answer"><span className="sr-only">Your answer</span><textarea rows={3} maxLength={4000} placeholder={"Tell " + (review.task ? asker : "the CFO") + " what you think..."} value={draft.written} disabled={draft.sending} onChange={(event) => onDraft({ written: event.target.value, error: "", receipt: undefined })} /></label>}
    {draft.error && !outcome && review.state === "open" && <p className="warning-text" role="alert">{draft.error} An unchanged retry keeps its request identity.</p>}
    {review.state !== "open" ? <p className={"question-outcome delivery " + settled.tone} role="status"><Icon name={settled.icon} />{settledLabel(item, snapshot.actions)}</p>
      : mark && <p className={"question-outcome delivery " + outcome?.status} role="status"><Icon name={mark.icon} />{mark.label}</p>}
    <div className="card-actions">
      {pager}
      {pending && !status && <button type="button" className="icon-button raised" disabled={!connected || draft.sending} aria-label="Clear this item without answering" data-tip="Clear" onClick={onClear}><Icon name="close" /></button>}
      {pending && status && <button type="button" className={target ? "status-dismiss" : "primary status-dismiss"} disabled={!connected || draft.sending} onClick={onClear}><Icon name="check" />Dismiss</button>}
      {pending && target?.kind === "item" && <button type="button" className="primary wait-open" onClick={() => onOpen(target.key)}><Icon name="next" />{target.label}</button>}
      {pending && target?.kind === "page" && <a className="primary button-link wait-open" href={target.url} target="_blank" rel="noreferrer"><Icon name="external" />{target.label}</a>}
      {answersHere && <button className="primary send-decision" type="submit" disabled={!connected || !draft.written.trim() || draft.sending}><Icon name={draft.sending ? "clock" : "send"} />{draft.sending ? "Sending" : draft.error ? "Retry" : "Send answer"}</button>}
    </div>
  </form>;
}
