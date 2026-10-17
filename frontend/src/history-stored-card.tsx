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
export function HistoryStoredCard({ request, asker, connected, instance }: { request: CredentialRequest; asker: string; connected: boolean; instance: string }) {
  const [isWriting, setIsWriting] = useState(false);
  const [isSending, setIsSending] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  // One ID for each note, kept across a retry, so the board queues it once.
  const sendID = useRef("");
  const send = async (text: string) => {
    setIsWriting(false); setIsSending(true); showFeedback("");
    sendID.current ||= crypto.randomUUID();
    try {
      await call("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance },
        body: JSON.stringify({ id: sendID.current, kind: "message", task_id: request.task, generation: "", text: "About your card " + request.id + " (" + request.why + "), whose values are stored: " + text }) });
      sendID.current = "";
      showFeedback("Sent to " + asker);
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
      <button type="button" className="history-stored-note" data-tip={"Write to " + asker + " about this card"} disabled={!connected || isSending} onClick={() => setIsWriting(true)}><Icon name="comment" />Something else?</button>
    </div>
    {isWriting && <AnswerWindow asker={asker} task={request.task} instance={instance} onCancel={() => setIsWriting(false)} onSend={(text) => void send(text)} />}
  </div>;
}
