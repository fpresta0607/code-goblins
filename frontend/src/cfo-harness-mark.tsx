import type { Snapshot } from "./types";
import { HarnessMark } from "./harness-mark";
import { harnessTip } from "./workflow";

// The mark of the harness the registered CFO runs, with the model of its
// newest session in that harness in its tip, on the CFO's card on the
// canvas.
export function CfoHarnessMark({ snapshot }: { snapshot: Snapshot }) {
  const harness = snapshot.cfo_harness;
  const model = snapshot.sessions.filter((session) => session.role === "cfo" && session.harness === harness).at(-1)?.model || "";
  return harness ? <HarnessMark harness={harness} label={harnessTip(harness, model, "")} /> : null;
}
