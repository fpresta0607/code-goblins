import { useEffect, useRef, useState, type ReactNode } from "react";
import { AfkOffer } from "./afk-offer";
import { AfkReport } from "./afk-report";
import { AFK_OFF, OPEN_AFK_REPORT, offerFor, type Occasion } from "./afk";
import { useAfkSwitch } from "./useAfkSwitch";
import { waitingItems } from "./commandQueue";
import type { Snapshot } from "./types";

// AFK mode over the whole board, whatever view it shows: the offer to turn it
// off at the Overlord's first click or key after he has been gone, or after
// the CFO turned it on at his ask. Nothing here opens by itself while he is
// away: the offer follows his own click or key. What the stretch came to is
// a report modal once AFK mode turns off.
// Until the first snapshot comes the board knows of no switch, which is off.
export function AfkBoard({ snapshot, now, children, onOpenCommand }: { snapshot: Snapshot | null; now: number; children: ReactNode; onOpenCommand: (key: string) => void }) {
  const afk = snapshot?.afk ?? AFK_OFF;
  const [priorState, setPriorState] = useState(afk.state);
  const [offering, setOffering] = useState<Occasion>("");
  const [isReportOpen, setIsReportOpen] = useState(false);
  const { pending, problem, turn, clear } = useAfkSwitch(snapshot?.instance ?? "");
  if (priorState !== afk.state) {
    if (priorState === "on" && afk.state === "off") setIsReportOpen(true);
    if (afk.state === "on") setIsReportOpen(false);
    setPriorState(afk.state);
    if (afk.state !== "on") setOffering("");
  }
  useEffect(() => {
    const open = () => setIsReportOpen(true);
    window.addEventListener(OPEN_AFK_REPORT, open);
    return () => window.removeEventListener(OPEN_AFK_REPORT, open);
  }, []);
  // The newest AFK state and the last click or key, for the listener below.
  const latest = useRef(afk);
  const lastTouch = useRef(0);
  useEffect(() => { latest.current = afk; });
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
      const occasion = onSwitch ? "" : offerFor(latest.current, lastTouch.current, at);
      if (occasion) setOffering(occasion);
      lastTouch.current = at;
    };
    window.addEventListener("click", touched, true);
    window.addEventListener("keydown", touched, true);
    return () => { window.removeEventListener("click", touched, true); window.removeEventListener("keydown", touched, true); };
  }, [away]);
  const turnOff = async () => { if (await turn(false)) setOffering(""); };
  return <>
    {children}
    {isReportOpen && afk.state === "off" && <AfkReport page={afk.page} hasPendingItems={!!snapshot && waitingItems(snapshot).length > 0} onClose={() => setIsReportOpen(false)} onOpenCommand={onOpenCommand} />}
    {offering && away && <AfkOffer afk={afk} occasion={offering} now={now} pending={pending} problem={problem} onTurnOff={() => void turnOff()} onStay={() => { setOffering(""); clear(); }} />}
  </>;
}
