import { Fragment, useMemo, useState, type ReactNode } from "react";
import { CodeCopy } from "./code-copy";
import { FullScreen } from "./full-screen";
import { Icon } from "./Icon";
import { pathElements, webLink, type PathRenderer } from "./messageText";
import { commandLines, plainTerminalText, softBreaks, terminalLines, type TerminalResult, type TerminalSpan } from "./terminal-text";
import "./code-card.css";

// wrapping draws text with the breaks a long word may wrap at.
const wrapping = (text: string): ReactNode => softBreaks(text).map((piece, at) => <Fragment key={at}>{at > 0 && <wbr />}{piece}</Fragment>);

// linked draws text as wrapping does and, given path, each local path in it
// by path, as a question's words draw one.
const linked = (text: string, path?: PathRenderer): ReactNode => path
  ? pathElements(text, path).map((piece, at) => typeof piece === "string" ? <Fragment key={at}>{wrapping(piece)}</Fragment> : piece)
  : wrapping(text);

function drawn(span: TerminalSpan, key: number, path?: PathRenderer): ReactNode {
  if (span.kind === "link") return webLink(span.text, key, span.text, wrapping);
  const look = [span.color && "terminal-" + span.color, span.isBold && "terminal-bold", span.isDim && "terminal-dim", span.isItalic && "terminal-italic", span.isUnderlined && "terminal-underlined", span.kind && "terminal-path"].filter(Boolean).join(" ");
  const text = span.kind === "path" ? linked(span.text, path) : wrapping(span.text);
  return look ? <span key={key} className={look}>{text}</span> : <Fragment key={key}>{text}</Fragment>;
}

// TerminalBlock is terminal text wherever the board shows any (#2524: "Just
// have a better way to format terminal text ... consistent across
// terminals"): a chat row's command and what it printed, a Command Center
// command and what its terminal showed, and a block a question quotes. In
// one card it draws the command under its shell's name, each of its lines
// under the prompt's mark, then what it printed under output, read by
// terminal-text, then how it ended under its own row's mark. Each part has
// its own Copy, which takes that part whole, and Full screen opens the card
// over the window. Given no command, it is the output alone. Given path, as
// a question's card gives it, each local path the block names is drawn by
// it.
export function TerminalBlock({ shell = "", command = "", output = "", result, path }: { shell?: string; command?: string; output?: string; result?: TerminalResult | null; path?: PathRenderer }) {
  const [isFull, setFull] = useState(false);
  const typed = commandLines(command);
  const lines = useMemo(() => terminalLines(output), [output]);
  const printed = useMemo(() => plainTerminalText(lines), [lines]);
  const card = (isOver: boolean) => {
    const full = <button type="button" className="code-expand" aria-label={isOver ? "Exit full screen" : "Full screen"} onClick={() => setFull(!isOver)}><Icon name={isOver ? "restore" : "maximize"} /></button>;
    return <figure className={"message-code terminal-block" + (/^(powershell|pwsh)$/i.test(shell) ? " powershell" : "")}>
      {typed.length > 0 && <>
        <figcaption><span>{shell || "command"}</span><CodeCopy text={typed.join("\n")} label="Copy command" />{!lines.length && full}</figcaption>
        <pre className="terminal-command"><code>{typed.map((line, at) => <span key={at} className="terminal-line">{linked(line, path)}{"\n"}</span>)}</code></pre>
      </>}
      {lines.length > 0 && <>
        <figcaption><span>output</span><CodeCopy text={printed} label={typed.length ? "Copy output" : "Copy"} />{full}</figcaption>
        <pre className="terminal-output">{lines.map((line, at) => <span key={at} className={"terminal-line" + (line.tone ? " " + line.tone : "")}>{line.spans.map((span, key) => drawn(span, key, path))}{"\n"}</span>)}</pre>
      </>}
      {result && <p className={"terminal-result " + result.tone}><Icon name={result.icon} />{result.text}</p>}
    </figure>;
  };
  return <>{card(false)}{isFull && <FullScreen label="Terminal text" onClose={() => setFull(false)}>{card(true)}</FullScreen>}</>;
}
