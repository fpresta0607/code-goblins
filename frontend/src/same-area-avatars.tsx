import type { Overlap } from "./types";
import { PersonAvatar } from "./person-avatar";
import { sameArea, sameAreaWords } from "./people";
import "./people.css";

// On a task's card, the avatar of each person whose open work meets its
// branch, opening that work; who and what are in its tip.
export function SameAreaAvatars({ overlaps }: { overlaps: Overlap[] }) {
  const areas = sameArea(overlaps);
  if (!areas.length) return null;
  return <span className="same-area-avatars">
    {areas.map((area) => {
      const words = "In the same area: " + sameAreaWords(area);
      return area.url
        ? <a key={area.person.login || area.person.name} className="same-area-person" href={area.url} target="_blank" rel="noreferrer" aria-label={words} data-tip={words}><PersonAvatar person={area.person} /></a>
        : <span key={area.person.login || area.person.name} className="same-area-person" role="img" aria-label={words} data-tip={words}><PersonAvatar person={area.person} /></span>;
    })}
  </span>;
}
