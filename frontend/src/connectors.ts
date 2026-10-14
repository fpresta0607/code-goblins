import type { BrandKey } from "./brandMarks.ts";
import type { IconName } from "./Icon.tsx";

export type Mark = { brand: BrandKey } | { glyph: IconName };

// Checked in order against the name with case and punctuation removed, so
// "chrome-devtools", "GITHUB_TOKEN" and "neon-auth" all find their service.
// Short names are anchored so "butterfly" never reads as Fly.io. Each also
// says in one line what the service does, under its name on its connector
// card.
const SERVICES: [RegExp, Mark, string][] = [
  [/github|^gh(token|pat)?$/, { brand: "github" }, "Repositories, pull requests and Actions"],
  [/chrome|devtools/, { brand: "chrome" }, "Drives a Chrome browser"],
  [/playwright/, { glyph: "browser-check" }, "Drives a browser for tests"],
  [/context7/, { brand: "context7" }, "Current library documentation"],
  [/supabase/, { brand: "supabase" }, "Supabase database, auth and storage"],
  [/sentry/, { brand: "sentry" }, "Errors and performance"],
  [/stripe/, { brand: "stripe" }, "Payments and billing"],
  [/vercel/, { brand: "vercel" }, "Deploys and hosting"],
  [/^fly/, { brand: "fly" }, "Fly.io apps and machines"],
  [/neon/, { brand: "neon" }, "Serverless Postgres"],
  [/notion/, { brand: "notion" }, "Notion pages and databases"],
  [/openrouter/, { brand: "openrouter" }, "Models from many providers"],
  [/openai/, { brand: "openai" }, "OpenAI models"],
  [/anthropic|claude/, { brand: "anthropic" }, "Claude models"],
  [/gemini/, { brand: "gemini" }, "Gemini models"],
  [/google/, { brand: "google" }, "Google APIs"],
  [/kimi|moonshot/, { brand: "moonshot" }, "Kimi models"],
  [/resend/, { brand: "resend" }, "Sends email"],
  [/upstash/, { brand: "upstash" }, "Serverless Redis and queues"],
  [/redis/, { brand: "redis" }, "Redis data store"],
  [/qdrant/, { brand: "qdrant" }, "Vector search"],
  [/railway/, { brand: "railway" }, "Railway apps and databases"],
  [/ionos/, { brand: "ionos" }, "IONOS hosting and domains"],
  [/postgres|^pg/, { brand: "postgres" }, "PostgreSQL database"],
  [/database|^db/, { glyph: "database" }, "Database access"],
];

const serviceOf = (name: string) => {
  const key = name.toLowerCase().replace(/[^a-z0-9]/g, "");
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

export function connectorMark(name: string, kind: "mcp" | "credential"): Mark {
  return serviceOf(name)?.[1] || (kind === "mcp" ? { brand: "mcp" } : { glyph: "key" });
}

// connectorLogo is a service's own logo on its connector card, bundled with
// the board, or its initial on a plain tile when the board carries none.
export function connectorLogo(name: string): { brand: BrandKey } | { initial: string } {
  const mark = serviceOf(name)?.[1];
  return mark && "brand" in mark ? { brand: mark.brand } : { initial: name.trim().charAt(0).toUpperCase() || "?" };
}

export function connectorAbout(name: string): string {
  return serviceOf(name)?.[2] || "";
}

export function harnessMark(harness: string): Mark {
  const marks: Record<string, Mark> = { claude: { brand: "claude" }, codex: { glyph: "codex" }, pi: { glyph: "pi" }, kimi: { brand: "kimi" } };
  return marks[harness] || { glyph: "terminal" };
}

export function modelMark(model: string): { mark: Mark; provider: string } {
  const name = model.toLowerCase();
  if (/claude|opus|sonnet|haiku|fable|mythos/.test(name)) return { mark: { brand: "anthropic" }, provider: "Anthropic" };
  if (/gpt|codex|openai|^o\d/.test(name)) return { mark: { brand: "openai" }, provider: "OpenAI" };
  if (/kimi|moonshot/.test(name)) return { mark: { brand: "moonshot" }, provider: "Moonshot AI" };
  if (/gemini/.test(name)) return { mark: { brand: "gemini" }, provider: "Google" };
  return { mark: { glyph: "sparkle" }, provider: "Model" };
}
