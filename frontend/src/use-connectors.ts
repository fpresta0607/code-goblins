import { useCallback, useEffect, useRef, useState } from "react";
import { message, reportToCeo, request } from "./api";
import { useClickFeedback } from "./click-feedback";
import { isAnyChecking, parseConnectorSnapshot, type ConnectorSnapshot } from "./connector-snapshot";
import type { ConnectorRow, RefreshTarget } from "./connector-rows";
import { object, string, type Run, type Task } from "./types";

// What a repair he started answered: its words, the Command Center card of
// its run, and the sign-in page it opened.
export interface Repair { notice: string; runId: string; signIn: string }

const NO_REPAIR: Repair = { notice: "", runId: "", signIn: "" };

// How often the snapshot is read again while a level is being checked, and how
// many times in a row: a check that never ends is read for a minute, then left
// until the Overlord refreshes or the board changes.
const POLL_MS = 2000;
export const POLL_LIMIT = 30;

export interface ConnectorActions {
  isBusy: boolean;
  refresh: (target: RefreshTarget) => void;
  fix: (project: string, connection: string, action: string) => void;
  grant: (task: string, connection: string, isGiving: boolean) => void;
  endLogin: (project: string, login: string, action: "sign-out" | "forget") => void;
  expectSignIn: (project: string) => void;
  // Remove takes one item of Needs a look out of where it is declared, and
  // rejects with the supervisor's reason when it refuses.
  remove: (row: ConnectorRow) => Promise<void>;
}

// useConnectors reads the Connectors page's one snapshot, GET /api/connectors,
// when the page opens, when the board's goblins or runs change and after what
// he does here, and every 2 s only while a level says it is checking, up to a
// limit. Searching reads nothing: it runs over what was read. A read that
// fails goes to the CEO, and a refusal of his own click is said beside it.
export function useConnectors(tasks: Task[], runs: Run[], instance: string, onRepair: (key: string) => void) {
  const [view, setView] = useState<ConnectorSnapshot>();
  const [isUnreadable, setIsUnreadable] = useState(false);
  const [isCapped, setIsCapped] = useState(false);
  const [reads, setReads] = useState(0);
  const [feedback, showFeedback] = useClickFeedback();
  const [repair, setRepair] = useState(NO_REPAIR);
  const [isBusy, setIsBusy] = useState(false);
  const signInProject = useRef("");
  const board = tasks.map((task) => task.id + ":" + task.generation).join(",") + "|" + runs.map((run) => run.id + ":" + run.finished_at).join(",");
  const readAgain = useCallback(() => setReads((count) => count + 1), []);
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let polls = 0;
    const read = async () => {
      try {
        const snapshot = parseConnectorSnapshot(await request("/api/connectors", controller.signal));
        if (controller.signal.aborted) return;
        setView(snapshot);
        setIsUnreadable(false);
        const isChecking = isAnyChecking(snapshot);
        setIsCapped(isChecking && polls >= POLL_LIMIT);
        if (isChecking && polls < POLL_LIMIT) {
          polls += 1;
          timer = setTimeout(() => void read(), POLL_MS);
        }
      } catch (error: unknown) {
        if (controller.signal.aborted) return;
        setIsUnreadable(true);
        reportToCeo("reading the connectors", message(error));
      }
    };
    void read();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [board, instance, reads]);
  const post = useCallback((path: string, body: Record<string, string>) => {
    return request(path, undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CEO-Token": instance }, body: JSON.stringify(body) });
  }, [instance]);
  const refresh = useCallback(async (target: RefreshTarget) => {
    try {
      await post("/api/connectors/refresh", { level: target.level, scope: target.scope });
      readAgain();
    } catch (error: unknown) { showFeedback(message(error)); }
  }, [post, readAgain, showFeedback]);
  // A goblin of the project whose session a login's sign-out or forget goes
  // through, as the supervisor wants one of the project to act for it.
  const goblinOf = (project: string) => view?.goblins.find((goblin) => goblin.project === project);
  const act = async (run: () => Promise<void>) => {
    setIsBusy(true);
    try { await run(); } catch (error: unknown) { showFeedback(message(error)); } finally { setIsBusy(false); }
  };
  const fix = (project: string, connection: string, action: string) => act(async () => {
    setRepair(NO_REPAIR);
    const result = object(await post("/api/connectors/fix", { project, connection, action }));
    const runId = string(result.run_id), url = string(result.url);
    const signIn = url && new URL(url).protocol === "https:" ? url : "";
    setRepair({ notice: string(result.message), runId, signIn });
    if (runId) onRepair("run:" + runId);
    if (signIn) {
      signInProject.current = project;
      window.open(signIn, "_blank", "noopener,noreferrer");
    }
    readAgain();
  });
  // Connect gives the goblin the connection and Disconnect takes it back, as
  // goblins auth grant and goblins auth revoke do.
  const grant = (task: string, connection: string, isGiving: boolean) => act(async () => {
    const goblin = view?.goblins.find((each) => each.task === task);
    if (!goblin) return;
    await post("/api/connections/grant", { task, generation: goblin.generation, connection, action: isGiving ? "grant" : "revoke" });
    readAgain();
  });
  const endLogin = (project: string, login: string, action: "sign-out" | "forget") => act(async () => {
    const goblin = goblinOf(project);
    if (!goblin) return;
    await post("/api/browser-logins/" + action, { task: goblin.task, generation: goblin.generation, login });
    readAgain();
  });
  // A goblin's item is removed from the session it was read from, so one that
  // restarted since is refused rather than changed. The answer is the
  // snapshot the removal left, shown at once.
  const remove = async (row: ConnectorRow) => {
    const generation = row.task ? view?.goblins.find((goblin) => goblin.task === row.task)?.generation ?? "" : "";
    setView(parseConnectorSnapshot(await post("/api/connectors/remove", { level: row.level, project: row.project, task: row.task, generation, connection: row.connection })));
  };
  useEffect(() => {
    const returned = () => {
      const project = signInProject.current;
      if (!project) return;
      signInProject.current = "";
      void refresh({ level: "project", scope: project });
    };
    window.addEventListener("focus", returned);
    return () => window.removeEventListener("focus", returned);
  }, [refresh]);
  const actions: ConnectorActions = {
    isBusy, refresh: (target) => void refresh(target), fix: (project, connection, action) => void fix(project, connection, action),
    grant: (task, connection, isGiving) => void grant(task, connection, isGiving), endLogin: (project, login, action) => void endLogin(project, login, action),
    expectSignIn: (project) => { signInProject.current = project; }, remove,
  };
  return { view, isUnreadable, isCapped, feedback, repair, actions, readAgain };
}
