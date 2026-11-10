// How a connector and its tools are named to a reader. A harness names a
// connector for itself: Claude Code's are "claude.ai Supabase" in its status
// and claude_ai_Supabase in a tool's name, a plugin's server is
// plugin:data:hex or plugin_data_hex, and a tool is mcp__server__tool. None
// of that is shown (the Overlord, 2026-11-10: a row such as
// claude_ai_Supabase execute_sql reads as Supabase and Execute SQL, with
// claude_ai_, mcp__ and plugin_ stripped from every connector name).

// What a harness puts before a connector's own name.
const PREFIX = /^(?:mcp__|claude[._ ]ai[._ ]+|plugin[_:])/i;

// Words a capital letter alone would spell wrong.
const CASED: Record<string, string> = {
  devtools: "DevTools", github: "GitHub", gitlab: "GitLab", ionos: "IONOS", javascript: "JavaScript", oauth: "OAuth", onedrive: "OneDrive",
  openai: "OpenAI", openrouter: "OpenRouter", postgresql: "PostgreSQL", precisiondocs: "PrecisionDocs", sharepoint: "SharePoint", typescript: "TypeScript",
};

// Words said in capitals, alone or with an s after them.
const ACRONYMS = new Set(["ai", "api", "ci", "cli", "css", "csv", "db", "dns", "eu", "html", "http", "https", "id", "ip", "json", "jwt", "mcp", "pdf", "pr", "rls", "sdk", "sql", "ssh", "ssl", "ui", "url", "vm"]);

// cased is a lower-case word as it is written, or nothing for a plain word.
function cased(word: string): string | undefined {
  if (Object.hasOwn(CASED, word)) return CASED[word];
  if (ACRONYMS.has(word)) return word.toUpperCase();
  if (word.endsWith("s") && ACRONYMS.has(word.slice(0, -1))) return word.slice(0, -1).toUpperCase() + "s";
  return undefined;
}

const capitalized = (word: string): string => word.charAt(0).toUpperCase() + word.slice(1);

// The words of a name written in camel case, as getIssue is get and Issue.
const camelWords = (word: string): string[] => word.replace(/([a-z0-9])([A-Z])/g, "$1 $2").replace(/([A-Z]+)([A-Z][a-z])/g, "$1 $2").split(" ");

// withoutPrefix is a connector's own name, without what its harness puts
// before it.
export function withoutPrefix(name: string): string {
  let rest = name.trim();
  for (let next = rest.replace(PREFIX, ""); next && next !== rest; next = rest.replace(PREFIX, "")) rest = next;
  return rest;
}

// connectorName is a connector's name in proper case: its harness's prefix
// gone, its words apart, a word said twice in a row said once, and each word
// with a capital unless it already has one of its own, as GitHub does. A
// name that starts in lower case and holds a capital is camel case, and its
// words are parted too.
export function connectorName(name: string): string {
  const words = withoutPrefix(name).split(/[\s_:-]+/).flatMap((word) => /^[a-z]/.test(word) ? camelWords(word) : [word]).filter(Boolean);
  return words.filter((word, index) => index === 0 || word.toLowerCase() !== words[index - 1].toLowerCase())
    .map((word) => /[A-Z]/.test(word) ? word : cased(word) ?? capitalized(word)).join(" ");
}

// toolWords is a tool's name in words, as execute_sql is Execute SQL: its
// first word with a capital, and a word that names its own connector first
// left out, since the connector is named beside it.
export function toolWords(tool: string, connector = ""): string {
  const words = tool.split(/[\s_.:/-]+/).flatMap(camelWords).filter(Boolean).map((word) => word.toLowerCase());
  if (words.length > 1 && words[0] === connector.toLowerCase().replace(/[^a-z0-9]/g, "")) words.shift();
  return words.map((word, index) => cased(word) ?? (index ? word : capitalized(word))).join(" ");
}
