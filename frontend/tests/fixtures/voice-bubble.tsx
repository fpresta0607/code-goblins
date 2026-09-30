import { useState } from "react";
import { createRoot } from "react-dom/client";
import { useDictation } from "../../src/useDictation";
import { useVoice } from "../../src/useVoice";
import { VoiceBubble } from "../../src/VoiceBubble";
import "../../src/styles.css";

// One native terminal pane with its voice bubble: the terminal is a text box
// whose keys reach dictation as the real terminal's do, and what dictation or
// the bubble types shows under it.
function Pane() {
  const voice = useVoice("fixture", "task:voice", true);
  const [typed, setTyped] = useState<string[]>([]);
  const dictation = useDictation((text) => { setTyped((prior) => [...prior, text]); voice.remember(text); }, voice.defers);
  return <main style={{ display: "flex", flexDirection: "column", height: 760 }}>
    <section className="native-terminal host-terminal" aria-label="Goblin terminal" style={{ flex: 1 }}>
      <textarea className="terminal-surface" aria-label="Terminal input" onKeyDown={(event) => dictation.key(event.nativeEvent)} onKeyUp={(event) => dictation.key(event.nativeEvent)} />
      <VoiceBubble voice={voice} listening={dictation.listening} level={dictation.level} onPaste={(text) => setTyped((prior) => [...prior, "pasted: " + text])} />
    </section>
    <output aria-label="Typed">{typed.join("\n")}</output>
  </main>;
}

createRoot(document.getElementById("root")!).render(<Pane />);
