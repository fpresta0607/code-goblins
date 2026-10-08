import { createContext, useContext } from "react";

// What the CFO's bar can ask of the board about AFK mode without the board's
// columns passing it down: open the report of the last stretch, show the
// report of the stretch his own switch just ended, and open a held item in
// the Command Center, where the Overlord answers it.
export interface AfkActions {
  openReport: () => void;
  turnedOff: () => void;
  answer: (item: string) => void;
}

const nothing = () => undefined;

export const AfkActionsContext = createContext<AfkActions>({ openReport: nothing, turnedOff: nothing, answer: nothing });

export const useAfkActions = (): AfkActions => useContext(AfkActionsContext);
