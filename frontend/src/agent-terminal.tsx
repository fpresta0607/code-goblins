import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { message, reportToCfo, request } from "./api";
import { object, strings, type Task, type TreeNode } from "./types";
import { babyName } from "./fleet-tree";
import "./agent-terminal.css";

// How often a sub-agent's terminal reads its record again while it shows.
const READ_EVERY_MS = 2000;

// A line that says what came back to the sub-agent, which is drawn quieter
// than what it was asked, said and did.
const isResult = (line: string) => /^\s+[⎿…]/.test(line);

// A sub-agent's terminal: the newest lines of its own record, the transcript
// its goblin's harness keeps of it, read again every two seconds while it
// shows and following its end unless the Overlord scrolls back. A sub-agent
// works inside its goblin's harness with no terminal of its own to type
// into, so this one only reads. A read that fails goes to the CFO, and the
// terminal keeps what it last showed.
export function AgentTerminal({ task, child, visible }: { task: Task; child: TreeNode; visible: boolean }) {
  const [lines, setLines] = useState<string[] | null>(null);
  const screen = useRef<HTMLElement>(null);
  const isFollowing = useRef(true);
  useEffect(() => {
    if (!visible) return;
    const controller = new AbortController();
    let reported = "";
    const read = async () => {
      try {
        const answer = await request("/api/tasks/" + encodeURIComponent(task.id) + "/agent?node=" + encodeURIComponent(child.id), controller.signal);
        setLines(strings(object(answer).lines));
      } catch (error: unknown) {
        if (controller.signal.aborted || message(error) === reported) return;
        reported = message(error);
        reportToCfo("reading the terminal of " + child.label, reported);
      }
    };
    void read();
    const timer = setInterval(() => void read(), READ_EVERY_MS);
    return () => { controller.abort(); clearInterval(timer); };
  }, [task.id, child.id, child.label, visible]);
  useLayoutEffect(() => {
    if (screen.current && isFollowing.current) screen.current.scrollTop = screen.current.scrollHeight;
  }, [lines]);
  return <section ref={screen} className="agent-terminal" role="log" aria-label={"Terminal of " + babyName(task, child)} tabIndex={0}
    onScroll={(event) => { const element = event.currentTarget; isFollowing.current = element.scrollHeight - element.scrollTop - element.clientHeight < 8; }}>
    {lines === null
      ? <div className="terminal-cover" role="status"><span className="terminal-spinner" aria-hidden="true" /><p>Connecting to the terminal</p></div>
      : lines.map((line, i) => <div key={i} className={isResult(line) ? "agent-result" : undefined}>{line}</div>)}
  </section>;
}
