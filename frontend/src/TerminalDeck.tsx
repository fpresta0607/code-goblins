import { useState } from "react";
import type { Session, Snapshot, Task } from "./types";
import { ownsTaskSession } from "./lineageTree";
import { NativeTerminal } from "./NativeTerminal";
import { HostTerminal } from "./HostTerminal";
import { TerminalEmpty } from "./TerminalEmpty";
import { CFO_KEY, cfoView, goblinView, idleView, keepLive } from "./terminalOrder";

// The terminal deck: every terminal the Overlord opens stays mounted and live
// while the board is open, so a switch only shows another terminal, with no
// reconnect, no replay and no blank frame. A native terminal keeps its own
// socket; Herdr views stay within Herdr's stream limit. The shown terminal
// fills the panel; the Overlord picks the goblin on the board.
export function TerminalDeck({ snapshot, task, node, cfo, shown, connected, focus, onOwner }: {
  snapshot: Snapshot; task?: Task; node?: Session; cfo: boolean; shown: boolean; connected: boolean; focus: number; onOwner?: () => void;
}) {
  const owner = !!task && !!task.generation && (!node || ownsTaskSession(node, task));
  const key = cfo ? CFO_KEY : owner ? task.id : "";
  const [live, setLive] = useState<string[]>([]);
  const herdr = (candidate: string) => candidate === CFO_KEY ? cfoView(snapshot).kind === "herdr" : snapshot.tasks.find((each) => each.id === candidate)?.backend !== "native";
  if (shown && key && live[0] !== key) setLive(keepLive(live, key, herdr));
  const idle = idleView(task, node);
  return <div className="terminal-deck" hidden={!shown}>
    <div className="deck-stage">
      {live.map((entry) => {
        const here = shown && entry === key;
        // A CFO in a native terminal is shown from its host like a native goblin.
        if (entry === CFO_KEY) {
          const view = cfoView(snapshot);
          return <div className="deck-slot" key={entry} hidden={!here}>
            {view.kind === "host" ? <HostTerminal query={view.query} label="CFO terminal" instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />
              : view.kind === "herdr" ? <NativeTerminal instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />
              : <TerminalEmpty text={view.text} />}
          </div>;
        }
        const each = snapshot.tasks.find((candidate) => candidate.id === entry && !!candidate.generation);
        if (!each) return null;
        const view = goblinView(each);
        return <div className="deck-slot" key={entry} hidden={!here}>
          {view.kind === "host"
            ? <HostTerminal query={view.query} label="Goblin terminal" instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />
            : <NativeTerminal task={each} node={snapshot.sessions.find((session) => ownsTaskSession(session, each))} instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />}
        </div>;
      })}
      {/* A queued task or a child session has no terminal of its own to keep. */}
      {shown && !key && <div className="deck-slot"><TerminalEmpty text={idle.text} onOwner={node ? onOwner : undefined} /></div>}
    </div>
  </div>;
}
