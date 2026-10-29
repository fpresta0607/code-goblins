import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import type { Snapshot, Task } from "./types";
import { PersonAvatar } from "./person-avatar";
import { panelPeople, personName, profileLink, sameAreaWords } from "./people";
import "./people.css";

// How many faces the row shows before the rest become a count.
const SHOWN = 5;

// Beside a goblin's project in its panel: the people active in that project's
// repository, as overlapping faces that open their GitHub profiles, each
// named to assistive technology. Whoever has work in the goblin's area comes
// first, ringed, and a muted line of its own under the row says who and what
// that work is. Past five, the rest are one +N (the
// Overlord, 2026-10-17: "condense it with a plus, and no truncation on the
// contributors"), which opens every name, whole. The list closes on Escape,
// which hands the keyboard back to +N, and on a press anywhere else.
export function PeopleRow({ snapshot, task }: { snapshot: Snapshot; task: Task }) {
  const [isOpen, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const more = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!isOpen) return;
    const away = (event: PointerEvent) => { if (event.target instanceof Node && !box.current?.contains(event.target)) setOpen(false); };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [isOpen]);
  const people = panelPeople(snapshot, task);
  if (!people.length) return null;
  const rest = people.length - SHOWN;
  const areas = people.flatMap(({ area }) => area ? [sameAreaWords(area)] : []);
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!isOpen || event.key !== "Escape") return;
    // The panel's own Escape, which closes it or goes back, is not for this press.
    event.preventDefault();
    event.stopPropagation();
    setOpen(false);
    more.current?.focus();
  };
  return <><div ref={box} className="people-row" onKeyDown={onKeyDown}>
    <ul className="people-faces" aria-label={"People in " + task.project}>
      {people.slice(0, SHOWN).map(({ person, area }) => {
        const name = personName(person), link = profileLink(person);
        const face = { "aria-label": area ? name + ", in the same area: " + area.what.join(", ") : name };
        return <li key={(person.login ? "login:" : "name:") + name} className={"person" + (area ? " same-area" : "")}>
          {link ? <a href={link} target="_blank" rel="noreferrer" {...face}><PersonAvatar person={person} /></a> : <span role="img" {...face}><PersonAvatar person={person} /></span>}
        </li>;
      })}
    </ul>
    {rest > 0 && <button ref={more} className="people-more" aria-label={rest + " more people in " + task.project} aria-expanded={isOpen} onClick={() => setOpen(!isOpen)}>+{rest}</button>}
    {isOpen && <ul className="people-list" aria-label={"Everyone in " + task.project}>
      {people.map(({ person, area }) => {
        const name = personName(person), link = profileLink(person);
        const content = <><PersonAvatar person={person} /><span className="person-name">{name}</span>{area && <span className="sr-only">, in the same area: {area.what.join(", ")}</span>}</>;
        return <li key={(person.login ? "login:" : "name:") + name} className={"person" + (area ? " same-area" : "")}>
          {link ? <a href={link} target="_blank" rel="noreferrer">{content}</a> : <span>{content}</span>}
        </li>;
      })}
    </ul>}
  </div>
  {areas.map((words) => <p key={words} className="muted people-same-area">In the same area: {words}</p>)}</>;
}
