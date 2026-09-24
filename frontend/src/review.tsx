import { useState } from "react";
import { message, request } from "./api";
import { alreadyKnown, deliveryMark, submissionFor, type Submission } from "./feedback";
import { Icon } from "./Icon";
import { parseAction, type Action, type FileDiff, type ReviewSelection, type Snapshot, type Task } from "./types";
import { parsePatchToRows, reviewRange } from "./diff";

interface ReviewDraft {
  selection: ReviewSelection;
  anchor: number;
  text: string;
  submission: Submission | null;
  sending: boolean;
  hidden: boolean;
  error: string;
  receipt?: Action;
}
// App owns drafts, keyed by task and revision. Recipient changes and view/pane
// toggles cannot discard an in-flight request or retarget a previous selection.
export function useReview(task: Task | undefined, snapshot: Snapshot | null) {
  const [drafts, setDrafts] = useState<Record<string, ReviewDraft>>({});
  const keyFor = (diff: FileDiff) => JSON.stringify([task?.id, diff.revision, diff.path]);
  const change = (key: string, fields: Partial<ReviewDraft>) => setDrafts((all) => all[key] ? { ...all, [key]: { ...all[key], ...fields } } : all);
  const payload = (draft: ReviewDraft) => JSON.stringify({ kind: "review", text: draft.text, ...draft.selection });
  const outcome = (draft: ReviewDraft) => alreadyKnown(draft.submission, payload(draft), snapshot?.actions || [])
    || (draft.submission?.payload === payload(draft) ? draft.receipt : undefined);
  const select = (diff: FileDiff, line: number, side: "old" | "new", extend: boolean) => {
    const key = keyFor(diff);
    setDrafts((all) => {
      const prior = all[key];
      if (prior?.sending || !task) return all;
      const anchor = extend && prior && prior.selection.generation === task.generation && prior.selection.side === side && prior.selection.diff_id === diff.fingerprint && prior.selection.head === diff.head ? prior.anchor : line;
      return { ...all, [key]: {
        text: prior?.text || "", submission: prior?.submission || null, receipt: prior?.receipt,
        anchor, sending: false, hidden: false, error: "",
        selection: { task_id: task.id, generation: task.generation, file: diff.path, line: Math.min(anchor, line), end_line: Math.max(anchor, line), side, head: diff.head, revision: diff.revision, diff_id: diff.fingerprint },
      } };
    });
  };
  const send = async (diff: FileDiff) => {
    const key = keyFor(diff), draft = drafts[key];
    if (!snapshot || !task || !draft || draft.sending || !draft.text.trim() || outcome(draft)) return;
    if (draft.selection.generation !== task.generation) {
      change(key, { error: "This task restarted or was replaced. Reselect the current lines for its new session." });
      return;
    }
    const attempt = submissionFor(payload(draft), draft.submission, () => crypto.randomUUID());
    change(key, { submission: attempt, sending: true, error: "", receipt: undefined });
    try {
      const receipt = parseAction(await request("/api/actions", undefined, {
        method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance },
        body: JSON.stringify({ id: attempt.id, kind: "review", text: draft.text, ...draft.selection }),
      }));
      change(key, { receipt, sending: false });
    } catch (error: unknown) { change(key, { error: message(error), sending: false }); }
  };
  return { drafts, keyFor, change, select, send, outcome, generation: task?.generation };
}

export type ReviewControls = ReturnType<typeof useReview>;

// The comment floats beside the selected lines instead of pushing the diff
// apart. Enter sends, Shift+Enter breaks a line, Escape cancels; once sent it
// shrinks to a chip whose check marks show delivery.
export function ReviewComment({ diff, reviews, connected, floating }: { diff: FileDiff; reviews: ReviewControls; connected: boolean; floating: boolean }) {
  const key = reviews.keyFor(diff), draft = reviews.drafts[key];
  if (!draft || draft.hidden) return null;
  const action = reviews.outcome(draft);
  const selection = draft.selection;
  const range = (selection.side === "old" ? "Old" : "New") + " " + (selection.line === selection.end_line ? "line " + selection.line : "lines " + selection.line + "–" + selection.end_line);
  const valid = reviewRange(parsePatchToRows(diff.patch), selection.line, selection.end_line, selection.side);
  const stale = selection.head !== diff.head || selection.diff_id !== diff.fingerprint;
  const staleGeneration = selection.generation !== reviews.generation;
  const blocked = !connected || draft.sending || !draft.text.trim() || !!action || !valid || stale || staleGeneration;
  const cancel = () => { if (!draft.sending) reviews.change(key, { hidden: true }); };
  const kind = "comment-overlay" + (floating ? " floating" : "");
  if (action) {
    const mark = deliveryMark(action);
    return <div className={kind + " sent"} role="status" aria-label={"Comment on " + range + ". " + mark.label}>
      <span className={"delivery " + action.status} aria-hidden="true"><Icon name={mark.icon} /></span>
      <p className="comment-sent-text"><strong>{range}</strong> {draft.text}</p>
      <button className="icon-button" aria-label="New comment on these lines" data-tip="New comment" data-tip-align="end" onClick={() => reviews.change(key, { text: "", submission: null, receipt: undefined, error: "" })}><Icon name="comment" /></button>
      <button className="icon-button" aria-label="Close" data-tip="Close" data-tip-align="end" onClick={cancel}><Icon name="close" /></button>
      {mark.trouble && <p className="warning-text">{mark.label}{action.message && " " + action.message}</p>}
    </div>;
  }
  return <section className={kind} aria-label={"Comment to the CFO on " + range + " of " + diff.path}>
    <header><strong>{range}</strong><span className="muted">{diff.path} · HEAD {selection.head.slice(0, 8)}{selection.revision && " · Commit " + selection.revision.slice(0, 8)}</span></header>
    <textarea aria-label={"Comment to the CFO on " + range} rows={3} maxLength={16000} value={draft.text} disabled={draft.sending}
      placeholder="Describe the change you want..." onChange={(event) => reviews.change(key, { text: event.target.value, error: "" })}
      onKeyDown={(event) => {
        if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); cancel(); }
        if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); if (!blocked) void reviews.send(diff); }
      }} />
    {!valid && <p className="warning-text">Select up to 200 contiguous visible diff lines.</p>}
    {stale && <p className="warning-text">This diff changed. Select the current lines before sending.</p>}
    {staleGeneration && <p className="warning-text" role="alert">This task restarted or was replaced. This comment belongs to its previous session. Reselect the current lines to review the new session.</p>}
    {draft.error && <p className="warning-text" role="alert">{draft.error} An unchanged retry keeps its request ID.</p>}
    <footer>
      <span className="muted">Enter sends · Shift+Enter new line · Esc cancels</span>
      <button className="icon-button send" disabled={blocked} aria-label="Send to the CFO" data-tip="Send to the CFO" data-tip-align="end" onClick={() => void reviews.send(diff)}><Icon name={draft.sending ? "clock" : "send"} /></button>
      <button className="icon-button" disabled={draft.sending} aria-label="Cancel comment" data-tip="Cancel" data-tip-align="end" onClick={cancel}><Icon name="close" /></button>
    </footer>
  </section>;
}
