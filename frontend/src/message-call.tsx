import { BabyGoblin } from "./BabyGoblin";
import { callWords } from "./call-words";
import { Disclosure } from "./Disclosure";
import { babyName } from "./fleet-tree";
import { Icon, type IconName } from "./Icon";
import { MessagePictures } from "./message-pictures";
import { PlanCard } from "./plan-card";
import type { CallState, MessageBlock } from "./session-messages";
import type { Task, TreeNode } from "./types";

const STATE_WORDS: Record<CallState, string> = { running: "Running", done: "", failed: "Failed", denied: "Denied", interrupted: "Interrupted", "": "" };
const STATE_MARKS: Partial<Record<CallState, IconName>> = { running: "clock", done: "check", failed: "warning", denied: "lock", interrupted: "pause" };

// A tool call in a reply, as one quiet row: its tool's icon, the verb it is
// told by (Read, Edited, Ran, Searched), what it was called on, and at its
// end an edit's added and removed lines and the mark of its outcome, a quiet
// static one while it runs so the footer stays the conversation's one
// animation, with its state's word for a reader. What came back, an
// edit's diff and its pictures fold under the row in the board's one
// Disclosure, which mounts them only while it is open. A call that started a
// sub-agent is its baby goblin's row instead, whose Open shows that baby
// goblin's terminal while its goblin's tree holds it, and a call that wrote
// a plan is that plan's checklist card. Whether its fold is open is the
// view's to keep (open, onOpenChange).
export function MessageCall({ call, task, picture, onChild, open, onOpenChange }: {
  call: MessageBlock; task?: Task; picture: (id: string) => string; onChild: (child: TreeNode, source: HTMLElement) => void;
  open: boolean; onOpenChange: (isOpen: boolean) => void;
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
  if (call.plan.length) return <PlanCard steps={call.plan} />;
  const words = callWords(call.tool, call.state === "running");
  const mark = STATE_MARKS[call.state];
  const head = <>
    <span className="call-icon"><Icon name={words.icon} /></span>
    <span className="call-what"><span className="call-verb">{words.verb}</span> {call.subject && <span className="call-subject">{call.subject}</span>}</span>
    <span className="call-end">
      {call.edit && <><span className="call-added">+{call.edit.added}</span><span className="call-removed">−{call.edit.removed}</span></>}
      <span className={"call-mark " + call.state}>{mark && <Icon name={mark} />}</span>
      {STATE_WORDS[call.state] && <span className="sr-only">{STATE_WORDS[call.state]}</span>}
    </span>
  </>;
  if (!call.result && !call.edit?.diff.length && !call.pictures.length) return <div className="message-call">{head}</div>;
  return <Disclosure kind="disclosure-line message-call" title={head} open={open} onOpenChange={onOpenChange}>
    {call.edit?.diff.length
      ? <div className="message-diff">{call.edit.diff.map((line, at) => <div key={at} className={line.startsWith("+") ? "line-added" : line.startsWith("-") ? "line-removed" : undefined}>{line}</div>)}</div>
      : call.result && <pre className="call-result">{call.result}</pre>}
    <MessagePictures pictures={call.pictures} picture={picture} />
  </Disclosure>;
}
