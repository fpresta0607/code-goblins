import { useState } from "react";
import { createRoot } from "react-dom/client";
import { useDictation } from "../../src/useDictation";
import { useVoice } from "../../src/useVoice";
import { VoiceBubble } from "../../src/VoiceBubble";
import "../../src/styles.css";

// One native terminal pane with its voice bubble: the terminal is a text box
// whose keys reach dictation as the real terminal's do, and what dictation or
// the bubble types shows under it.
function Pane({ pane, label, shown }: { pane: string; label: string; shown: boolean }) {
  const voice = useVoice(pane);
  const [typed, setTyped] = useState<string[]>([]);
  const dictation = useDictation((text) => { setTyped((prior) => [...prior, text]); voice.remember(text); }, "test-instance");
  return <main style={{ display: shown ? "flex" : "none", flexDirection: "column", height: 760 }}>
    <section className="native-terminal host-terminal" aria-label={label} style={{ flex: 1 }}>
      <textarea className="terminal-surface" aria-label="Terminal input" onKeyDown={(event) => dictation.key(event.nativeEvent)} onKeyUp={(event) => dictation.key(event.nativeEvent)} />
      <VoiceBubble voice={voice} listening={dictation.listening} level={dictation.level} model={dictation.model} onPaste={(text) => setTyped((prior) => [...prior, "pasted: " + text])} />
      {dictation.note && <p className="terminal-error" role="status">{dictation.note}</p>}
    </section>
    <output aria-label="Typed">{typed.join("\n")}</output>
  </main>;
}

// With ?panes=2 a second pane stays mounted and hidden, as the terminal deck
// keeps it, until the switch shows it instead.
function Deck() {
  const [second, setSecond] = useState(false);
  if (new URLSearchParams(location.search).get("panes") !== "2") return <Pane pane="task:voice" label="Goblin terminal" shown />;
  return <>
    <button onClick={() => setSecond((prior) => !prior)}>Switch pane</button>
    <Pane pane="task:voice" label="Goblin terminal" shown={!second} />
    <Pane pane="cfo" label="CFO terminal" shown={second} />
  </>;
}

createRoot(document.getElementById("root")!).render(<Deck />);
