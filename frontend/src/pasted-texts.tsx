import { useState } from "react";
import { Icon } from "./Icon";
import { pastedTextName, type PastedText } from "./kept-files";
import { PastedTextWindow } from "./pasted-text-window";
import "./pasted-texts.css";

// The long pastes a message carries as text files (#2404), each as a chip
// that names its lines, as the box showed it before Send. A click opens its
// text in a window (PastedTextWindow), read back from the address source
// gives for its path.
export function PastedTexts({ pastes, source }: { pastes: PastedText[]; source: (path: string) => string }) {
  const [shown, setShown] = useState<PastedText | null>(null);
  if (!pastes.length) return null;
  return <div className="pasted-texts">
    {pastes.map((paste) => <button key={paste.path} type="button" className="pasted-text"
      onClick={(event) => { event.stopPropagation(); setShown(paste); }}><Icon name="file" />{pastedTextName(paste.lines)}</button>)}
    {shown && <PastedTextWindow name={pastedTextName(shown.lines)} source={source(shown.path)} onClose={() => setShown(null)} />}
  </div>;
}
