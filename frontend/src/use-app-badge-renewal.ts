import { useEffect } from "react";
import { message, reportToCeo } from "./api";
import { isPushingApp, renewAppBadge } from "./app-badge";

// useAppBadgeRenewal renews his phone app's App badge each time the app
// opens, wherever on the board it opens.
export function useAppBadgeRenewal(instance: string): void {
  useEffect(() => {
    if (!instance || !isPushingApp()) return;
    renewAppBadge(instance).catch((error: unknown) => reportToCeo("the app badge", message(error)));
  }, [instance]);
}
