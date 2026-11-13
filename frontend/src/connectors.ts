import type { BrandKey } from "./brandMarks.ts";
import { connectorName, withoutPrefix } from "./connector-names.ts";
import type { IconName } from "./Icon.tsx";

export type Mark = { brand: BrandKey } | { glyph: IconName };

// Checked in order against a connector's own name with case and punctuation
// removed, so "chrome-devtools", "GITHUB_TOKEN", "neon-auth" and "claude.ai
// Supabase" all find their service. Short names are anchored so "butterfly"
// never reads as Fly.io. Each names its logo, a file of its own under
// src/connector-logos (public/assets/NOTICE.txt) that loads only where it is
// shown, or none where the board carries no logo for it, and says in one line
// what the service does.
const SERVICES: [RegExp, string, string][] = [
  [/github|^gh(token|pat)?$/, "github", "Repositories, pull requests and Actions"],
  [/chrome|devtools/, "chrome", "Drives a Chrome browser"],
  [/playwright/, "", "Drives a browser for tests"],
  [/context7/, "context7", "Current library documentation"],
  [/supabase/, "supabase", "Supabase database, auth and storage"],
  [/sentry/, "sentry", "Errors and performance"],
  [/stripe/, "stripe", "Payments and billing"],
  [/vercel/, "vercel", "Deploys and hosting"],
  [/^fly/, "fly", "Fly.io apps and machines"],
  [/neon/, "neon", "Serverless Postgres"],
  [/notion/, "notion", "Notion pages and databases"],
  [/openrouter/, "openrouter", "Models from many providers"],
  [/openai/, "openai", "OpenAI models"],
  [/anthropic|claude/, "anthropic", "Claude models"],
  [/gemini/, "gemini", "Gemini models"],
  [/gmail/, "gmail", "Gmail messages and drafts"],
  [/googledrive/, "google-drive", "Google Drive files"],
  [/googlecalendar/, "google-calendar", "Google Calendar events"],
  [/google/, "google", "Google APIs"],
  [/kimi|moonshot/, "moonshot", "Kimi models"],
  [/resend/, "resend", "Sends email"],
  [/upstash/, "upstash", "Serverless Redis and queues"],
  [/redis/, "redis", "Redis data store"],
  [/qdrant/, "qdrant", "Vector search"],
  [/railway/, "railway", "Railway apps and databases"],
  [/ionos/, "ionos", "IONOS hosting and domains"],
  [/postgres|^pg/, "postgres", "PostgreSQL database"],
  [/database|^db/, "", "Database access"],
  [/^crawler$/, "", "Finds and reads every page and file of a site"],
];

const serviceOf = (name: string) => {
  const key = withoutPrefix(name).toLowerCase().replace(/[^a-z0-9]/g, "");
  return key ? SERVICES.find(([pattern]) => pattern.test(key)) : undefined;
};

// The shell a run item uses. Git Bash wears the GNU Bash mark; Simple Icons
// carries no PowerShell mark, so both PowerShells use the terminal glyph.
export function shellMark(shell: string): Mark {
  return shell === "bash" ? { brand: "bash" } : { glyph: "terminal" };
}

export function shellLabel(shell: string): string {
  return ({ powershell: "Windows PowerShell", pwsh: "PowerShell 7", bash: "Git Bash" } as Record<string, string>)[shell] || shell;
}

// The one button on a run card says where the command runs, in the
// Overlord's words for it ("run in powershell button"); an elevated item
// says that instead, since Windows asks him first.
export function runLabel(shell: string, admin: boolean): string {
  if (admin) return "Run as administrator";
  return "Run in " + (({ powershell: "PowerShell", pwsh: "PowerShell 7", bash: "Git Bash" } as Record<string, string>)[shell] || shell);
}

// connectorLogo is a service's own logo, by the name of its file among the
// logos bundled with the board, or its name's initial for a plain tile when
// the board carries none.
export function connectorLogo(name: string): { file: string } | { initial: string } {
  const file = serviceOf(name)?.[1];
  return file ? { file } : { initial: connectorName(name).charAt(0).toUpperCase() || "?" };
}

export function connectorAbout(name: string): string {
  return serviceOf(name)?.[2] || "";
}

// The harnesses with a mark of their own.
const HARNESS_MARKS: Record<string, Mark> = { claude: { brand: "claude" }, codex: { glyph: "codex" }, pi: { glyph: "pi" }, kimi: { brand: "kimi" } };

export function harnessMark(harness: string): Mark {
  return HARNESS_MARKS[harness] || { glyph: "terminal" };
}

export function hasHarnessMark(harness: string): boolean {
  return Object.hasOwn(HARNESS_MARKS, harness);
}

export function modelMark(model: string): { mark: Mark; provider: string } {
  const name = model.toLowerCase();
  if (/claude|opus|sonnet|haiku|fable|mythos/.test(name)) return { mark: { brand: "anthropic" }, provider: "Anthropic" };
  if (/gpt|codex|openai|^o\d/.test(name)) return { mark: { brand: "openai" }, provider: "OpenAI" };
  if (/kimi|moonshot/.test(name)) return { mark: { brand: "moonshot" }, provider: "Moonshot AI" };
  if (/gemini/.test(name)) return { mark: { brand: "gemini" }, provider: "Google" };
  return { mark: { glyph: "sparkle" }, provider: "Model" };
}
