import { useState } from "react";
import { message, request } from "./api";
import { Icon } from "./Icon";
import type { Snapshot } from "./types";
import { harnessName } from "./workflow";
import "./harness-update.css";

// The CFO's Update button, before the AFK switch in its panel's header, shown
// only while an update of the harness the CFO runs waits for a restart. His
// press restarts the CFO onto it, on its own conversation, once its turn has
// ended; pressed again before then it takes the press back. Nothing presses
// it but him. What a press could not do goes to onProblem, which the header
// says under itself.
export function CfoUpdate({ snapshot, onProblem }: { snapshot: Snapshot; onProblem: (problem: string) => void }) {
  const [isSending, setSending] = useState(false);
  const update = snapshot.cfo_update;
  if (!update) return null;
  const name = harnessName(update.harness);
  if (update.updating) return <button className="harness-update" disabled><Icon name="download" />Updating…</button>;
  const press = async () => {
    setSending(true);
    onProblem("");
    try {
      await request("/api/cfo/update", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify(update.pending ? { cancel: true } : {}) });
    } catch (error) {
      onProblem(message(error));
    } finally {
      setSending(false);
    }
  };
  const tip = update.pending ? "Take the update back" : name + " was updated. Restart the CFO onto it at its next stopping point. Its conversation is kept.";
  return <button className={"harness-update" + (update.pending ? " pending" : "")} aria-label={update.pending ? "Cancel the " + name + " update" : "Update " + name} data-tip={tip} data-tip-align="end" disabled={isSending} onClick={() => void press()}>
    <Icon name={update.pending ? "clock" : "download"} />{update.pending ? "Cancel update" : "Update"}
  </button>;
}
