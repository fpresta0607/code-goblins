import { useEffect, useState } from "react";
import { message, request } from "./api";
import { Icon } from "./Icon";
import type { Snapshot } from "./types";
import { harnessName } from "./workflow";
import "./harness-update.css";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

// The CEO's Update button, on the line under its panel's header row, shown
// only while an update of the harness the CEO runs waits for a restart. His
// press restarts the CEO onto it, on its own conversation, once its turn has
// ended; pressed again before then it takes the press back. Nothing presses
// it but him. What a press could not do, his request refused or the restart
// it asked for, says so in a few words under it for a moment.
export function CeoUpdate({ snapshot }: { snapshot: Snapshot }) {
  const [isSending, setSending] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const problem = snapshot.ceo_update?.problem || "";
  useEffect(() => { if (problem) showFeedback(problem); }, [problem, showFeedback]);
  const update = snapshot.ceo_update;
  if (!update) return null;
  const name = harnessName(update.harness);
  if (update.updating) return <button className="harness-update" disabled><Icon name="download" />Updating…</button>;
  const press = async () => {
    setSending(true);
    showFeedback("");
    try {
      await request("/api/ceo/update", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CEO-Token": snapshot.instance }, body: JSON.stringify(update.pending ? { cancel: true } : {}) });
    } catch (error: unknown) { showFeedback(message(error)); } finally {
      setSending(false);
    }
  };
  return <><button className={"harness-update" + (update.pending ? " pending" : "")} aria-label={update.pending ? "Cancel the " + name + " update" : "Update " + name} disabled={isSending} onClick={() => void press()}>
    <Icon name={update.pending ? "clock" : "download"} />{update.pending ? "Cancel update" : "Update"}
  </button><ClickFeedback text={feedback} /></>;
}
