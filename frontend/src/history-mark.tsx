import { Avatar } from "./Avatar";
import { Icon, type IconName } from "./Icon";

export function HistoryMark({ icon, tone, label }: { icon: IconName; tone: string; label: string }) {
  return <span className={"history-mark " + tone} role="img" aria-label={label}>
    <Avatar persona="cfo" small />
    <span className="mark-badge"><Icon name={icon} /></span>
  </span>;
}
