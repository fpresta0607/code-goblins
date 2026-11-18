import { useContext } from "react";
import { Icon } from "./Icon";
import { pastedTextName, type PastedText } from "./kept-files";
import { PastedTextOpener } from "./pasted-text-opener";
import "./pasted-texts.css";

// The long pastes a message carries as text files (#2404), each as a chip
// that names its lines, as the box showed it before Send. A click opens its
// text in the conversation's window for it (PastedTextOpener), read back from
// the address source gives for its path.
export function PastedTexts({ pastes, source }: { pastes: PastedText[]; source: (path: string) => string }) {
  const open = useContext(PastedTextOpener);
  if (!pastes.length) return null;
  return <div className="pasted-texts">
    {pastes.map((paste) => <button key={paste.path} type="button" className="pasted-text"
      onClick={(event) => { event.stopPropagation(); open({ name: pastedTextName(paste.lines), source: source(paste.path) }); }}><Icon name="file" />{pastedTextName(paste.lines)}</button>)}
  </div>;
}
