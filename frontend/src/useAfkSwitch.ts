import { useState } from "react";
import { message, request } from "./api";
import { useClickFeedback } from "./click-feedback";

export interface AfkSwitch {
  pending: boolean;
  // problem is what his press met, for a moment.
  problem: string;
  turn: (on: boolean) => Promise<boolean>;
  clear: () => void;
}

// useAfkSwitch asks the supervisor to turn AFK mode on or off from this board.
// Whether the request is the Overlord's own is the supervisor's to prove, so
// nothing here decides it, and a refusal comes back as problem for a moment.
export function useAfkSwitch(instance: string): AfkSwitch {
  const [pending, setPending] = useState(false);
  const [problem, showProblem] = useClickFeedback();
  const turn = async (on: boolean): Promise<boolean> => {
    setPending(true);
    showProblem("");
    try {
      await request("/api/afk", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ on }) });
      return true;
    } catch (error: unknown) {
      showProblem(message(error));
      return false;
    } finally {
      setPending(false);
    }
  };
  return { pending, problem, turn, clear: () => showProblem("") };
}
