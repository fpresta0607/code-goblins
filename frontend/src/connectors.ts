import type { BrandKey } from "./brandMarks.ts";
import type { IconName } from "./Icon.tsx";

export type Mark = { brand: BrandKey } | { glyph: IconName };

// Checked in order against the name with case and punctuation removed, so
// "chrome-devtools", "GITHUB_TOKEN" and "neon-auth" all find their service.
// Short names are anchored so "butterfly" never reads as Fly.io.
const SERVICES: [RegExp, Mark][] = [
  [/github|^gh(token|pat)?$/, { brand: "github" }],
  [/chrome|devtools/, { brand: "chrome" }],
  [/playwright/, { glyph: "browser-check" }],
  [/context7/, { glyph: "book" }],
  [/supabase/, { brand: "supabase" }],
  [/sentry/, { brand: "sentry" }],
  [/stripe/, { brand: "stripe" }],
  [/vercel/, { brand: "vercel" }],
  [/^fly/, { brand: "fly" }],
  [/neon/, { brand: "neon" }],
  [/notion/, { brand: "notion" }],
  [/openrouter/, { brand: "openrouter" }],
  [/openai/, { glyph: "openai" }],
  [/anthropic|claude/, { brand: "anthropic" }],
  [/gemini/, { brand: "gemini" }],
  [/kimi|moonshot/, { brand: "moonshot" }],
  [/resend/, { brand: "resend" }],
  [/upstash/, { brand: "upstash" }],
  [/redis/, { brand: "redis" }],
  [/qdrant/, { brand: "qdrant" }],
  [/railway/, { brand: "railway" }],
  [/ionos/, { brand: "ionos" }],
  [/postgres|^pg/, { brand: "postgres" }],
  [/database|^db/, { glyph: "database" }],
];

// The shell a run item uses. Git Bash wears the GNU Bash mark; Simple Icons
// carries no PowerShell mark, so both PowerShells use the terminal glyph.
export function shellMark(shell: string): Mark {
  return shell === "bash" ? { brand: "bash" } : { glyph: "terminal" };
}

export function shellLabel(shell: string): string {
  return ({ powershell: "Windows PowerShell", pwsh: "PowerShell 7", bash: "Git Bash" } as Record<string, string>)[shell] || shell;
}

export function connectorMark(name: string, kind: "mcp" | "credential"): Mark {
  const key = name.toLowerCase().replace(/[^a-z0-9]/g, "");
  return SERVICES.find(([pattern]) => key && pattern.test(key))?.[1] || (kind === "mcp" ? { brand: "mcp" } : { glyph: "key" });
}

export function harnessMark(harness: string): Mark {
  const marks: Record<string, Mark> = { claude: { brand: "claude" }, codex: { glyph: "openai" }, pi: { glyph: "pi" }, kimi: { brand: "kimi" } };
  return marks[harness] || { glyph: "sparkle" };
}

export function modelMark(model: string): { mark: Mark; provider: string } {
  const name = model.toLowerCase();
  if (/claude|opus|sonnet|haiku|fable|mythos/.test(name)) return { mark: { brand: "anthropic" }, provider: "Anthropic" };
  if (/gpt|codex|openai|^o\d/.test(name)) return { mark: { glyph: "openai" }, provider: "OpenAI" };
  if (/kimi|moonshot/.test(name)) return { mark: { brand: "moonshot" }, provider: "Moonshot AI" };
  if (/gemini/.test(name)) return { mark: { brand: "gemini" }, provider: "Google" };
  return { mark: { glyph: "sparkle" }, provider: "Model" };
}
