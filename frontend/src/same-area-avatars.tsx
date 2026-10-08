import type { Overlap } from "./types";
import { PersonAvatar } from "./person-avatar";
import { sameArea, sameAreaWords } from "./people";
import "./people.css";

// How many avatars a card shows before the rest become a count.
const SHOWN = 3;

// On a task's card, the avatar of each person whose open work meets its
// branch, opening that work; who and what are in its tip. Past three, the
// rest are one +N whose tip names them, so the row stays inside the card; the
// goblin's panel names everyone.
export function SameAreaAvatars({ overlaps }: { overlaps: Overlap[] }) {
  const areas = sameArea(overlaps);
  if (!areas.length) return null;
  const rest = areas.slice(SHOWN);
  const more = "Also in the same area: " + rest.map(sameAreaWords).join(". ");
  return <span className="same-area-avatars">
    {areas.slice(0, SHOWN).map((area) => {
      const words = "In the same area: " + sameAreaWords(area);
      return area.url
        ? <a key={area.person.login || area.person.name} className="same-area-person" href={area.url} target="_blank" rel="noreferrer" aria-label={words} data-tip={words}><PersonAvatar person={area.person} /></a>
        : <span key={area.person.login || area.person.name} className="same-area-person" role="img" aria-label={words} data-tip={words}><PersonAvatar person={area.person} /></span>;
    })}
    {rest.length > 0 && <span className="same-area-more" role="img" aria-label={more} data-tip={more}>+{rest.length}</span>}
  </span>;
}
