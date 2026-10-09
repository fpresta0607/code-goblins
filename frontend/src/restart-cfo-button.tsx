import { useState } from "react";
import { object, type Snapshot } from "./types";
import { message, request } from "./api";
import { Icon } from "./Icon";
import { RestartCfoDialog } from "./restart-cfo-dialog";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

// Restart for a CFO that runs in its native terminal, in the CFO's panel
// header right below its name and off its bar: it does what goblins resume
// does, as for a screen that froze, which its tip says, once he confirms it,
// since it interrupts what the CFO is doing. A CFO still starting has not
// registered yet, so it has none, and nor does the header's compact form over
// its terminal. A restart always restarts the CFO, and one that started it on
// a new conversation says so in one plain line, which stays while the
// restarted CFO starts, when the board opens its terminal and Restart is gone.
export function RestartCfoButton({ snapshot, isCompact }: { snapshot: Snapshot; isCompact: boolean }) {
  const [restarting, setRestarting] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const isOffered = !isCompact && snapshot.cfo_runs && !snapshot.cfo_starting && !!snapshot.cfo_terminal;
  if (!isOffered && !feedback) return null;
  // The board shows the restarted CFO from its next snapshot, with its
  // terminal on the new host, so a restart that came back on its
  // conversation has nothing more to say here.
  const restart = async () => {
    setConfirming(false);
    setRestarting(true);
    showFeedback("");
    try {
      const answer = object(await request("/api/cfo/restart", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: "{}" }));
      if (answer.resumed === false) showFeedback("The CFO restarted on a new conversation");
    } catch (error: unknown) { showFeedback(message(error)); } finally {
      setRestarting(false);
    }
  };
  return <div className="cfo-restart">
    {isOffered && <button className="labelled-button" disabled={restarting} data-tip="Starts the CFO again on its conversation, for a screen that froze." data-tip-align="start" onClick={() => setConfirming(true)}><Icon name="refresh" />{restarting ? "Restarting the CFO…" : "Restart the CFO"}</button>}
    <ClickFeedback text={feedback} />
    {confirming && <RestartCfoDialog onRestart={() => void restart()} onClose={() => setConfirming(false)} />}
  </div>;
}
