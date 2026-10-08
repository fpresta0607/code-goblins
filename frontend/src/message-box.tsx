import { useRef, useState } from "react";
import type { Snapshot, Task } from "./types";
import { message, reportToCfo, request } from "./api";
import { messagesTo, messageState } from "./messages";
import "./message-box.css";

// How many of his latest messages the box shows above itself.
const SHOWN = 5;

// A message box where a terminal cannot take typing now: a goblin paused,
// resuming or starting, or no CFO running. Enter sends what he wrote, and the
// supervisor delivers it once, whatever the goblin or the CFO is doing:
// typed into its terminal once it runs, or carried by a paused goblin's
// resume. Each message he sent shows with where it is. One the board could
// not send stays in the box, and the CFO hears why. task names the goblin,
// and none means the CFO.
export function MessageBox({ snapshot, task, name }: { snapshot: Snapshot; task?: Task; name: string }) {
  const [draft, setDraft] = useState("");
  const [isSending, setSending] = useState(false);
  // One ID for what he is about to send, so a send retried after its answer
  // was lost is the same message, never a second one.
  const sendID = useRef("");
  const sent = messagesTo(snapshot, task?.id || "").slice(-SHOWN);
  const send = async () => {
    const text = draft.trim();
    if (!text || isSending) return;
    sendID.current ||= crypto.randomUUID();
    setSending(true);
    try {
      await request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ id: sendID.current, kind: "message", task_id: task?.id || "", generation: "", text }) });
      sendID.current = "";
      setDraft("");
    } catch (error: unknown) {
      reportToCfo("a message to " + name, message(error));
    } finally {
      setSending(false);
    }
  };
  return <section className="message-box" aria-label={"Messages to " + name}>
    {sent.length > 0 && <ol className="message-list">
      {sent.map((action) => <li key={action.id}><span className="message-text">{action.text}</span><span className="message-state">{messageState(action)}</span></li>)}
    </ol>}
    <textarea aria-label={"Message " + name} placeholder={"Message " + name} rows={2} value={draft} disabled={isSending}
      onChange={(event) => { setDraft(event.target.value); sendID.current = ""; }}
      onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(); } }} />
  </section>;
}
