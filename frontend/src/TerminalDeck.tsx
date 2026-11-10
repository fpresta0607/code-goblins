import type { ReactNode } from "react";
import type { Session, Snapshot, Task, TreeNode } from "./types";
import { ownsTaskSession } from "./lineageTree";
import { NativeTerminal } from "./NativeTerminal";
import { HostTerminal } from "./HostTerminal";
import { TerminalEmpty } from "./TerminalEmpty";
import { AgentTerminal } from "./agent-terminal";
import { EndedSession } from "./ended-session";
import { sessionEnd } from "./session-end";
import { CFO_KEY, cfoView, goblinView, idleView } from "./terminalOrder";
import { Composer } from "./composer";
import { takesMessages } from "./messages";
import { messagesQuery } from "./session-messages";
import { taskStatus } from "./task-status";
import { goblinName, terminalOf } from "./task-words";

// The terminal deck draws the terminal of the session the panel shows and no
// other: a session he left keeps no connection, offers no link and holds no
// key or microphone of its own, so nothing typed or said reaches it from
// another session's pane, and a switch back connects afresh and replays its
// screen (the Overlord, 2026-11-04). A panel that shows nothing has no deck,
// so a closed one keeps no terminal either, and its session's Task view, which
// a panel still shows, keeps its own. The shown terminal fills the panel; the
// Overlord picks the goblin on the board. A goblin's baby goblin (child) shows
// its own output as its terminal, never its goblin's (the Overlord,
// 2026-10-13). A goblin's slot whose terminal cannot take typing now, a goblin
// paused, resuming or starting, has a message box under what it shows, so what
// he writes is queued and delivered once (the Overlord, 2026-10-08), unless
// its session has a Messages view, whose box does that. The CFO's slot has one
// too while no CFO runs, closed or restarting, since a message starts a
// session that is not running (the Overlord, 2026-11-04). Each native terminal
// offers its link by its key (terminal-link.ts), so a Messages view's box
// types through it. behind keeps that connection parsing for Chat, with its
// visual surface detached until the terminal is requested.
export function TerminalDeck({ snapshot, task, node, child, cfo, shown, behind = false, hasComposer = false, connected, focus, onOwner, onHistory }: {
  snapshot: Snapshot; task?: Task; node?: Session; child?: TreeNode; cfo: boolean; shown: boolean; behind?: boolean; hasComposer?: boolean; connected: boolean; focus: number; onOwner?: () => void; onHistory?: () => void;
}) {
  const owner = !!task && (!!task.generation || !!sessionEnd(task)) && (!node || ownsTaskSession(node, task));
  const key = cfo ? CFO_KEY : owner && !child ? task.id : "";
  const idle = idleView(task, node, task && taskStatus(task, snapshot).text);
  const isShown = shown && !behind;
  const slot = (key: string, view: ReactNode) => <div className="deck-slot" key={key} hidden={!shown}>{view}</div>;
  // A box is keyed by its goblin, since the idle slot shows one goblin after
  // another and each keeps a draft of its own; a box to no goblin is the CFO's.
  const messaging = (key: string, view: ReactNode, to?: Task) => messagesQuery(snapshot, to) ? slot(key, view)
    : <div className="deck-slot with-messages" key={key} hidden={!shown}>{view}<Composer key={to?.id ?? CFO_KEY} snapshot={snapshot} task={to} name={to ? goblinName(to) : "CEO"} terminal={to?.id ?? CFO_KEY} /></div>;
  const terminal = (): ReactNode => {
    // A CFO in a native terminal is shown from its host like a native goblin.
    if (key === CFO_KEY) {
      const view = cfoView(snapshot);
      if (view.kind === "empty") return messaging(key, <TerminalEmpty text={view.text} />);
      return slot(key, view.kind === "host" ? <HostTerminal key={view.query} query={view.query} harness={snapshot.cfo_harness} label="CEO terminal" reportAs="the CEO's terminal" instance={snapshot.instance} visible={connected} shown={isShown} focus={shown ? focus : 0} link={key} hasComposer={shown && hasComposer} onHistory={shown ? onHistory : undefined} />
        : <NativeTerminal harness={snapshot.cfo_harness} instance={snapshot.instance} visible={connected} shown={isShown} focus={isShown ? focus : 0} />);
    }
    const each = snapshot.tasks.find((candidate) => candidate.id === key);
    if (!each) return null;
    const ended = sessionEnd(each);
    if (ended) {
      const view = <EndedSession task={each} kind={ended} />;
      return takesMessages(each) ? messaging(key, view, each) : slot(key, view);
    }
    if (!each.generation) return null;
    const view = goblinView(each);
    if (view.kind === "empty") return takesMessages(each) ? messaging(key, <TerminalEmpty text={view.text} />, each) : slot(key, <TerminalEmpty text={view.text} />);
    return slot(key, view.kind === "host"
      ? <HostTerminal key={view.query} query={view.query} harness={each.harness} label="Goblin terminal" reportAs={terminalOf(each)} instance={snapshot.instance} visible={connected} shown={isShown} focus={shown ? focus : 0} link={key} hasComposer={shown && hasComposer} onHistory={shown ? onHistory : undefined} />
      : <NativeTerminal task={each} node={snapshot.sessions.find((session) => ownsTaskSession(session, each))} harness={each.harness} instance={snapshot.instance} visible={connected} shown={isShown} focus={isShown ? focus : 0} />);
  };
  return <div className={"terminal-deck" + (behind ? " behind" : "")} hidden={!shown}>
    <div className="deck-stage">
      {key && terminal()}
      {shown && child && task && <div className="deck-slot"><AgentTerminal key={task.id + " " + child.id} task={task} child={child} visible={connected} /></div>}
      {/* A queued task or a child session has no terminal of its own. */}
      {shown && !key && !child && (task && takesMessages(task) ? messaging("idle", <TerminalEmpty text={idle.text} />, task)
        : slot("idle", <TerminalEmpty text={idle.text} onOwner={node ? onOwner : undefined} />))}
    </div>
  </div>;
}
