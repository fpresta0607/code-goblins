import { listNames } from "./credentials.ts";
import { array, object, string, strings } from "./types.ts";

// A browser login a goblin's Connections lists: one its project may use,
// whether he is signed in to it, when he last signed in and when a fetch
// last used it. It holds nothing of the session.
export type BrowserLogin = {
  id: string; provider: string; account: string; projects: string[];
  signedIn: boolean; signedInAt: string; usedAt: string; signsOutAfterFetch: boolean; inUse: boolean;
};

export function parseBrowserLogins(value: unknown): BrowserLogin[] {
  return array(value).map((item) => {
    const login = object(item);
    return {
      id: string(login.id), provider: string(login.provider), account: string(login.account), projects: strings(login.projects),
      signedIn: login.signed_in === true, signedInAt: string(login.signed_in_at), usedAt: string(login.used_at),
      signsOutAfterFetch: login.signs_out_after_fetch === true, inUse: login.in_use === true,
    };
  });
}

// How long before now a time was, as a person says it, or "" for no time.
export function ago(time: string, now: number): string {
  const at = Date.parse(time);
  if (!Number.isFinite(at)) return "";
  const minutes = Math.max(0, Math.floor((now - at) / 60_000));
  if (minutes < 1) return "just now";
  const [count, unit] = minutes < 60 ? [minutes, "minute"] : minutes < 24 * 60 ? [Math.floor(minutes / 60), "hour"] : [Math.floor(minutes / (24 * 60)), "day"];
  return `${count} ${unit}${count === 1 ? "" : "s"} ago`;
}

export const loginName = (login: BrowserLogin) => login.provider + " · " + login.account;

// What a login's row says under its name: the projects it serves, and when
// he signed in and a fetch last used it.
export function loginDetail(login: BrowserLogin, now: number): string {
  const used = ago(login.usedAt, now);
  const parts = ["For " + listNames(login.projects) + "."];
  if (login.signsOutAfterFetch) parts.push("It signs out after each fetch, so each fetch brings the sign-in card.");
  if (login.signedIn) parts.push(`You signed in ${ago(login.signedInAt, now)}` + (used ? `, a fetch used it ${used}.` : "."));
  else {
    if (!login.signsOutAfterFetch) parts.push("You are signed out, so the next fetch brings the sign-in card.");
    if (used) parts.push(`Last used ${used}.`);
  }
  return parts.join(" ");
}

// What forgetting a login does, for the dialog that asks first.
export function forgetDetail(login: BrowserLogin): string {
  return `${loginName(login)}. Its browser profile is deleted, so the next fetch for ${listNames(login.projects)} asks you to sign in again. Your ${login.provider} account and its keys are not touched.`;
}
