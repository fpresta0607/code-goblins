import { useState } from "react";
import { useAfkSwitch, type AfkSwitch } from "./useAfkSwitch";
import type { Afk } from "./types";

export interface AfkToggleState extends Pick<AfkSwitch, "pending" | "problem" | "pressedOn" | "clear"> {
  isAsking: boolean;
  press: () => void;
  turnOn: () => void;
  closeQuestion: () => void;
}

// useAfkToggle is what the AFK toggle keeps between his press and the
// supervisor's answer: the question it asks first, the press, and what the
// press met. It belongs to whatever draws the toggle, not the toggle, since the
// CFO's bar draws it from a different place in each of its forms, and a bar
// that changes form still owes him the question and the answer. Turning on
// asks first. Turning off needs no question.
export function useAfkToggle(afk: Afk, instance: string): AfkToggleState {
  const [isAsking, setIsAsking] = useState(false);
  const { pending, problem, pressedOn, turn, clear } = useAfkSwitch(instance);
  const turnOn = async () => { if (await turn(true)) setIsAsking(false); };
  return {
    pending, problem, pressedOn, clear, isAsking,
    press: () => { if (afk.state === "off") { clear(); setIsAsking(true); } else void turn(false); },
    turnOn: () => void turnOn(),
    closeQuestion: () => { setIsAsking(false); clear(); },
  };
}
