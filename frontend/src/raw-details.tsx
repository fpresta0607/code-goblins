import { Disclosure } from "./Disclosure";

// The words behind a plain sentence, one click away: what a goblin or the
// supervisor wrote, as written, so a failure can still be diagnosed. label
// is More for a note, Details for raw text. isPlain marks a description
// written for a person, which reads as the panel's prose.
export function RawDetails({ lines, label = "Details", isPlain = false }: { lines: string[]; label?: "Details" | "More"; isPlain?: boolean }) {
  if (!lines.length) return null;
  return <Disclosure kind="disclosure-line raw-details" title={label}>
    <div className={"raw-details-text" + (isPlain ? " plain" : "")}>{lines.map((line, index) => <p key={index}>{line}</p>)}</div>
  </Disclosure>;
}
