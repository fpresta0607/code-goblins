import type { Afk } from "./types.ts";

// AFK mode on the board: what the supervisor reports of the Overlord's switch,
// read into the words the board shows. The switch itself, and who may turn it,
// are the supervisor's.

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

// AFK_OFF is the switch as a board with no snapshot yet knows it: off.
export const AFK_OFF: Afk = { state: "off", since: "", from: "", asked: "", decided: 0, held: [], page: "" };

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
// offer and his page of the stretch, which have the room for them. It is empty while AFK
// mode is not on.
export function afkLine(afk: Afk, now: number, zone?: string, locale?: string): string {
  if (afk.state !== "on") return "";
  const who = afk.asked ? "turned on " + AT_YOUR_ASK : switchedBy(afk.from, "");
  return "AFK since " + afkTime(afk.since, now, zone, locale) + (who ? ", " + who : "") + ". " + afk.decided + " decided.";
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
