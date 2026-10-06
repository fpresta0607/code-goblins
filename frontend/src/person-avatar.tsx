import { useState } from "react";
import type { Person } from "./types";
import { personName, safeAvatar } from "./people";
import "./people.css";

// A person's GitHub avatar, or the first letter of their name when GitHub
// has none for them or it does not load, as when the machine is offline.
export function PersonAvatar({ person }: { person: Person }) {
  const [isBroken, setBroken] = useState(false);
  const source = safeAvatar(person.avatar_url);
  if (!source || isBroken) return <span className="person-avatar initial" aria-hidden="true">{personName(person).slice(0, 1).toUpperCase()}</span>;
  return <img className="person-avatar" src={source} alt="" referrerPolicy="no-referrer" loading="lazy" onError={() => setBroken(true)} />;
}
