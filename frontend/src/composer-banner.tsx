import { useEffect, useState } from "react";

// How long a notice over the box shows before it fades, and how long the fade
// takes, as composer.css draws it.
const SHOWN_MS = 4000;
const FADE_MS = 400;

// A notice of the message box, such as a send it refused or Nothing was heard,
// as a small banner just above the box that fades a few seconds after it
// shows, laid over whatever is there so that neither the conversation nor the
// box moves (the Overlord, 2026-10-29: "a little banner right above the chat
// input that is only temporary, goes away slightly and doesn't move the
// screen up"). Its box keys it by each notice, so each shows anew; isRefusal
// says it is a refusal, read out at once. Any other notice is the box's note,
// which a screen reader hears from the box's own status line, so the banner
// does not read it a second time.
export function ComposerBanner({ text, isRefusal }: { text: string; isRefusal: boolean }) {
  const [stage, setStage] = useState<"shown" | "leaving" | "gone">("shown");
  useEffect(() => {
    if (!text) return;
    const leave = setTimeout(() => setStage("leaving"), SHOWN_MS);
    const gone = setTimeout(() => setStage("gone"), SHOWN_MS + FADE_MS);
    return () => { clearTimeout(leave); clearTimeout(gone); };
  }, [text]);
  if (!text || stage === "gone") return null;
  return <p className={"composer-banner" + (isRefusal ? " refusal" : "") + (stage === "leaving" ? " leaving" : "")} role={isRefusal ? "alert" : undefined} aria-hidden={isRefusal ? undefined : true}>{text}</p>;
}
