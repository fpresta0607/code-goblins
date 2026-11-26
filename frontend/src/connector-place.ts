// Where the Connectors page is: its home, one of its levels, or one project or
// goblin of them.
export type Place =
  | { kind: "home" }
  | { kind: "pc" }
  | { kind: "projects" }
  | { kind: "goblins" }
  | { kind: "project"; project: string }
  | { kind: "goblin"; task: string };

export const HOME: Place = { kind: "home" };

export function placeKey(place: Place): string {
  return place.kind === "project" ? "project:" + place.project : place.kind === "goblin" ? "goblin:" + place.task : place.kind;
}

export interface Crumb { label: string; place: Place }

// The breadcrumb to a place, the place itself last: a goblin sits under its
// project, which sits under Projects.
export function crumbsTo(place: Place, goblin: { name: string; project: string }): Crumb[] {
  const home: Crumb = { label: "Connectors", place: HOME };
  switch (place.kind) {
    case "home": return [home];
    case "pc": return [home, { label: "This PC and its accounts", place }];
    case "projects": return [home, { label: "Projects", place }];
    case "goblins": return [home, { label: "Goblins", place }];
    case "project": return [home, { label: "Projects", place: { kind: "projects" } }, { label: place.project, place }];
    case "goblin": return [home, { label: "Projects", place: { kind: "projects" } }, { label: goblin.project, place: { kind: "project", project: goblin.project } }, { label: goblin.name, place }];
  }
}
