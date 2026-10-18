import { useEffect, useRef, useState } from "react"
import { Terminal } from "@xterm/xterm"
import "@xterm/xterm/css/xterm.css"
import { reportToCfo } from "./api"
import { object, strings, type Task, type TreeNode } from "./types"
import { babyName } from "./fleet-tree"
import { terminalDocument } from "./terminalDocument"
import { FALLBACK_FONT, LINE_HEIGHT, PADDING, THEME } from "./terminalView"
import { followFontSize, panelFit, storedFontSize } from "./terminalStream"
import { drawOnGpu } from "./terminal-gpu"
import { type AgentEntry, elapsed, parseAgentEntries, redraw, SPINNER_STEP_MS, transcriptRows, workingRow } from "./claude-transcript"
import "./agent-terminal.css"

// A baby goblin's terminal: its own output in the terminal surface a goblin's
// live terminal uses, at the same font, size, colours and padding, and
// following the size chosen in any terminal. A sub-agent's transcript is drawn
// as Claude Code draws a conversation (claude-transcript.ts), with Claude
// Code's working line under it while the baby goblin works; any other child's
// lines are written as they came. It follows new output while it shows the
// last lines, and keeps its place while he reads further up.
export function AgentTerminal({ task, child, visible }: { task: Task; child: TreeNode; visible: boolean }) {
  const surface = useRef<HTMLDivElement>(null)
  const [status, setStatus] = useState("Connecting to the terminal")
  const [isClosed, setClosed] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const work = useRef({ isWorking: child.state === "working", started: child.started })
  useEffect(() => { work.current = { isWorking: child.state === "working", started: child.started } }, [child.state, child.started])
  useEffect(() => {
    const container = surface.current
    if (!visible || !container) return
    const nonce = document.querySelector<HTMLMetaElement>('meta[name="cfo-style-nonce"]')?.content || ""
    const terminal = new Terminal({
      documentOverride: terminalDocument(nonce), disableStdin: true, convertEol: true,
      fontFamily: FALLBACK_FONT, fontSize: storedFontSize(), lineHeight: LINE_HEIGHT,
      cursorBlink: false, scrollback: 5000, screenReaderMode: true, theme: THEME,
      linkHandler: { activate: () => {} },
    })
    terminal.open(container)
    drawOnGpu(terminal)
    terminal.write("\x1b[?25l")
    // fit sizes the grid to the panel as a goblin's terminal does (panelFit),
    // by its cells as xterm draws them, again whenever a font that loads or a
    // size chosen changes them.
    let isDisposed = false, fitted = ""
    const cell = () => {
      const screen = terminal.element?.querySelector(".xterm-screen")?.getBoundingClientRect()
      return screen ? { width: screen.width / terminal.cols, height: screen.height / terminal.rows } : null
    }
    const fit = () => {
      const measured = cell()
      if (!measured || !terminal.element) return
      const size = panelFit(container.clientWidth, container.clientHeight, measured, PADDING)
      if (!size) return
      fitted = measured.width + " " + measured.height
      terminal.element.style.padding = `${size.top}px ${size.right}px ${size.bottom}px ${size.left}px`
      if (size.cols !== terminal.cols || size.rows !== terminal.rows) terminal.resize(size.cols, size.rows)
    }
    const remeasure = () => terminal.refresh(0, terminal.rows - 1)
    const rendered = terminal.onRender(() => {
      const measured = cell()
      if (measured && measured.width + " " + measured.height !== fitted) fit()
    })
    const resize = new ResizeObserver(fit)
    resize.observe(container)
    const stopFollowing = followFontSize((size) => { terminal.options.fontSize = size; remeasure() })
    void document.fonts.load(terminal.options.fontSize + 'px "JetBrains Mono"').then(() => {
      if (isDisposed || !document.fonts.check(terminal.options.fontSize + 'px "JetBrains Mono"')) return
      terminal.options.fontFamily = '"JetBrains Mono", ' + FALLBACK_FONT
      remeasure()
    }, () => {})

    // write puts output on the terminal, at the bottom while he follows it
    // and where he was while he reads further up.
    const write = (data: string, isWhole: boolean) => {
      const buffer = terminal.buffer.active
      const wasFollowing = buffer.viewportY >= buffer.baseY, position = buffer.viewportY
      // Queue the reset with the output so pending writes cannot outlive it.
      terminal.write((isWhole ? "\x1bc\x1b[?25l" : "") + data, () => {
        if (wasFollowing) terminal.scrollToBottom()
        else terminal.scrollToLine(position)
      })
    }
    // A transcript's rows, laid out for the terminal's width, and the rows on
    // its screen now, the working line under them while the baby goblin works.
    let entries: AgentEntry[] | null = null, body: string[] = [], shown: string[] = [], frame = 0, laidOutFor = 0
    const layOut = (next: AgentEntry[]) => {
      entries = next
      laidOutFor = terminal.cols
      body = transcriptRows(next, laidOutFor)
    }
    const draw = (isWhole: boolean) => {
      if (!entries) return
      const next = work.current.isWorking ? [...body, "", workingRow(frame, elapsed(work.current.started, Date.now()))] : body
      const change = isWhole ? null : redraw(shown, next, terminal.rows)
      if (change !== "") write(change ?? next.join("\r\n"), change === null)
      shown = next
    }
    const resized = terminal.onResize(({ cols }) => {
      if (!entries || cols === laidOutFor) return
      layOut(entries)
      draw(true)
    })
    const spinner = setInterval(() => {
      if (work.current.isWorking) frame++
      draw(false)
    }, SPINNER_STEP_MS)
    let previous = "", failures = 0
    const stream = new EventSource("/api/tasks/" + encodeURIComponent(task.id) + "/agent?node=" + encodeURIComponent(child.id) + "&stream=1")
    stream.addEventListener("output", (event: MessageEvent<string>) => {
      try {
        const output = object(JSON.parse(event.data))
        if (output.entries !== undefined) {
          layOut(parseAgentEntries(output.entries))
          draw(false)
        } else {
          const text = strings(output.lines).join("\n")
          const isAppend = text.startsWith(previous)
          write(isAppend ? text.slice(previous.length) : text, !isAppend)
          previous = text
        }
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
    return () => {
      isDisposed = true
      stream.close(); clearInterval(spinner); resize.disconnect(); stopFollowing()
      rendered.dispose(); resized.dispose(); terminal.dispose()
    }
  }, [task.id, child.id, visible, attempt])
  return <section className="agent-terminal" role="log" aria-label={"Terminal of " + babyName(task, child)}>
    <div ref={surface} className="agent-output" />
    {status && <div className="agent-status" role="status">{status}{isClosed && <button type="button" onClick={() => { setClosed(false); setStatus("Connecting to the terminal"); setAttempt(value => value + 1) }}>Reconnect</button>}</div>}
  </section>
}
