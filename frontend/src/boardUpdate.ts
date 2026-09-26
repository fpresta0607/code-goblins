// updateAction is what a tab does once the supervisor serves another board
// build than the one it loaded: nothing while the builds match or either is
// unknown; reload by itself only while the tab is hidden and the Overlord is
// not in the middle of an answer; otherwise offer a reload, so the board never
// reloads under his hands.
export function updateAction({ loaded, served, hidden, answering }: { loaded: string; served: string; hidden: boolean; answering: boolean }): "none" | "banner" | "reload" {
  if (!loaded || !served || loaded === served) return "none";
  return hidden && !answering ? "reload" : "banner";
}
