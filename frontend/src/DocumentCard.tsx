import type { ReactNode } from "react";
import type { Review, ReviewDocument, Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { documentFacts, settledIcon, settledLabel, type Item } from "./commandQueue";
import { personaFor } from "./workflow";

// One delivered document: who sent it, its file icon and facts, and Open and
// Download. Open shows only when the browser can open the copy or there is a
// link, so a download never reads Opened. Either takes it out of the queue into
// the history; Clear closes it unopened.
export function DocumentCard({ review, document, snapshot, connected, onOpened, onClear, pager }: {
  review: Review; document: ReviewDocument; snapshot: Snapshot; connected: boolean;
  onOpened: (how: "Opened" | "Downloaded") => void; onClear: () => void; pager?: ReactNode;
}) {
  const task = snapshot.tasks.find((candidate) => candidate.id === review.task);
  const sender = review.task ? task?.title || review.task : "the CFO";
  const file = "/api/reviews/" + encodeURIComponent(review.id) + "/document";
  const item: Item = { kind: "review", key: "review:" + review.id, review };
  const settled = settledIcon(item, snapshot.actions);
  const open = review.state === "open";
  const dot = document.name.lastIndexOf(".");
  const badge = dot > 0 ? document.name.slice(dot + 1, dot + 5).toUpperCase() : "FILE";
  const opened = (how: "Opened" | "Downloaded") => { if (open && connected) onOpened(how); };
  return <div className="question-card document-card">
    <p className="asker"><Avatar persona={review.task ? personaFor(task) : "cfo"} small /><span><strong>{review.task ? sender : "The CFO"}</strong> sent a document · waiting {age(review.created_at).replace(/ ago$/, "")}</span></p>
    <h3 id={"review-" + review.id} tabIndex={-1}>{review.title}</h3>
    <div className="doc-row">
      <span className="file-icon" aria-hidden="true"><Icon name="file" /><b>{badge}</b></span>
      <span className="doc-copy"><strong>{document.name}</strong><span>{documentFacts(document, sender)}</span></span>
    </div>
    {!open && <p className={"question-outcome delivery " + settled.tone} role="status"><Icon name={settled.icon} />{settledLabel(item, snapshot.actions)}</p>}
    <div className="card-actions">
      {pager}
      {open && <button type="button" className="icon-button raised" disabled={!connected} aria-label="Clear this document without opening it" data-tip="Clear" onClick={onClear}><Icon name="close" /></button>}
      {(document.link || document.kind) && <a className="icon-button raised pill-link" href={document.link || file} target="_blank" rel="noreferrer" onClick={() => opened("Opened")}><Icon name="external" /><span>Open</span></a>}
      <a className="primary download-link" href={file + "?download=1"} download={document.name} onClick={() => opened("Downloaded")}><Icon name="download" /><span>Download</span></a>
    </div>
  </div>;
}
