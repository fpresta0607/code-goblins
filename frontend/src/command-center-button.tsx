import type { Ref } from "react";
import { Icon } from "./Icon";
import "./lantern.css";

// The Command Center's button in the bar, the summary of its inbox's menu,
// with the count of what waits on him. Its count wears the lantern box before
// the Command Center's own code has arrived, so the lantern's styles come with
// the button.
export function CommandCenterButton({ needing, ref }: { needing: number; ref?: Ref<HTMLElement> }) {
  return <summary ref={ref} className="icon-button" aria-label={"Command Center" + (needing ? ", " + needing + " waiting on you" : "")}><Icon name="command-center" />{needing > 0 && <span className="count-badge" aria-hidden="true">{needing}</span>}</summary>;
}
