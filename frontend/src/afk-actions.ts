import { createContext, useContext } from "react";

// What the CFO's panel can ask of the board about AFK mode without the
// board's columns passing it down: open the report of the last stretch, and
// show the report of the stretch his own switch just ended.
export interface AfkActions {
  openReport: () => void;
  turnedOff: () => void;
}

const nothing = () => undefined;

export const AfkActionsContext = createContext<AfkActions>({ openReport: nothing, turnedOff: nothing });

export const useAfkActions = (): AfkActions => useContext(AfkActionsContext);
