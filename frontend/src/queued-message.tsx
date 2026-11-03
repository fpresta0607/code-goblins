import { memo, useState } from "react";
import { message, reportToCfo, request } from "./api";
import { Icon } from "./Icon";
import { keptFileAddress, keptPictures } from "./kept-files";
import { MessagePictures } from "./message-pictures";
import { messageState } from "./messages";
import type { Action } from "./types";

// TAKE_BACK_MESSAGE names the event Edit sends with a queued message's ID, for
// the box of its session to take it back (composer.tsx).
export const TAKE_BACK_MESSAGE = "cfo-take-back-message";

// QUEUED_NOTICE names the event a queued message sends with its task's ID and
// what a press on it could not do, for the box of its session to say over
// itself (composer.tsx).
export const QUEUED_NOTICE = "cfo-queued-notice";

// One message the board holds for delivery, shown where it waits: its words,
// what it waits on in the supervisor's own plain reason, and the three things
// he can do with it (the Overlord, 2026-10-26: "Does this seem to be a way to
// like send now or edit the cue messages either or delete them?").
// Send now clears its wait so the next safe moment types it; Edit takes it
// back off the queue into the box, as Up does, to change and send again (the
// Overlord, 2026-10-28: no little edit window inside the chat); Cancel takes
// it back unsent, by its own row, so it never reaches the harness. pane draws
// it as his own bubble at the conversation's end, in its one scroll, the
// pictures the box kept for it above its bubble in place of their paths as his
// sent message shows them; queue in the message box's own list. It shows each
// state it is really in (the CFO, 2026-11-02): held by the board, dimmed with
// Queued under it and its three controls; typed into the session, solid with
// Sent under it and no controls, since it is then its harness's to take; and
// taken, when the session's record shows it and his own row takes its place
// (the Overlord, 2026-10-29: "the user can't really tell what's been sent and
// what's getting sent", and 2026-10-30: "id ratehr it just say qeued"). What
// it waits on is its description for a screen reader, and nothing about it is
// drawn as a warning. Every message the board still holds offers Send now
// (the Overlord, 2026-10-28: "send now should be an option with all queued
// messages"): one that cannot go now says why over the box when pressed, as
// whatever else a press could not do does, and a press that comes after the
// board let it go says where it went.
export const QueuedMessage = memo(function QueuedMessage({ action, instance, name, variant, onDeleted }: QueuedProps) {
  const [isSending, setSending] = useState(false);
  // What he already asked of this message, so a second press asks nothing
  // more (the Overlord, 2026-10-28: "lack of indmepotent click on teh
  // queue"): Send now stays pressed until the message leaves the queue, and a
  // Cancel taken takes the actions away. A Cancel's request ID is the
  // message's own, so one sent again from another screen withdraws nothing
  // twice.
  const [asked, setAsked] = useState<"" | "now" | "withdrawn">("");
  const post = (body: object) => request("/api/actions", undefined, {
    method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify(body),
  });
  const refuse = (text: string) => window.dispatchEvent(new CustomEvent(QUEUED_NOTICE, { detail: { task: action.task_id, text } }));
  const act = async (body: { kind: string; text?: string }, after: () => void) => {
    if (isSending) return;
    setSending(true);
    const id = body.kind === "message_withdraw" ? "withdraw-" + action.id : crypto.randomUUID();
    try { await post({ id, task_id: action.task_id, target: action.id, ...body }); after(); }
    catch (error: unknown) {
      refuse(message(error));
      // A press the supervisor answered is refused for a reason he reads over
      // the box; only one that never had its answer is the board's own
      // failure, which the CFO hears of.
      if (!(error instanceof Error && error.cause !== undefined)) reportToCfo("a queued message to " + name, message(error));
    }
    finally { setSending(false); }
  };
  // A message is typed only once it awaits its session; one the board is
  // trying to type, running with nothing awaited, it still holds.
  const isHeld = action.status === "queued" || action.status === "running" && !action.awaiting;
  const isTyped = action.status === "running" && !!action.awaiting;
  const state = isHeld ? "Queued" : isTyped ? "Sent" : messageState(action);
  const reason = isHeld || isTyped ? action.message : "";
  const kept = keptPictures(action.text);
  const why = "queued-why-" + action.id;
  return <div className={"queued-message " + variant} data-queued={action.id}>
    {variant === "pane" && <MessagePictures sources={kept.paths.map((path) => keptFileAddress(instance, action.task_id || undefined, path))} />}
    {variant === "pane" && kept.text && <div className={"message-bubble queued-bubble" + (isHeld ? " waiting" : "")} aria-describedby={why}>{kept.text}</div>}
    {variant === "pane" && <p className="queued-state">{state}</p>}
    {variant === "queue" && <span className="composer-sent-text" aria-describedby={why}>{action.text}</span>}
    {variant === "queue" && <p className="queued-line"><strong>{state}</strong></p>}
    <p className="sr-only" id={why}>{reason || state}</p>
    {isHeld && asked !== "withdrawn" && <div className="queued-actions">
      <button type="button" className="icon-button" aria-label="Send now" disabled={isSending || asked === "now"}
        onClick={() => action.can_send_now ? void act({ kind: "message_now" }, () => setAsked("now")) : refuse(action.send_now_why || "It cannot be typed into its session now.")}><Icon name="arrow-up" /></button>
      <button type="button" className="icon-button" aria-label="Edit" disabled={isSending}
        onClick={() => window.dispatchEvent(new CustomEvent<string>(TAKE_BACK_MESSAGE, { detail: action.id }))}><Icon name="edit" /></button>
      <button type="button" className="icon-button" aria-label="Cancel" disabled={isSending}
        onClick={() => void act({ kind: "message_withdraw", text: action.text }, () => { setAsked("withdrawn"); onDeleted?.(); })}><Icon name="close" /></button>
    </div>}
  </div>;
}, isSameQueued);

type QueuedProps = { action: Action; instance: string; name: string; variant: "pane" | "queue"; onDeleted?: () => void };

// Each snapshot parses every action anew, so a comparison by identity drew
// every queued message again on every snapshot, 315 times in 120 s of a live
// board. It is drawn again only when what it shows or sends changes.
function isSameQueued(prior: QueuedProps, next: QueuedProps): boolean {
  const [was, is] = [prior.action, next.action];
  return was.id === is.id && was.task_id === is.task_id && was.text === is.text && was.status === is.status && was.message === is.message
    && !!was.awaiting === !!is.awaiting && was.can_send_now === is.can_send_now && was.send_now_why === is.send_now_why
    && prior.instance === next.instance && prior.name === next.name && prior.variant === next.variant && prior.onDeleted === next.onDeleted;
}
