// age says how long before now a time was, in whole minutes or longer. now is
// the app's clock, which steps a minute: seconds read while drawing stood still
// between redraws and then jumped (#1641).
export function age(timestamp: string, now: number): string {
  if (!timestamp || timestamp.startsWith("0001")) return "No evidence";
  const minutes = Math.max(0, Math.floor((now - Date.parse(timestamp)) / 60_000));
  if (!Number.isFinite(minutes)) return "Unknown freshness";
  if (minutes < 1) return "under a minute ago";
  if (minutes < 60) return `${minutes}m ago`;
  if (minutes < 1440) return `${Math.floor(minutes / 60)}h ago`;
  return `${Math.floor(minutes / 1440)}d ago`;
}
