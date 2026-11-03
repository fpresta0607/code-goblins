import { useEffect, useRef } from "react";
import { request } from "./api";
import { inDesktopApp } from "./dictation";
import { windowMayMove } from "./window-move";

// useWindowMove asks the supervisor, once for each connection, to move the
// desktop window onto the program an update installed, once the window is in
// the tray. An update restarts the supervisor, so the page reconnects after
// each one and asks again. The supervisor moves only a window an update left
// on its old program, and answers that it did not move any other. The page
// looks as it connects, as it is hidden or shown, and as what he left unsent
// changes, which is all that can let it ask, so no timer looks again (#1598).
export function useWindowMove(instance: string, connected: boolean, answering: () => boolean): void {
  const asked = useRef(false);
  useEffect(() => {
    if (!connected) { asked.current = false; return; }
    const look = () => {
      if (asked.current || !windowMayMove({ inWindow: inDesktopApp(), connected, answering: answering(), hidden: document.hidden })) return;
      asked.current = true;
      request("/api/window/move", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ hidden: document.hidden }) }).catch(() => {});
    };
    look();
    document.addEventListener("visibilitychange", look);
    return () => document.removeEventListener("visibilitychange", look);
  }, [instance, connected, answering]);
}
