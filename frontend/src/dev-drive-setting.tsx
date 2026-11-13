import { useState } from "react";
import { message, request } from "./api";
import type { DevDriveView } from "./types";
import "./afk.css";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

const actionLabels: Record<DevDriveView["action"], string> = { "": "", "set-up": "Set up", "try-again": "Try again", attach: "Attach" };

// The Dev Drive in the CEO's panel: its name, what the supervisor says of it
// in plain words (what is done, what is left and what it waits on), and a
// button only where a press does what the supervisor cannot do alone. Every
// step that needs the Overlord arrives as its own Command Center item, which
// the button only asks for; the last step, moving the gate's home, the
// supervisor takes itself. A refusal says so under the row for a moment.
export function DevDriveSetting({ drive, instance }: { drive: DevDriveView; instance: string }) {
  const [pending, setPending] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const ask = async () => {
    setPending(true); showFeedback("");
    try {
      await request("/api/dev-drive", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CEO-Token": instance }, body: JSON.stringify({ want: true }) });
    } catch (error: unknown) { showFeedback(message(error)); }
    setPending(false);
  };
  const label = actionLabels[drive.action];
  if (!label && !drive.status && !feedback) return null;
  return <div className="dev-drive-setting">
    <strong>Dev Drive</strong>
    {label && <button className="secondary" disabled={pending} onClick={() => void ask()}>{label}</button>}
    {drive.status && <p role="status">{drive.status}</p>}
    <ClickFeedback text={feedback} />
  </div>;
}
