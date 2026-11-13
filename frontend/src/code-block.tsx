import { useState } from "react";
import { message } from "./api";
import { CodeCopy } from "./code-copy";
import { FullScreen } from "./full-screen";
import { Icon } from "./Icon";
import { Notice } from "./notice";
import { literalLinks } from "./messageText";
import "./code-card.css";

// CodeBlock is a code block in a message, exactly as written, in a rounded
// card under a quiet header with the language its fence names, Copy as its
// icon alone and Full screen (#2524). The card shows its first lines and
// scrolls a long line sideways inside itself, so nothing runs off a phone's
// side; Full screen opens it over the whole window, every line wrapped, until
// Escape or its button closes it.
// Given onRun, a PowerShell command in a reply's own words also has Run beside
// Copy (#2051), which asks the board to run the block in a terminal of its
// own; it waits while that terminal starts, so a second press runs nothing,
// and says why when the board refused.
export function CodeBlock({ text, language, onRun }: { text: string; language: string; onRun?: () => Promise<void> }) {
  const [isFull, setFull] = useState(false);
  const [isStarting, setStarting] = useState(false);
  const [refused, setRefused] = useState("");
  const run = async () => {
    if (!onRun || isStarting) return;
    setStarting(true);
    setRefused("");
    try {
      await onRun();
    } catch (error: unknown) {
      setRefused(message(error));
    } finally {
      setStarting(false);
    }
  };
  const card = (isOver: boolean) => <figure className="message-code">
    <figcaption>
      <span>{language}</span>
      <CodeCopy text={text} />
      {onRun && <button type="button" className="code-run" disabled={isStarting} onClick={() => void run()}>
        <Icon name={isStarting ? "clock" : "play"} />{isStarting ? "Starting" : "Run"}
      </button>}
      <button type="button" className="code-expand" aria-label={isOver ? "Exit full screen" : "Full screen"} onClick={() => setFull(!isOver)}><Icon name={isOver ? "restore" : "maximize"} /></button>
    </figcaption>
    {refused && <Notice tone="warning">{refused}</Notice>}
    <pre><code>{literalLinks(text)}</code></pre>
  </figure>;
  return <>{card(false)}{isFull && <FullScreen label={language ? language + " code" : "Code"} onClose={() => setFull(false)}>{card(true)}</FullScreen>}</>;
}
