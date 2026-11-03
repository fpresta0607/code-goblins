import { decisionAndDetails, messageElements, type PathRenderer } from "./messageText";
import { copyValues } from "./CopyValue";
import { Disclosure } from "./Disclosure";

// A question's words on its card: the decision as body text, which names the
// card, and the evidence after its "Details:" line folded under Technical
// details, which opens by click, tap, Enter or Space and mounts only while
// open. A question without that line shows whole, as its asker wrote it.
export function QuestionText({ id, text, path }: { id: string; text: string; path?: PathRenderer }) {
  const { decision, details } = decisionAndDetails(text);
  return <>
    <div className="question-body question-decision" id={id} tabIndex={-1}>{messageElements(decision, copyValues, path)}</div>
    {details && <Disclosure kind="disclosure-line question-details" title="Technical details">
      <div className="question-body question-evidence">{messageElements(details, copyValues, path)}</div>
    </Disclosure>}
  </>;
}
