import { useState } from "react";
import type { Snapshot } from "./types";
import { message, request } from "./api";
import { Icon } from "./Icon";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

// Reopen the CFO brings a closed CFO back as goblins does, from the CFO's bar
// and from the item that says the supervisor stopped bringing back a CFO that
// kept ending. The board shows the reopened CFO from its next snapshot, so a
// reopen that worked has nothing more to say here.
export function ReopenCfoButton({ snapshot }: { snapshot: Snapshot }) {
  const [isReopening, setReopening] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const reopen = async () => {
    setReopening(true);
    showFeedback("");
    try {
      await request("/api/cfo/reopen", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: "{}" });
    } catch (error: unknown) { showFeedback(message(error)); } finally {
      setReopening(false);
    }
  };
  return <>
    <button className="labelled-button primary" disabled={isReopening} onClick={() => void reopen()}><Icon name="play" />{isReopening ? "Reopening the CFO…" : "Reopen the CFO"}</button>
    <ClickFeedback text={feedback} />
  </>;
}
