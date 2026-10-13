import { lazy, useSyncExternalStore, type ComponentType } from "react";
import { message, reportToCfo } from "./api";

// Whether a page of the board that arrives the first time it is shown could
// not arrive, which a tab loaded before an install meets: the supervisor
// serves the new build's pages and no longer has its own.
let isMissing = false;
const listeners = new Set<() => void>();

// boardPage is a page of the board that arrives the first time it is shown.
// One that cannot arrive draws nothing, so the board stays drawn around it,
// where an import that failed would take the whole board down (2026-10-13:
// AFK off after an update left his board black). The CFO hears why, and
// usePageMissing tells the board, which reloads onto the build the supervisor
// serves or offers Reload.
export function boardPage<Props extends object>(load: () => Promise<ComponentType<Props>>, name: string) {
  return lazy(async (): Promise<{ default: ComponentType<Props> }> => {
    try {
      return { default: await load() };
    } catch (error) {
      reportToCfo("loading the board's " + name, message(error));
      isMissing = true;
      for (const listener of listeners) listener();
      return { default: () => null };
    }
  });
}

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
};

export function usePageMissing(): boolean {
  return useSyncExternalStore(subscribe, () => isMissing);
}
