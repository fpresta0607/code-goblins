import { array, object, parseAfkHeld, string, strings, type Afk, type AfkHeld } from "./types.ts";
import { pullRequestLabel } from "./workflow.ts";

// AFK mode on the board: what the supervisor reports of the Overlord's switch,
// read into the words the board shows. The switch itself, and who may turn it,
// are the supervisor's.

// AfkDecision is one decision the CFO made under AFK mode's authority, with
// the evidence it stands on.
export interface AfkDecision {
  at: string; kind: string; what: string; link: string; evidence: string; outcome: string; task: string;
}
// AfkSection is one heading of the report with the decisions under it.
export interface AfkSection {
  title: string; entries: AfkDecision[];
}
export interface AfkFinish {
  task: string; pr: string; at: string;
}
// AfkReport is the report of a stretch of AFK mode: what the CFO decided, what
// each goblin finished, what is held for the Overlord and what was spent.
// asked and ended_asked hold his words when the CFO made that switch at his
// ask, and are empty when he made it himself.
export interface AfkReport {
  session: string; since: string; ended: string; lasted: string; from: string; asked: string; ended_from: string; ended_asked: string;
  sections: AfkSection[]; finished: AfkFinish[]; held: AfkHeld[]; spent: string[]; notes: string[];
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
          return { at: string(e.at), kind: string(e.kind), what: string(e.what), link: string(e.link), evidence: string(e.evidence), outcome: string(e.outcome), task: string(e.task) };
        }),
      };
    }),
    finished: array(v.finished).map((value) => { const f = object(value); return { task: string(f.task), pr: string(f.pr), at: string(f.at) }; }),
    held: array(v.held).map(parseAfkHeld),
    spent: strings(v.spent),
    notes: strings(v.notes),
  };
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

// stillWaiting are the held items that still wait on the Overlord, and
// stillHeld those of the stretch that is on.
export const stillWaiting = (held: AfkHeld[]): AfkHeld[] => held.filter((one) => one.waiting);
export const stillHeld = (afk: Afk): AfkHeld[] => stillWaiting(afk.held);

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
// turned it on and from where, how much the CFO decided and how much still
// waits on him. His words are left to the offer and the report, which have
// the room for them. It is empty while AFK mode is not on.
export function afkLine(afk: Afk, now: number, zone?: string, locale?: string): string {
  if (afk.state !== "on") return "";
  const who = afk.asked ? "turned on " + AT_YOUR_ASK : switchedBy(afk.from, "");
  return "AFK since " + afkTime(afk.since, now, zone, locale) + (who ? ", " + who : "") + ". " + afk.decided + " decided, " + stillHeld(afk).length + " held for you.";
}

// UNREADABLE is what the CFO panel's header says under its toggle while the
// switch cannot be read. Such a switch is never taken for on, so the board
// prompts him as usual, and his press on the toggle puts it back to off.
export const UNREADABLE = "AFK's switch cannot be read, so nothing is decided for you. Press the toggle to reset it.";

// heldWho names whose a held item is: the goblin's, or the CFO's own.
export const heldWho = (held: AfkHeld): string => held.task || "CFO";

// heldSays is what became of a held item, and what its goblin did meanwhile.
export function heldSays(held: AfkHeld): string {
  const sentence = (text: string) => { const said = text.trim().replace(/\.$/, ""); return said ? said.charAt(0).toUpperCase() + said.slice(1) + "." : ""; };
  return [sentence(held.now), held.meanwhile ? sentence("Meanwhile: " + held.meanwhile) : ""].filter(Boolean).join(" ");
}

// AWAY_MS is how long the board sees no click or key before it takes the next
// one for the Overlord coming back.
export const AWAY_MS = 5 * 60 * 1000;

// offersOff says whether a click or key at now is the Overlord coming back,
// which is when the board offers to turn AFK mode off: AFK mode is on, and
// nothing was clicked or typed on this page for AWAY_MS, counted from when it
// turned on or from his last click or key (lastTouch, zero for none), the
// later of the two. So the clicks he makes right after turning it on offer
// nothing, and the first one after he has been gone does.
export function offersOff(afk: Afk, lastTouch: number, now: number): boolean {
  if (afk.state !== "on") return false;
  const since = Date.parse(afk.since);
  return now - Math.max(Number.isNaN(since) ? 0 : since, lastTouch) >= AWAY_MS;
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
// a merge made.
export function decisionSays(entry: AfkDecision): { text: string; href: string; outcome: string; basis: string } {
  const answered = entry.kind === "answer" && entry.task !== "";
  const what = safeLink(entry.what) && /\/pull\/\d+/.test(entry.what) ? pullRequestLabel(entry.what) : entry.what;
  return {
    text: answered ? entry.task : (entry.task ? entry.task + ": " : "") + what,
    href: safeLink(entry.link) || safeLink(entry.what),
    outcome: entry.kind === "merge" && !entry.outcome ? "no outcome was recorded" : entry.outcome,
    // The log words an answer as "asked: ... answered: ...".
    basis: answered ? entry.evidence.replace(/^asked: /, "Asked: ").replace(" answered: ", " Answered: ") : "Evidence: " + entry.evidence,
  };
}

// turnedOff says whether AFK mode turned off between two snapshots with a
// report kept, which is when the board shows the report.
export const turnedOff = (previous: Afk, next: Afk): boolean => previous.state === "on" && next.state === "off" && next.report !== "";
