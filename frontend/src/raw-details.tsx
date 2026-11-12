import { Disclosure } from "./Disclosure";
import { linkedSentence, linkedText, webAddresses } from "./messageText";
import type { SaidLink } from "./task-words";

// The words behind a plain sentence, one click away: what a goblin or the
// supervisor wrote, as written, so a failure can still be diagnosed. isPlain
// marks a description written for a person, which reads as the panel's prose.
// Each address a line holds is a link, one the board knows read by its short
// name with its whole address in its tip, so a failure stays diagnosable, and
// a plain line links the words it says the report's addresses by.
export function RawDetails({ lines, isPlain = false, links = [] }: { lines: string[]; isPlain?: boolean; links?: SaidLink[] }) {
  if (!lines.length) return null;
  return <Disclosure kind="disclosure-line raw-details" title="Details">
    <div className={"raw-details-text" + (isPlain ? " plain" : "")}>{lines.map((line, index) => <p key={index}>{webAddresses(line).length ? linkedText(line) : linkedSentence(line, links)}</p>)}</div>
  </Disclosure>;
}
