import { useState } from "react";
import { message, request } from "./api";
import { HostTerminal } from "./HostTerminal";
import { Icon } from "./Icon";
import type { Run } from "./types";

// A command's live terminal on its card, as the Overlord asked on 2026-10-08:
// "why can we not render terminal right in command center in place of the
// notification". It is the native terminal the supervisor hosts for the
// command, drawn by the board's own terminal view, so what he types reaches
// the command and a sign-in it opens works as in a window. It takes the
// keyboard as it appears, since he pressed Run to use it, and Stop ends the
// command and everything it started.
export function RunTerminal({ run, instance, connected }: { run: Run; instance: string; connected: boolean }) {
  const [stopping, setStopping] = useState(false);
  const [error, setError] = useState("");
  const stop = async () => {
    setStopping(true);
    setError("");
    try {
      await request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ id: crypto.randomUUID(), kind: "run_stop", run_id: run.id, generation: run.identity }) });
    } catch (failure: unknown) {
      setError(message(failure));
      setStopping(false);
    }
  };
  return <section className="run-live">
    <HostTerminal query={"run=" + encodeURIComponent(run.id)} harness="" label={"Terminal of " + run.title} instance={instance} visible={connected} shown focus={1} />
    <div className="run-live-actions">
      {error && <p className="warning-text" role="alert">{error}</p>}
      <button type="button" disabled={!connected || stopping} onClick={() => void stop()}><Icon name="stop-circle" />{stopping ? "Stopping" : "Stop"}</button>
    </div>
  </section>;
}
