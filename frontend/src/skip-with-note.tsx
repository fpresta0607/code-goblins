import { useState } from "react";
import { AnswerWindow } from "./answer-window";
import { Icon } from "./Icon";

// Skip and the pencil beside the decision button of a card that has no
// choices or answer box of its own, such as a command to run, a goblin's wait
// or a credential card. Skip tells the asker to go on without it. The pencil
// opens a small window where he types, says or pastes his answer, which
// reaches whoever asked as his word. task is the asking goblin's, empty for
// the CFO: a pasted picture is kept where the asker can read it. A card that
// posts his answer itself returns how the board took it: the window stays
// open until then, and a refusal shows there in full with his words kept.
export function SkipWithNote({ asker, task, instance, disabled, onSkip, onAnswer }: {
  asker: string; task: string; instance: string; disabled: boolean; onSkip: () => void; onAnswer: (text: string) => Promise<string> | void;
}) {
  const [isOpen, setIsOpen] = useState(false);
  const [isSending, setIsSending] = useState(false);
  const [refusal, setRefusal] = useState("");
  const answer = async (text: string) => {
    const taken = onAnswer(text);
    if (!taken) { setIsOpen(false); return; }
    setIsSending(true); setRefusal("");
    const refused = await taken;
    setIsSending(false);
    if (refused) setRefusal(refused);
    else setIsOpen(false);
  };
  return <>
    <button type="button" className="icon-button raised" disabled={disabled} aria-label="Skip this card" onClick={onSkip}><Icon name="close" /></button>
    <button type="button" className="icon-button raised skip-with-note" aria-label="Something else?" disabled={disabled} onClick={() => setIsOpen(true)}><Icon name="edit" /></button>
    {isOpen && <AnswerWindow asker={asker} task={task} instance={instance} isSending={isSending} refusal={refusal} onCancel={() => { setIsOpen(false); setRefusal(""); }}
      onSend={(text) => void answer(text)} />}
  </>;
}
