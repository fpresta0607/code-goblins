import { BABY_NAMES, type Baby } from "./fleet-tree";
import "./fleet-tree.css";

// A child of a goblin drawn as a baby goblin, whose prop says what kind it
// is: one head from the generated atlas goblin-babies.png. Its tip names the
// kind; the label beside it says what it does. A silent one wears an amber
// ring.
export function BabyGoblin({ baby, silent = false, small = false }: { baby: Baby; silent?: boolean; small?: boolean }) {
  return <span className={"baby-goblin baby-" + baby + (small ? " small" : "") + (silent ? " silent" : "")}
    role="img" aria-label={BABY_NAMES[baby]} data-tip={BABY_NAMES[baby]} />;
}
