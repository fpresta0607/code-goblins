import { lazy, Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { AfkActionsContext } from "./afk-actions";
import { AfkOffer } from "./afk-offer";
import { AFK_OFF, offersOff, turnedOff } from "./afk";
import { useAfkSwitch } from "./useAfkSwitch";
import type { Afk, Snapshot } from "./types";

// The report is opened seldom, so its page arrives the first time it is shown.
const AfkReportPage = lazy(() => import("./afk-report").then((module) => ({ default: module.AfkReportPage })));

// AFK mode over the whole board, whatever view it shows: the report, which
// opens when AFK mode turns off and again when the CFO's bar asks, and the
// offer to turn it off at the Overlord's first click or key after he has been
// gone. Nothing here opens by itself while he is away: the report follows his
// own off, and the offer his own click or key. Until the first snapshot comes
// the board knows of no switch, which is off. onCommand opens the Command
// Center at an item, or at the first that waits when it names none.
export function AfkBoard({ snapshot, now, onCommand, children }: { snapshot: Snapshot | null; now: number; onCommand: (item: string) => void; children: ReactNode }) {
  const afk = snapshot?.afk ?? AFK_OFF;
  const [prior, setPrior] = useState<Afk>(afk);
  const [reporting, setReporting] = useState(false);
  const [offering, setOffering] = useState(false);
  const { pending, problem, turn, clear } = useAfkSwitch(snapshot?.instance ?? "");
  if (prior.state !== afk.state || prior.report !== afk.report) {
    setPrior(afk);
    if (turnedOff(prior, afk)) setReporting(true);
    if (afk.state !== "on") setOffering(false);
  }
  // The newest AFK state, the last click or key and the newest onCommand, for
  // the listener and the actions below.
  const latest = useRef(afk);
  const lastTouch = useRef(0);
  const command = useRef(onCommand);
  useEffect(() => { latest.current = afk; command.current = onCommand; });
  const away = afk.state === "on";
  useEffect(() => {
    if (!away) return;
    // Capture, on the window: a terminal and the Command Center keep their
    // keys from bubbling, and he is back whichever of them he touches. The
    // click or key still does what he meant by it, and the offer follows: a
    // press that opened the offer under itself would land on the offer. A
    // press on the AFK switch itself is already his answer.
    const touched = (event: Event) => {
      const at = Date.now();
      const onSwitch = event.target instanceof Element && !!event.target.closest(".afk-toggle, .afk-dialog");
      if (!onSwitch && offersOff(latest.current, lastTouch.current, at)) setOffering(true);
      lastTouch.current = at;
    };
    window.addEventListener("click", touched, true);
    window.addEventListener("keydown", touched, true);
    return () => { window.removeEventListener("click", touched, true); window.removeEventListener("keydown", touched, true); };
  }, [away]);
  const actions = useMemo(() => ({ openReport: () => setReporting(true), answer: (item: string) => command.current(item) }), []);
  return <AfkActionsContext.Provider value={actions}>
    {children}
    {offering && away && <AfkOffer afk={afk} now={now} pending={pending} problem={problem} onTurnOff={() => void turn(false)} onStay={() => { setOffering(false); clear(); }} />}
    {reporting && snapshot && <Suspense fallback={null}><AfkReportPage tasks={snapshot.tasks} now={now} onClose={() => setReporting(false)} onCommand={() => { setReporting(false); onCommand(""); }} /></Suspense>}
  </AfkActionsContext.Provider>;
}
