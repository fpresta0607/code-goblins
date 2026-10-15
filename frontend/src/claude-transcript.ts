import { array, number, object, string, strings } from "./types.ts";

// A baby goblin's transcript drawn as Claude Code draws a conversation, in
// the colours of its dark theme, for the same terminal surface a goblin's
// live terminal uses (the Overlord, 2026-10-15: "Baby goblins should look and
// feel just like the same Claude shells"). The supervisor reads the
// sub-agent's own record into entries (fleettree.AgentEntry); this lays them
// out for the terminal's width, as Claude Code wraps a conversation to its
// terminal's, and the terminal draws the rows.

// AgentEntry is one step of a sub-agent's work: a prompt it was given, a
// reply it wrote, or a call with its outcome and the first lines it returned.
export interface AgentEntry {
  kind: "prompt" | "reply" | "call";
  text: string; tool: string; subject: string; state: "running" | "done" | "failed" | "";
  result: string[]; more: number;
}

const KINDS = ["prompt", "reply", "call"] as const;
const STATES = ["running", "done", "failed"] as const;

export function parseAgentEntries(value: unknown): AgentEntry[] {
  return array(value).map((item) => {
    const entry = object(item);
    const kind = KINDS.find((each) => each === entry.kind);
    if (!kind) throw new Error("Invalid transcript entry");
    return { kind, text: string(entry.text), tool: string(entry.tool), subject: string(entry.subject),
      state: STATES.find((each) => each === entry.state) ?? "", result: strings(entry.result), more: number(entry.more) };
  });
}

// Claude Code's dark theme, as SGR parameters: its own orange, the green of
// a call that succeeded, the red of one that failed, the grey of quieter
// text, and the grey behind a prompt.
const CLAUDE = "38;2;215;119;87";
const SUCCESS = "38;2;78;186;101";
const ERROR = "38;2;255;107;128";
const QUIET = "38;2;153;153;153";
const PROMPT = "48;2;55;55;55";
const BOLD = "1";
const DIM = "2";
const CODE = "38;2;177;185;249";

interface Span { text: string; style: string }
interface Cell { char: string; style: string }

// rows draws entries for a terminal cols wide, a blank row between entries as
// Claude Code spaces its messages: a prompt after > on its grey, a reply after
// ●, and a call after ● in the colour of its outcome, grey while it runs, its
// tool in bold, with what came back under ⎿ in grey and how many lines more,
// dimmed.
export function transcriptRows(entries: AgentEntry[], cols: number): string[] {
  const rows: string[] = [];
  for (const entry of entries) {
    if (rows.length) rows.push("");
    rows.push(...entryRows(entry, Math.max(cols, 8)));
  }
  return rows;
}

function entryRows(entry: AgentEntry, cols: number): string[] {
  if (entry.kind === "prompt") {
    return block([{ text: entry.text, style: "" }], cols, [{ text: "> ", style: QUIET }], [{ text: "  ", style: "" }])
      .map((row) => paint(fill(row, cols).map((cell) => ({ char: cell.char, style: join(cell.style, PROMPT) }))));
  }
  if (entry.kind === "reply") return block(markdown(entry.text), cols, [{ text: "● ", style: "" }], [{ text: "  ", style: "" }]).map(paint);
  const mark = entry.state === "done" ? SUCCESS : entry.state === "failed" ? ERROR : QUIET;
  const call: Span[] = [{ text: entry.tool, style: BOLD }, ...entry.subject ? [{ text: "(" + entry.subject + ")", style: "" }] : []];
  const rows = block(call, cols, [{ text: "● ", style: mark }], [{ text: "  ", style: "" }]).map(paint);
  entry.result.forEach((line, index) => {
    const style = entry.state === "failed" && index === 0 ? ERROR : QUIET;
    rows.push(...block([{ text: line, style }], cols, [{ text: index ? "     " : "  ⎿  ", style: QUIET }], [{ text: "     ", style: "" }]).map(paint));
  });
  if (entry.more > 0) rows.push(paint(cells([{ text: `     … +${entry.more} ${entry.more === 1 ? "line" : "lines"}`, style: DIM }])));
  return rows;
}

// The working line under a running baby goblin, as Claude Code draws its
// own: a turning star and the word in its orange, how long in grey.
const SPINNER = ["·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"];
export const SPINNER_STEP_MS = 120;

export function workingRow(frame: number, elapsed: string): string {
  return paint(cells([{ text: SPINNER[frame % SPINNER.length] + " Working…", style: CLAUDE }, ...elapsed ? [{ text: " (" + elapsed + ")", style: QUIET }] : []]));
}

// elapsed is how long since started as Claude Code counts its own turn.
export function elapsed(started: string, now: number): string {
  const start = Date.parse(started);
  if (!started || started.startsWith("0001") || !Number.isFinite(start)) return "";
  const seconds = Math.max(0, Math.floor((now - start) / 1000));
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor(seconds / 60) % 60}m`;
}

// redraw is what to write to a terminal showing previous rows, its cursor at
// the end of the last, for it to show next: only the rows from the first that
// changed, or null when that row has left the terminal's screen of
// screenRows, so the whole transcript must be written again.
export function redraw(previous: string[], next: string[], screenRows: number): string | null {
  let same = 0;
  while (same < previous.length && same < next.length && previous[same] === next[same]) same++;
  if (same === previous.length && same === next.length) return "";
  if (!previous.length) return next.join("\r\n");
  if (same === previous.length) return "\r\n" + next.slice(same).join("\r\n");
  const from = Math.min(same, Math.max(0, next.length - 1));
  const up = previous.length - 1 - from;
  if (up >= screenRows) return null;
  return (up ? `\x1b[${up}A` : "") + "\r\x1b[J" + next.slice(from).join("\r\n");
}

// markdown is a reply's headings and bold in bold and its inline code in
// Claude Code's blue, the marks themselves left out.
function markdown(text: string): Span[] {
  return text.split("\n").flatMap((line, index) => {
    const heading = line.match(/^#{1,6} (.*)$/);
    const spans: Span[] = heading ? [{ text: heading[1], style: BOLD }] : line.split(/(`[^`]+`|\*\*[^*]+\*\*)/).filter(Boolean).map((part) =>
      part.startsWith("`") && part.endsWith("`") && part.length > 1 ? { text: part.slice(1, -1), style: CODE }
        : part.startsWith("**") && part.endsWith("**") && part.length > 3 ? { text: part.slice(2, -2), style: BOLD }
          : { text: part, style: "" });
    return index ? [{ text: "\n", style: "" }, ...spans] : spans;
  });
}

// block wraps spans to cols as Claude Code wraps a message: at spaces where
// it can, and a word longer than a row, such as a long path, broken at the
// row's end, every row after the first under the text, never under its mark.
function block(spans: Span[], cols: number, first: Span[], rest: Span[]): Cell[][] {
  const indent = cells(rest);
  const width = Math.max(1, cols - indent.length);
  const rows: Cell[][] = [];
  for (const line of lines(cells(spans))) {
    for (const row of wrap(line, width)) rows.push([...(rows.length ? indent : cells(first)), ...row]);
  }
  return rows.length ? rows : [cells(first)];
}

function lines(all: Cell[]): Cell[][] {
  const split: Cell[][] = [[]];
  for (const cell of all) {
    if (cell.char === "\n") split.push([]);
    else if (cell.char !== "\r") split[split.length - 1].push(cell.char === "\t" ? { char: " ", style: cell.style } : cell);
  }
  return split;
}

function wrap(line: Cell[], width: number): Cell[][] {
  // Each word after the first keeps the space before it, in its own style.
  const words: { space: Cell | null; cells: Cell[] }[] = [{ space: null, cells: [] }];
  for (const cell of line) {
    if (cell.char === " ") words.push({ space: cell, cells: [] });
    else words[words.length - 1].cells.push(cell);
  }
  const rows: Cell[][] = [];
  let row: Cell[] | null = null;
  for (const { space, cells: each } of words) {
    let word = each;
    if (row && space && row.length + 1 + word.length <= width) {
      row.push(space, ...word);
      continue;
    }
    if (row) rows.push(row);
    while (word.length > width) {
      rows.push(word.slice(0, width));
      word = word.slice(width);
    }
    row = word;
  }
  if (row) rows.push(row);
  return rows;
}

function cells(spans: Span[]): Cell[] {
  return spans.flatMap((span) => Array.from(span.text, (char) => ({ char, style: span.style })));
}

function fill(row: Cell[], cols: number): Cell[] {
  return [...row, ...Array.from({ length: Math.max(0, cols - row.length) }, () => ({ char: " ", style: "" }))];
}

function join(first: string, second: string): string {
  return first ? first + ";" + second : second;
}

// paint is a row as the terminal takes it, each run of one style in its SGR,
// reset after.
function paint(row: Cell[]): string {
  let text = "", current = "";
  for (const cell of row) {
    if (cell.style !== current) {
      text += "\x1b[0" + (cell.style ? ";" + cell.style : "") + "m";
      current = cell.style;
    }
    text += cell.char;
  }
  return current ? text + "\x1b[0m" : text;
}
