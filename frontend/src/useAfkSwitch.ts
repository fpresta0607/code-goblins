import { useState } from "react";
import { message, request } from "./api";

export interface AfkSwitch {
  pending: boolean;
  // problem is what his press met, until he closes it or presses again, and
  // pressedOn says which way that press turned the switch.
  problem: string;
  pressedOn: boolean;
  turn: (on: boolean) => Promise<boolean>;
  clear: () => void;
}

// useAfkSwitch asks the supervisor to turn AFK mode on or off from this board.
// Whether the request is the Overlord's own is the supervisor's to prove, so
// nothing here decides it, and a refusal comes back as problem, the one short
// sentence the supervisor has for him, with the way he pressed, which a
// refusal that arrives after he closed the question still says.
export function useAfkSwitch(instance: string): AfkSwitch {
  const [pending, setPending] = useState(false);
  const [refused, setRefused] = useState({ problem: "", pressedOn: false });
  const turn = async (on: boolean): Promise<boolean> => {
    setPending(true);
    setRefused({ problem: "", pressedOn: on });
    try {
      await request("/api/afk", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ on }) });
      return true;
    } catch (error: unknown) {
      setRefused({ problem: message(error), pressedOn: on });
      return false;
    } finally {
      setPending(false);
    }
  };
  return { pending, problem: refused.problem, pressedOn: refused.pressedOn, turn, clear: () => setRefused({ problem: "", pressedOn: false }) };
}
