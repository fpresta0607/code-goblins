import { AfkToggle } from "./afk-toggle";
import { CfoUpdate } from "./cfo-update";
import { RestartPcButton } from "./restart-pc-button";
import type { Snapshot } from "./types";

// The CFO's switches, AFK with the restart and update beside it, in the CFO
// panel's header, or in its "..." menu on a phone (phone-menu.tsx).
export function CfoSwitches({ snapshot }: { snapshot: Snapshot }) {
  return <AfkToggle afk={snapshot.afk} instance={snapshot.instance} leading={<><RestartPcButton snapshot={snapshot} /><CfoUpdate snapshot={snapshot} /></>} />;
}
