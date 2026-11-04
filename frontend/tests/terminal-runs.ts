// The board-level terminal specs run under Terminal on a Codex goblin and on
// a Claude Code one. Both harnesses also have a Modern conversation, which
// only the style menu of the session's picture swaps for the terminal.
export const RUNS = [{ name: "under Terminal on Codex", harness: "codex" }, { name: "under Terminal on Claude Code", harness: "claude" }] as const;
