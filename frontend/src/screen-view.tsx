import { useEffect, useRef, useState, useSyncExternalStore, type FormEvent, type KeyboardEvent, type PointerEvent } from "react";
import { boolean, number, object, string } from "./types";
import { Icon } from "./Icon";
import { Notice } from "./notice";
import { isLeaveKey, isPasteKey, keyMessage, mouseMessage, pagePoint, wheelMessage, type FrameSize } from "./screen-input";
import "./screen-view.css";

// ScreenState is what the supervisor tells a view of the browser: who drives
// it, whether this view may take it, its page by title and host alone, and
// where on that page its goblin last pressed its mouse (#2537).
interface ScreenState { isDriven: boolean; canDrive: boolean; title: string; host: string; hasPage: boolean; driveNote: string; pointer: PagePlace | null }

// PagePlace is a place on the goblin's page, in its CSS pixels.
interface PagePlace { x: number; y: number }

type Said = { kind: "state"; state: ScreenState } | { kind: "frame"; data: string; size: FrameSize };

function parseSaid(text: string): Said | null {
  try {
    const said = object(JSON.parse(text));
    if (said.type === "state") {
      const pointer = said.pointer == null ? null : object(said.pointer);
      return { kind: "state", state: { isDriven: boolean(said.is_driven), canDrive: boolean(said.can_drive), title: string(said.title), host: string(said.host), hasPage: boolean(said.has_page), driveNote: string(said.drive_note), pointer: pointer && { x: number(pointer.x), y: number(pointer.y) } } };
    }
    if (said.type === "frame") return { kind: "frame", data: string(said.data), size: { width: number(said.width), height: number(said.height) } };
  } catch { /* a message of no shape the view knows is passed by */ }
  return null;
}

const modifiers = (event: { altKey: boolean; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean }) => ({ altKey: event.altKey, ctrlKey: event.ctrlKey, metaKey: event.metaKey, shiftKey: event.shiftKey });

// The most a paste carries to the page, as the relay takes it.
const MAX_PASTE = 4000;
// A touch that moves less than this is a tap, which clicks.
const TAP_SLOP = 8;
// The goblin's pointer mark: a ring of this radius and stroke, in pixels of
// the screen as it is shown, so it reads alike at any size of the view, drawn
// in fixed colours since it sits on the page's own.
const POINTER_RADIUS = 11;
const POINTER_STROKE = 3;

const watchVisibility = (changed: () => void) => {
  document.addEventListener("visibilitychange", changed);
  return () => document.removeEventListener("visibilitychange", changed);
};
const isPageVisible = () => document.visibilityState === "visible";

// ScreenView shows a goblin's own browser live: the page it has open, drawn
// from the browser's screencast, and, once he takes control from the board
// on his PC, his clicks and keys go to that page until he hands it back, with
// a few words of what he did, which go to the goblin. The view streams only
// while it is on screen in a visible tab: a view hidden, scrolled away,
// closed or in a tab out of sight closes its connection, so the browser
// sends no frame nobody sees. Nothing typed on the screen is kept or shown by
// the board: it goes to the page alone. A view that shouldFocus takes the
// keyboard as it opens, as one a chat row opened does (#2362). Where the
// goblin last pressed its mouse is marked on its page, a ring drawn over the
// frame as a state or a frame arrives and at no other time, and not while
// he drives (#2537).
export function ScreenView({ task, generation, name, instance, connected, isFull = false, shouldFocus = false }: {
  task: string; generation: string; name: string; instance: string; connected: boolean; isFull?: boolean; shouldFocus?: boolean;
}) {
  const region = useRef<HTMLElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const handBackWords = useRef<HTMLInputElement>(null);
  const link = useRef<WebSocket | null>(null);
  const page = useRef<FrameSize>({ width: 0, height: 0 });
  const shown = useRef<ImageBitmap | null>(null);
  const pointer = useRef<PagePlace | null>(null);
  const isVisible = useSyncExternalStore(watchVisibility, isPageVisible);
  const [isOnScreen, setOnScreen] = useState(false);
  const [state, setState] = useState<ScreenState | null>(null);
  const [closedWhy, setClosedWhy] = useState("");
  const [hasFrame, setHasFrame] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [words, setWords] = useState("");
  useEffect(() => {
    if (shouldFocus) region.current?.focus();
  }, [shouldFocus]);
  useEffect(() => {
    const element = region.current;
    if (!element) return;
    const observer = new IntersectionObserver(([entry]) => setOnScreen(entry.isIntersecting));
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  const isLive = connected && isVisible && isOnScreen && !!generation;
  useEffect(() => {
    if (!isLive) return;
    let isActive = true;
    const url = new URL("/api/screen", location.href);
    url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
    url.search = new URLSearchParams({ task, generation, token: instance }).toString();
    const socket = new WebSocket(url);
    link.current = socket;
    socket.onopen = () => { if (isActive) setClosedWhy(""); };
    // paint draws the last frame and, over it, the goblin's pointer.
    const paint = () => {
      const element = canvas.current, bitmap = shown.current, context = element?.getContext("2d");
      if (!element || !bitmap || !context) return;
      context.drawImage(bitmap, 0, 0);
      const place = pointer.current, size = page.current;
      if (!place || size.width <= 0 || size.height <= 0) return;
      const shownScale = element.clientWidth > 0 ? element.width / element.clientWidth : 1;
      context.beginPath();
      context.arc(place.x * element.width / size.width, place.y * element.height / size.height, POINTER_RADIUS * shownScale, 0, 2 * Math.PI);
      context.lineWidth = (POINTER_STROKE + 2) * shownScale;
      context.strokeStyle = "#ffffff";
      context.stroke();
      context.lineWidth = POINTER_STROKE * shownScale;
      context.strokeStyle = "#d9480f";
      context.stroke();
    };
    // Each frame is drawn before the view asks for the next, so a slow
    // board is sent no more than it draws.
    const draw = async (data: string, size: FrameSize) => {
      try {
        const bytes = Uint8Array.from(atob(data), (character) => character.charCodeAt(0));
        const bitmap = await createImageBitmap(new Blob([bytes], { type: "image/jpeg" }));
        const element = canvas.current;
        if (isActive && element) {
          if (element.width !== bitmap.width) element.width = bitmap.width;
          if (element.height !== bitmap.height) element.height = bitmap.height;
          shown.current?.close();
          shown.current = bitmap;
          page.current = { width: size.width || bitmap.width, height: size.height || bitmap.height };
          paint();
          setHasFrame(true);
        } else bitmap.close();
      } catch { /* a frame that does not decode is passed by */ }
      if (isActive && socket.readyState === WebSocket.OPEN) socket.send('{"type":"ack"}');
    };
    socket.onmessage = (event: MessageEvent<unknown>) => {
      if (!isActive || typeof event.data !== "string") return;
      const said = parseSaid(event.data);
      if (said?.kind === "state") {
        setState(said.state);
        pointer.current = said.state.isDriven ? null : said.state.pointer;
        paint();
      } else if (said?.kind === "frame") void draw(said.data, said.size);
    };
    socket.onclose = (event) => {
      if (!isActive) return;
      link.current = null;
      setState(null);
      setClosedWhy(event.reason || "The goblin's browser stopped answering.");
    };
    return () => {
      isActive = false;
      link.current = null;
      socket.close();
      shown.current?.close();
      shown.current = null;
      pointer.current = null;
    };
  }, [isLive, task, generation, instance, attempt]);
  const send = (message: object) => {
    const socket = link.current;
    if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(message));
  };
  const isDriving = !!state?.isDriven && state.canDrive;
  // Taking control hands the keyboard to the screen once he has it.
  const wantsKeyboard = useRef(false);
  useEffect(() => {
    if (isDriving && wantsKeyboard.current) canvas.current?.focus();
    wantsKeyboard.current = false;
  }, [isDriving]);
  // The wheel scrolls the page, not the board, which a passive listener,
  // React's own, cannot keep it from.
  useEffect(() => {
    const element = canvas.current;
    if (!element || !isDriving) return;
    const wheel = (event: WheelEvent) => {
      event.preventDefault();
      const message = wheelMessage(pagePoint(event.clientX, event.clientY, element.getBoundingClientRect(), page.current), event, page.current);
      if (link.current?.readyState === WebSocket.OPEN) link.current.send(JSON.stringify(message));
    };
    element.addEventListener("wheel", wheel, { passive: false });
    return () => element.removeEventListener("wheel", wheel);
  }, [isDriving]);
  const at = (event: { clientX: number; clientY: number; currentTarget: Element }) => pagePoint(event.clientX, event.clientY, event.currentTarget.getBoundingClientRect(), page.current);
  // A click's count, from the presses before it, and a touch's drag, which
  // scrolls the page as a finger does.
  const clicks = useRef({ at: 0, x: 0, y: 0, count: 0 });
  const touch = useRef<{ x: number; y: number; isMoved: boolean } | null>(null);
  const move = useRef<{ x: number; y: number; pointer: PointerEvent<HTMLCanvasElement> } | null>(null);
  const press = (event: PointerEvent<HTMLCanvasElement>) => {
    if (!isDriving) return;
    event.preventDefault();
    event.currentTarget.focus();
    event.currentTarget.setPointerCapture(event.pointerId);
    if (event.pointerType === "touch") { touch.current = { x: event.clientX, y: event.clientY, isMoved: false }; return; }
    const point = at(event), prior = clicks.current;
    const count = event.timeStamp - prior.at < 500 && Math.abs(point.x - prior.x) < 4 && Math.abs(point.y - prior.y) < 4 ? Math.min(prior.count + 1, 3) : 1;
    clicks.current = { at: event.timeStamp, x: point.x, y: point.y, count };
    send(mouseMessage("mousePressed", point, event, count));
  };
  const drag = (event: PointerEvent<HTMLCanvasElement>) => {
    if (!isDriving) return;
    if (event.pointerType === "touch") {
      const start = touch.current;
      if (!start) return;
      const deltaX = start.x - event.clientX, deltaY = start.y - event.clientY;
      if (!start.isMoved && Math.hypot(deltaX, deltaY) < TAP_SLOP) return;
      const box = event.currentTarget.getBoundingClientRect();
      const scale = box.height > 0 ? page.current.height / box.height : 1;
      touch.current = { x: event.clientX, y: event.clientY, isMoved: true };
      send(wheelMessage(at(event), { ...modifiers(event), deltaX: deltaX * scale, deltaY: deltaY * scale, deltaMode: 0, buttons: 0 }, page.current));
      return;
    }
    // Moves go at most once a frame, the newest of them.
    const isWaiting = move.current !== null;
    move.current = { ...at(event), pointer: event };
    if (isWaiting) return;
    requestAnimationFrame(() => {
      const latest = move.current;
      move.current = null;
      if (latest) send(mouseMessage("mouseMoved", latest, latest.pointer, 0));
    });
  };
  const release = (event: PointerEvent<HTMLCanvasElement>) => {
    if (!isDriving) return;
    const point = at(event);
    if (event.pointerType === "touch") {
      const start = touch.current;
      touch.current = null;
      if (!start || start.isMoved) return;
      const tap = { ...modifiers(event), button: 0, buttons: 1 };
      send(mouseMessage("mousePressed", point, tap, 1));
      send(mouseMessage("mouseReleased", point, { ...tap, buttons: 0 }, 1));
      return;
    }
    send(mouseMessage("mouseReleased", point, event, clicks.current.count));
  };
  const key = (event: KeyboardEvent<HTMLCanvasElement>, kind: "keyDown" | "keyUp") => {
    if (!isDriving) return;
    if (isLeaveKey(event)) {
      event.preventDefault();
      if (kind === "keyDown") handBackWords.current?.focus();
      return;
    }
    if (isPasteKey(event)) return;
    event.preventDefault();
    send(keyMessage(event, kind));
  };
  const handBack = (event: FormEvent) => {
    event.preventDefault();
    send({ type: "hand-back", reason: words });
    setWords("");
  };
  const take = () => {
    wantsKeyboard.current = true;
    send({ type: "take" });
  };
  const pageName = state?.hasPage ? (state.title || "an untitled page") + (state.host ? " on " + state.host : "") : "no page";
  const status = !isLive ? (connected ? "" : "Reconnecting to the board")
    : closedWhy ? ""
    : !state ? "Connecting to " + name + "'s browser"
    : isDriving ? "You are driving " + name + "'s browser. Your clicks and keys on the screen go to its page. F6 leaves the screen."
    : state.isDriven ? name + "'s browser is being driven from the board on your PC."
    : name + " drives its browser.";
  return <section ref={region} className={"screen-view" + (isFull ? " full" : "") + (isDriving ? " driving" : "")} aria-label={name + "'s browser"} tabIndex={-1}>
    <div className="screen-bar">
      <p className="screen-page" title={pageName}><Icon name="screen" /><span>{state ? state.hasPage ? <><strong>{state.title || "Untitled page"}</strong>{state.host && <span className="muted"> {state.host}</span>}</> : "No page is open" : name + "'s browser"}</span></p>
      {state && !state.isDriven && (state.canDrive
        ? <button type="button" className="primary" onClick={take}>Take control</button>
        : <span className="muted screen-note">{state.driveNote}</span>)}
    </div>
    {state?.isDriven && <form className="screen-hand-back" onSubmit={handBack}>
      <label>What you did <input ref={handBackWords} value={words} maxLength={300} autoComplete="off" placeholder="Signed in" onChange={(event) => setWords(event.target.value)} /></label>
      <button type="submit" className="primary">Hand back</button>
    </form>}
    <p className="screen-status" role="status">{status}</p>
    {closedWhy && <Notice form="box" tone="warning" actions={<button type="button" disabled={!connected} onClick={() => setAttempt((prior) => prior + 1)}>Reconnect</button>}>{closedWhy}</Notice>}
    <div className="screen-stage">
      <canvas ref={canvas} className="screen-canvas" hidden={!hasFrame} tabIndex={isDriving ? 0 : -1}
        role={isDriving ? "application" : "img"} aria-roledescription="browser screen"
        aria-label={isDriving ? name + "'s browser, showing " + pageName + ". Your clicks and keys here go to the page. F6 leaves the screen." : name + "'s browser, showing " + pageName}
        onPointerDown={press} onPointerMove={drag} onPointerUp={release} onKeyDown={(event) => key(event, "keyDown")} onKeyUp={(event) => key(event, "keyUp")}
        onPaste={(event) => {
          if (!isDriving) return;
          event.preventDefault();
          const text = event.clipboardData.getData("text/plain").slice(0, MAX_PASTE);
          if (text) send({ type: "text", text });
        }}
        onContextMenu={(event) => { if (isDriving) event.preventDefault(); }} />
      {!hasFrame && isLive && !closedWhy && <p className="screen-waiting muted">Waiting for the page to draw</p>}
    </div>
  </section>;
}
