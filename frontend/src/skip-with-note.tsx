import { useState } from "react";
import { AnswerWindow } from "./answer-window";
import { Icon } from "./Icon";

// Skip and the pencil beside the decision button of a card that has no
// choices or answer box of its own, such as a command to run, a goblin's wait
// or a credential card. Skip tells the asker to go on without it. The pencil
// opens a small window where he types, says or pastes his answer, which
// reaches whoever asked as his word. task is the asking goblin's, empty for
// the CFO: a pasted picture is kept where the asker can read it.
export function SkipWithNote({ asker, task, instance, disabled, onSkip, onAnswer }: {
  asker: string; task: string; instance: string; disabled: boolean; onSkip: () => void; onAnswer: (text: string) => void;
}) {
  const [isOpen, setIsOpen] = useState(false);
  return <>
    <button type="button" className="icon-button raised" disabled={disabled} aria-label="Skip this card" data-tip={"Skip: " + asker + " goes on without it"} onClick={onSkip}><Icon name="close" /></button>
    <button type="button" className="icon-button raised skip-with-note" aria-label="Answer in your own words" data-tip={"Answer " + asker + " in your own words"} disabled={disabled} onClick={() => setIsOpen(true)}><Icon name="edit" /></button>
    {isOpen && <AnswerWindow asker={asker} task={task} instance={instance} onCancel={() => setIsOpen(false)}
      onSend={(text) => { setIsOpen(false); onAnswer(text); }} />}
  </>;
}
