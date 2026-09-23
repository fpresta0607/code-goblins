export function age(timestamp: string): string {
  if (!timestamp || timestamp.startsWith("0001")) return "No evidence";
  const seconds = Math.max(0, Math.floor((Date.now() - Date.parse(timestamp)) / 1000));
  if (!Number.isFinite(seconds)) return "Unknown freshness";
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

export function Badge({ phase }: { phase: string }) {
  const labels: Record<string, string> = {
    working: "In progress",
    ready: "Checks passed",
    merged: "Verify landed content",
    done: "Delivered",
  };
  return <span className={`badge phase-${phase}`}>{labels[phase] || phase || "Unknown"}</span>;
}
