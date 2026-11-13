import { Fragment, type ReactNode } from "react";
import { decisionAndDetails, messageElements, type PathRenderer } from "./messageText";
import { copyValues } from "./CopyValue";
import { Disclosure } from "./Disclosure";
import { fencedParts } from "./session-messages";
import { TerminalBlock } from "./terminal-block";

// words draws a question's words: a block it fences, such as what a command
// printed or the prompt it stopped at, as terminal text (#2524), and the
// rest as the question contract's text, shown as written.
const words = (text: string, path?: PathRenderer): ReactNode => fencedParts(text).map((part, at) => part.kind === "code" ? <TerminalBlock key={at} output={part.text} />
  : <Fragment key={at}>{messageElements(part.text, copyValues, path)}</Fragment>);

// A question's words on its card: the decision as body text, which names the
// card, and the evidence after its "Details:" line folded under Technical
// details, which opens by click, tap, Enter or Space and mounts only while
// open. A question without that line shows whole, as its asker wrote it,
// and a block either part fences reads as terminal text does everywhere.
export function QuestionText({ id, text, path }: { id: string; text: string; path?: PathRenderer }) {
  const { decision, details } = decisionAndDetails(text);
  return <>
    <div className="question-body question-decision" id={id} tabIndex={-1}>{words(decision, path)}</div>
    {details && <Disclosure kind="disclosure-line question-details" title="Technical details">
      <div className="question-body question-evidence">{words(details, path)}</div>
    </Disclosure>}
  </>;
}
