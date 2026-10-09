import { useEffect, useRef } from "react";
import { request } from "./api";
import { inDesktopApp } from "./dictation";
import { windowMayMove } from "./window-move";

// LOOK_MS spaces the page's looks at whether it is idle.
const LOOK_MS = 30_000;

// useWindowMove asks the supervisor, once for each connection, to move the
// desktop window onto the program an update installed, as soon as the page
// is idle. An update restarts the supervisor, so the page reconnects after
// each one and asks again. The supervisor moves only a window an update left
// on its old program, and answers that it did not move any other.
export function useWindowMove(instance: string, connected: boolean, answering: () => boolean): void {
  const asked = useRef(false);
  // When he last clicked or pressed a key on the board, from when the page
  // loaded.
  const touched = useRef(0);
  useEffect(() => {
    const touch = () => { touched.current = Date.now(); };
    touch();
    document.addEventListener("pointerdown", touch, true);
    document.addEventListener("keydown", touch, true);
    return () => { document.removeEventListener("pointerdown", touch, true); document.removeEventListener("keydown", touch, true); };
  }, []);
  useEffect(() => {
    if (!connected) { asked.current = false; return; }
    const look = () => {
      if (asked.current || !windowMayMove({ inWindow: inDesktopApp(), connected, answering: answering(), hidden: document.hidden, quietFor: Date.now() - touched.current })) return;
      asked.current = true;
      request("/api/window/move", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ hidden: document.hidden }) }).catch(() => {});
    };
    look();
    const timer = setInterval(look, LOOK_MS);
    document.addEventListener("visibilitychange", look);
    return () => { clearInterval(timer); document.removeEventListener("visibilitychange", look); };
  }, [instance, connected, answering]);
}
