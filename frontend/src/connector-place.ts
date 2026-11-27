// Where the Connectors page is: its home, Needs a look, one of its scopes, or
// one project or goblin of them (#3010). The home is the tree; where the page
// is wide it shows Needs a look beside it.
export type Place =
  | { kind: "home" }
  | { kind: "look" }
  | { kind: "pc" }
  | { kind: "yours" }
  | { kind: "projects" }
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
    case "look": return [home, { label: "Needs a look", place }];
    case "pc": return [home, { label: "This PC", place }];
    case "yours": return [home, { label: "Your Claude and Codex", place }];
    case "projects": return [home, { label: "Projects", place }];
    case "project": return [home, { label: "Projects", place: { kind: "projects" } }, { label: place.project, place }];
    case "goblin": return [home, { label: "Projects", place: { kind: "projects" } }, { label: goblin.project, place: { kind: "project", project: goblin.project } }, { label: goblin.name, place }];
  }
}
