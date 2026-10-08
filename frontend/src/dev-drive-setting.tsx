import { useState } from "react";
import { message, request } from "./api";
import type { DevDriveView } from "./types";
import "./afk.css";

const actionLabels: Record<DevDriveView["action"], string> = { "": "", "set-up": "Set up", "try-again": "Try again", attach: "Attach" };

// The Dev Drive in the CFO's panel: whether the home's worktrees, scratch and
// package caches are on one, what one is, and the one button that asks for the
// next step. Every step that needs the Overlord, as administrator or not,
// arrives as its own Command Center item; this only asks for it.
export function DevDriveSetting({ drive, instance }: { drive: DevDriveView; instance: string }) {
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState("");
  const ask = async () => {
    setPending(true); setProblem("");
    try {
      await request("/api/dev-drive", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ want: true }) });
    } catch (error: unknown) { setProblem(message(error)); }
    setPending(false);
  };
  const label = actionLabels[drive.action];
  return <div className="start-at-login dev-drive-setting">
    <div>
      <strong>Dev Drive</strong>
      <p>{drive.explain}</p>
      <p>{drive.line}</p>
      {drive.waiting && <p role="status">Its next step waits for you in the Command Center.</p>}
      {problem && <p className="afk-problem" role="alert">{problem}</p>}
    </div>
    {label && <button className="secondary" disabled={pending} onClick={() => void ask()}>{label}</button>}
  </div>;
}
