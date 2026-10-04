import { useState } from "react";
import { message, request } from "./api";

export interface AfkSwitch {
  pending: boolean;
  // problem is the supervisor's refusal, in its words, until the next try.
  problem: string;
  turn: (on: boolean) => Promise<boolean>;
  clear: () => void;
}

// useAfkSwitch asks the supervisor to turn AFK mode on or off from this board.
// Whether the request is the Overlord's own is the supervisor's to prove, so a
// refusal comes back as problem and nothing here decides it.
export function useAfkSwitch(instance: string): AfkSwitch {
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState("");
  const turn = async (on: boolean): Promise<boolean> => {
    setPending(true);
    setProblem("");
    try {
      await request("/api/afk", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ on }) });
      return true;
    } catch (error: unknown) {
      setProblem(message(error));
      return false;
    } finally {
      setPending(false);
    }
  };
  return { pending, problem, turn, clear: () => setProblem("") };
}
