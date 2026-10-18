import { scrawlLink } from "./scrawl-link";
import { useEffect, useState, type ReactNode } from "react";
import type { Review, Snapshot } from "./types";
import { deliveryMark } from "./feedback";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { PagePreview } from "./page-preview";
import { age } from "./presentation";
import { reviewLine, settledIcon, settledLabel, waitReason, waitsOnOverlord, waitTarget, type Item } from "./commandQueue";
import { personaFor } from "./workflow";
import { inlineElements, leadAndRest, messageElements } from "./messageText";
import { copyValues } from "./CopyValue";
import type { Draft } from "./QuestionCard";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { goblinName } from "./task-words";
import { DictatedAnswer } from "./dictated-answer";
import { SkipWithNote } from "./skip-with-note";
import { pathLinks } from "./path-link";

export const reviewImages = (review: Review) => Array.from({ length: review.image_count }, (_, n) => "/api/reviews/" + encodeURIComponent(review.id) + "/images/" + n);

// One review item, saying each thing once: who asks by the goblin's name and
// title, what to look at, its images and its page. A Scrawl page is answered
// on the page, so its card has Skip and one button that opens it, and no text
// box at all (the Overlord, 2026-10-17: "There does not need to be a text box
// in when there is a scroll open review page involved"); one the supervisor
// watches also shows itself, live and scaled down. A goblin waiting on the
// Overlord is a status that opens what it waits on (its page, file or link)
// from its one action button. Without a page it has Skip beside its
// button, telling the goblin to go on without it, and the pencil, which sends
// his answer in words to the asker once. Anything else takes a written answer
// that goes to the asker once, with Skip beside its Send.
export function ReviewCard({ review, snapshot, connected, draft, onDraft, onSend, onClear, onAnswer, onOpen, onImage, pager }: {
  review: Review; snapshot: Snapshot; connected: boolean; draft: Draft;
  onDraft: (changes: Partial<Draft>) => void; onSend: () => void; onClear: () => void; onAnswer: (text: string) => void; onOpen: (key: string) => void; onImage: (index: number) => void; pager?: ReactNode;
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
  // A card with its own answer box has Skip beside its Send; a wait without a
  // page has Skip and the pencil, which answers in words; a page has Skip
  // alone.
  const hasBox = !review.lavish && !status;
  const hasPencil = !review.lavish && !!status;
  const answersHere = pending && hasBox;
  const target = status ? waitTarget(review, snapshot) : null;
  // The goblin's own words, formatted: its lead line heads the card, and the
  // details, a table of values to enter, or a closing line follow it. Words
  // that open with a table have no lead line and are all body.
  const { lead, rest } = leadAndRest(status ? waitReason(review) : review.title);
  // Each local path its words name opens in File Explorer.
  const paths = pathLinks({ kind: "review", id: review.id, instance: snapshot.instance });
  return <form className="question-card" aria-labelledby={"review-" + review.id} onSubmit={(event) => { event.preventDefault(); onSend(); }}>
    <p className="asker"><Avatar persona={review.task ? personaFor(task) : "cfo"} small /><span><strong>{asker}</strong> {status ? "is waiting on you" : "asks"} · waiting {age(review.created_at).replace(/ ago$/, "")}</span></p>
    {!lead ? <div className="question-body" id={"review-" + review.id} tabIndex={-1}>{messageElements(rest, copyValues, paths)}</div>
      : <h3 id={"review-" + review.id} tabIndex={-1}>{inlineElements(lead, copyValues, paths)}</h3>}
    {lead && rest && <div className="question-body">{messageElements(rest, copyValues, paths)}</div>}
    {review.lavish && review.watched && <PagePreview src={"/api/reviews/" + encodeURIComponent(review.id) + "/preview"} />}
    {images.length > 0 && <div className="question-thumbs" aria-label="Images to review">
      {images.map((src, index) => <button type="button" key={src} aria-label={"View image " + (index + 1) + " of " + images.length + " full size"} onClick={() => onImage(index)}>
        {missing.has(src) ? <span className="image-missing"><Icon name="images" /></span> : <img src={src} alt="" onError={() => setMissing((prior) => new Set([...prior, src]))} />}<span>{index + 1}</span>
      </button>)}
    </div>}
    {answersHere && <DictatedAnswer label="Your answer" placeholder={"Tell " + (review.task ? asker : "the CFO") + " what you think..."} value={draft.written} instance={snapshot.instance} disabled={draft.sending} onText={(written) => onDraft({ written, error: "", receipt: undefined })} />}
    {review.state !== "open" ? <p className={"question-outcome delivery " + settled.tone} role="status"><Icon name={settled.icon} />{settledLabel(item, snapshot.actions)}</p>
      : mark && <p className={"question-outcome delivery " + outcome?.status} role="status"><Icon name={mark.icon} />{mark.label}</p>}
    <div className="card-actions">
      <ClickFeedback text={feedback} />
      {pager}
      {pending && !hasPencil && <button type="button" className="icon-button raised" disabled={!connected || draft.sending} aria-label="Skip this card" data-tip={status ? "Skip: " + asker + " goes on without it" : "Clear it without answering"} onClick={onClear}><Icon name="close" /></button>}
      {pending && hasPencil && <SkipWithNote asker={review.task ? asker : "the CFO"} task={review.task} instance={snapshot.instance} disabled={!connected || draft.sending} onSkip={onClear} onAnswer={onAnswer} />}
      {pending && target?.kind === "item" && <button type="button" className="primary wait-open" onClick={() => onOpen(target.key)}><Icon name="next" />{target.label}</button>}
      {pending && target?.kind === "page" && <a className="primary button-link wait-open" href={scrawlLink(target.url)} target="_blank" rel="noreferrer"><Icon name="external" />{target.label}</a>}
      {review.lavish && !status && <a className="primary button-link open-page" href={scrawlLink(review.lavish)} target="_blank" rel="noreferrer" aria-label={"Open review: " + reviewLine(review)}><Icon name="external" />Open review</a>}
      {answersHere && <button className="primary send-decision" type="submit" disabled={!connected || !draft.written.trim() || draft.sending}><Icon name={draft.sending ? "clock" : "send"} />{draft.sending ? "Sending" : draft.error ? "Retry" : "Send answer"}</button>}
    </div>
  </form>;
}
