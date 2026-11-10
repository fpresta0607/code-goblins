import { useCallback, useEffect, useRef, useState } from "react";
import { message, reportToCfo, request } from "./api";
import { parseBrowserLogins, type BrowserLogin } from "./browser-logins";
import { useClickFeedback } from "./click-feedback";
import { askKey, goblinKey } from "./connector-list";
import { connectorReader, type ConnectorReader, type Connections } from "./connector-reader";
import { parseConnections, type ConnectionEntry } from "./connections";
import { goblinName } from "./task-words";
import { object, string, type Run, type Task } from "./types";

// What the page last read of one goblin: its connections, when, and whether
// that read failed.
export interface Reading { connections?: Connections; readAt: number; hasFailed: boolean }

// What a repair he started answered: its words, the Command Center card of
// its run, and the sign-in page it opened.
export interface Repair { notice: string; runId: string; signIn: string }

const NO_REPAIR: Repair = { notice: "", runId: "", signIn: "" };

// When the latest repair of a goblin's connection ended, which its
// connections are read again after.
function repairedAt(task: Task, runs: Run[]): string {
  return runs.filter((run) => run.connection_task === task.id && run.connection_generation === task.generation).map((run) => run.finished_at).sort().at(-1) || "";
}

// useConnectors reads the connections of every running goblin for the
// Connectors page and carries what the page does to them. Each goblin is
// read until its check has ended and again only when a repair of it ends,
// he gives or takes back one of its connectors, or he asks for a recheck,
// so a page left open asks the supervisor nothing. A read that fails goes
// to the CFO by goblin and task, and a refusal of his own click is said
// beside it.
export function useConnectors(goblins: Task[], runs: Run[], instance: string, onRepair: (key: string) => void) {
  const [readings, setReadings] = useState<ReadonlyMap<string, Reading>>(new Map());
  // What his Connect or Disconnect asked of a goblin's connector, shown at
  // once and kept until a finished check shows it.
  const [asked, setAsked] = useState<ReadonlyMap<string, boolean>>(new Map());
  const [feedback, showFeedback] = useClickFeedback();
  const [repair, setRepair] = useState(NO_REPAIR);
  const [isBusy, setIsBusy] = useState(false);
  const reader = useRef<ConnectorReader | null>(null);
  const isSignInPending = useRef(false);
  // The goblins that run, each with when its latest repair ended.
  const live = goblins.map((task) => ({ task, repaired: repairedAt(task, runs) }));
  const running = useRef(live);
  useEffect(() => { running.current = live; });
  useEffect(() => {
    const made = connectorReader(instance, (task, connections) => {
      const goblin = goblinKey(task);
      setReadings((prior) => new Map(prior).set(goblin, { connections, readAt: Date.now(), hasFailed: false }));
      if (connections.checking) return;
      setAsked((prior) => {
        const kept = [...prior].filter(([key, isCarried]) => {
          const entry = connections.entries.find((each) => askKey(goblin, each.id) === key);
          return !key.startsWith(goblin + "\n") || !!entry && entry.grant !== (isCarried ? "revoke" : "grant");
        });
        return kept.length === prior.size ? prior : new Map(kept);
      });
    }, (task, error, isRecheck) => {
      const goblin = goblinKey(task);
      setReadings((prior) => {
        const connections = prior.get(goblin)?.connections;
        return new Map(prior).set(goblin, { connections: connections && { ...connections, checking: false }, readAt: Date.now(), hasFailed: true });
      });
      if (isRecheck) showFeedback(message(error));
      else reportToCfo("reading the connections of " + goblinName(task) + " (" + task.id + ")", message(error));
    });
    reader.current = made;
    return () => { made.stop(); reader.current = null; };
  }, [instance, showFeedback]);
  const wanted = live.map(({ task, repaired }) => goblinKey(task) + "\n" + repaired).join("\n\n");
  useEffect(() => {
    for (const { task, repaired } of running.current) reader.current?.read(task, repaired);
  }, [wanted, instance]);
  const post = useCallback((task: Task, endpoint: string, extra: Record<string, string> = {}) => {
    return request(endpoint, undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ task: task.id, generation: task.generation, ...extra }) });
  }, [instance]);
  const recheck = useCallback(() => {
    setReadings((prior) => new Map([...prior].map(([goblin, reading]) => [goblin, reading.connections ? { ...reading, connections: { ...reading.connections, checking: true } } : reading])));
    for (const { task } of running.current) reader.current?.recheck(task);
  }, []);
  useEffect(() => {
    const returned = () => {
      if (!isSignInPending.current) return;
      isSignInPending.current = false;
      recheck();
    };
    window.addEventListener("focus", returned);
    return () => window.removeEventListener("focus", returned);
  }, [recheck]);
  const fix = async (task: Task, entry: ConnectionEntry, action: string) => {
    setIsBusy(true);
    setRepair(NO_REPAIR);
    try {
      const result = object(await post(task, "/api/connections/fix", { connection: entry.id, action }));
      const runId = string(result.run_id), url = string(result.url);
      const signIn = url && new URL(url).protocol === "https:" ? url : "";
      setRepair({ notice: string(result.message), runId, signIn });
      if (runId) onRepair("run:" + runId);
      if (signIn) {
        isSignInPending.current = true;
        window.open(signIn, "_blank", "noopener,noreferrer");
      }
    } catch (error: unknown) { showFeedback(message(error)); }
    finally { setIsBusy(false); }
  };
  // Connect gives the goblin the connector and Disconnect takes it back, as
  // cfo auth grant and cfo auth revoke do; its row changes at once.
  const give = async (task: Task, entry: ConnectionEntry, isGiving: boolean) => {
    const goblin = goblinKey(task);
    setAsked((prior) => new Map(prior).set(askKey(goblin, entry.id), isGiving));
    showFeedback("");
    try {
      const connections = parseConnections(await post(task, "/api/connections/grant", { connection: entry.id, action: isGiving ? "grant" : "revoke" }));
      setReadings((prior) => new Map(prior).set(goblin, { connections, readAt: Date.now(), hasFailed: false }));
    } catch (error: unknown) {
      setAsked((prior) => { const next = new Map(prior); next.delete(askKey(goblin, entry.id)); return next; });
      showFeedback(message(error));
    } finally { reader.current?.again(task); }
  };
  const endLogin = async (task: Task, login: BrowserLogin, action: "sign-out" | "forget") => {
    setIsBusy(true);
    try {
      const logins = parseBrowserLogins(object(await post(task, "/api/browser-logins/" + action, { login: login.id })).browser_logins);
      const after = (each: BrowserLogin) => logins.find((next) => next.id === each.id) ?? each;
      setReadings((prior) => new Map([...prior].map(([goblin, reading]) => [goblin, reading.connections ? { ...reading, connections: { ...reading.connections, browserLogins: reading.connections.browserLogins.filter((each) => action !== "forget" || each.id !== login.id).map(after) } } : reading])));
    } catch (error: unknown) { showFeedback(message(error)); }
    finally { setIsBusy(false); }
  };
  return { readings, asked, feedback, repair, isBusy, recheck, fix, give, endLogin, expectSignIn: () => { isSignInPending.current = true; } };
}
