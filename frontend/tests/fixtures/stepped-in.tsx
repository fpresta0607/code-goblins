import type { ReactNode } from "react";
import { PanelStage } from "../../src/panel-stage";

// A terminal spec's second run, the stepped-in project in
// playwright.config.ts, draws each terminal stepped into a Messages view's
// place, in the stage the board draws it in, with the messages hidden and the
// Back to messages bar floating over the terminal's top edge.
export function SteppedIn({ children }: { children: ReactNode }) {
  if (sessionStorage.getItem("fixture-stepped-in") !== "true") return <>{children}</>;
  return <PanelStage hidden={false} isSteppedIn onBack={() => {}}>
    <section className="messages-view" aria-label="Messages" hidden />
    {children}
  </PanelStage>;
}
