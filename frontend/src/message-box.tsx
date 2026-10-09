import { useRef, useState, type PointerEvent } from "react";
import type { Snapshot, Task } from "./types";
import { message, reportToCfo, request } from "./api";
import { Icon } from "./Icon";
import { messagesTo, messageState } from "./messages";
import { goblinName } from "./task-words";
import { useDictation } from "./useDictation";
import { VoiceBars } from "./voice-bars";
import "./message-box.css";

// How many of his latest messages the box shows above itself.
const SHOWN = 5;

// A message box where a goblin's terminal cannot take typing now: a goblin
// paused, resuming or starting. Send or Enter sends what he wrote, and the
// supervisor delivers it once, whatever the goblin is doing: typed into its
// terminal once it runs, or carried by a paused goblin's resume. The CFO has
// no box. Holding the microphone, or Ctrl+Shift+Space in the box,
// dictates into it as a terminal does. Each message he sent shows with where
// it is, and those a paused goblin's resume will carry, read from its resume
// note, can each be deleted, so the resume never carries it (the Overlord,
// 2026-10-09). One the board could not send or delete stays, and the CFO
// hears why.
export function MessageBox({ snapshot, task }: { snapshot: Snapshot; task: Task }) {
  const name = goblinName(task);
  const [draft, setDraft] = useState("");
  const [isSending, setSending] = useState(false);
  // The kept message whose delete is on its way.
  const [deleting, setDeleting] = useState("");
  // One ID for what he is about to send, so a send retried after its answer
  // was lost is the same message, never a second one.
  const sendID = useRef("");
  // What he dictates joins what he wrote, after a space.
  const dictation = useDictation((text) => {
    setDraft((prior) => [prior.trimEnd(), text.trim()].filter(Boolean).join(" "));
    sendID.current = "";
  }, snapshot.instance);
  const sent = messagesTo(snapshot, task.id).slice(-SHOWN);
  const kept = task.lifecycle?.kept_messages ?? [];
  const post = (body: object) => request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify(body) });
  const send = async () => {
    const text = draft.trim();
    if (!text || isSending) return;
    sendID.current ||= crypto.randomUUID();
    setSending(true);
    try {
      await post({ id: sendID.current, kind: "message", task_id: task.id, generation: "", text });
      sendID.current = "";
      setDraft("");
    } catch (error: unknown) {
      reportToCfo("a message to " + name, message(error));
    } finally {
      setSending(false);
    }
  };
  // A delete names the message by its goblin and its words, which the
  // supervisor takes out of the resume note.
  const withdraw = async (text: string, key: string) => {
    setDeleting(key);
    try {
      await post({ id: crypto.randomUUID(), kind: "message_withdraw", task_id: task.id, generation: "", text });
    } catch (error: unknown) {
      reportToCfo("deleting a message to " + name, message(error));
    } finally {
      setDeleting("");
    }
  };
  // The microphone listens while it is held, by pointer or by key, and keeps
  // the keyboard in the box.
  const hold = (event: PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    dictation.start();
  };
  return <section className="message-box" aria-label={"Messages to " + name}>
    {sent.length + kept.length > 0 && <ol className="message-list">
      {sent.map((action) => <li key={action.id}><span className="message-text">{action.text}</span><span className="message-state">{messageState(action)}</span></li>)}
      {kept.map((text, index) => {
        const key = index + ":" + text;
        return <li key={key}>
          <span className="message-text">{text}</span>
          <span className="message-state">Kept for resume</span>
          <button type="button" className="icon-button message-delete" aria-label={"Delete: " + text} data-tip="Delete before its resume"
            disabled={deleting === key} onClick={() => void withdraw(text, key)}><Icon name="trash" /></button>
        </li>;
      })}
    </ol>}
    <textarea aria-label={"Message " + name} placeholder={"Message " + name} rows={2} value={draft} disabled={isSending}
      onChange={(event) => { setDraft(event.target.value); sendID.current = ""; }}
      onKeyDown={(event) => {
        if (dictation.key(event.nativeEvent) !== null) return;
        if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(); }
      }}
      onKeyUp={(event) => { dictation.key(event.nativeEvent); }} />
    <div className="message-actions">
      <button type="button" className={"voice-bubble" + (dictation.listening ? " recording" : "")} aria-label={dictation.listening ? "Listening" : "Hold to dictate"}
        data-tip={dictation.listening ? "Release to add what you said" : "Hold to dictate"}
        onPointerDown={hold} onPointerUp={dictation.stop} onPointerCancel={dictation.stop}
        onKeyDown={(event) => { if (event.key === " " || event.key === "Enter") { event.preventDefault(); dictation.start(); } }}
        onKeyUp={(event) => { if (event.key === " " || event.key === "Enter") dictation.stop(); }}>
        {dictation.listening ? <VoiceBars level={dictation.level} /> : <Icon name="mic" />}
      </button>
      <button type="button" className="primary send-decision" disabled={isSending || !draft.trim()} onClick={() => void send()}>
        <Icon name={isSending ? "clock" : "send"} />{isSending ? "Sending" : "Send"}
      </button>
    </div>
    {dictation.note && <p className="message-note" role="status">{dictation.note}</p>}
  </section>;
}
