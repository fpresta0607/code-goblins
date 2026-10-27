import { useEffect, useState } from "react";

// useNow is the time, read again at each whole step and once the page shows
// again, and never while it is hidden. The board's clocks show whole minutes,
// and a clock read every second redrew the whole board every second: 220 to
// 411 component renders a second on an idle board (#1599). A part that shows
// seconds reads its own clock at a step of a second, and only while it does.
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
