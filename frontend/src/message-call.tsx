import { BabyGoblin } from "./BabyGoblin";
import { CallOpens } from "./call-opens";
import { CallPictures } from "./call-pictures";
import { callWords, commandLanguage } from "./call-words";
import { ConnectorLogo } from "./connector-logo";
import { callPictures } from "./conversation-pictures";
import { Disclosure } from "./Disclosure";
import { babyName, babyShortName, phaseOf, stateWord } from "./fleet-tree";
import { Icon, type IconName } from "./Icon";
import { MessagePictures } from "./message-pictures";
import { PlanCard } from "./plan-card";
import type { CallState, MessageBlock } from "./session-messages";
import { TerminalBlock } from "./terminal-block";
import type { TerminalResult } from "./terminal-text";
import type { Task, TreeNode } from "./types";
import { literalLinks } from "./messageText";

const STATE_WORDS: Record<CallState, string> = { running: "Running", done: "", failed: "Failed", denied: "Denied", interrupted: "Interrupted", "": "" };
const STATE_MARKS: Partial<Record<CallState, IconName>> = { running: "clock", done: "check", failed: "warning", denied: "lock", interrupted: "pause" };
// How a command ended, or that it still runs, as its open row's last line
// says under the row's own mark.
const RESULTS: Partial<Record<CallState, Omit<TerminalResult, "icon">>> = { running: { tone: "running", text: "Running" }, done: { tone: "success", text: "Completed" }, failed: { tone: "error", text: "Failed" }, denied: { tone: "warning", text: "Denied" }, interrupted: { tone: "warning", text: "Interrupted" } };

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
// loaded, all it was called on, a command with what it printed and how it
// ended in one terminal block (#2524), what any other call brought back in
// one, the diff of what a call edited in place of what it printed (#2534),
// its pictures, and under a command that drove the goblin's browser its
// Screen view (CallOpens). A call that started a
// sub-agent is its baby goblin's row instead, which leads with its
// description, as the rows of one round of sub-agents differ only in that
// (#2957), and says what every row repeats on one quiet line under it. While
// its goblin's tree holds the baby goblin, the row is one button named by its
// whole name that opens that baby goblin's terminal, its quiet line saying its
// short name, the kind of agent and the baby goblin's own state, not the
// call's, as a background sub-agent's call is done at once while its baby
// goblin works. Otherwise it says the call's own description, kind and state,
// with nothing to open. A call that wrote a plan is that plan's
// checklist card. Whether its fold is open is the
// view's to keep (open, onOpenChange).
export function MessageCall({ call, task, picture, onChild, open, onOpenChange }: {
  call: MessageBlock; task?: Task; picture: (id: string) => string; onChild: (child: TreeNode, source: HTMLElement) => void;
  open: boolean; onOpenChange: (isOpen: boolean) => void;
}) {
  if (call.agent) {
    const node = call.agent.node;
    const child = task?.tree?.children.find((each) => each.id === node);
    if (task && child) {
      const name = babyName(task, child), state = stateWord(child);
      const whoAndKind = [babyShortName(task, child), call.agent.type].filter(Boolean).join(" · ");
      return <button type="button" className={"message-baby " + call.state} aria-label={"Open " + name + ", " + state} onClick={(event) => onChild(child, event.currentTarget)}>
        <BabyGoblin baby="subagent" small hasTip={false} />
        <span className="baby-what"><strong>{child.label}</strong>
          <small>{whoAndKind && <span>{whoAndKind}</span>}<span className={"plain-status phase-" + phaseOf(child)}><span className="status-dot" />{state}</span></small></span>
        <Icon name="next" />
      </button>;
    }
    return <div className={"message-baby " + call.state}>
      <BabyGoblin baby="subagent" small hasTip={false} />
      <span className="baby-what"><strong>{call.subject || "Sub-agent"}</strong>
        <small>{call.agent.type && <span>{call.agent.type}</span>}<span className="call-end">{call.state === "done" ? "Done" : STATE_WORDS[call.state]}</span></small></span>
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
  const hasDiff = !!call.edit?.diff.length;
  const ending = RESULTS[call.state];
  return <Disclosure kind="disclosure-line message-call" title={head} open={open} onOpenChange={onOpenChange}>
    {words.connector && <p className="call-full call-raw">{call.tool}</p>}
    {words.loaded && words.loaded.length > 0 && <ul className="call-loaded">{words.loaded.map((name, at) => <li key={at}>{name}</li>)}</ul>}
    {language ? <TerminalBlock shell={language} command={input} output={hasDiff ? "" : call.result} diff={call.edit?.diff} result={ending && mark && { ...ending, icon: mark }} />
      : input && <p className="call-full">{literalLinks(input)}</p>}
    {!language && (hasDiff || call.result) && <TerminalBlock output={hasDiff ? "" : call.result} diff={call.edit?.diff} />}
    <MessagePictures sources={call.pictures.map((each) => picture(each.id))} />
    {language && <CallOpens command={input} />}
  </Disclosure>;
}
