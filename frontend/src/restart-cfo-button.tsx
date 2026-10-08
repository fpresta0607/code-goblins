import { useState } from "react";
import type { Snapshot } from "./types";
import { message, request } from "./api";
import { Icon } from "./Icon";
import { RestartCfoDialog } from "./restart-cfo-dialog";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

// Restart for a CFO that runs in its native terminal, in the CFO's panel and
// off its bar: it does what goblins resume does, as for a screen that froze,
// once he confirms it, since it interrupts what the CFO is doing. A CFO still
// starting has not registered yet, so it has none.
export function RestartCfoButton({ snapshot }: { snapshot: Snapshot }) {
  const [restarting, setRestarting] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  if (!snapshot.cfo_runs || snapshot.cfo_starting || !snapshot.cfo_terminal) return null;
  // The board shows the restarted CFO from its next snapshot, with its
  // terminal on the new host, so a restart that worked has nothing more to do
  // here.
  const restart = async () => {
    setConfirming(false);
    setRestarting(true);
    showFeedback("");
    try {
      await request("/api/cfo/restart", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: "{}" });
    } catch (error: unknown) { showFeedback(message(error)); } finally {
      setRestarting(false);
    }
  };
  return <div className="cfo-restart">
    <button className="labelled-button" disabled={restarting} onClick={() => setConfirming(true)}><Icon name="refresh" />{restarting ? "Restarting the CFO…" : "Restart the CFO"}</button>
    <ClickFeedback text={feedback} />
    {confirming && <RestartCfoDialog onRestart={() => void restart()} onClose={() => setConfirming(false)} />}
  </div>;
}
