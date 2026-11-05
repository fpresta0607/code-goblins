import type { ReactNode } from "react";

// A terminal spec's second run, the stepped-in project in
// playwright.config.ts, draws each terminal in a Messages view's place, in
// the stage the board draws it in, with the messages mounted and hidden under
// it, as Classic draws a session the board reads as messages.
export function SteppedIn({ children }: { children: ReactNode }) {
  if (sessionStorage.getItem("fixture-stepped-in") !== "true") return <>{children}</>;
  return <div className="panel-stage">
    <section className="messages-view" aria-label="Messages" hidden />
    {children}
  </div>;
}
