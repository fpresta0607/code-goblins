import type { Snapshot } from "./types";
import { HarnessMark } from "./harness-mark";
import { cfoEngine, harnessTip } from "./workflow";

// The mark of the harness the registered CFO runs, with the model and effort
// its engine record names in its tip, as a goblin's card names its task
// record's, on the CFO's card on the canvas.
export function CfoHarnessMark({ snapshot }: { snapshot: Snapshot }) {
  const harness = snapshot.cfo_harness;
  const { model, effort } = cfoEngine(snapshot);
  return harness ? <HarnessMark harness={harness} label={harnessTip(harness, model, effort)} /> : null;
}
