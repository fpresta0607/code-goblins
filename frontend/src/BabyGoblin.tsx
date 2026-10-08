import { BABY_NAMES, type Baby } from "./fleet-tree";
import "./fleet-tree.css";

// A child of a goblin drawn as a baby goblin, whose prop says what kind it
// is: one head from the generated atlas goblin-babies.png. Its tip names the
// kind, unless it is part of a card whose own tip says more; the label beside
// it says what it does.
export function BabyGoblin({ baby, small = false, hasTip = true }: { baby: Baby; small?: boolean; hasTip?: boolean }) {
  return <span className={"baby-goblin baby-" + baby + (small ? " small" : "")}
    role="img" aria-label={BABY_NAMES[baby]} {...hasTip ? { "data-tip": BABY_NAMES[baby] } : {}} />;
}
