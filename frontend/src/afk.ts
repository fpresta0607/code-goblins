import { array, boolean, number, object, parseAfkHeld, string, strings, type Afk, type AfkHeld } from "./types.ts";
import { freeGigabytes } from "./start.ts";
import { pullRequestLabel } from "./workflow.ts";

// AFK mode on the board: what the supervisor reports of the Overlord's switch,
// read into the words the board shows. The switch itself, and who may turn it,
// are the supervisor's.

// AfkDecision is one decision the CFO made under AFK mode's authority, with
// the evidence it stands on.
export interface AfkDecision {
  at: string; kind: string; what: string; link: string; evidence: string; outcome: string; task: string;
  // diagnosis and tried are what a line left for him found wrong and what the
  // CFO already tried, and struck the CFO's reason for striking a line it
  // logged by mistake, empty for a line it stands by.
  diagnosis: string; tried: string; struck: string;
}
// AfkSection is one heading of the report with the decisions under it.
export interface AfkSection {
  title: string; entries: AfkDecision[];
}
export interface AfkFinish {
  task: string; pr: string; at: string;
}
// AfkAllowance is one allowance under the report's Spent: a window's percent
// used when AFK mode turned on and when it turned off, null for a reading not
// taken, with reset when the window reset in between, or for credits what was
// spent of them in unit.
export interface AfkAllowance {
  provider: string; window: string; on: number | null; off: number | null; reset: boolean; credits: boolean; spent: number; unit: string;
}
// AfkDisk is the free space of the drive the board's disk meter reads, under
// the report's Spent: the bytes free when AFK mode turned on and when it
// turned off, of the drive's total.
export interface AfkDisk {
  drive: string; total: number; on: number; off: number;
}
// AfkReport is the report of a stretch of AFK mode: what the CFO decided, what
// each goblin finished, what is held for the Overlord and what was spent.
// asked and ended_asked hold his words when the CFO made that switch at his
// ask, and are empty when he made it himself. disk is null unless free disk
// was read at both ends.
export interface AfkReport {
  session: string; since: string; ended: string; lasted: string; from: string; asked: string; ended_from: string; ended_asked: string;
  sections: AfkSection[]; finished: AfkFinish[]; held: AfkHeld[]; spent: AfkAllowance[]; disk: AfkDisk | null; notes: string[];
}

const percent = (value: unknown): number | null => value == null ? null : number(value);

function parseAfkAllowance(value: unknown): AfkAllowance {
  const a = object(value);
  return {
    provider: string(a.provider), window: string(a.window), on: percent(a.on), off: percent(a.off),
    reset: a.reset == null ? false : boolean(a.reset), credits: a.credits == null ? false : boolean(a.credits), spent: number(a.spent), unit: string(a.unit),
  };
}

function parseAfkDisk(value: unknown): AfkDisk {
  const d = object(value);
  return { drive: string(d.drive), total: number(d.total), on: number(d.on), off: number(d.off) };
}

// parseAfkReport reads the supervisor's answer to GET /api/afk/report, which
// is null while no stretch has ended.
export function parseAfkReport(value: unknown): AfkReport | null {
  const v = object(value);
  if (v.found !== true) return null;
  return {
    session: string(v.session), since: string(v.since), ended: string(v.ended), lasted: string(v.lasted), from: string(v.from), asked: string(v.asked), ended_from: string(v.ended_from), ended_asked: string(v.ended_asked),
    sections: array(v.sections).map((value) => {
      const s = object(value);
      return {
        title: string(s.title),
        entries: array(s.entries).map((value) => {
          const e = object(value);
          return { at: string(e.at), kind: string(e.kind), what: string(e.what), link: string(e.link), evidence: string(e.evidence), outcome: string(e.outcome), task: string(e.task), diagnosis: string(e.diagnosis), tried: string(e.tried), struck: string(e.struck) };
        }),
      };
    }),
    finished: array(v.finished).map((value) => { const f = object(value); return { task: string(f.task), pr: string(f.pr), at: string(f.at) }; }),
    held: array(v.held).map(parseAfkHeld),
    spent: array(v.spent).map(parseAfkAllowance),
    disk: v.disk == null ? null : parseAfkDisk(v.disk),
    notes: strings(v.notes),
  };
}

const shown = (value: number): string => String(Math.round(value * 10) / 10);

// shownUnder says whether an allowance is one Spent shows: a weekly limit, or
// credits, which the supervisor reports only once some were spent. A shorter
// window, such as Claude's five hours, is left out, as the Overlord asked on
// 2026-10-08 ("dont need to show 5 hour limit make it simpler like how much
// is left in weekly limits only and credits only iff used").
export const shownUnder = (allowance: AfkAllowance): boolean => allowance.credits || /\bweek/i.test(allowance.window);

// allowanceSays is one allowance under Spent as the report shows it: its name,
// how much of a weekly limit is left at its last reading, or the credits
// spent, how much of the limit AFK mode used, and the words a screen reader
// says for its graph. A limit that renewed in between started again from
// nothing, and a reading not taken leaves no change to say, so none is said,
// never a line saying it was not read, as he asked on 2026-10-07.
export function allowanceSays(allowance: AfkAllowance): { name: string; value: string; change: string; label: string } {
  const provider = allowance.provider.charAt(0).toUpperCase() + allowance.provider.slice(1);
  if (allowance.credits) {
    const spent = shown(allowance.spent) + " " + allowance.unit + " spent";
    return { name: provider + " credits", value: spent, change: "", label: provider + " credits: " + spent };
  }
  const name = provider + " " + allowance.window.replace(/\bweek\b/i, "weekly") + " limit";
  const { on, off } = allowance;
  const last = off ?? on ?? 0;
  const value = shown(Math.max(0, 100 - last)) + "% left";
  if (on !== null && off !== null) {
    const change = allowance.reset ? "renewed" : "AFK used " + shown(Math.max(0, off - on)) + "%";
    return { name, value, change, label: name + ": " + value + ", " + (allowance.reset ? "renewed while AFK was on" : "from " + shown(on) + "% to " + shown(off) + "% used while AFK was on") };
  }
  return { name, value, change: "", label: name + ": " + value + " at AFK " + (on !== null ? "on" : "off") };
}

// allowanceGraph is where an allowance's graph under Spent marks, as percents
// of its bar: before is what was used before AFK mode turned on, and from and
// to the stretch it used while on, which its arrow spans. A window that reset
// in between used its whole stretch from nothing. A limit read at only one
// end has no graph, since what was used before AFK and while it was on cannot
// be told apart, and neither do credits.
export function allowanceGraph(allowance: AfkAllowance): { before: number; from: number; to: number } | null {
  const { on, off } = allowance;
  if (allowance.credits || on === null || off === null) return null;
  if (allowance.reset) return { before: 0, from: 0, to: off };
  return { before: Math.min(on, off), from: Math.min(on, off), to: off };
}

// diskMoved is how far free disk moved while AFK mode was on, in gigabytes to
// the one decimal the report says: under zero when AFK used disk, over zero
// when it freed some, and zero for a change too small to say.
const diskMoved = (disk: AfkDisk): number => Math.round((disk.off - disk.on) / 2 ** 30 * 10) / 10;

// diskSays is free disk under Spent as the report shows it, worded as the
// allowances beside it are: the drive, how much of it was free when AFK
// turned off, how much AFK used or freed, and the words a screen reader says
// for its graph.
export function diskSays(disk: AfkDisk): { name: string; value: string; change: string; label: string } {
  const name = "Disk" + (disk.drive ? " (" + disk.drive + ")" : "");
  const value = freeGigabytes(disk.off) + " GB free";
  const moved = diskMoved(disk);
  const change = moved > 0 ? "AFK freed " + shown(moved) + " GB" : "AFK used " + shown(Math.abs(moved)) + " GB";
  return { name, value, change, label: name + ": " + value + ", from " + freeGigabytes(disk.on) + " GB to " + freeGigabytes(disk.off) + " GB free while AFK was on" };
}

// diskGraph is where free disk's graph under Spent marks, as percents of the
// drive: before is what was used all through, and from and to where its use
// stood when AFK mode turned on and when it turned off, which its arrow spans,
// pointing back when AFK freed disk. A change too small to say moves nothing,
// and a drive of no size has no graph.
export function diskGraph(disk: AfkDisk): { before: number; from: number; to: number } | null {
  if (disk.total <= 0) return null;
  const used = (free: number) => Math.round(Math.min(100, Math.max(0, (1 - free / disk.total) * 100)) * 100) / 100;
  const from = used(disk.on), to = diskMoved(disk) === 0 ? from : used(disk.off);
  return { before: Math.min(from, to), from, to };
}

// afkTime is a time as the board says it: the time of day, with the day in
// front when it is not today. zone and locale are the reader's own unless a
// test names them.
export function afkTime(at: string, now: number, zone?: string, locale?: string): string {
  const date = new Date(at);
  if (Number.isNaN(date.getTime())) return "";
  const time = date.toLocaleTimeString(locale, { hour: "numeric", minute: "2-digit", timeZone: zone });
  const day = (when: Date) => when.toLocaleDateString(locale, { year: "numeric", month: "short", day: "numeric", timeZone: zone });
  return day(date) === day(new Date(now)) ? time : date.toLocaleDateString(locale, { month: "short", day: "numeric", timeZone: zone }) + ", " + time;
}

// stillWaiting are the items held or left for him that still wait on the
// Overlord, and settled those that no longer do, each with what became of it.
export const stillWaiting = (held: AfkHeld[]): AfkHeld[] => held.filter((one) => one.waiting);
export const settled = (held: AfkHeld[]): AfkHeld[] => held.filter((one) => !one.waiting);

// counted is a count with its noun, one or many.
const counted = (count: number, one: string, many: string): string => count + " " + (count === 1 ? one : many);

// listed joins phrases as a sentence lists them: a, b and c.
const listed = (phrases: string[]): string => phrases.length < 2 ? phrases.join("") : phrases.slice(0, -1).join(", ") + " and " + phrases[phrases.length - 1];

// What the CFO did that the report's headline names, by the heading it is
// under: what reached his repositories, his production and his machine.
const HEADLINED: [string, (count: number) => string][] = [
  ["Merged", (count) => "merged " + counted(count, "pull request", "pull requests")],
  ["Deployed", (count) => "made " + counted(count, "deploy", "deploys")],
  ["Migrations applied", (count) => "applied " + counted(count, "migration", "migrations")],
  ["Installed", (count) => "installed " + counted(count, "build", "builds")],
];

// afkHeadline is the report's first words, in place of a count for every
// heading: how long he was away, how many things wait on him, and what the
// CFO merged, deployed, migrated and installed, which is empty when it did
// none of those. zone and locale are the reader's own unless a test names
// them.
export function afkHeadline(report: AfkReport, now: number, zone?: string, locale?: string): { away: string; waiting: string; did: string } {
  const open = stillWaiting(report.held).length;
  const did = HEADLINED.flatMap(([title, says]) => {
    const count = report.sections.find((section) => section.title === title)?.entries.length ?? 0;
    return count ? [says(count)] : [];
  });
  return {
    away: "You were away " + report.lasted + ", from " + afkTime(report.since, now, zone, locale) + " to " + afkTime(report.ended, now, zone, locale) + ".",
    waiting: open ? counted(open, "thing waits", "things wait") + " on you." : "Nothing waits on you.",
    did: did.length ? "The CFO " + listed(did) + "." : "",
  };
}

// AFK_OFF is the switch as a board with no snapshot yet knows it: off.
export const AFK_OFF: Afk = { state: "off", since: "", from: "", asked: "", decided: 0, held: [], report: "" };

// yours is where the Overlord made the switch, in the words he reads. The
// supervisor words it for the CFO, as his own terminal or board with the
// program and its process, which the log keeps.
const yours = (from: string): string => from.replace(/^his own /, "your ").replace(/ \(.*\)$/, "");

// AT_YOUR_ASK is who made a switch the CFO made at the Overlord's ask.
const AT_YOUR_ASK = "by the CFO at your ask";

// switchedBy says who made a switch, to follow "turned on" or "off": the
// Overlord, from where he did it, or the CFO at his ask, with the words of
// his the CFO quoted. It is empty when the supervisor said neither.
export function switchedBy(from: string, asked: string): string {
  if (asked) return AT_YOUR_ASK + ": “" + asked + "”";
  return from ? "from " + yours(from) : "";
}

// afkLine is what the CFO's bar says while AFK mode is on: since when, who
// turned it on and from where, and how much the CFO decided. Nothing is held
// for him while it is on, so it counts nothing held. His words are left to the
// offer and the report, which have the room for them. It is empty while AFK
// mode is not on.
export function afkLine(afk: Afk, now: number, zone?: string, locale?: string): string {
  if (afk.state !== "on") return "";
  const who = afk.asked ? "turned on " + AT_YOUR_ASK : switchedBy(afk.from, "");
  return "AFK since " + afkTime(afk.since, now, zone, locale) + (who ? ", " + who : "") + ". " + afk.decided + " decided.";
}


// heldWho names whose a held item is: the goblin's, or the CFO's own.
export const heldWho = (held: AfkHeld): string => held.task || "CFO";

// heldSays is what became of a held item, and what its goblin did meanwhile.
export function heldSays(held: AfkHeld): string {
  const sentence = (text: string) => { const said = text.trim().replace(/\.$/, ""); return said ? said.charAt(0).toUpperCase() + said.slice(1) + "." : ""; };
  return [sentence(held.now), held.meanwhile ? sentence("Meanwhile: " + held.meanwhile) : ""].filter(Boolean).join(" ");
}

// heldRecommends says what was recommended for a held item and by whom: the
// CFO for its own item, or the goblin whose question it is. It is in the
// present while the item still waits on him, in the past once it does not,
// and empty when nothing was recommended.
export function heldRecommends(held: AfkHeld): string {
  if (!held.recommendation) return "";
  return (held.task || "The CFO") + (held.waiting ? " recommends: " : " recommended: ") + held.recommendation.replace(/\.$/, "") + ".";
}

// AWAY_MS is how long the board sees no click or key before it takes the next
// one for the Overlord coming back.
export const AWAY_MS = 5 * 60 * 1000;

// Occasion is why a click or key brings the offer to turn AFK mode off:
// "back" when he is back after the board saw none for a while, "asked" when it
// is his first since the CFO turned it on at his ask, and "" when it brings
// nothing.
export type Occasion = "" | "back" | "asked";

// offerFor is what a click or key at now is to the offer. While AFK mode is
// on it is "back" when nothing was clicked or typed on this page for AWAY_MS,
// counted from when it turned on or from his last click or key (lastTouch,
// zero for none), the later of the two, and "asked" when the CFO turned it on
// at his ask and this is his first click or key since, so a switch made on
// his words meets him at once. The clicks he makes right after turning it on
// himself bring nothing, and the first one after he has been gone does.
export function offerFor(afk: Afk, lastTouch: number, now: number): Occasion {
  if (afk.state !== "on") return "";
  const parsed = Date.parse(afk.since);
  const since = Number.isNaN(parsed) ? 0 : parsed;
  if (now - Math.max(since, lastTouch) >= AWAY_MS) return "back";
  return afk.asked && lastTouch < since ? "asked" : "";
}

// safeLink is url when it is a web link the board may open, and empty for
// anything else: the log's words are an agent's, never a script to run.
export const safeLink = (url: string): string => /^https:\/\/[^\s]+$/i.test(url) ? url : "";

// decisionSays is one decision as the report lists it: whose and what, the
// link it opens, how it ended, and what it stood on. A pull request logged by
// its link is named the way a person would say it. An answer is logged under
// its question's id, which tells a reader nothing, so its goblin names it and
// the question and the answer, which are its evidence, follow as two
// sentences. A merge word with no outcome says so, since a word given is not
// a merge made. A line left for him adds what was found wrong and what was
// tried, and a struck line leads with why the CFO struck it; a strike of an
// earlier stretch's line carries only that reason.
export function decisionSays(entry: AfkDecision): { text: string; href: string; outcome: string; basis: string } {
  const answered = entry.kind === "answer" && entry.task !== "";
  const what = safeLink(entry.what) && /\/pull\/\d+/.test(entry.what) ? pullRequestLabel(entry.what) : entry.what;
  return {
    text: answered ? entry.task : (entry.task ? entry.task + ": " : "") + what,
    href: safeLink(entry.link) || safeLink(entry.what),
    outcome: entry.kind === "merge" && !entry.outcome ? "no outcome was recorded" : entry.outcome,
    // The log words an answer as "asked: ... answered: ...".
    basis: answered ? entry.evidence.replace(/^asked: /, "Asked: ").replace(" answered: ", " Answered: ") : basisOf(entry),
  };
}

// basisOf is what a decision stood on, as sentences: why it was struck, its
// evidence, and for a line left for him what was found and tried.
function basisOf(entry: AfkDecision): string {
  const sentence = (label: string, text: string) => text ? label + ": " + text.replace(/\.$/, "") + "." : "";
  if (entry.kind === "strike") return sentence("Struck", entry.struck);
  if (!entry.struck && !entry.diagnosis && !entry.tried) return "Evidence: " + entry.evidence;
  return [sentence("Struck", entry.struck), sentence("Evidence", entry.evidence), sentence("Found", entry.diagnosis), sentence("Tried", entry.tried)].filter(Boolean).join(" ");
}

// turnedOff says whether AFK mode turned off between two snapshots with a
// report kept, which is when the board shows the report.
export const turnedOff = (previous: Afk, next: Afk): boolean => previous.state === "on" && next.state === "off" && next.report !== "";
