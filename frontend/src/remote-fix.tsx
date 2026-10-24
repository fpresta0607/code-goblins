import { CopyValue } from "./CopyValue";
import { Icon } from "./Icon";
import { RunTerminal } from "./run-terminal";
import { runLabel } from "./connectors";
import type { Run } from "./types";

// A fix runs from his press on, the press's claim making it running at once;
// one left ready never started and holds nothing up.
export const isLive = (run?: Run) => run?.state === "running";

// A command that mends a row of Remote access, on this PC: the command to
// copy, and a play button that runs it in a PowerShell terminal of its own,
// drawn here while it runs as a run card draws its command's. The Overlord,
// 2026-10-18: "It stays in the section, never as a Command Center card."
export function RemoteFix({ command, run, instance, connected, pending, disabled, onRun }: { command: string; run?: Run; instance: string; connected: boolean; pending: boolean; disabled: boolean; onRun: () => void }) {
  const live = isLive(run) ? run : undefined;
  const isBusy = pending || !!live;
  const label = runLabel("powershell", false);
  return <div className="remote-fix">
    {command && <div className="remote-fix-command">
      <CopyValue value={command} />
      <button type="button" className="icon-button raised" aria-label={label} data-tip={label} aria-busy={isBusy} disabled={!connected || disabled || isBusy} onClick={onRun}>
        {isBusy ? <span className="card-start-spinner" aria-hidden="true" /> : <Icon name="play" />}
      </button>
    </div>}
    {live?.terminal && <RunTerminal run={live} instance={instance} connected={connected} />}
  </div>;
}
