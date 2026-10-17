import { useRef, useState } from "react";
import { message, request as call } from "./api";
import { type CredentialRequest } from "./types";
import { AnswerWindow } from "./answer-window";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { Icon } from "./Icon";
import { inputLabel, linkLabel, stepText } from "./credentials";

// The steps of a card whose values are stored, as its History row unfolds
// them: the page and the asker's steps, and each input by its label, which
// says it is already stored, or sent for a plain value, and never shows a
// value. Nothing here stores, replaces or deletes one. Something else? still
// reaches the asker as his word, as a message about the card.
export function HistoryStoredCard({ request, asker, to, connected, instance }: { request: CredentialRequest; asker: string; to: string; connected: boolean; instance: string }) {
  const [isWriting, setIsWriting] = useState(false);
  const [isSending, setIsSending] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  // A card whose goblin has ended goes to the CFO, named by its task.
  const recipient = to ? asker : "the CFO";
  // One ID for each note, kept across a retry of the same words, so the
  // board queues it once.
  const sent = useRef({ text: "", id: "" });
  const send = async (note: string) => {
    setIsWriting(false); setIsSending(true); showFeedback("");
    const text = (to || !request.task ? "About your card " + request.id : "About the card " + request.id + " of task " + request.task) + " (" + request.why + "), whose values are stored: " + note;
    if (sent.current.text !== text) sent.current = { text, id: crypto.randomUUID() };
    try {
      await call("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance },
        body: JSON.stringify({ id: sent.current.id, kind: "message", task_id: to, generation: "", text }) });
      sent.current = { text: "", id: "" };
      showFeedback("Sent to " + recipient);
    } catch (failure: unknown) { showFeedback(message(failure)); }
    finally { setIsSending(false); }
  };
  const inputs = [...request.names, ...request.plain];
  return <div className="history-stored">
    {(request.link || request.steps.length > 0) && <ol className="credential-steps" aria-label={inputs.length ? "How to get them" : "What to do"}>
      {request.link && <li>Open <a href={request.link} target="_blank" rel="noreferrer"><span>{linkLabel(request.link)}</span><Icon name="external" /></a></li>}
      {request.steps.map((step, index) => <li key={index}>{stepText(step)}</li>)}
    </ol>}
    {inputs.length > 0 && <ul className="history-stored-values" aria-label="Its values">
      {request.names.map((name) => <li key={name}><span>{inputLabel(request, name)}</span><span className="history-stored-state"><Icon name="check" />Already stored</span></li>)}
      {request.plain.map((name) => <li key={name}><span>{inputLabel(request, name)}</span><span className="history-stored-state"><Icon name="check" />Already sent to {asker}</span></li>)}
    </ul>}
    <div className="history-stored-actions">
      <ClickFeedback text={feedback} />
      <button type="button" className="history-stored-note" data-tip={"Write to " + recipient + " about this card"} disabled={!connected || isSending} onClick={() => setIsWriting(true)}><Icon name="comment" />Something else?</button>
    </div>
    {isWriting && <AnswerWindow asker={recipient} task={to} instance={instance} onCancel={() => setIsWriting(false)} onSend={(text) => void send(text)} />}
  </div>;
}
