import { useState, type ReactNode } from "react";
import type { Session, Snapshot, Task, TreeNode } from "./types";
import { ownsTaskSession } from "./lineageTree";
import { NativeTerminal } from "./NativeTerminal";
import { HostTerminal } from "./HostTerminal";
import { TerminalEmpty } from "./TerminalEmpty";
import { AgentTerminal } from "./agent-terminal";
import { EndedSession } from "./ended-session";
import { sessionEnd } from "./session-end";
import { CFO_KEY, cfoView, goblinView, idleView, keepLive } from "./terminalOrder";
import { Composer } from "./composer";
import { takesMessages } from "./messages";
import { messagesQuery } from "./session-messages";
import { taskStatus } from "./task-status";
import { goblinName, terminalOf } from "./task-words";

// The terminal deck keeps opened terminal components and their connections
// alive while the board is open, so switching does not reconnect or replay.
// A native terminal keeps its own socket; Herdr views stay within Herdr's
// stream limit. The shown terminal
// fills the panel; the Overlord picks the goblin on the board. A goblin's
// baby goblin (child) shows its own output as its terminal, never its
// goblin's (the Overlord, 2026-10-13). A goblin's slot whose terminal cannot
// take typing now, a goblin paused, resuming or starting, has a message box
// under what it shows, so what he writes is queued and delivered once (the
// Overlord, 2026-10-08), unless its session has a Messages view, whose box
// does that. The CFO's slot has none: while no CFO runs, closed or
// restarting, it only says so (the Overlord, 2026-10-09). Each native
// terminal offers its link by its key (terminal-link.ts), so a Messages
// view's box types through it. behind keeps that connection parsing for
// Chat, with its visual surface detached until the terminal is requested.
export function TerminalDeck({ snapshot, task, node, child, cfo, shown, behind = false, hasComposer = false, connected, focus, onOwner, onHistory }: {
  snapshot: Snapshot; task?: Task; node?: Session; child?: TreeNode; cfo: boolean; shown: boolean; behind?: boolean; hasComposer?: boolean; connected: boolean; focus: number; onOwner?: () => void; onHistory?: () => void;
}) {
  const owner = !!task && (!!task.generation || !!sessionEnd(task)) && (!node || ownsTaskSession(node, task));
  const key = cfo ? CFO_KEY : owner && !child ? task.id : "";
  const [live, setLive] = useState<string[]>([]);
  const herdr = (candidate: string) => {
    if (candidate === CFO_KEY) return cfoView(snapshot).kind === "herdr";
    const each = snapshot.tasks.find((task) => task.id === candidate);
    return !!each?.generation && !sessionEnd(each) && goblinView(each).kind === "herdr";
  };
  const front = shown && key ? key : live[0];
  const next = front ? keepLive(live, front, herdr) : live;
  if (next.length !== live.length || next.some((entry, index) => entry !== live[index])) setLive(next);
  const idle = idleView(task, node, task && taskStatus(task, snapshot).text);
  const slot = (key: string, here: boolean, view: ReactNode) => <div className="deck-slot" key={key} hidden={!here}>{view}</div>;
  // A box is keyed by its goblin, since the idle slot shows one goblin after
  // another and each keeps a draft of its own.
  const messaging = (key: string, here: boolean, view: ReactNode, to: Task) => messagesQuery(snapshot, to) ? slot(key, here, view)
    : <div className="deck-slot with-messages" key={key} hidden={!here}>{view}<Composer key={to.id} snapshot={snapshot} task={to} name={goblinName(to)} terminal={to.id} /></div>;
  return <div className={"terminal-deck" + (behind ? " behind" : "")} hidden={!shown}>
    <div className="deck-stage">
      {live.map((entry) => {
        const here = shown && entry === key;
        const isShown = here && !behind;
        // A CFO in a native terminal is shown from its host like a native goblin.
        if (entry === CFO_KEY) {
          const view = cfoView(snapshot);
          if (view.kind === "empty") return slot(entry, here, <TerminalEmpty text={view.text} />);
          return slot(entry, here, view.kind === "host" ? <HostTerminal key={view.query} query={view.query} harness={snapshot.cfo_harness} label="CFO terminal" reportAs="the CFO's terminal" instance={snapshot.instance} visible={connected} shown={isShown} focus={isShown ? focus : 0} link={entry} hasComposer={here && hasComposer} onHistory={here ? onHistory : undefined} />
            : <NativeTerminal harness={snapshot.cfo_harness} instance={snapshot.instance} visible={connected} shown={isShown} focus={isShown ? focus : 0} />);
        }
        const each = snapshot.tasks.find((candidate) => candidate.id === entry);
        if (!each) return null;
        const ended = sessionEnd(each);
        if (ended) {
          const view = <EndedSession task={each} kind={ended} />;
          return takesMessages(each) ? messaging(entry, here, view, each) : slot(entry, here, view);
        }
        if (!each.generation) return null;
        const view = goblinView(each);
        if (view.kind === "empty") return takesMessages(each) ? messaging(entry, here, <TerminalEmpty text={view.text} />, each) : slot(entry, here, <TerminalEmpty text={view.text} />);
        return slot(entry, here, view.kind === "host"
          ? <HostTerminal key={view.query} query={view.query} harness={each.harness} label="Goblin terminal" reportAs={terminalOf(each)} instance={snapshot.instance} visible={connected} shown={isShown} focus={isShown ? focus : 0} link={entry} hasComposer={here && hasComposer} onHistory={here ? onHistory : undefined} />
          : <NativeTerminal task={each} node={snapshot.sessions.find((session) => ownsTaskSession(session, each))} harness={each.harness} instance={snapshot.instance} visible={connected} shown={isShown} focus={isShown ? focus : 0} />);
      })}
      {shown && child && task && <div className="deck-slot"><AgentTerminal key={task.id + " " + child.id} task={task} child={child} visible={connected} /></div>}
      {/* A queued task or a child session has no terminal of its own to keep. */}
      {shown && !key && !child && (task && takesMessages(task) ? messaging("idle", true, <TerminalEmpty text={idle.text} />, task)
        : slot("idle", true, <TerminalEmpty text={idle.text} onOwner={node ? onOwner : undefined} />))}
    </div>
  </div>;
}
