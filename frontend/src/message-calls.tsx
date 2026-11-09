import { callTally } from "./call-words";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import { MessageCall } from "./message-call";
import { callRuns, type MessageBlock } from "./session-messages";
import type { Task, TreeNode } from "./types";

// A run of a reply's calls made one after another, as Claude's own app shows
// them (#2191): calls that finished as asked, two or more in a row, fold into
// one row, a list's icon and what they did counted, such as Ran 5 commands,
// that opens to each call's own row; a call still running stays one live row
// with its running verb beside them, and an error, a question, pictures, a
// plan and a baby goblin are never folded (callRuns). at is where the run
// starts in its reply, and fold keeps each fold's state by key, so a fold
// stays open as a call beside it finishes and joins it.
export function MessageCalls({ calls, at, task, picture, onChild, fold }: {
  calls: MessageBlock[]; at: number; task?: Task; picture: (id: string) => string; onChild: (child: TreeNode, source: HTMLElement) => void;
  fold: (key: string) => { open: boolean; onOpenChange: (isOpen: boolean) => void };
}) {
  const row = (call: MessageBlock, index: number) => <MessageCall key={"call:" + index} call={call} task={task} picture={picture} onChild={onChild} {...fold("call:" + at + ":" + index)} />;
  return <div className="message-steps">{callRuns(calls).map((run) => run.kind === "call" ? row(run.call, run.at)
    : <Disclosure key={"calls:" + run.at} kind="disclosure-line message-call message-call-group" {...fold("calls:" + at + ":" + run.at)}
      title={<><span className="call-icon"><Icon name="list" /></span><span className="call-what"><span className="call-verb">{callTally(run.calls.map((call) => call.tool))}</span></span></>}>
      {run.calls.map((call, offset) => row(call, run.at + offset))}
    </Disclosure>)}</div>;
}
