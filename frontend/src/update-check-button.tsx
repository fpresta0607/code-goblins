import { message, reportToCeo, request } from "./api";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { Icon } from "./Icon";
import type { Snapshot, UpdateCheck } from "./types";
import { harnessName } from "./workflow";
import "./harness-update.css";

// Retry update, on the line under a chat's header row while the
// conversation's terminal says its harness's update failed, as Claude Code's
// "✘ Auto-update failed · Run claude doctor", and the install is not the
// newest: beside Restart PC and Update on the CEO's panel, alone on a
// goblin's (the Overlord, 2026-11-26: "run claude doctor should be a button
// like the update/restart in caht pane not below the prompt line in modern
// terminal", #2956). His press has the supervisor run the harness's own
// update check outside the conversation, typing nothing into it (#2333).
// Under it, its words are the terminal's notice and how the last check
// ended: why it failed, or what it said. What a press could not do says so
// under it for a moment, and a press that never reached the supervisor is
// the CEO's to know of too.
export function UpdateCheckButton({ snapshot, harness, notice }: { snapshot: Snapshot; harness: string; notice: string }) {
  const [feedback, showFeedback] = useClickFeedback();
  const name = harnessName(harness);
  const check: UpdateCheck | undefined = snapshot.update_checks[harness];
  const isChecking = check?.running === true;
  const said = isChecking ? "" : check?.result ?? "";
  const press = () => {
    showFeedback("");
    request("/api/harness/update-check", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CEO-Token": snapshot.instance }, body: JSON.stringify({ harness }) }).catch((error: unknown) => {
      showFeedback(message(error));
      if (!(error instanceof Error && error.cause !== undefined)) reportToCeo("the update check of " + name, message(error));
    });
  };
  return <>
    <button className="harness-update update-check" aria-label={"Retry the " + name + " update"} aria-description="Runs the update check again, outside the conversation" disabled={isChecking} onClick={press}>
      <Icon name="refresh" />{isChecking ? "Checking…" : "Retry update"}
    </button>
    <p className="update-check-said" role="status" aria-label="Update check">{notice}{said && (check?.failed ? ": " + said : " · Checked again: " + said)}</p>
    <ClickFeedback text={feedback} />
  </>;
}
