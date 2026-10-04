import { createContext, useContext } from "react";

// What the CFO's bar can ask of the board about AFK mode without the board's
// columns passing it down: open the report of the last stretch, and open a
// held item in the Command Center, where the Overlord answers it.
export interface AfkActions {
  openReport: () => void;
  answer: (item: string) => void;
}

const nothing = () => undefined;

export const AfkActionsContext = createContext<AfkActions>({ openReport: nothing, answer: nothing });

export const useAfkActions = (): AfkActions => useContext(AfkActionsContext);
