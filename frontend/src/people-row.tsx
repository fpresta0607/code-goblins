import type { Snapshot, Task } from "./types";
import { PersonAvatar } from "./person-avatar";
import { panelPeople, personName, profileLink, sameAreaWords } from "./people";
import "./people.css";

// Under a goblin's project in its panel: the people active in that project's
// repository, each an avatar and a name, opening their GitHub profile. Whoever
// has work in the goblin's area comes first, ringed, with that work in the tip.
export function PeopleRow({ snapshot, task }: { snapshot: Snapshot; task: Task }) {
  const people = panelPeople(snapshot, task);
  if (!people.length) return null;
  return <ul className="people-row" aria-label={"People in " + task.project}>
    {people.map(({ person, area }) => {
      const name = personName(person), link = profileLink(person);
      const tip = area ? { "data-tip": "In the same area: " + sameAreaWords(area) } : {};
      const content = <><PersonAvatar person={person} /><span className="person-name">{name}</span>{area && <span className="sr-only">, in the same area: {area.what.join(", ")}</span>}</>;
      return <li key={(person.login ? "login:" : "name:") + name} className={"person" + (area ? " same-area" : "")}>
        {link ? <a href={link} target="_blank" rel="noreferrer" {...tip}>{content}</a> : <span {...tip}>{content}</span>}
      </li>;
    })}
  </ul>;
}
