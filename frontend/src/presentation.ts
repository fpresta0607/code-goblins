const UNDER_A_MINUTE = "under a minute ago";

// age says how long before now a time was, in whole minutes or longer. now is
// the app's clock, which steps a minute: seconds read while drawing stood still
// between redraws and then jumped (#1641).
export function age(timestamp: string, now: number): string {
  if (!timestamp || timestamp.startsWith("0001")) return "No evidence";
  const minutes = Math.max(0, Math.floor((now - Date.parse(timestamp)) / 60_000));
  if (!Number.isFinite(minutes)) return "Unknown freshness";
  if (minutes < 1) return UNDER_A_MINUTE;
  if (minutes < 60) return `${minutes}m ago`;
  if (minutes < 1440) return `${Math.floor(minutes / 60)}h ago`;
  return `${Math.floor(minutes / 1440)}d ago`;
}

// rowAge is age for a list row's narrow time column, which takes its width from
// the row's text: at phone width the words for a first minute left a History
// row's summary a pixel wide. It says them as the board's other timestamps do.
export function rowAge(timestamp: string, now: number): string {
  const said = age(timestamp, now);
  return said === UNDER_A_MINUTE ? "just now" : said;
}
