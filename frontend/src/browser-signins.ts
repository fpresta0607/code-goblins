import type { BrowserSignIn } from "./types.ts";

export const signInHeading = (signin: BrowserSignIn): string => "Sign in to " + signin.provider + " for " + signin.project;

export const signInOutcome = (signin: BrowserSignIn): string => signin.state === "signed_in"
  ? "Signed in to " + signin.provider + ", " + signin.account + ". The window closed and the fetch carries on."
  : signin.reason || "The sign-in request expired";
