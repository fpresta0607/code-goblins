import { useEffect, useRef, useState } from "react";
import { BrowserLoginRow } from "./browser-login-row";
import { loginName, type BrowserLogin } from "./browser-logins";
import { ClickFeedback } from "./click-feedback";
import { ConnectorDetail } from "./connector-detail";
import { goblinKey, listConnectors, matchesConnector, type Connector } from "./connector-list";
import { ConnectorRow } from "./connector-row";
import { ForgetLoginDialog } from "./forget-login-dialog";
import { Icon } from "./Icon";
import type { Snapshot, Task } from "./types";
import { useConnectors } from "./use-connectors";
import "./connectors-page.css";

type Tab = "yours" | "discover" | "logins";

// What a tab says when it lists nothing.
const EMPTY: Record<Tab, string> = {
  yours: "No goblin carries a connector yet.",
  discover: "Nothing more to connect.",
  logins: "No browser logins.",
};

// The Connectors page, one page for every goblin's connectors in the place
// of a panel in each goblin's task panel (the Overlord, 2026-11-10: "I also
// don't like the connectors panel inside the task panels for every goblin.
// It doesn't look like Codex or Claude Code's connectors panel, which is
// much, much cleaner, in the way that you can navigate it and use it",
// #2303). Its list is laid out as Claude's: Yours, the connectors a goblin
// carries, and Discover, those on offer that none does, with a search, and
// each row opens that connector's own page. A project's browser logins are
// a third list. It reads the goblins that run, so with none running it has
// nothing to read.
export function ConnectorsPage({ snapshot, onRepair }: { snapshot: Snapshot; onRepair: (key: string) => void }) {
  const goblins = snapshot.tasks.filter((task) => task.generation && !task.archived);
  const { readings, asked, feedback, repair, isBusy, recheck, fix, give, endLogin, expectSignIn } = useConnectors(goblins, snapshot.runs || [], snapshot.instance, onRepair);
  const [tab, setTab] = useState<Tab>("yours");
  const [search, setSearch] = useState("");
  const [opened, setOpened] = useState("");
  const [forgetting, setForgetting] = useState<{ login: BrowserLogin; task: Task }>();
  const returnFocus = useRef<HTMLElement | null>(null);
  // A connector's page opens with the keyboard on its way back, and the way
  // back hands it to the row it was opened from.
  const backButton = useRef<HTMLButtonElement>(null);
  useEffect(() => { if (opened) backButton.current?.focus(); }, [opened]);
  const byKey = new Map(goblins.map((task) => [goblinKey(task), task]));
  const connectors = listConnectors(goblins.map((task) => ({ goblin: goblinKey(task), entries: readings.get(goblinKey(task))?.connections?.entries ?? [] })), asked);
  // Each login once, with a goblin whose project may use it, which signs it
  // out or forgets it.
  const logins = new Map<string, { login: BrowserLogin; task: Task }>();
  for (const task of goblins) for (const login of readings.get(goblinKey(task))?.connections?.browserLogins ?? []) if (!logins.has(login.id)) logins.set(login.id, { login, task });
  const read = goblins.map((task) => readings.get(goblinKey(task)));
  const isChecking = read.some((reading) => !reading || !!reading.connections?.checking);
  const hasFailed = read.some((reading) => reading?.hasFailed || !!reading?.connections?.error);
  // When the page last read a goblin, which a login's times are told from.
  const readAt = Math.max(0, ...read.map((reading) => reading?.readAt ?? 0));
  const found = connectors.filter((connector) => matchesConnector(connector, search));
  const lists: Record<Exclude<Tab, "logins">, Connector[]> = { yours: found.filter((connector) => connector.status !== "available"), discover: found.filter((connector) => connector.status === "available") };
  const foundLogins = [...logins.values()].filter(({ login }) => loginName(login).toLowerCase().includes(search.trim().toLowerCase()));
  const tabs: [Tab, string, number][] = [["yours", "Yours", lists.yours.length], ["discover", "Discover", lists.discover.length]];
  if (logins.size > 0) tabs.push(["logins", "Browser logins", foundLogins.length]);
  const shown: Tab = tab === "logins" && logins.size === 0 ? "yours" : tab;
  const count = shown === "logins" ? foundLogins.length : lists[shown].length;
  const open = connectors.find((connector) => connector.key === opened);
  const openConnector = (connector: Connector, source: HTMLElement) => { returnFocus.current = source; setOpened(connector.key); };
  const back = () => {
    setOpened("");
    requestAnimationFrame(() => returnFocus.current?.isConnected && returnFocus.current.focus());
  };
  return <div className="connectors-page">
    <header className="connectors-head">
      {open ? <button ref={backButton} type="button" className="text-button connector-back" onClick={back}><Icon name="back" />Connectors</button> : <h2>Connectors</h2>}
      <p role="status">{goblins.length === 0 ? "" : isChecking ? "Checking connectors..." : hasFailed ? "Check failed" : ""}</p>
      <button type="button" className="icon-button" aria-label="Recheck connectors" disabled={isBusy || isChecking || goblins.length === 0} onClick={recheck}><Icon name="refresh" /></button>
    </header>
    <ClickFeedback text={feedback} />
    {repair.notice && <div className="connector-notice">
      <p role="status">{repair.notice}</p>
      {repair.runId && <button type="button" onClick={() => onRepair("run:" + repair.runId)}>Open repair card</button>}
      {repair.signIn && <a href={repair.signIn} target="_blank" rel="noreferrer" onClick={expectSignIn}>Open sign-in</a>}
    </div>}
    {open && <ConnectorDetail key={open.key} connector={open} goblins={byKey} readings={readings} asked={asked} isBusy={isBusy}
      onFix={(task, use, action) => void fix(task, use.entry, action)} onGive={(task, use, isGiving) => void give(task, use.entry, isGiving)} />}
    <div className="connectors-list" hidden={!!open}>
      <div className="connectors-bar">
        <div className="connectors-tabs" role="group" aria-label="Connectors shown">
          {tabs.map(([id, label, size]) => <button key={id} type="button" aria-pressed={shown === id} onClick={() => setTab(id)}>{label}<span className="connector-count">{size}</span></button>)}
        </div>
        <label className="connectors-search"><Icon name="search" /><input type="search" value={search} placeholder="Search" aria-label="Search connectors" onChange={(event) => setSearch(event.currentTarget.value)} /></label>
      </div>
      {shown !== "logins" && count > 0 && <>
        <div className="connector-columns" aria-hidden="true"><span>Connector</span><span>Type</span><span>Status</span></div>
        <ul className="connector-rows" aria-label={shown === "yours" ? "Your connectors" : "Connectors to discover"}>
          {lists[shown].map((connector) => <ConnectorRow key={connector.key} connector={connector} onOpen={openConnector} />)}
        </ul>
      </>}
      {shown === "logins" && count > 0 && <ul className="connector-logins" aria-label="Browser logins">
        {foundLogins.map(({ login, task }) => <BrowserLoginRow key={login.id} login={login} now={readAt} isBusy={isBusy} onSignOut={() => void endLogin(task, login, "sign-out")} onForget={() => setForgetting({ login, task })} />)}
      </ul>}
      {count === 0 && <p className="connectors-empty">{goblins.length === 0 ? "No goblin is running. Connectors are read from the goblins that run."
        : search.trim() ? "Nothing matches \"" + search.trim() + "\"." : isChecking ? "" : EMPTY[shown]}</p>}
    </div>
    {forgetting && <ForgetLoginDialog login={forgetting.login} onCancel={() => setForgetting(undefined)} onForget={() => { setForgetting(undefined); void endLogin(forgetting.task, forgetting.login, "forget"); }} />}
  </div>;
}
