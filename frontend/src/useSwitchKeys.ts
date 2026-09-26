import { useEffect, useRef } from "react";
import { switchKey, switchTarget, type DeckEntry } from "./terminalOrder";

// Ctrl+Alt+Up and Down and Ctrl+Alt+1 to 9 switch terminals from anywhere on
// the board. They are caught on the way down, before a terminal sees them, so
// no program ever receives them.
export function useSwitchKeys(order: DeckEntry[], current: string, onSwitch: (key: string) => void) {
  const latest = useRef({ order, current, onSwitch });
  useEffect(() => { latest.current = { order, current, onSwitch }; });
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      const key = switchKey(event);
      // An open dialog, such as the Command Center, keeps its own keys.
      if (!key || document.querySelector("dialog[open]")) return;
      event.preventDefault();
      event.stopPropagation();
      if (event.repeat && "index" in key) return;
      const target = switchTarget(latest.current.order, latest.current.current, key);
      if (target) latest.current.onSwitch(target.key);
    };
    window.addEventListener("keydown", listener, true);
    return () => window.removeEventListener("keydown", listener, true);
  }, []);
}
