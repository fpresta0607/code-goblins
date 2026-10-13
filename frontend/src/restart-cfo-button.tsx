import { useState } from "react";
import { object, type Snapshot } from "./types";
import { message, request } from "./api";
import { Icon } from "./Icon";
import { CfoHarnessSelector } from "./cfo-harness-selector";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

// isCfoRestartOffered says the CFO runs in its native terminal and has
// registered, so Restart can reach it: a CFO still starting has none.
export const isCfoRestartOffered = (snapshot: Snapshot) => snapshot.cfo_runs && !snapshot.cfo_starting && !!snapshot.cfo_terminal;

// Restart for a CFO that runs in its native terminal, under Harness in the
// CFO's panel and off its bar: it does what goblins resume does, as for a
// screen that froze. A restart always restarts the CFO, and one that started
// it on a new conversation says so in one plain line, which stays while the
// restarted CFO starts, when Restart is gone.
export function RestartCfoButton({ snapshot }: { snapshot: Snapshot }) {
  const [isRestarting, setRestarting] = useState(false);
  const [selection, setSelection] = useState({ currentHarness: snapshot.cfo_harness, harness: snapshot.cfo_harness });
  const [feedback, showFeedback] = useClickFeedback();
  if (selection.currentHarness !== snapshot.cfo_harness) setSelection({ currentHarness: snapshot.cfo_harness, harness: snapshot.cfo_harness });
  const isOffered = isCfoRestartOffered(snapshot);
  if (!isOffered && !feedback) return null;
  // The board shows the restarted CFO from its next snapshot, with its
  // terminal on the new host, so a restart that came back on its
  // conversation has nothing more to say here.
  const restart = async () => {
    setRestarting(true);
    showFeedback("");
    try {
      const answer = object(await request("/api/cfo/restart", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ harness: selection.harness }) }));
      if (answer.resumed === false) showFeedback("The CFO restarted on a new conversation");
    } catch (error: unknown) { showFeedback(message(error)); } finally {
      setRestarting(false);
    }
  };
  return <div className="cfo-restart">
    {isOffered && <div className="cfo-restart-controls" role="group" aria-label="Restart" aria-busy={isRestarting}>
      <button className="labelled-button cfo-restart-button" aria-label="Restart the CFO" disabled={isRestarting} onClick={() => void restart()}><Icon name="refresh" />Restart</button>
      <CfoHarnessSelector harness={selection.harness} isDisabled={isRestarting} onSelect={(harness) => setSelection({ currentHarness: snapshot.cfo_harness, harness })} />
    </div>}
    <ClickFeedback text={feedback} />
  </div>;
}
