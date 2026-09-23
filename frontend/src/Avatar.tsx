import type { Persona } from "./workflow";

// Original generated PNG atlas, consumed unchanged. Persona is a visual aid,
// never evidence of a native role or a randomly selected agent identity.
export function Avatar({ persona, small = false }: { persona: Persona; small?: boolean }) {
  return <span className={"goblin-avatar persona-" + persona + (small ? " small" : "")} aria-hidden="true" />;
}
