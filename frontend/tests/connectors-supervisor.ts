import type { Page } from "./site";

// The supervisor's connectors API, played for the Connectors page's specs:
// GET /api/connectors answers the saved snapshot, and the page's posts are
// recorded. Every project, goblin, server and login here is made up.
export const NOW = "2026-11-25T12:00:00Z";
export const minutesBefore = (minutes: number): string => new Date(Date.parse(NOW) - minutes * 60_000).toISOString();

export type Json = Record<string, unknown>;
export interface Login { id: string; [field: string]: unknown }
export interface LevelJson { items: Json[]; checked_at: string; checking: boolean; error?: string }
export interface ProjectJson extends LevelJson { name: string; path: string; browser_logins: Login[] }
export interface GoblinJson extends LevelJson { task: string; generation: string; project: string; harness: string; is_confirmed: boolean; note?: string }
export interface View { instance: string; machine: LevelJson; projects: ProjectJson[]; goblins: GoblinJson[] }

export interface Supervisor {
  view: View;
  // What the page asked: every request to /api/connectors and the other
  // endpoints it uses, in order, and its posts with their token.
  requests: string[];
  reads: number;
  posts: { path: string; token: string | undefined; body: unknown }[];
  reports: { where: string; text: string }[];
  // Whether the snapshot cannot be read, what a post is refused with, what a
  // repair answers, and a grant held until the test lets it go.
  isUnreadable: boolean;
  refuses: string;
  repaired: object;
  held?: Promise<void>;
  // Ends the check a refresh started, as the supervisor's worker does.
  settle: (level: "machine" | "project" | "goblin", scope: string, checkedAt: string) => void;
}

export function item(level: string, group: string, kind: string, name: string, status: string, extra: Json = {}): Json {
  return { id: kind + ":" + name, level, group, kind, name, status, checked_at: minutesBefore(4), ...extra };
}

export const level = (items: Json[], checkedMinutesAgo = 4): LevelJson => ({ items, checked_at: minutesBefore(checkedMinutesAgo), checking: false });

export function supervisorWith(view: Omit<View, "instance">): Supervisor {
  const supervisor: Supervisor = {
    view: { instance: "connectors-proof", ...view }, requests: [], reads: 0, posts: [], reports: [], isUnreadable: false, refuses: "", repaired: {},
    settle: (target, scope, checkedAt) => {
      const found = levelFor(supervisor.view, target, scope);
      found.checking = false;
      found.checked_at = checkedAt;
      for (const each of found.items) each.checked_at = checkedAt;
    },
  };
  return supervisor;
}

function levelFor(view: View, target: string, scope: string): LevelJson {
  const found = target === "machine" ? view.machine : target === "project" ? view.projects.find((project) => project.name === scope) : view.goblins.find((goblin) => goblin.task === scope);
  if (!found) throw new Error("No such level " + target + " " + scope);
  return found;
}

// play answers the page's requests from the supervisor a test holds.
export async function play(page: Page, supervisor: Supervisor): Promise<void> {
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const body = request.method() === "POST" ? request.postDataJSON() as { level?: string; scope?: string; task?: string; project?: string; login?: string; connection?: string; action?: string; generation?: string } : undefined;
    if (path === "/api/ceo/report") {
      supervisor.reports.push(body as unknown as { where: string; text: string });
      return route.fulfill({ status: 202, json: { reported: true } });
    }
    if (path.startsWith("/api/connectors") || path.startsWith("/api/connections") || path.startsWith("/api/browser-logins")) supervisor.requests.push(request.method() + " " + path);
    if (body) supervisor.posts.push({ path, token: request.headers()["x-ceo-token"], body });
    const refused = () => route.fulfill({ status: 409, json: { error: supervisor.refuses } });
    if (path === "/api/connectors" && request.method() === "GET") {
      supervisor.reads += 1;
      return supervisor.isUnreadable ? route.fulfill({ status: 503, json: { error: "Connectors cannot be read now." } }) : route.fulfill({ json: supervisor.view });
    }
    if (path === "/api/connectors/refresh") {
      levelFor(supervisor.view, body?.level ?? "", body?.scope ?? "").checking = true;
      return route.fulfill({ json: supervisor.view });
    }
    if (path === "/api/connectors/fix") return supervisor.refuses ? refused() : route.fulfill({ json: supervisor.repaired });
    if (path === "/api/connectors/remove") {
      if (supervisor.refuses) return refused();
      const { view } = supervisor;
      const found = body?.task ? view.goblins.find((goblin) => goblin.task === body.task) : body?.project ? view.projects.find((project) => project.name === body.project) : view.machine;
      if (found) found.items = found.items.filter((each) => each.id !== body?.connection);
      return route.fulfill({ json: view });
    }
    if (path === "/api/connections/grant") {
      await supervisor.held;
      if (supervisor.refuses) return refused();
      const goblin = supervisor.view.goblins.find((each) => each.task === body?.task);
      const row = goblin?.items.find((each) => each.id === body?.connection);
      if (row) {
        const isGiving = body?.action === "grant";
        row.grant = isGiving ? "revoke" : "grant";
        row.group = isGiving ? "carried" : "not_given";
        row.status = isGiving ? "set" : "not_given";
      }
      return route.fulfill({ json: supervisor.view });
    }
    if (path === "/api/browser-logins/sign-out" || path === "/api/browser-logins/forget") {
      const project = supervisor.view.projects.find((each) => each.browser_logins.some((login) => login.id === body?.login));
      if (project) project.browser_logins = path.endsWith("forget") ? project.browser_logins.filter((login) => login.id !== body?.login)
        : project.browser_logins.map((login) => login.id === body?.login ? { ...login, signed_in: false, signed_in_at: "" } : login);
      return route.fulfill({ json: { browser_logins: project?.browser_logins ?? [] } });
    }
    if (path === "/api/workspace") return route.fulfill({ json: { repository: "code-goblins", root: "C:/scratch/work", branch: "main", harness: "claude", notes: [] } });
    if (path === "/api/engines") return route.fulfill({ json: { harnesses: [] } });
    return route.fulfill({ json: {} });
  });
}

// A small fleet of made-up projects, goblins and servers: this PC with a tool
// signed in, one that did not answer and a credential every goblin carries;
// acme, checked a few minutes ago, whose supabase points at one Supabase
// project; globex, checked an hour and a half ago, whose supabase points at
// another and is missing a variable; and a goblin in each, Zeke's server
// failed to load.
export function fleet(): Supervisor {
  const acme: ProjectJson = {
    name: "acme", path: "C:/work/acme", browser_logins: [],
    ...level([
      item("project", "service", "service", "github", "verified", { detail: "Signed in", credentials: [{ name: "GITHUB_TOKEN", is_resolved: true }], source: "its probe" }),
      item("project", "service", "service", "supabase", "set", { target: "Supabase project …k3tv", credentials: [{ name: "SUPABASE_URL", is_resolved: true }, { name: "SUPABASE_SERVICE_KEY", is_resolved: false }] }),
      item("project", "mcp", "mcp", "supabase", "not_given", { target: "Supabase project …k3tv", detail: "Sign-in only: a goblin cannot finish its browser sign-in." }),
    ]),
  };
  const globex: ProjectJson = {
    name: "globex", path: "C:/work/globex", browser_logins: [],
    ...level([
      item("project", "service", "service", "supabase", "missing", { target: "Supabase project …q7wk", detail: "SUPABASE_KEY is not stored.", actions: ["store:SUPABASE_KEY", "login"], credentials: [{ name: "SUPABASE_KEY", is_resolved: false }] }),
    ], 90),
  };
  return supervisorWith({
    machine: level([
      item("pc", "cli", "cli", "gh", "verified", { detail: "Signed in", source: "gh auth status" }),
      item("pc", "cli", "cli", "vercel", "set", { detail: "Installed" }),
      item("pc", "cli", "cli", "gcloud", "not_checked", { detail: "Its status command did not answer in 20 s." }),
      item("pc", "environment", "credential", "ACME_API_KEY", "everywhere", { detail: "Every terminal and goblin carries it, with no grant. No project declares it." }),
      item("pc", "built_in", "connector", "claude-in-chrome", "set", { detail: "Built in to Claude: every Claude goblin loads it" }),
      item("account", "harness", "harness", "claude", "verified", { detail: "Signed in" }),
      item("account", "account_connector", "connector", "claude.ai Supabase", "not_given", { detail: "Your own Claude sessions can load it. Goblins start with a strict MCP config." }),
    ]),
    projects: [acme, globex],
    goblins: [
      {
        task: "cg-touch", generation: "cg-touch-1", project: "acme", harness: "claude", is_confirmed: true, ...level([
          item("goblin", "failed", "mcp", "acme-docs", "failed", { id: "failed:acme-docs", detail: "Connection closed", source: "its session record" }),
          item("goblin", "loaded", "mcp", "acme-search", "verified", { tools: ["search_pages", "read_page"], source: "its session record" }),
          item("goblin", "carried", "service", "github", "set", { grant: "revoke" }),
          item("goblin", "not_given", "service", "supabase", "not_given", { grant: "grant", detail: "The CEO gives it with goblins auth grant cg-touch supabase." }),
        ], 30),
      },
      { task: "cg-other", generation: "cg-other-1", project: "globex", harness: "codex", is_confirmed: false, note: "Codex does not record which servers loaded: it has the Codex user config's servers on the Account level.", ...level([], 30) },
    ],
  });
}

// A snapshot the size of today's fleet: 22 goblins in 22 projects, about 400
// items, a few in trouble.
export function bigFleet(): Supervisor {
  const projects: ProjectJson[] = Array.from({ length: 22 }, (_, at) => ({
    name: "project-" + String(at + 1).padStart(2, "0"), path: "C:/work/project-" + (at + 1), browser_logins: [],
    ...level([
      ...Array.from({ length: 14 }, (_, service) => item("project", "service", "service", "service-" + (service + 1), service === 3 ? "missing" : "set", { credentials: [{ name: "SERVICE_" + (service + 1) + "_KEY", is_resolved: service !== 3 }] })),
      item("project", "mcp", "mcp", "docs-server", "set", { target: "local command" }),
    ]),
  }));
  return supervisorWith({
    machine: level([
      ...Array.from({ length: 30 }, (_, at) => item("pc", "cli", "cli", "tool-" + (at + 1), at === 0 ? "verified" : "set")),
      ...Array.from({ length: 14 }, (_, at) => item("account", "user_config", "mcp", "server-" + (at + 1), "not_given")),
    ]),
    projects,
    goblins: projects.map((project, at) => ({
      task: "goblin-" + (at + 1), generation: "goblin-" + (at + 1) + "-1", project: project.name, harness: "claude", is_confirmed: true,
      ...level([item("goblin", "loaded", "mcp", "docs-server", "verified", { tools: ["read_page"] }), item("goblin", "carried", "service", "service-1", "set", { grant: "revoke" })], 30),
    })),
  });
}
