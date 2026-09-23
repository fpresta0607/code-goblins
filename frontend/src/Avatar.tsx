import type { Persona } from "./workflow";

// Original generated PNG atlas, consumed unchanged. Persona is a visual aid,
// never evidence of a native role: a keyword match where the work names one,
// otherwise a stable choice per task so concurrent goblins look different.
export function Avatar({ persona, small = false }: { persona: Persona; small?: boolean }) {
  return <span className={"goblin-avatar persona-" + persona + (small ? " small" : "")} aria-hidden="true" />;
}
