import { useState } from "react";
import { useAfkSwitch, type AfkSwitch } from "./useAfkSwitch";
import type { Afk } from "./types";

export interface AfkToggleState extends Pick<AfkSwitch, "pending" | "problem" | "pressedOn" | "clear"> {
  isAsking: boolean;
  press: (state: Afk["state"]) => void;
  turnOn: () => void;
  closeQuestion: () => void;
}

// useAfkToggle is what the AFK toggle keeps between his press and the
// supervisor's answer: the question it asks first, the press, and what the
// press met. The board keeps one, not the bar or the card, since each comes and
// goes with the view and the CEO, and a press still owes him its question and
// its answer wherever he reads it. A press says the state of the switch he
// pressed, so nothing here reads one before a snapshot exists. Turning on asks
// first. Turning off needs no question.
export function useAfkToggle(instance: string): AfkToggleState {
  const [isAsking, setIsAsking] = useState(false);
  const { pending, problem, pressedOn, turn, clear } = useAfkSwitch(instance);
  const turnOn = async () => { if (await turn(true)) setIsAsking(false); };
  return {
    pending, problem, pressedOn, clear, isAsking,
    press: (state) => { if (state === "off") { clear(); setIsAsking(true); } else void turn(false); },
    turnOn: () => void turnOn(),
    closeQuestion: () => { setIsAsking(false); clear(); },
  };
}
