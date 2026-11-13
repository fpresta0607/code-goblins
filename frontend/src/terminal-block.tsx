import { Fragment, useMemo, useState, type ReactNode } from "react";
import { CodeCopy } from "./code-copy";
import { FullScreen } from "./full-screen";
import { Icon, type IconName } from "./Icon";
import { webLink } from "./messageText";
import { commandLines, plainTerminalText, softBreaks, terminalLines, type TerminalResult, type TerminalSpan } from "./terminal-text";
import "./code-card.css";

const RESULT_ICONS: Record<TerminalResult["tone"], IconName> = { success: "check", error: "warning", warning: "pause", running: "clock" };

// wrapping draws text with the breaks a long word may wrap at.
const wrapping = (text: string): ReactNode => softBreaks(text).map((piece, at) => <Fragment key={at}>{at > 0 && <wbr />}{piece}</Fragment>);

function drawn(span: TerminalSpan, key: number): ReactNode {
  if (span.kind === "link") return webLink(span.text, key, span.text, wrapping);
  const look = [span.color && "terminal-" + span.color, span.isBold && "terminal-bold", span.isDim && "terminal-dim", span.isItalic && "terminal-italic", span.isUnderlined && "terminal-underlined", span.kind && "terminal-path"].filter(Boolean).join(" ");
  return look ? <span key={key} className={look}>{wrapping(span.text)}</span> : <Fragment key={key}>{wrapping(span.text)}</Fragment>;
}

// TerminalBlock is terminal text wherever the board shows any (#2524: "Just
// have a better way to format terminal text ... consistent across
// terminals"): a chat row's command and what it printed, a Command Center
// command and what its terminal showed, and a block a question quotes. In
// one card it draws the command under its shell's name, each of its lines
// under the prompt's mark, then what it printed under output, read by
// terminal-text, then how it ended. Each part has its own Copy, which takes
// that part whole, and Full screen opens the card over the window. Given no
// command, it is the output alone.
export function TerminalBlock({ shell = "", command = "", output = "", result }: { shell?: string; command?: string; output?: string; result?: TerminalResult | null }) {
  const [isFull, setFull] = useState(false);
  const typed = commandLines(command);
  const lines = useMemo(() => terminalLines(output), [output]);
  const card = (isOver: boolean) => {
    const full = <button type="button" className="code-expand" aria-label={isOver ? "Exit full screen" : "Full screen"} onClick={() => setFull(!isOver)}><Icon name={isOver ? "restore" : "maximize"} /></button>;
    return <figure className={"message-code terminal-block" + (/^(powershell|pwsh)$/i.test(shell) ? " powershell" : "")}>
      {typed.length > 0 && <>
        <figcaption><span>{shell || "command"}</span><CodeCopy text={typed.join("\n")} label="Copy command" />{!lines.length && full}</figcaption>
        <pre className="terminal-command"><code>{typed.map((line, at) => <span key={at} className="terminal-line">{wrapping(line)}{"\n"}</span>)}</code></pre>
      </>}
      {lines.length > 0 && <>
        <figcaption><span>output</span><CodeCopy text={plainTerminalText(output)} label={typed.length ? "Copy output" : "Copy"} />{full}</figcaption>
        <pre className="terminal-output">{lines.map((line, at) => <span key={at} className={"terminal-line" + (line.tone ? " " + line.tone : "")}>{line.spans.map(drawn)}{"\n"}</span>)}</pre>
      </>}
      {result && <p className={"terminal-result " + result.tone}><Icon name={RESULT_ICONS[result.tone]} />{result.text}</p>}
    </figure>;
  };
  return <>{card(false)}{isFull && <FullScreen label="Terminal text" onClose={() => setFull(false)}>{card(true)}</FullScreen>}</>;
}
