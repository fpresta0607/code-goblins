import { useState } from "react";
import type { Action, Snapshot } from "./types";
import { parseAction } from "./types";
import { message, request } from "./api";
import { alreadyKnown, submissionFor, type Submission } from "./feedback";

interface Recipient {
  kind: "feedback" | "cfo_message";
  generation: string;
  task_id?: string;
}
interface Draft {
  text: string;
  recipient: Recipient;
  submission: Submission | null;
  sending: boolean;
  error: string;
  receipt?: Action;
}

export function useMessages(snapshot: Snapshot | null) {
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const change = (key: string, fields: Partial<Draft>) => setDrafts((all) => all[key] ? { ...all, [key]: { ...all[key], ...fields } } : all);
  const payload = (draft: Draft) => JSON.stringify({ ...draft.recipient, text: draft.text });
  const outcome = (draft: Draft) => alreadyKnown(draft.submission, payload(draft), snapshot?.actions || [])
    || (draft.submission?.payload === payload(draft) ? draft.receipt : undefined);
  const edit = (key: string, recipient: Recipient, text: string) => { if (!recipient.generation) return; setDrafts((all) => ({ ...all, [key]: {
    ...all[key], recipient: all[key]?.recipient || recipient, text, error: "",
    sending: all[key]?.sending || false, submission: all[key]?.submission || null,
  } })); };
  const reset = (key: string, recipient: Recipient) => setDrafts((all) => ({ ...all, [key]: { text: "", recipient, sending: false, error: "", submission: null } }));
  const send = async (key: string, recipient: Recipient) => {
    const draft = drafts[key];
    if (!snapshot || !recipient.generation || !draft || draft.sending || !draft.text.trim() || outcome(draft)) return;
    if (draft.recipient.generation !== recipient.generation) {
      change(key, { error: "The recipient changed. Start a new message for the current session." });
      return;
    }
    const attempt = submissionFor(payload(draft), draft.submission, () => crypto.randomUUID());
    change(key, { submission: attempt, sending: true, error: "", receipt: undefined });
    try {
      const receipt = parseAction(await request("/api/actions", undefined, {
        method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance },
        body: JSON.stringify({ id: attempt.id, ...draft.recipient, text: draft.text }),
      }));
      change(key, { receipt, sending: false });
    } catch (error: unknown) { change(key, { error: message(error), sending: false }); }
  };
  return { drafts, edit, reset, send, outcome };
}

export type MessageControls = ReturnType<typeof useMessages>;

export function MessageComposer({ messages, recipient, channel, connected, disabled = false }: {
  messages: MessageControls; recipient: Recipient; channel: string; connected: boolean; disabled?: boolean;
}) {
  const draft = messages.drafts[channel];
  const action = draft && messages.outcome(draft);
  const stale = !!draft && !!recipient.generation && draft.recipient.generation !== recipient.generation;
  const cfo = recipient.kind === "cfo_message";
  const label = cfo ? "Message to CFO" : "Instruction to task agent";
  return <section className="message-composer" aria-label={label}>
    <div className="composer-row">
      <textarea rows={2} aria-label={label} placeholder={cfo ? "Ask the CFO…" : "Send an instruction…"} maxLength={16000}
        disabled={draft?.sending || !recipient.generation} value={draft?.text || ""} onChange={(event) => messages.edit(channel, recipient, event.target.value)} />
      <button className="primary" aria-label={cfo ? "Send to CFO" : "Send instruction"} disabled={!connected || !recipient.generation || disabled || draft?.sending || !draft?.text.trim() || !!action || stale}
        onClick={() => { void messages.send(channel, recipient); }}>
        <svg width="22" height="22" viewBox="0 0 24 24" aria-hidden="true"><path d="m21 3-7 18-4-7-7-4 18-7Zm-11 11 11-11" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg>
      </button>
    </div>
    {!cfo && <p className="composer-note">Verified submission. Pipeline custody is checked before delivery.</p>}
    {stale && <p className="warning-text" role="alert">{cfo ? "The primary CFO changed." : "This task restarted or was replaced."} This draft belongs to its previous session.</p>}
    {draft?.error && !action && <p className="error-box" role="alert">{draft.error} An unchanged retry retains its request ID.</p>}
    {action && <p className={"action-result " + action.status} role="status">{action.status}: {action.message || "Queued for delivery."}</p>}
    {(action || stale) && <button className="new-instruction" disabled={draft?.sending || !recipient.generation} onClick={() => messages.reset(channel, recipient)}>Start a new message</button>}
  </section>;
}
