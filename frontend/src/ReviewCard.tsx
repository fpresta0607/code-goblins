import { useState } from "react";
import type { Review, Snapshot } from "./types";
import { deliveryMark } from "./feedback";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { settledIcon, settledLabel, type Item } from "./commandQueue";
import { personaFor } from "./workflow";
import type { Draft } from "./QuestionCard";

export const reviewImages = (review: Review) => Array.from({ length: review.image_count }, (_, n) => "/api/reviews/" + encodeURIComponent(review.id) + "/images/" + n);

// One review item: what to look at and who asks, its images and Lavish page,
// then a written answer that goes to the asker once, or Clear to close it.
export function ReviewCard({ review, snapshot, connected, draft, onDraft, onSend, onClear, onImage }: {
  review: Review; snapshot: Snapshot; connected: boolean; draft: Draft;
  onDraft: (changes: Partial<Draft>) => void; onSend: () => void; onClear: () => void; onImage: (index: number) => void;
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
  return <form className="question-card" aria-labelledby={"review-" + review.id} onSubmit={(event) => { event.preventDefault(); onSend(); }}>
    <p className="asker"><Avatar persona={review.task ? personaFor(task) : "cfo"} small /><span><strong>{asker}</strong> asks · waiting {age(review.created_at).replace(/ ago$/, "")}</span></p>
    <h3 id={"review-" + review.id} tabIndex={-1}>{review.title}</h3>
    {images.length > 0 && <div className="question-thumbs" aria-label="Images to review">
      {images.map((src, index) => <button type="button" key={src} aria-label={"View image " + (index + 1) + " of " + images.length + " full size"} onClick={() => onImage(index)}>
        {missing.has(src) ? <span className="image-missing"><Icon name="images" /></span> : <img src={src} alt="" onError={() => setMissing((prior) => new Set([...prior, src]))} />}<span>{index + 1}</span>
      </button>)}
    </div>}
    {pending && <label className="written-answer"><span className="sr-only">Your answer</span><textarea rows={3} maxLength={4000} placeholder={"Tell " + (review.task ? asker : "the CFO") + " what you think..."} value={draft.written} disabled={draft.sending} onChange={(event) => onDraft({ written: event.target.value, error: "", receipt: undefined })} /></label>}
    {draft.error && !outcome && <p className="warning-text" role="alert">{draft.error} An unchanged retry keeps its request identity.</p>}
    {review.state !== "open" ? <p className={"question-outcome delivery " + settled.tone} role="status"><Icon name={settled.icon} />{settledLabel(item, snapshot.actions)}</p>
      : mark && <p className={"question-outcome delivery " + outcome?.status} role="status"><Icon name={mark.icon} />{mark.label}</p>}
    <div className="card-actions">
      {review.lavish && <a className="icon-button raised pill-link" href={review.lavish} target="_blank" rel="noreferrer" aria-label="Annotate in Lavish" data-tip="Annotate in Lavish"><Icon name="external" /><span>Lavish</span></a>}
      {pending && <button type="button" className="icon-button raised" disabled={!connected || draft.sending} aria-label="Clear this item without answering" data-tip="Clear" onClick={onClear}><Icon name="close" /></button>}
      {pending && <button className="primary send-decision" type="submit" disabled={!connected || !draft.written.trim() || draft.sending}><Icon name={draft.sending ? "clock" : "send"} />{draft.sending ? "Sending" : "Send answer"}</button>}
    </div>
  </form>;
}
