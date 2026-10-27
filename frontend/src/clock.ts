import { useEffect, useState } from "react";

// useNow is the time, read again at each whole step and once the page shows
// again, and never while it is hidden. The board's clocks show whole minutes,
// and a clock read every second redrew the whole board every second: 220 to
// 411 component renders a second on an idle board (#1599). A part that shows
// seconds reads its own clock at a step of a second, and only while it does.
// Each step falls on the wall clock's whole step, not on when what a card
// times began, so a card's whole minute can show up to 59 seconds late, which
// the Overlord accepted on 2026-10-27. A check of a time the supervisor wrote reads the later of
// this clock and when the stream took the latest snapshot, App's freshNow, so
// nothing reported since the last step looks to come from the future.
export function useNow(step: number): number {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = () => {
      setNow(Date.now());
      timer = document.hidden ? undefined : setTimeout(tick, step - (Date.now() % step));
    };
    const shown = () => {
      clearTimeout(timer);
      timer = undefined;
      if (!document.hidden) tick();
    };
    timer = setTimeout(tick, step - (Date.now() % step));
    document.addEventListener("visibilitychange", shown);
    return () => {
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", shown);
    };
  }, [step]);
  return now;
}
