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
// sent message shows them; queue in the message box's own list. In pane it
// waits dimmed, with a few words under it, while the board holds it or the
// session works, and is solid with Sent once it goes to an idle session, until
// the session's record shows it read (the Overlord, 2026-10-29: "the user
// can't really tell what's been sent and what's getting sent"). What it waits
// on is its description for a screen reader. Every message the board still
// holds offers Send now (the Overlord, 2026-10-28: "send now should be an
// option with all queued messages"): one that cannot go now says why under it
// when pressed. One already typed into the terminal is the harness's to take,
// so the board offers nothing to do with it.
export const QueuedMessage = memo(function QueuedMessage({ action, instance, name, variant, isWorking = false, onDeleted }: QueuedProps) {
  const [problem, setProblem] = useState("");
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
  const act = async (body: { kind: string; text?: string }, after: () => void) => {
    if (isSending) return;
    setSending(true);
    setProblem("");
    const id = body.kind === "message_withdraw" ? "withdraw-" + action.id : crypto.randomUUID();
    try { await post({ id, task_id: action.task_id, target: action.id, ...body }); after(); }
    catch (error: unknown) { setProblem(message(error)); reportToCfo("a queued message to " + name, message(error)); }
    finally { setSending(false); }
  };
  // A message is typed only once it awaits the turn; one the board is
  // looking at again, running with nothing awaited, it still holds.
  const isHeld = action.status === "queued" || action.status === "running" && !action.awaiting;
  const isTyped = action.status === "running" && !!action.awaiting;
  const isPending = isHeld || isTyped;
  const isWaiting = action.status === "queued" || isPending && isWorking;
  const state = variant === "pane" ? (isWaiting ? "Waiting for " + (action.task_id ? "the goblin's" : "the CFO's") + " next step" : "Sent")
    : isHeld ? "Queued" : isTyped ? "Typed, awaiting the turn" : messageState(action);
  const canAct = isHeld && (variant === "queue" || isWaiting);
  const reason = isPending ? action.message : "";
  const kept = keptPictures(action.text);
  const why = "queued-why-" + action.id;
  return <div className={"queued-message " + variant} data-queued={action.id}>
    {variant === "pane" && <MessagePictures sources={kept.paths.map((path) => keptFileAddress(instance, action.task_id || undefined, path))} />}
    {variant === "pane" && kept.text && <div className={"message-bubble queued-bubble" + (isWaiting ? " waiting" : "")} aria-describedby={why}>{kept.text}</div>}
    {variant === "pane" && <p className="queued-state">{state}</p>}
    {variant === "queue" && <span className="composer-sent-text" aria-describedby={why}>{action.text}</span>}
    {variant === "queue" && <p className="queued-line"><strong>{state}</strong></p>}
    <p className="sr-only" id={why}>{reason || state}</p>
    {canAct && asked !== "withdrawn" && <div className="queued-actions">
      <button type="button" className="icon-button" aria-label="Send now" disabled={isSending || asked === "now"}
        onClick={() => action.can_send_now ? void act({ kind: "message_now" }, () => setAsked("now")) : setProblem(action.send_now_why || "It cannot be typed into its session now.")}><Icon name="arrow-up" /></button>
      <button type="button" className="icon-button" aria-label="Edit" disabled={isSending}
        onClick={() => window.dispatchEvent(new CustomEvent<string>(TAKE_BACK_MESSAGE, { detail: action.id }))}><Icon name="edit" /></button>
      <button type="button" className="icon-button" aria-label="Cancel" disabled={isSending}
        onClick={() => void act({ kind: "message_withdraw", text: action.text }, () => { setAsked("withdrawn"); onDeleted?.(); })}><Icon name="close" /></button>
    </div>}
    {problem && <p className="queued-problem" role="status">{problem}</p>}
  </div>;
}, isSameQueued);

type QueuedProps = { action: Action; instance: string; name: string; variant: "pane" | "queue"; isWorking?: boolean; onDeleted?: () => void };

// Each snapshot parses every action anew, so a comparison by identity drew
// every queued message again on every snapshot, 315 times in 120 s of a live
// board. It is drawn again only when what it shows or sends changes.
function isSameQueued(prior: QueuedProps, next: QueuedProps): boolean {
  const [was, is] = [prior.action, next.action];
  return was.id === is.id && was.task_id === is.task_id && was.text === is.text && was.status === is.status && was.message === is.message
    && !!was.awaiting === !!is.awaiting && was.can_send_now === is.can_send_now && was.send_now_why === is.send_now_why
    && prior.instance === next.instance && prior.name === next.name && prior.variant === next.variant && prior.isWorking === next.isWorking && prior.onDeleted === next.onDeleted;
}
