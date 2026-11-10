import { CallPictures } from "./call-pictures";
import { callTally } from "./call-words";
import { callPictures } from "./conversation-pictures";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import { MessageCall } from "./message-call";
import { callRuns, type MessageBlock } from "./session-messages";
import type { Task, TreeNode } from "./types";

// A run of a reply's calls made one after another, as Claude's own app shows
// them (#2191): calls that finished as asked, two or more in a row, fold into
// one row, a list's icon and what they did counted, such as Ran 5 commands,
// with the thumbnails of the pictures they hold at its end (CallPictures),
// that opens to each call's own row; a call still running stays one live row
// with its running verb beside them, and an error, a question, a plan and a
// baby goblin are never folded (callRuns). at is where the run
// starts in its reply. What is open is kept by call, by its id or else its
// place in the reply, never by fold: a fold is open while any call in it was
// open, its own row or the fold it stood in, so a row he opened stays open in
// the fold it joins, and a fold he opened stays open as a call on either side
// finishes and joins it.
export function MessageCalls({ calls, at, task, picture, onChild, fold }: {
  calls: MessageBlock[]; at: number; task?: Task; picture: (id: string) => string; onChild: (child: TreeNode, source: HTMLElement) => void;
  fold: (key: string) => { open: boolean; onOpenChange: (isOpen: boolean) => void };
}) {
  const id = (call: MessageBlock, index: number) => call.call_id || at + ":" + index;
  const row = (call: MessageBlock, key: string, state: ReturnType<typeof fold>) => <MessageCall key={key} call={call} task={task} picture={picture} onChild={onChild} {...state} />;
  return <div className="message-steps">{callRuns(calls).map((run) => {
    if (run.kind === "call") return row(run.call, "call:" + id(run.call, run.at), fold("call:" + id(run.call, run.at)));
    const ids = run.calls.map((call, offset) => id(call, run.at + offset));
    const rows = ids.map((each) => fold("call:" + each));
    const folds = ids.map((each) => fold("fold:" + each));
    const pictures = callPictures(run.calls, picture);
    const onOpenChange = (isOpen: boolean) => {
      folds.forEach((each) => each.onOpenChange(isOpen));
      if (!isOpen) rows.forEach((each) => each.onOpenChange(false));
    };
    return <Disclosure key={"calls:" + ids[0]} kind="disclosure-line message-call message-call-group" open={[...rows, ...folds].some((each) => each.open)} onOpenChange={onOpenChange}
      title={<><span className="call-icon"><Icon name="list" /></span><span className="call-what"><span className="call-verb">{callTally(run.calls.map((call) => call.tool))}</span></span>
        {pictures.length > 0 && <span className="call-end"><CallPictures pictures={pictures} /></span>}</>}>
      {run.calls.map((call, offset) => row(call, "call:" + ids[offset], { open: rows[offset].open, onOpenChange: (isOpen) => {
        folds[offset].onOpenChange(true);
        rows[offset].onOpenChange(isOpen);
      } }))}
    </Disclosure>;
  })}</div>;
}
