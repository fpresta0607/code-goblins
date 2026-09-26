import { useState } from "react";
import type { Session, Snapshot, Task } from "./types";
import { ownsTaskSession } from "./lineageTree";
import { NativeTerminal } from "./NativeTerminal";
import { HostTerminal } from "./HostTerminal";
import { TerminalSwitcher } from "./TerminalSwitcher";
import { CFO_KEY, keepLive, switchOrder } from "./terminalOrder";

// The terminal deck: every terminal the Overlord opens stays mounted and live
// while the board is open, so a switch only shows another terminal, with no
// reconnect, no replay and no blank frame. A native terminal keeps its own
// socket; Herdr views stay within Herdr's stream limit. The switcher beside
// it lists the CFO first, then each goblin with a terminal.
export function TerminalDeck({ snapshot, task, node, cfo, shown, connected, focus, onSwitch, onOwner }: {
  snapshot: Snapshot; task?: Task; node?: Session; cfo: boolean; shown: boolean; connected: boolean; focus: number;
  onSwitch: (key: string) => void; onOwner?: () => void;
}) {
  const owner = !!task && !!task.generation && (!node || ownsTaskSession(node, task));
  const key = cfo ? CFO_KEY : owner ? task.id : "";
  const [live, setLive] = useState<string[]>([]);
  const herdr = (candidate: string) => candidate === CFO_KEY || snapshot.tasks.find((each) => each.id === candidate)?.backend !== "native";
  if (shown && key && live[0] !== key) setLive(keepLive(live, key, herdr));
  const order = switchOrder(snapshot.tasks);
  return <div className="terminal-deck" hidden={!shown}>
    <TerminalSwitcher order={order} current={key} snapshot={snapshot} onSwitch={onSwitch} />
    <div className="deck-stage">
      {live.map((entry) => {
        const here = shown && entry === key;
        if (entry === CFO_KEY) return <div className="deck-slot" key={entry} hidden={!here}><NativeTerminal instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} /></div>;
        const each = snapshot.tasks.find((candidate) => candidate.id === entry && !!candidate.generation);
        if (!each) return null;
        return <div className="deck-slot" key={entry} hidden={!here}>
          {each.backend === "native"
            ? <HostTerminal task={each} instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />
            : <NativeTerminal task={each} node={snapshot.sessions.find((session) => ownsTaskSession(session, each))} instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />}
        </div>;
      })}
      {/* A queued task or a child session has no terminal of its own to keep. */}
      {shown && !key && <div className="deck-slot"><NativeTerminal task={task} node={node} instance={snapshot.instance} visible={connected} shown onOwner={onOwner} /></div>}
    </div>
  </div>;
}
