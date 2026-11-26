import { useEffect, useRef, useState } from "react"
import { Terminal } from "@xterm/xterm"
import "@xterm/xterm/css/xterm.css"
import { reportToCeo } from "./api"
import { object, strings, type Task, type TreeNode } from "./types"
import { babyName } from "./fleet-tree"
import { Icon } from "./Icon"
import { Notice } from "./notice"
import { terminalDocument } from "./terminalDocument"
import { FALLBACK_FONT, LINE_HEIGHT, PADDING, THEME } from "./terminalView"
import { followFontSize, panelFit, storedFontSize } from "./terminalStream"
import { drawOnGpu } from "./terminal-gpu"
import { linkTerminalAddresses, openWebAddress } from "./terminal-links"
import { type AgentEntry, elapsed, parseAgentEntries, redraw, SPINNER_STEP_MS, transcriptRows, workingRow } from "./claude-transcript"
import "./agent-terminal.css"

// A stream that keeps dropping stops retrying and says why.
const MAX_RETRIES = 5

// What the supervisor's answer to a refused stream says, when it says it.
function refusalError(body: unknown): string {
  try {
    const error = object(body).error
    return typeof error === "string" ? error : ""
  } catch {
    return ""
  }
}

// A baby goblin's terminal: its own output in the terminal surface a goblin's
// live terminal uses, at the same font, size, colours and padding, and
// following the size chosen in any terminal. A sub-agent's transcript is drawn
// as Claude Code draws a conversation (claude-transcript.ts), with Claude
// Code's working line under it while the baby goblin works; any other child's
// lines are written as they came. It follows new output while it shows the
// last lines, and keeps its place while he reads further up, where Jump to
// bottom takes him back. One with no output yet says so, as an empty
// conversation does, so its panel is never a blank page (the Overlord,
// 2026-11-04: "I hit one and one of the chats is hit open and the page is
// like blank."). The terminal is made once and its stream connects apart from
// it, as a goblin's does, so a stream that drops or is refused never clears
// the screen: it is tried again up to MAX_RETRIES times in a row under a
// Reconnecting note (a plain connecting line while nothing is shown yet),
// except a refusal for good (403, 404), and then it closes, says why and
// offers Reconnect.
export function AgentTerminal({ task, child, visible }: { task: Task; child: TreeNode; visible: boolean }) {
  const surface = useRef<HTMLDivElement>(null)
  const reconnect = useRef<(() => void) | null>(null)
  const [phase, setPhase] = useState<"connecting" | "live" | "closed">("connecting")
  const [closedWhy, setClosedWhy] = useState("")
  const [hasOutput, setHasOutput] = useState(false)
  const [isEmpty, setEmpty] = useState(false)
  const [isReadingAbove, setReadingAbove] = useState(false)
  const jump = useRef<() => void>(() => {})
  const work = useRef({ isWorking: child.state === "working", started: child.started })
  useEffect(() => { work.current = { isWorking: child.state === "working", started: child.started } }, [child.state, child.started])
  useEffect(() => {
    const container = surface.current
    if (!visible || !container) return
    setPhase("connecting"); setClosedWhy(""); setHasOutput(false); setEmpty(false); setReadingAbove(false)
    const nonce = document.querySelector<HTMLMetaElement>('meta[name="ceo-style-nonce"]')?.content || ""
    const terminal = new Terminal({
      documentOverride: terminalDocument(nonce), disableStdin: true, convertEol: true,
      fontFamily: FALLBACK_FONT, fontSize: storedFontSize(), lineHeight: LINE_HEIGHT,
      cursorBlink: false, scrollback: 5000, screenReaderMode: true, theme: THEME,
      linkHandler: { activate: (_event, address) => openWebAddress(address) },
    })
    linkTerminalAddresses(terminal)
    terminal.open(container)
    drawOnGpu(terminal)
    terminal.write("\x1b[?25l")
    jump.current = () => terminal.scrollToBottom()
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

    // Whether he reads above the last screen, which Jump to bottom answers.
    const trackReading = () => {
      const buffer = terminal.buffer.active
      setReadingAbove(buffer.viewportY < buffer.baseY)
    }
    const scrolled = terminal.onScroll(trackReading)
    // write puts output on the terminal, at the bottom while he follows it
    // and where he was while he reads further up.
    const write = (data: string, isWhole: boolean) => {
      const buffer = terminal.buffer.active
      const wasFollowing = buffer.viewportY >= buffer.baseY, position = buffer.viewportY
      // Queue the reset with the output so pending writes cannot outlive it.
      terminal.write((isWhole ? "\x1bc\x1b[?25l" : "") + data, () => {
        if (wasFollowing) terminal.scrollToBottom()
        else terminal.scrollToLine(position)
        trackReading()
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

    // The stream, which connects apart from the terminal above. A stream that
    // fails is closed here, never left to the browser's own retry, and tried
    // again after a longer wait each time, up to MAX_RETRIES in a row, which
    // any output resets.
    const address = "/api/tasks/" + encodeURIComponent(task.id) + "/agent?node=" + encodeURIComponent(child.id)
    let previous = "", retries = 0
    let stream: EventSource | null = null, retryTimer: ReturnType<typeof setTimeout> | undefined, refusal: AbortController | null = null
    const stop = () => {
      stream?.close()
      stream = null
      clearTimeout(retryTimer)
      refusal?.abort()
      refusal = null
    }
    const close = (why: string) => {
      stop()
      setClosedWhy(why)
      setPhase("closed")
    }
    const retry = (why: string) => {
      if (retries >= MAX_RETRIES) {
        close(why)
        return
      }
      stop()
      retries++
      setPhase("connecting")
      retryTimer = setTimeout(connect, 400 * retries)
    }
    // A stream the supervisor refuses fails before it opens, and says why
    // only to a plain read of the same address: a 403 or a 404 is for good,
    // any other answer or a failed read is tried again.
    const readRefusal = async () => {
      const controller = new AbortController()
      refusal = controller
      let why = ""
      try {
        const response = await fetch(address, { signal: controller.signal })
        if (response.status === 403 || response.status === 404) why = refusalError(await response.json().catch(() => null)) || "The child's output is not available"
      } catch {
        // A read that failed is no refusal: the stream is tried again below.
      }
      if (controller.signal.aborted) return
      if (why) close(why)
      else retry("The output connection closed")
    }
    function connect() {
      stop()
      const opened = new EventSource(address + "&stream=1")
      stream = opened
      opened.addEventListener("output", (event: MessageEvent<string>) => {
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
          retries = 0
          setEmpty(!(output.entries !== undefined ? body.length : previous.length))
          setHasOutput(true)
          setPhase("live")
        } catch {
          close("The child's output could not be read")
          reportToCeo("reading a child's terminal", "Invalid output stream")
        }
      })
      opened.addEventListener("unavailable", () => retry("The child's output is no longer available"))
      opened.onerror = () => {
        if (opened.readyState !== EventSource.CLOSED) retry("The output connection closed")
        else {
          stop()
          void readRefusal()
        }
      }
    }
    reconnect.current = () => {
      retries = 0
      setClosedWhy("")
      setPhase("connecting")
      connect()
    }
    connect()
    return () => {
      isDisposed = true
      reconnect.current = null
      stop(); clearInterval(spinner); resize.disconnect(); stopFollowing()
      scrolled.dispose(); rendered.dispose(); resized.dispose(); terminal.dispose()
    }
  }, [task.id, child.id, visible])
  const closed = <Notice form="box" icon="terminal" className={hasOutput ? "terminal-foot" : undefined}
    actions={<button type="button" className="primary" onClick={() => reconnect.current?.()}>Reconnect</button>}>{closedWhy}</Notice>
  return <section className="agent-terminal" role="log" aria-label={"Terminal of " + babyName(task, child)}>
    <div className="agent-screen">
      <div ref={surface} className="agent-output" />
      {phase === "connecting" && hasOutput && <Notice tone="warning" className="terminal-corner">Reconnecting</Notice>}
      {isReadingAbove && <button type="button" className="icon-button raised terminal-jump" aria-label="Jump to bottom" onClick={() => jump.current()}><Icon name="arrow-down" /></button>}
    </div>
    {phase === "connecting" && !hasOutput && <p className="terminal-connecting" role="status"><span className="terminal-spinner" aria-hidden="true" />Connecting to the terminal</p>}
    {phase === "live" && isEmpty && <Notice>No output yet</Notice>}
    {phase === "closed" && (hasOutput ? closed : <div className="terminal-cover">{closed}</div>)}
  </section>
}
