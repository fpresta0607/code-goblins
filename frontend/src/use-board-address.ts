import { useEffect, useRef } from "react";
import { boardPlace, OPEN_ADDRESS } from "./board-address";
import { waitingItems } from "./commandQueue";
import type { Snapshot } from "./types";

// useBoardAddress opens the place a board address names (#2363): the page's
// own address as it loads, once the board has a snapshot to know the place
// by, and an address a link in the board or a tapped push hands it later, in
// this page with no reload. Only a goblin the board holds opens, by openTask,
// or an item waiting on him, by openItem; any other address opens nothing.
// The page's address goes back to plain once read, so a reload opens nothing
// again.
export function useBoardAddress(snapshot: Snapshot | null | undefined, openTask: (id: string) => void, openItem: (key: string) => void): void {
  const latest = useRef({ snapshot, openTask, openItem });
  useEffect(() => { latest.current = { snapshot, openTask, openItem }; });
  const hasSnapshot = !!snapshot;
  useEffect(() => {
    if (!hasSnapshot) return;
    const open = (address: string) => {
      const place = boardPlace(address, location.origin);
      const { snapshot: board, openTask: toTask, openItem: toItem } = latest.current;
      if (!place || !board) return;
      if (place.kind === "task" && board.tasks.some((task) => task.id === place.id)) toTask(place.id);
      if (place.kind === "item" && waitingItems(board).some((item) => item.key === place.id)) toItem(place.id);
    };
    const loaded = location.href;
    if (boardPlace(loaded, location.origin)) {
      history.replaceState(history.state, "", location.pathname);
      open(loaded);
    }
    const heard = (event: Event) => {
      if (event instanceof CustomEvent && typeof event.detail === "string") open(event.detail);
    };
    // A tapped push's address, from the app's push worker (public/push-worker.js).
    const told = (event: MessageEvent<unknown>) => {
      const { data } = event;
      if (typeof data === "object" && data !== null && "open" in data && typeof data.open === "string") open(data.open);
    };
    // A page served over plain http on another machine has no workers.
    const workers = "serviceWorker" in navigator ? navigator.serviceWorker : null;
    window.addEventListener(OPEN_ADDRESS, heard);
    workers?.addEventListener("message", told);
    return () => {
      window.removeEventListener(OPEN_ADDRESS, heard);
      workers?.removeEventListener("message", told);
    };
  }, [hasSnapshot]);
}
