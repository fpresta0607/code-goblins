import { BabyGoblin } from "./BabyGoblin";
import { CallPictures } from "./call-pictures";
import { callWords, commandLanguage } from "./call-words";
import { CodeBlock } from "./code-block";
import { ConnectorLogo } from "./connector-logo";
import { callPictures } from "./conversation-pictures";
import { Disclosure } from "./Disclosure";
import { babyName } from "./fleet-tree";
import { Icon, type IconName } from "./Icon";
import { MessagePictures } from "./message-pictures";
import { PlanCard } from "./plan-card";
import type { CallState, MessageBlock } from "./session-messages";
import type { Task, TreeNode } from "./types";
import { literalLinks } from "./messageText";

const STATE_WORDS: Record<CallState, string> = { running: "Running", done: "", failed: "Failed", denied: "Denied", interrupted: "Interrupted", "": "" };
const STATE_MARKS: Partial<Record<CallState, IconName>> = { running: "clock", done: "check", failed: "warning", denied: "lock", interrupted: "pause" };

// A tool call in a reply, as one quiet line: its tool's icon, the verb it is
// told by (Read, Edited, Ran, Searched), what it was called on cut with an
// ellipsis, and at its end an edit's added and removed lines and the mark of
// its outcome, before them the thumbnails of the pictures it read, took or
// produced (CallPictures), a quiet static mark while it runs so the end of the reply in
// progress stays the conversation's one animation, with its state's word for
// a reader. A connector's call wears that connector's logo and reads as its
// name and its tool in words, as Supabase · List projects, and a tool search
// wears a wrench and says how many tools it loaded (#2302). The line opens in
// the board's one Disclosure, which mounts what it holds only while it is
// open: a connector's tool as its harness names it, the tools a search
// loaded, all it was called on, a command in a code
// card, then what came back in one, an edit's diff and its pictures. A call that started a
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
  const words = callWords(call.tool, call.state === "running", call.subject);
  const mark = STATE_MARKS[call.state];
  // A search that loaded tools by name says how many, and its query, the
  // names as the harness writes them, waits in the open row.
  const subject = words.loaded ? "" : call.subject;
  const head = <>
    <span className="call-icon">{words.connector ? <ConnectorLogo name={words.connector} isInline /> : <Icon name={words.icon} />}</span>
    <span className="call-what"><span className="call-verb">{words.verb}</span> {words.action && <><span className="call-action">· {words.action}</span> </>}{subject && <span className="call-subject">{subject}</span>}</span>
    <span className="call-end">
      <CallPictures pictures={callPictures([call], picture)} />
      {call.edit && <><span className="call-added">+{call.edit.added}</span><span className="call-removed">−{call.edit.removed}</span></>}
      <span className={"call-mark " + call.state}>{mark && <Icon name={mark} />}</span>
      {STATE_WORDS[call.state] && <span className="sr-only">{STATE_WORDS[call.state]}</span>}
    </span>
  </>;
  if (!words.connector && !call.subject && !call.input && !call.result && !call.edit?.diff.length && !call.pictures.length) return <div className="message-call">{head}</div>;
  const language = commandLanguage(call.tool);
  const input = call.input || call.subject;
  return <Disclosure kind="disclosure-line message-call" title={head} open={open} onOpenChange={onOpenChange}>
    {words.connector && <p className="call-full call-raw">{call.tool}</p>}
    {words.loaded && words.loaded.length > 0 && <ul className="call-loaded">{words.loaded.map((name, at) => <li key={at}>{name}</li>)}</ul>}
    {input && (language ? <CodeBlock text={input} language={language} /> : <p className="call-full">{literalLinks(input)}</p>)}
    {call.edit?.diff.length
      ? <div className="message-diff">{call.edit.diff.map((line, at) => <div key={at} className={line.startsWith("+") ? "line-added" : line.startsWith("-") ? "line-removed" : undefined}>{line}</div>)}</div>
      : call.result && <CodeBlock text={call.result} language="output" textClass="call-result" />}
    <MessagePictures sources={call.pictures.map((each) => picture(each.id))} />
  </Disclosure>;
}
