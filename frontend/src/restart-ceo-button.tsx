import { useState } from "react";
import { object, type Snapshot } from "./types";
import { message, request } from "./api";
import { Icon } from "./Icon";
import { CeoHarnessSelector } from "./ceo-harness-selector";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

// isCeoRestartOffered says the CEO runs in its native terminal and has
// registered, so Restart can reach it: a CEO still starting has none.
export const isCeoRestartOffered = (snapshot: Snapshot) => snapshot.ceo_runs && !snapshot.ceo_starting && !!snapshot.ceo_terminal;

// Restart for a CEO that runs in its native terminal, under Harness in the
// CEO's panel and off its bar: it does what goblins resume does, as for a
// screen that froze. A restart always restarts the CEO, and one that started
// it on a new conversation says so in one plain line, which stays while the
// restarted CEO starts, when Restart is gone.
export function RestartCeoButton({ snapshot }: { snapshot: Snapshot }) {
  const [isRestarting, setRestarting] = useState(false);
  const [selection, setSelection] = useState({ currentHarness: snapshot.ceo_harness, harness: snapshot.ceo_harness });
  const [feedback, showFeedback] = useClickFeedback();
  if (selection.currentHarness !== snapshot.ceo_harness) setSelection({ currentHarness: snapshot.ceo_harness, harness: snapshot.ceo_harness });
  const isOffered = isCeoRestartOffered(snapshot);
  if (!isOffered && !feedback) return null;
  // The board shows the restarted CEO from its next snapshot, with its
  // terminal on the new host, so a restart that came back on its
  // conversation has nothing more to say here.
  const restart = async () => {
    setRestarting(true);
    showFeedback("");
    try {
      const answer = object(await request("/api/ceo/restart", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CEO-Token": snapshot.instance }, body: JSON.stringify({ harness: selection.harness }) }));
      if (answer.resumed === false) showFeedback("The CEO restarted on a new conversation");
    } catch (error: unknown) { showFeedback(message(error)); } finally {
      setRestarting(false);
    }
  };
  return <div className="ceo-restart">
    {isOffered && <div className="ceo-restart-controls" role="group" aria-label="Restart" aria-busy={isRestarting}>
      <button className="labelled-button ceo-restart-button" aria-label="Restart the CEO" disabled={isRestarting} onClick={() => void restart()}><Icon name="refresh" />Restart</button>
      <CeoHarnessSelector harness={selection.harness} isDisabled={isRestarting} onSelect={(harness) => setSelection({ currentHarness: snapshot.ceo_harness, harness })} />
    </div>}
    <ClickFeedback text={feedback} />
  </div>;
}
