// The words behind a plain sentence, one click away: what a goblin or the
// supervisor wrote, as written, so a failure can still be diagnosed. label
// is More for a note, Details for raw text.
export function RawDetails({ lines, label = "Details" }: { lines: string[]; label?: "Details" | "More" }) {
  if (!lines.length) return null;
  return <details className="raw-details">
    <summary>{label}</summary>
    <div className="raw-details-text">{lines.map((line, index) => <p key={index}>{line}</p>)}</div>
  </details>;
}
