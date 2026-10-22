import type { PanelView } from "./GoblinPanel";
import type { Session, Task } from "./types";
import { sessionEnd } from "./session-end.ts";

// How long a goblin's session has run, or a queued task has waited, in whole
// minutes, hours or days; empty when the start is unknown, so a card never
// shows a clock it cannot back.
export function clockText(since: string, now: number, kind: "running" | "waiting"): string {
  const start = since && !since.startsWith("0001") ? Date.parse(since) : NaN;
  if (!Number.isFinite(start)) return "";
  const minutes = Math.max(0, Math.floor((now - start) / 60_000));
  if (minutes < 1) return kind === "running" ? "just started" : "just queued";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

// When a card's goblin paused, finished or resumes, or its train ended, kept
// short (the Overlord, 2026-10-17: "why do resume dates need to be so long
// and typed out, avoid overlap"): the time of day when that is today, the
// weekday within six days of today either way, else the month and day.
export function cardDate(at: Date, now: number): string {
  const day = (date: Date) => Date.UTC(date.getFullYear(), date.getMonth(), date.getDate());
  const days = Math.round((day(at) - day(new Date(now))) / 86_400_000);
  if (days === 0) return at.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  if (Math.abs(days) <= 6) return at.toLocaleDateString(undefined, { weekday: "short" });
  return at.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// The whole date and time a short card date stands for, for its tip.
export const fullDate = (at: Date): string => at.toLocaleString(undefined, { weekday: "short", year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });

// A goblin's description is cut to a few lines with Show more while it runs
// past them; opened, it offers Show less.
export function showMoreLabel(expanded: boolean, overflowing: boolean): "Show more" | "Show less" | "" {
  if (expanded) return "Show less";
  return overflowing ? "Show more" : "";
}

// The views a panel offers: a queued task has no terminal yet, so its panel
// is its Task view alone; the CFO, a started task and a reported child
// session each have a terminal too, and a session whose messages the board
// reads (messagesQuery) its Messages view beside it.
export function panelViews(task?: Task, node?: Session, hasMessages = false): PanelView[] {
  const views = terminalViews(task, node);
  return hasMessages && views.includes("terminal") ? ["messages", ...views] : views;
}

function terminalViews(task?: Task, node?: Session): PanelView[] {
  if (task && sessionEnd(task)) return ["task", "terminal"];
  if (task && (task.archived || ["pausing", "stopping"].includes(task.phase))) return ["task"];
  // A queued task whose Start is on its way takes a message in its Terminal
  // view until its terminal is up.
  return task && !task.generation && !task.starting && !node ? ["task"] : ["task", "terminal"];
}
