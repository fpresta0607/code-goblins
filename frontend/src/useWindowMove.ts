import { useEffect, useRef } from "react";
import { request } from "./api";
import { inDesktopApp } from "./dictation";
import { windowMayMove } from "./window-move";

// LOOK_MS spaces the page's looks at whether it may ask.
const LOOK_MS = 30_000;

// useWindowMove asks the supervisor, once for each connection, to move the
// desktop window onto the program an update installed, once the window is in
// the tray. An update restarts the supervisor, so the page reconnects after
// each one and asks again. The supervisor moves only a window an update left
// on its old program, and answers that it did not move any other.
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
    const timer = setInterval(look, LOOK_MS);
    document.addEventListener("visibilitychange", look);
    return () => { clearInterval(timer); document.removeEventListener("visibilitychange", look); };
  }, [instance, connected, answering]);
}
