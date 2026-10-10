import { Disclosure } from "./Disclosure";

// The words behind a plain sentence, one click away: what a goblin or the
// supervisor wrote, as written, so a failure can still be diagnosed. isPlain
// marks a description written for a person, which reads as the panel's prose.
export function RawDetails({ lines, isPlain = false }: { lines: string[]; isPlain?: boolean }) {
  if (!lines.length) return null;
  return <Disclosure kind="disclosure-line raw-details" title="Details">
    <div className={"raw-details-text" + (isPlain ? " plain" : "")}>{lines.map((line, index) => <p key={index}>{line}</p>)}</div>
  </Disclosure>;
}
