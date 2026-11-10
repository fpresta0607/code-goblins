import type { Page } from "./site";

// The supervisor's connection API, played for the Connectors page's specs:
// what each goblin's check finds, whether it is still running, and what the
// page asked of it. Every connector, goblin and login here is made up.
export const CHECKED_AT = "2026-11-10T14:56:12Z";

export interface Login { id: string; [field: string]: unknown }

export interface Supervisor {
  // Each goblin's entries and whether its check still runs, by task.
  entries: Record<string, object[]>;
  checking: Record<string, boolean>;
  // The browser logins every goblin's project may use.
  logins: Login[];
  // What a finished check failed with, and whether connections can be read.
  error: string;
  isUnreadable: boolean;
  // What a grant or a repair is refused with, what a repair answers, and a
  // grant held until the test lets it go.
  refuses: string;
  repaired: object;
  held?: Promise<void>;
  // What the page asked: its reads by task, its posts, and its reports to
  // the CFO.
  reads: Record<string, number>;
  posts: { path: string; token: string | undefined; body: unknown }[];
  reports: { where: string; text: string }[];
}

export function supervisorWith(entries: Record<string, object[]>): Supervisor {
  return { entries, checking: {}, logins: [], error: "", isUnreadable: false, refuses: "", repaired: {}, reads: {}, posts: [], reports: [] };
}

// play answers the page's requests from the supervisor a test holds.
export async function play(page: Page, supervisor: Supervisor): Promise<void> {
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const body = route.request().method() === "POST" ? route.request().postDataJSON() as { task?: string; generation?: string; login?: string } : undefined;
    if (path === "/api/cfo/report") {
      supervisor.reports.push(body as { where: string; text: string });
      return route.fulfill({ status: 202, json: { reported: true } });
    }
    const task = body?.task ?? url.searchParams.get("task") ?? "";
    const connections = () => ({
      instance: "fixture", generation: body?.generation ?? url.searchParams.get("generation"), runtime: { process_id: 42, started_at: "2026-11-10T14:00:00Z", workspace: "C:/scratch/work" },
      checking: supervisor.checking[task] ?? false, checked_at: CHECKED_AT, error: supervisor.error, entries: supervisor.entries[task] ?? [], browser_logins: supervisor.logins,
    });
    const refused = () => route.fulfill({ status: 409, json: { error: supervisor.refuses } });
    if (body) supervisor.posts.push({ path, token: route.request().headers()["x-cfo-token"], body });
    if (path === "/api/connections") {
      supervisor.reads[task] = (supervisor.reads[task] ?? 0) + 1;
      return supervisor.isUnreadable ? route.fulfill({ status: 503, json: { error: "Connections cannot be checked now." } }) : route.fulfill({ json: connections() });
    }
    if (path === "/api/connections/check") return route.fulfill({ json: connections() });
    if (path === "/api/connections/grant") {
      await supervisor.held;
      return supervisor.refuses ? refused() : route.fulfill({ json: { ...connections(), checking: true } });
    }
    if (path === "/api/connections/fix") return supervisor.refuses ? refused() : route.fulfill({ json: supervisor.repaired });
    if (path === "/api/browser-logins/sign-out" || path === "/api/browser-logins/forget") {
      supervisor.logins = path.endsWith("forget") ? supervisor.logins.filter((login) => login.id !== body?.login)
        : supervisor.logins.map((login) => login.id === body?.login ? { ...login, signed_in: false, signed_in_at: "" } : login);
      return route.fulfill({ json: { browser_logins: supervisor.logins } });
    }
    if (path === "/api/workspace") return route.fulfill({ json: { repository: "code-goblins", root: "C:/scratch/work", branch: "main", harness: "claude", notes: [] } });
    if (path === "/api/engines") return route.fulfill({ json: { harnesses: [] } });
    return route.fulfill({ json: {} });
  });
}
