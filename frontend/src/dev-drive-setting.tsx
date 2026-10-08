import { useState } from "react";
import { message, request } from "./api";
import type { DevDriveView } from "./types";
import "./afk.css";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

const actionLabels: Record<DevDriveView["action"], string> = { "": "", "set-up": "Set up", "try-again": "Try again", attach: "Attach" };

// The Dev Drive in the CFO's panel: its name and the one button that asks for
// the next step on the row's header line, as Start at login and its switch,
// and one short note under them, whether the home's worktrees, scratch and
// package caches are on one or what one is. Every step that needs the
// Overlord, as administrator or not, arrives as its own Command Center item;
// this only asks for it, and a refusal says so under the note for a moment.
export function DevDriveSetting({ drive, instance }: { drive: DevDriveView; instance: string }) {
  const [pending, setPending] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const ask = async () => {
    setPending(true); showFeedback("");
    try {
      await request("/api/dev-drive", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ want: true }) });
    } catch (error: unknown) { showFeedback(message(error)); }
    setPending(false);
  };
  const label = actionLabels[drive.action];
  return <div className="start-at-login dev-drive-setting">
    <strong>Dev Drive</strong>
    {label && <button className="secondary" disabled={pending} onClick={() => void ask()}>{label}</button>}
    {drive.waiting ? <p role="status">Its next step waits for you in the Command Center.</p> : <p>{drive.note}</p>}
    <ClickFeedback text={feedback} />
  </div>;
}
