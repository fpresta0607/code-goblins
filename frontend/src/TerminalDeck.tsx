import { useState, type ReactNode } from "react";
import type { Session, Snapshot, Task } from "./types";
import { ownsTaskSession } from "./lineageTree";
import { NativeTerminal } from "./NativeTerminal";
import { HostTerminal } from "./HostTerminal";
import { TerminalEmpty } from "./TerminalEmpty";
import { EndedSession } from "./ended-session";
import { sessionEnd } from "./session-end";
import { CFO_KEY, cfoView, goblinView, idleView, keepLive } from "./terminalOrder";
import { MessageBox } from "./message-box";
import { takesMessages } from "./messages";
import { goblinName } from "./task-words";

// The terminal deck: every terminal the Overlord opens stays mounted and live
// while the board is open, so a switch only shows another terminal, with no
// reconnect, no replay and no blank frame. A native terminal keeps its own
// socket; Herdr views stay within Herdr's stream limit. The shown terminal
// fills the panel; the Overlord picks the goblin on the board. A slot whose
// terminal cannot take typing now, a goblin paused, resuming or starting or
// no CFO running, has a message box under what it shows, so what he writes
// is queued and delivered once (the Overlord, 2026-10-08).
export function TerminalDeck({ snapshot, task, node, cfo, shown, connected, focus, onOwner }: {
  snapshot: Snapshot; task?: Task; node?: Session; cfo: boolean; shown: boolean; connected: boolean; focus: number; onOwner?: () => void;
}) {
  const owner = !!task && (!!task.generation || !!sessionEnd(task)) && (!node || ownsTaskSession(node, task));
  const key = cfo ? CFO_KEY : owner ? task.id : "";
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
  const slot = (key: string, here: boolean, view: ReactNode) => <div className="deck-slot" key={key} hidden={!here}>{view}</div>;
  const messaging = (key: string, here: boolean, view: ReactNode, name: string, to?: Task) => <div className="deck-slot with-messages" key={key} hidden={!here}>{view}<MessageBox snapshot={snapshot} task={to} name={name} /></div>;
  return <div className="terminal-deck" hidden={!shown}>
    <div className="deck-stage">
      {live.map((entry) => {
        const here = shown && entry === key;
        // A CFO in a native terminal is shown from its host like a native goblin.
        if (entry === CFO_KEY) {
          const view = cfoView(snapshot);
          if (view.kind === "empty") return messaging(entry, here, <TerminalEmpty text={view.text} />, "the CFO");
          return slot(entry, here, view.kind === "host" ? <HostTerminal query={view.query} harness={snapshot.sessions.filter((session) => session.role === "cfo" && session.host_id === snapshot.cfo_terminal).at(-1)?.harness || ""} label="CFO terminal" instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />
            : <NativeTerminal harness={snapshot.cfo_harness} instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />);
        }
        const each = snapshot.tasks.find((candidate) => candidate.id === entry);
        if (!each) return null;
        const ended = sessionEnd(each);
        if (ended) return takesMessages(each) ? messaging(entry, here, <EndedSession task={each} kind={ended} />, goblinName(each), each) : slot(entry, here, <EndedSession task={each} kind={ended} />);
        if (!each.generation) return null;
        const view = goblinView(each);
        if (view.kind === "empty") return takesMessages(each) ? messaging(entry, here, <TerminalEmpty text={view.text} />, goblinName(each), each) : slot(entry, here, <TerminalEmpty text={view.text} />);
        return slot(entry, here, view.kind === "host"
          ? <HostTerminal query={view.query} harness={each.harness} label="Goblin terminal" instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />
          : <NativeTerminal task={each} node={snapshot.sessions.find((session) => ownsTaskSession(session, each))} harness={each.harness} instance={snapshot.instance} visible={connected} shown={here} focus={here ? focus : 0} />);
      })}
      {/* A queued task or a child session has no terminal of its own to keep. */}
      {shown && !key && (task && takesMessages(task) ? messaging("idle", true, <TerminalEmpty text={idle.text} />, goblinName(task), task)
        : slot("idle", true, <TerminalEmpty text={idle.text} onOwner={node ? onOwner : undefined} />))}
    </div>
  </div>;
}
