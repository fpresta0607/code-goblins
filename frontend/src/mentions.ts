import { object, strings } from "./types.ts";

// The @ he is typing: where it starts in the draft and the words after it
// the caret has reached, as the harness's own @ takes a file.
export interface Mention { start: number; query: string }

// mentionAt is the @ the caret stands in, one that starts the draft or
// follows a space, with no space after it yet, or null.
export function mentionAt(text: string, caret: number): Mention | null {
  const match = /(^|\s)@([^\s@]*)$/.exec(text.slice(0, caret));
  return match ? { start: caret - match[2].length - 1, query: match[2] } : null;
}

// mentionFile puts the chosen file in the mention's place as @path and a
// space, keeping the rest of the draft, and says where the caret goes.
export function mentionFile(text: string, mention: Mention, caret: number, path: string): { text: string; caret: number } {
  const chosen = "@" + path + " ";
  const after = text.slice(caret).replace(/^\S*/, "").replace(/^ /, "");
  return { text: text.slice(0, mention.start) + chosen + after, caret: mention.start + chosen.length };
}

// mentionedFiles are the files a list read for an earlier query of the same
// @ that still match the query he has typed since, so a pick before the new
// list arrives is one he asked for.
export function mentionedFiles(files: string[], read: string, query: string): string[] {
  if (read === query) return files;
  const wanted = query.toLowerCase();
  return files.filter((file) => file.toLowerCase().includes(wanted));
}

// parseFiles reads the supervisor's answer to GET /api/files.
export function parseFiles(value: unknown): string[] {
  return strings(object(value).files).filter(Boolean);
}
