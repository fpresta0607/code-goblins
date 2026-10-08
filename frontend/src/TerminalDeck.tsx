import { useState } from "react";
import type { Session, Snapshot, Task, TreeNode } from "./types";
import { ownsTaskSession } from "./lineageTree";
import { NativeTerminal } from "./NativeTerminal";
import { HostTerminal } from "./HostTerminal";
import { TerminalEmpty } from "./TerminalEmpty";
import { AgentTerminal } from "./agent-terminal";
import { EndedSession } from "./ended-session";
import { sessionEnd } from "./session-end";
import { CFO_KEY, cfoView, goblinView, idleView, keepLive } from "./terminalOrder";

// The terminal deck: every terminal the Overlord opens stays mounted and live
// while the board is open, so a switch only shows another terminal, with no
// reconnect, no replay and no blank frame. A native terminal keeps its own
// socket; Herdr views stay within Herdr's stream limit. The shown terminal
// fills the panel; the Overlord picks the goblin on the board. A goblin's
// sub-agent (child) shows its own record as its terminal; any other baby
// goblin shows its goblin's terminal.
export function TerminalDeck({ snapshot, task, node, child, cfo, shown, connected, focus, onOwner }: {
  snapshot: Snapshot; task?: Task; node?: Session; child?: TreeNode; cfo: boolean; shown: boolean; connected: boolean; focus: number; onOwner?: () => void;
}) {
  const owner = !!task && (!!task.generation || !!sessionEnd(task)) && (!node || ownsTaskSession(node, task));
  const agent = child?.kind === "subagent" ? child : undefined;
  const key = cfo ? CFO_KEY : owner && !agent ? task.id : "";
  const [live, setLive] = useState<string[]>([]);
  const herdr = (candidate: string) => {
    if (candidate === CFO_KEY) return cfoView(snapshot).kind === "herdr";
    const each = snapshot.tasks.find((task) => task.id === candidate);
    return !!each?.generation && !sessionEnd(each) && goblinView(each).kind === "herdr";
  };
  const front = shown && key ? key : live[0];
  const next = front ? keepLive(live, front, herdr) : live;
  if (next.length !== live.length || next.some((entry, index) => entry !== live[index])) setLive(next);
  const idle = idleView(task, node);
  return <div className="terminal-deck" hidden={!shown}>
    <div className="deck-stage">
      {live.map((entry) => {
        const here = shown && entry === key;
        // A CFO in a native terminal is shown from its host like a native goblin.
        if (entry === CFO_KEY) {
          const view = cfoView(snapshot);
          return <div className="deck-slot" key={entry} hidden={!here}>
            {view.kind === "host" ? <HostTerminal query={view.query} harness={snapshot.sessions.filter((session) => session.role === "cfo" && session.host_id === snapshot.cfo_terminal).at(-1)?.harness || ""} label="CFO terminal" instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />
              : view.kind === "herdr" ? <NativeTerminal harness={snapshot.cfo_harness} instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />
              : <TerminalEmpty text={view.text} />}
          </div>;
        }
        const each = snapshot.tasks.find((candidate) => candidate.id === entry);
        if (!each) return null;
        const ended = sessionEnd(each);
        if (ended) return <div className="deck-slot" key={entry} hidden={!here}><EndedSession task={each} kind={ended} /></div>;
        if (!each.generation) return null;
        const view = goblinView(each);
        return <div className="deck-slot" key={entry} hidden={!here}>
          {view.kind === "host"
            ? <HostTerminal query={view.query} harness={each.harness} label="Goblin terminal" instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />
            : view.kind === "herdr" ? <NativeTerminal task={each} node={snapshot.sessions.find((session) => ownsTaskSession(session, each))} harness={each.harness} instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />
            : <TerminalEmpty text={view.text} />}
        </div>;
      })}
      {shown && agent && task && <div className="deck-slot"><AgentTerminal task={task} child={agent} visible={connected} /></div>}
      {/* A queued task or a child session has no terminal of its own to keep. */}
      {shown && !key && !agent && <div className="deck-slot"><TerminalEmpty text={idle.text} onOwner={node ? onOwner : undefined} /></div>}
    </div>
  </div>;
}
