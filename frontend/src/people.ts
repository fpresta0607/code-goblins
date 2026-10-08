import type { Overlap, Person, Snapshot, Task, Ticket } from "./types";

// Only GitHub's own avatar host is drawn: the board's policy loads no other
// image from outside it, and a link the supervisor passes on is still
// checked here.
export function safeAvatar(url: string): string {
  return /^https:\/\/avatars\.githubusercontent\.com\/[^\s"'<>]*$/.test(url) ? url : "";
}

// A ticket, a person's work and a profile open only on GitHub.
export function safeGitHubLink(url: string): string {
  return /^https:\/\/github\.com\/[^\s"'<>]+$/.test(url) ? url : "";
}

export function personName(person: Person): string {
  return person.login || person.name;
}

export function profileLink(person: Person): string {
  return person.login ? safeGitHubLink("https://github.com/" + encodeURIComponent(person.login)) : "";
}

// A ticket stays open until its task merges or closes; a merged one is
// closed as completed, the rest as not planned, as GitHub draws them.
export function ticketLook(ticket: Ticket): "open" | "merged" | "closed" {
  return ticket.state === "merged" ? "merged" : ticket.state === "closed" ? "closed" : "open";
}

export function ticketWords(ticket: Ticket): string {
  return "Ticket #" + ticket.number + (ticket.state ? ": " + ticket.state : "");
}

// SameArea is one person whose open work meets a goblin's branch, with each
// piece of it, such as "PR #1446", and a link to the first.
export interface SameArea { person: Person; what: string[]; url: string }

const key = (person: Person) => (person.login ? "login:" : "name:") + personName(person).toLowerCase();

// sameArea gathers a task's overlaps by person, in the order they came.
export function sameArea(overlaps: Overlap[]): SameArea[] {
  const byPerson = new Map<string, SameArea>();
  for (const overlap of overlaps) {
    if (!personName(overlap)) continue;
    const found = byPerson.get(key(overlap));
    if (found) {
      if (!found.what.includes(overlap.what)) found.what.push(overlap.what);
      continue;
    }
    byPerson.set(key(overlap), { person: { login: overlap.login, name: overlap.name, avatar_url: overlap.avatar_url }, what: overlap.what ? [overlap.what] : [], url: safeGitHubLink(overlap.url) });
  }
  return [...byPerson.values()];
}

export function sameAreaWords(area: SameArea): string {
  return personName(area.person) + (area.what.length ? ": " + area.what.join(", ") : "");
}

// The people a goblin's panel names: whoever's work meets its branch first,
// then the rest of its project's active contributors, each once.
export function panelPeople(snapshot: Snapshot, task: Task): { person: Person; area?: SameArea }[] {
  const people: { person: Person; area?: SameArea }[] = sameArea(task.overlaps).map((area) => ({ person: area.person, area }));
  const contributors = snapshot.projects.find((project) => project.name === task.project)?.contributors || [];
  for (const person of contributors) {
    if (!personName(person) || people.some((shown) => key(shown.person) === key(person))) continue;
    people.push({ person });
  }
  return people;
}
