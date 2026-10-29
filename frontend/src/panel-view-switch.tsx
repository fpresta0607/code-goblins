import type { PanelView } from "./GoblinPanel";
import { Icon, type IconName } from "./Icon";

// The panel's one way between its conversation and its task panel: a
// segmented control of two buttons, never a fold or a dropdown (the
// Overlord, 2026-10-26: "i dont like how you remove the selector at the top
// between caht and task panel bring that back i dont like the drop down
// also"). Each button keeps the panel's one view, and the group names itself
// so a screen reader reads what it chooses. The buttons are native, so the
// keyboard reaches and presses them at every width. Each is an icon alone,
// named for a screen reader, with no words and no tip, in the panel header's
// own row beside the CFO's and AFK's controls, never a row of its own (the
// Overlord, 2026-10-28: "chat and task doesn't need to be full width, it can
// fit inline with cfo and afk and just be the icons").
const LABELS: Record<PanelView, string> = { terminal: "Chat", task: "Task" };
const ICONS: Record<PanelView, IconName> = { terminal: "comment", task: "task" };

export function PanelViewSwitch({ view, views, onView }: { view: PanelView; views: PanelView[]; onView: (view: PanelView) => void }) {
  if (views.length < 2) return null;
  return <div className="panel-view-switch" role="group" aria-label="Panel view">
    {(["terminal", "task"] as const).filter((each) => views.includes(each)).map((each) => <button key={each} type="button" aria-label={LABELS[each]} aria-pressed={view === each} onClick={() => onView(each)}><Icon name={ICONS[each]} /></button>)}
  </div>;
}
