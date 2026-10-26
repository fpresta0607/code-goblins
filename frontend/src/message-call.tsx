import { BabyGoblin } from "./BabyGoblin";
import { Disclosure } from "./Disclosure";
import { babyName } from "./fleet-tree";
import { Icon, type IconName } from "./Icon";
import { MessagePictures } from "./message-pictures";
import type { CallState, MessageBlock } from "./session-messages";
import type { Task, TreeNode } from "./types";

const STATE_WORDS: Record<CallState, string> = { running: "Running", done: "", failed: "Failed", denied: "Denied", interrupted: "Interrupted", "": "" };
const STATE_MARKS: Record<CallState, IconName> = { running: "clock", done: "check", failed: "warning", denied: "lock", interrupted: "pause", "": "check" };

// A tool call in a reply, as one row: the mark of its outcome, its tool in
// bold and what it was called on, and at its end its state while it is not
// done, or an edit's added and removed lines. What came back, an edit's diff
// and its pictures fold under the row, in the board's one Disclosure. A call
// that started a sub-agent is its baby goblin's row instead, whose Open shows
// that baby goblin's terminal while its goblin's tree holds it.
export function MessageCall({ call, task, picture, onChild }: {
  call: MessageBlock; task?: Task; picture: (id: string) => string; onChild: (child: TreeNode, source: HTMLElement) => void;
}) {
  if (call.agent) {
    const node = call.agent.node;
    const child = task?.tree?.children.find((each) => each.id === node);
    return <div className={"message-baby " + call.state}>
      <BabyGoblin baby="subagent" small hasTip={false} />
      <span className="call-what"><strong>{task && child ? babyName(task, child) : "Baby goblin"}</strong> <span className="call-subject">{[call.agent.type, call.subject].filter(Boolean).join(" · ")}</span></span>
      <span className="call-end">{STATE_WORDS[call.state]}
        {child && <button type="button" className="text-button" onClick={(event) => onChild(child, event.currentTarget)}>Open<Icon name="next" /></button>}</span>
    </div>;
  }
  const head = <>
    <span className={"call-mark " + call.state}><Icon name={STATE_MARKS[call.state]} /></span>
    <span className="call-what"><strong>{call.tool}</strong> {call.subject && <span className="call-subject">{call.subject}</span>}</span>
    <span className="call-end">{call.edit ? <><span className="call-added">+{call.edit.added}</span><span className="call-removed">−{call.edit.removed}</span></> : STATE_WORDS[call.state]}</span>
  </>;
  if (!call.result && !call.edit?.diff.length && !call.pictures.length) return <div className="message-call">{head}</div>;
  return <Disclosure kind="disclosure-line message-call" title={head}>
    {call.edit?.diff.length
      ? <div className="message-diff">{call.edit.diff.map((line, at) => <div key={at} className={line.startsWith("+") ? "line-added" : line.startsWith("-") ? "line-removed" : undefined}>{line}</div>)}</div>
      : call.result && <pre className="call-result">{call.result}</pre>}
    <MessagePictures pictures={call.pictures} picture={picture} />
  </Disclosure>;
}
