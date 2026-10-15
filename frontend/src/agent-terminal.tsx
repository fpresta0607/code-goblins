import { useEffect, useRef, useState } from "react"
import { Terminal } from "@xterm/xterm"
import "@xterm/xterm/css/xterm.css"
import { reportToCfo } from "./api"
import { object, strings, type Task, type TreeNode } from "./types"
import { babyName } from "./fleet-tree"
import { terminalDocument } from "./terminalDocument"
import "./agent-terminal.css"

export function AgentTerminal({ task, child, visible }: { task: Task; child: TreeNode; visible: boolean }) {
  const surface = useRef<HTMLDivElement>(null)
  const [status, setStatus] = useState("Connecting to the terminal")
  const [isClosed, setClosed] = useState(false)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    const container = surface.current
    if (!visible || !container) return
    const nonce = document.querySelector<HTMLMetaElement>('meta[name="cfo-style-nonce"]')?.content || ""
    const terminal = new Terminal({
      documentOverride: terminalDocument(nonce), disableStdin: true, convertEol: true,
      fontFamily: '"Cascadia Mono", Consolas, monospace', fontSize: 14, lineHeight: 1.55,
      cursorBlink: false, scrollback: 5000, screenReaderMode: true,
      theme: { background: "#071015", foreground: "#d8e9e2", selectionBackground: "#286856" },
      linkHandler: { activate: () => {} },
    })
    terminal.open(container)
    terminal.write("\x1b[?25l")
    const measure = document.createElement("canvas").getContext("2d")!
    measure.font = '14px "Cascadia Mono", Consolas, monospace'
    const fit = () => terminal.resize(Math.max(2, Math.floor(container.clientWidth / measure.measureText("M").width)), Math.max(1, Math.floor(container.clientHeight / 22)))
    const resize = new ResizeObserver(fit)
    resize.observe(container)
    fit()
    let previous = "", failures = 0
    const stream = new EventSource("/api/tasks/" + encodeURIComponent(task.id) + "/agent?node=" + encodeURIComponent(child.id) + "&stream=1")
    stream.addEventListener("output", (event: MessageEvent<string>) => {
      try {
        const text = strings(object(JSON.parse(event.data)).lines).join("\n")
        const wasFollowing = terminal.buffer.active.viewportY >= terminal.buffer.active.baseY
        const position = terminal.buffer.active.viewportY
        const isAppend = text.startsWith(previous)
        if (!isAppend) { terminal.reset(); terminal.write("\x1b[?25l") }
        terminal.write(isAppend ? text.slice(previous.length) : text, () => {
          if (wasFollowing) terminal.scrollToBottom()
          else terminal.scrollToLine(position)
        })
        previous = text
        failures = 0
        setStatus("")
        setClosed(false)
      } catch {
        stream.close()
        setStatus("The child's output could not be read")
        setClosed(true)
        reportToCfo("reading a child's terminal", "Invalid output stream")
      }
    })
    stream.addEventListener("unavailable", () => {
      stream.close()
      setStatus("The child's output is no longer available")
      setClosed(true)
    })
    stream.onerror = () => {
      if (++failures >= 5) {
        stream.close()
        setClosed(true)
        setStatus("The output connection closed")
      } else setStatus("Reconnecting to the terminal")
    }
    return () => { stream.close(); resize.disconnect(); terminal.dispose() }
  }, [task.id, child.id, visible, attempt])
  return <section className="agent-terminal" role="log" aria-label={"Terminal of " + babyName(task, child)}>
    <div ref={surface} className="agent-output" />
    {status && <div className="agent-status" role="status">{status}{isClosed && <button type="button" onClick={() => { setClosed(false); setStatus("Connecting to the terminal"); setAttempt(value => value + 1) }}>Reconnect</button>}</div>}
  </section>
}
