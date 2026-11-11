import type { Snapshot } from "./types";
import { HarnessMark } from "./harness-mark";
import { ceoEngine, harnessTip } from "./workflow";

// The mark of the harness the registered CEO runs, with the model and effort
// its engine record names in its tip, as a goblin's card names its task
// record's, on the CEO's card on the canvas.
export function CeoHarnessMark({ snapshot }: { snapshot: Snapshot }) {
  const harness = snapshot.ceo_harness;
  const { model, effort } = ceoEngine(snapshot);
  return harness ? <HarnessMark harness={harness} label={harnessTip(harness, model, effort)} /> : null;
}
