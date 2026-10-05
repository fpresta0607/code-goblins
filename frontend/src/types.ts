export interface RuntimeEvidence {
  state: string;
  reason: string;
  at: string;
}
export interface Evaluation {
  phase: string;
  reason: string;
  head: string;
  pr: string;
  verified: boolean;
  at: string;
}
export interface Task extends Evaluation {
  lifecycle?: LifecycleStatus;
  // teardown names the task's stopped processes, from this or an earlier
  // session, that Windows is still tearing down.
  teardown: string[];
  detail: string;
  queue_revision: string;
  notes: string[];
  action_error: string;
  pending_engine?: { harness: string; model: string; effort: string; when: string };
  switching?: boolean;
  branch: string;
  runtime?: RuntimeEvidence;
  id: string;
  title: string;
  project: string;
  harness: string;
  // backend is the terminal the task runs in: native, herdr, or empty before it starts.
  backend: string;
  model: string;
  effort: string;
  mode: string;
  generation: string;
  session: string;
  dependencies: string[];
  activity: string;
  handoff?: boolean;
  last_report?: string;
  retired_at?: string;
  // report is the kind of the goblin's latest report: working, blocked,
  // failed, done, waiting, or empty.
  report: string;
  // waiting_on names what a waiting task waits on: another task's id,
  // overlord, ci or deploy; gate_step is the gate step of a task in review.
  waiting_on: string;
  gate_step: string;
  archived: boolean;
  merged: boolean;
  // closed says GitHub closed a finished task's pull request without merging.
  closed: boolean;
  // since is when a live task's session started, or when queued work's brief
  // was written; empty when neither is known.
  since: string;
  // brief says queued work has its brief, which Start needs; starting that
  // its Start runs cfo spawn now, and start_error why its last Start failed.
  brief: boolean;
  starting: boolean;
  start_error: string;
}
export interface LifecycleStatus {
  phase: string; action: string; at: string; kept: string[]; stopped: string[]; problems: string[];
  handoff_saved: boolean; validation_restarts: boolean;
}
// Memory is the machine's free memory and free commit (memory plus page file)
// in bytes beside the fleet's floor, under which nothing starts, and the mark
// at which the CFO starts the next queued task; with the kernel's pools and,
// while commit is the tighter, the apps holding the most of it.
export interface Memory {
  available: number; total: number; commit_available: number; commit_limit: number;
  paged_pool: number; nonpaged_pool: number; floor: number; next: number; holders: CommitHolder[];
}
// CommitHolder is one app's commit: its first process and every process it
// started.
export interface CommitHolder { name: string; commit: number }
export interface Session {
  runtime?: RuntimeEvidence;
  id: string;
  native_id: string;
  harness: string;
  role: string;
  task_id: string;
  generation: string;
  parent: string;
  reported_root: string;
  relation: string;
  model: string;
  agent_type: string;
  phase: string;
  turn_id: string;
  host_id: string;
  last_event_id: string;
  updated_at: string;
}
export interface Action {
  question_id: string;
  answer_kind: string;
  id: string;
  kind: string;
  task_id: string;
  generation: string;
  status: string;
  message: string;
  // awaiting: who a delivery typed and submitted waits on to report taking
  // it, empty when it waits on nobody. advice: what to do about one that
  // never arrived, in the Overlord's words.
  awaiting: "" | "the CFO" | "the goblin";
  advice: string;
  text: string;
  file: string;
  line: number;
  side: string;
  updated_at: string;
}
export interface Decision {
  seq: number;
  time: string;
  kind: string;
  key: string;
  detail: string;
}
export interface BoardActivity {
  cfo_identity?:string; live?:boolean;
  id:string; kind:string; task_id:string; generation:string; source:string; target:string; state:string; url:string; at:string; until:string;
}
export interface SubscriptionUsage {
  provider: "claude" | "codex";
  status: "available" | "unavailable" | "stale" | "auth_required";
  percent_remaining: number | null;
  read_at: string;
  resets_at: string;
  source: "oauth" | "api" | "";
}
export interface Snapshot {
  subscriptions?: SubscriptionUsage[];
  activity?: BoardActivity[];
  example: boolean;
  instance: string;
  revision: number;
  started: string;
  at: string;
  reconciled: string;
  healthy: boolean;
  error: string;
  registration: string;
  // The native terminal the registered CFO runs in; empty while it runs in Herdr.
  cfo_terminal: string;
  // The harness the registered CFO runs, such as claude; empty while none is registered.
  cfo_harness: string;
  // build names the board bundle the supervisor serves.
  build: string;
  // cfo_runs says a CFO is registered and running or starting; without one
  // the board shows its first-run page.
  cfo_runs: boolean;
  // cfo_starting says the CFO runs in its terminal but has not registered
  // yet: Claude Code registers through its SessionStart hook after its
  // onboarding and sign-in, a Codex or pi CFO when its first prompt runs cfo
  // register.
  cfo_starting: boolean;
  inbox: number;
  tasks: Task[];
  sessions: Session[];
  retired: string[];
  actions: Action[];
  decisions: Decision[];
  issues: string[];
  // attention is the Overlord's order of the live goblins, top first; a goblin it does not name has not been placed.
  attention: string[];
  questions?: Question[];
  reviews?: Review[];
  runs?: Run[];
  credentials?: CredentialRequest[];
  // memory is absent on a board that cannot start goblins or read it.
  memory: Memory | null;
  afk: Afk;
}
// AfkHeld is an item held for the Overlord while AFK mode is on: its key in
// the Command Center, its goblin (empty for the CFO's own), what it asks,
// whether it still waits on him and what became of it, and what its goblin
// reported meanwhile.
export interface AfkHeld {
  item: string; task: string; what: string; at: string; waiting: boolean; now: string; meanwhile: string;
}
// Afk is AFK mode, the Overlord's switch for running the fleet while he is
// away: on, off, or unreadable when the supervisor cannot read the switch.
// While on it says since when and from where, how many decisions the CFO
// logged and what is held for him; asked holds his words when the CFO turned
// it on at his ask, and is empty when he turned it on himself. While off,
// report names the last stretch that ended, once its report is kept.
export interface Afk {
  state: "off" | "on" | "unreadable";
  since: string; from: string; asked: string; decided: number; held: AfkHeld[];
  report: string;
}
export interface Question {
  id: string; identity: string; text: string; options: string[]; recommended: string; answer: string; answer_kind: string; created_at: string; answer_id: string; status: string; message: string;
  // Who closed the question, with which choice and when ("" for a written answer).
  answered_option: string; answered_by: string; answered_at: string;
  // task names the goblin that asked; it is empty for the CFO's own question.
  task: string;
  // page is the open review item whose page carries this question, which
  // shows it; answered_in says where an answer given outside its card came
  // from, such as "page".
  page: string; answered_in: string;
  // image_count is how many review images the goblin attached, one for each choice in order.
  image_count: number;
  // generation is the asking goblin's session; empty for the CFO's question.
  generation: string;
}
// A review item waits on the Overlord until he answers or clears it, or its
// reporter withdraws it: an image review, a Lavish page, or a wait on him.
export interface Review {
  id: string; identity: string; task: string; title: string; image_count: number; lavish: string;
  // watched: the supervisor polls the item's Lavish page, so his answer or
  // end of the review there closes the item.
  watched: boolean;
  // answered_by and answered_in say who answered the item outside the
  // Command Center and where, such as "overlord" on its "page".
  answered_by: string; answered_in: string;
  // question is the goblin's pending question this item's page carries, and
  // window_closed_at when the page's review window last closed, if it has.
  question: string; window_closed_at: string;
  // document is a delivered file, or null for any other item.
  document: ReviewDocument | null;
  state: string; answer: string; answer_id: string; delivered: boolean; reason: string; created_at: string; updated_at: string;
}
// A document delivered with cfo deliver: kind is set only when the browser
// may open it, and link, when set, is where Open goes instead of the copy.
export interface ReviewDocument { name: string; size: number; kind: string; link: string }
// A command the CFO needs the Overlord to run (API8): the stored text is what
// runs; the board shows it verbatim and names the item when he presses Run.
export interface Run {
  id: string; identity: string; title: string; shell: string; admin: boolean; command: string; cwd: string;
  state: string; exit_code: number | null; output: string; reason: string;
  created_at: string; expires_at: string; ran_at: string; finished_at: string;
  // connection_task and connection_generation name the goblin a connection repair belongs to.
  connection_task: string; connection_generation: string;
  // credential_request names the credential request whose card opened this
  // terminal, and credential_names the names it stores.
  credential_request: string; credential_names: string[];
}
// A request for credential values by name: the Overlord pastes each value on
// its card, and the board stores it in the project's credential scope. It
// never carries a value.
export interface CredentialRequest {
  id: string; generation: string; identity: string;
  // by is "cfo" or "goblin"; task is the goblin that needs the values.
  by: string; task: string;
  project: string; repository: string;
  // env_file is a local env file at the root of the repository where each
  // saved value is also set; written are the names set there.
  env_file: string;
  names: string[]; why: string; link: string;
  // existing are the names the scope already held when the board last looked.
  existing: string[];
  hints: CredentialHint[];
  // services are the auth.json services that read each name.
  services: Record<string, string[]>;
  // state is open, saved or expired; typed are the saved names typed in its terminal.
  state: string; saved: string[]; replaced: string[]; typed: string[]; written: string[]; told: string[]; reason: string;
  created_at: string; expires_at: string; closed_at: string;
}
// A name's format hint from its project's auth.json: how its value should
// start, and advice for values that start a certain way.
export interface CredentialHint { name: string; prefixes: string[]; warn: { prefix: string; say: string }[] }
export interface ChangedFile {
  path: string;
  status: string;
}
export interface FileDiff {
  path: string;
  patch: string;
  code: string;
  head: string;
  revision: string;
  binary: boolean;
  code_omitted: boolean;
  fingerprint: string;
}
// Setup is the first-run page: the home the CFO starts in, the agent the
// quick start remembered, or "" when none was chosen, the projects folder and
// the git checkouts in it, or why it offers none, the agents this machine
// has, and whether a CFO already runs.
export interface Setup {
  home: string;
  agent: string;
  projects_root: string;
  checkouts: string[];
  problem: string;
  agents: SetupAgent[];
  cfo_runs: boolean;
}
// SetupAgent is one agent the first-run page shows: whether it is the
// recommended one and the few words on what a CFO in it gets, both from the
// supervisor's table of what is proved, and why Start cannot pick it when it
// cannot.
export interface SetupAgent {
  id: string;
  name: string;
  recommended: boolean;
  note: string;
  installed: boolean;
  signed_in: boolean;
  reason: string;
}
export interface Commit {
  sha: string;
  short: string;
  subject: string;
  author: string;
  date: string;
}
export interface ReviewSelection {
  task_id: string;
  generation: string;
  file: string;
  line: number;
  end_line: number;
  side: "old" | "new";
  head: string;
  revision: string;
  diff_id: string;
}

export function decisionText(detail: string): string {
  if (!detail.startsWith("review: ")) return detail;
  try {
    const record = object(JSON.parse(detail.slice(8)));
    const file = string(record.file), text = string(record.text);
    const first = number(record.line), last = number(record.end_line);
    const side = string(record.side);
    if (!file || !text || !Number.isInteger(first) || !Number.isInteger(last) || first < 1 || last < first || (side !== "old" && side !== "new")) {
      throw new Error("Invalid review context");
    }
    return `${file} · ${side} ${first === last ? "line " + first : "lines " + first + "–" + last}\n${text}`;
  } catch {
    return "Review request metadata is invalid. Inspect the durable CFO queue.";
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
export function object(value: unknown): Record<string, unknown> {
  if (!isRecord(value)) throw new Error("Invalid response object");
  return value;
}
export function array(value: unknown): unknown[] {
  if (value == null) return [];
  if (!Array.isArray(value)) throw new Error("Invalid response list");
  return value;
}
export function string(value: unknown): string {
  if (value == null) return "";
  if (typeof value !== "string") throw new Error("Invalid response text");
  return value;
}
function number(value: unknown): number {
  if (value == null) return 0;
  if (typeof value !== "number" || !Number.isFinite(value))
    throw new Error("Invalid response number");
  return value;
}
function boolean(value: unknown): boolean {
  if (typeof value !== "boolean") throw new Error("Invalid response boolean");
  return value;
}
export const strings = (value: unknown): string[] => array(value).map(string);
export function parseAction(value: unknown): Action {
  const v = object(value);
  return {
    id: string(v.id),
    kind: string(v.kind),
    question_id: string(v.question_id),
    answer_kind: string(v.answer_kind),
    task_id: string(v.task_id),
    generation: string(v.generation),
    status: string(v.status),
    message: string(v.message),
    awaiting: isRecord(v.awaiting) ? (string(v.awaiting.task) ? "the goblin" : "the CFO") : "",
    advice: string(v.advice),
    text: string(v.text),
    file: string(v.file),
    line: number(v.line),
    side: string(v.side),
    updated_at: string(v.updated_at),
  };
}
function parseRuntime(value: unknown): RuntimeEvidence | undefined {
  if (value == null) return;
  const r = object(value);
  return { state: string(r.state), reason: string(r.reason), at: string(r.at) };
}
function parseCredentialRequest(value: unknown): CredentialRequest {
  const c = object(value);
  const services: Record<string, string[]> = {};
  for (const [name, users] of Object.entries(c.services == null ? {} : object(c.services))) services[name] = strings(users);
  return {
    id: string(c.id), generation: string(c.generation), identity: string(c.identity), by: string(c.by), task: string(c.task),
    project: string(c.project), repository: string(c.repository), env_file: string(c.env_file), names: strings(c.names), why: string(c.why), link: string(c.link),
    existing: strings(c.existing), services,
    hints: array(c.hints).map((hint) => {
      const h = object(hint);
      return { name: string(h.name), prefixes: strings(h.prefixes), warn: array(h.warn).map((warning) => { const w = object(warning); return { prefix: string(w.prefix), say: string(w.say) }; }) };
    }),
    state: string(c.state), saved: strings(c.saved), replaced: strings(c.replaced), typed: strings(c.typed), written: strings(c.written), told: strings(c.told), reason: string(c.reason),
    created_at: string(c.created_at), expires_at: string(c.expires_at), closed_at: string(c.closed_at),
  };
}
export function parseAfkHeld(value: unknown): AfkHeld {
  const h = object(value);
  return { item: string(h.item), task: string(h.task), what: string(h.what), at: string(h.at), waiting: h.waiting === undefined ? false : boolean(h.waiting), now: string(h.now), meanwhile: string(h.meanwhile) };
}
// A supervisor from before AFK mode reached the board sends none, which is off.
function parseAfk(value: unknown): Afk {
  const a = value == null ? {} : object(value);
  const state = a.state == null ? "off" : string(a.state);
  if (state !== "off" && state !== "on" && state !== "unreadable") throw new Error("Invalid AFK state");
  return { state, since: string(a.since), from: string(a.from), asked: string(a.asked), decided: number(a.decided), held: array(a.held).map(parseAfkHeld), report: string(a.report) };
}
// The Command Center's items as the supervisor's stream sends them between
// snapshots: its questions, review items, runs, credential requests and
// actions, with the supervisor they are from and its revision.
export interface Items { instance: string; revision: number; questions: Question[]; reviews: Review[]; runs: Run[]; credentials: CredentialRequest[]; actions: Action[] }
function itemLists(v: Record<string, unknown>) {
  return {
    questions: array(v.questions).map((value) => {
      const q = object(value);
      return { id: string(q.id), identity: string(q.identity), text: string(q.text), options: strings(q.options), recommended: string(q.recommended), answer: string(q.answer), answer_kind: string(q.answer_kind), created_at: string(q.created_at), answer_id: string(q.answer_id), status: string(q.status), message: string(q.message), answered_option: string(q.answered_option), answered_by: string(q.answered_by), answered_at: string(q.answered_at), task: string(q.task), image_count: number(q.image_count), generation: string(q.generation), page: string(q.page), answered_in: string(q.answered_in) };
    }),
    reviews: array(v.reviews).map((value) => {
      const r = object(value);
      return { id: string(r.id), identity: string(r.identity), task: string(r.task), title: string(r.title), image_count: number(r.image_count), lavish: string(r.lavish), watched: string(r.lavish_page) !== "",
        document: r.document === undefined || r.document === null ? null : (({ name, size, kind, link }) => ({ name: string(name), size: number(size), kind: string(kind), link: string(link) }))(object(r.document)),
        state: string(r.state), answer: string(r.answer), answer_id: string(r.answer_id), delivered: r.delivered === undefined ? false : boolean(r.delivered), reason: string(r.reason),
        answered_by: string(r.answered_by), answered_in: string(r.answered_in), question: string(r.question), window_closed_at: string(r.window_closed_at),
        created_at: string(r.created_at), updated_at: string(r.updated_at) };
    }),
    runs: array(v.runs).map((value) => {
      const r = object(value);
      return { id: string(r.id), identity: string(r.identity), title: string(r.title), shell: string(r.shell), admin: r.admin === undefined ? false : boolean(r.admin),
        command: string(r.command), cwd: string(r.cwd), state: string(r.state), exit_code: r.exit_code === undefined || r.exit_code === null ? null : number(r.exit_code),
        output: string(r.output), reason: string(r.reason), created_at: string(r.created_at), expires_at: string(r.expires_at), ran_at: string(r.ran_at), finished_at: string(r.finished_at),
        connection_task: string(r.connection_task), connection_generation: string(r.connection_generation),
        credential_request: string(r.credential_request), credential_names: strings(r.credential_names) };
    }),
    credentials: array(v.credentials).map(parseCredentialRequest),
    actions: array(v.actions).map(parseAction),
  };
}
export function parseItems(value: unknown): Items {
  const v = object(value);
  return { instance: string(v.instance), revision: number(v.revision), ...itemLists(v) };
}
export function parseSnapshot(value: unknown): Snapshot {
  const v = object(value);
  return {
    subscriptions: array(v.subscriptions).flatMap((value): SubscriptionUsage[] => {
      const usage = object(value);
      if (usage.provider !== "claude" && usage.provider !== "codex") return [];
      const status = usage.status === "available" || usage.status === "stale" || usage.status === "auth_required" ? usage.status : "unavailable";
      const remaining = usage.percent_remaining;
      const isMeasured = status === "available" && usage.source === "oauth" && typeof remaining === "number" && Number.isFinite(remaining) && remaining >= 0 && remaining <= 100;
      return [{ provider: usage.provider, status: status === "available" && !isMeasured ? "unavailable" : status, percent_remaining: isMeasured ? remaining : null,
        read_at: string(usage.read_at), resets_at: string(usage.resets_at), source: usage.source === "oauth" || usage.source === "api" ? usage.source : "" }];
    }),
    afk: parseAfk(v.afk),
    instance: string(v.instance),
    revision: number(v.revision),
    started: string(v.started),
    at: string(v.at),
    reconciled: string(v.reconciled),
    healthy: boolean(v.healthy),
    example: v.example === undefined ? false : boolean(v.example),
    error: string(v.error),
    registration: v.registration === undefined ? "" : string(v.registration),
    cfo_terminal: v.cfo_terminal === undefined ? "" : string(v.cfo_terminal),
    cfo_harness: v.cfo_harness === undefined ? "" : string(v.cfo_harness),
    build: string(v.build),
    cfo_runs: v.cfo_runs === undefined || boolean(v.cfo_runs),
    cfo_starting: v.cfo_starting === undefined ? false : boolean(v.cfo_starting),
    inbox: number(v.inbox),
    memory: v.memory === undefined || v.memory === null ? null : (({ available, total, commit_available, commit_limit, paged_pool, nonpaged_pool, floor, next, holders }) => ({
      available: number(available), total: number(total), commit_available: number(commit_available), commit_limit: number(commit_limit),
      paged_pool: number(paged_pool), nonpaged_pool: number(nonpaged_pool), floor: number(floor), next: number(next),
      holders: array(holders).map((value) => { const h = object(value); return { name: string(h.name), commit: number(h.commit) }; }),
    }))(object(v.memory)),
    retired: strings(v.retired),
    issues: strings(v.issues),
    attention: strings(v.attention),
    activity: array(v.activity).map(value=>{const a=object(value);return {id:string(a.id),kind:string(a.kind),task_id:string(a.task_id),generation:string(a.generation),cfo_identity:string(a.cfo_identity),live:a.live===undefined?false:boolean(a.live),source:string(a.source),target:string(a.target),state:string(a.state),url:string(a.url),at:string(a.at),until:string(a.until)};}),
    ...itemLists(v),
    tasks: array(v.tasks).map((value) => {
      const t = object(value);
      return {
        lifecycle: t.lifecycle == null ? undefined : ((record) => ({ phase: string(record.phase), action: string(record.action), at: string(record.at), kept: strings(record.kept), stopped: strings(record.stopped), problems: strings(record.problems), handoff_saved: boolean(record.handoff_saved), validation_restarts: boolean(record.validation_restarts) }))(object(t.lifecycle)),
        teardown: strings(t.teardown), detail: string(t.detail), queue_revision: string(t.queue_revision), notes: strings(t.notes), action_error: string(t.action_error), branch: string(t.branch),
        pending_engine: t.pending_engine == null ? undefined : ((choice) => ({ harness: string(choice.harness), model: string(choice.model), effort: string(choice.effort), when: string(choice.when) }))(object(t.pending_engine)),
        switching: t.switching === undefined ? false : boolean(t.switching),
        runtime: parseRuntime(t.runtime),
        id: string(t.id),
        title: string(t.title),
        project: string(t.project),
        harness: string(t.harness),
        backend: string(t.backend),
        model: string(t.model),
        effort: string(t.effort),
        mode: string(t.mode),
        generation: string(t.generation),
        session: string(t.session),
        dependencies: strings(t.dependencies),
        activity: t.activity === undefined ? "" : string(t.activity),
        handoff: t.handoff === undefined ? false : boolean(t.handoff),
        last_report: string(t.last_report),
        retired_at: string(t.retired_at),
        report: t.report === undefined ? "" : string(t.report),
        waiting_on: t.waiting_on === undefined ? "" : string(t.waiting_on),
        gate_step: t.gate_step === undefined ? "" : string(t.gate_step),
        archived: t.archived === undefined ? false : boolean(t.archived),
        merged: t.merged === undefined ? false : boolean(t.merged),
        closed: t.closed === undefined ? false : boolean(t.closed),
        since: string(t.since),
        brief: t.brief === undefined ? false : boolean(t.brief),
        starting: t.starting === undefined ? false : boolean(t.starting),
        start_error: string(t.start_error),
        phase: string(t.phase),
        reason: string(t.reason),
        head: string(t.head),
        pr: string(t.pr),
        verified: boolean(t.verified),
        at: string(t.at),
      };
    }),
    sessions: array(v.sessions).map((value) => {
      const s = object(value);
      return {
        runtime: parseRuntime(s.runtime),
        id: string(s.id),
        native_id: string(s.native_id),
        harness: string(s.harness),
        role: string(s.role),
        task_id: string(s.task_id),
        generation: string(s.generation),
        parent: string(s.parent),
        reported_root: string(s.reported_root),
        relation: string(s.relation),
        model: string(s.model),
        agent_type: string(s.agent_type),
        phase: string(s.phase),
        turn_id: string(s.turn_id),
        host_id: string(s.host_id),
        last_event_id: string(s.last_event_id),
        updated_at: string(s.updated_at),
      };
    }),
    decisions: array(v.decisions).map((value) => {
      const d = object(value);
      return {
        seq: number(d.seq),
        time: string(d.time),
        key: string(d.key),
        kind: string(d.kind),
        detail: string(d.detail),
      };
    }),
  };
}
export function parseFiles(value: unknown): ChangedFile[] {
  return array(value).map((value) => {
    const v = object(value);
    return { path: string(v.path), status: string(v.status) };
  });
}
export function parseDiff(value: unknown): FileDiff {
  const v = object(value);
  return {
    path: string(v.path),
    patch: string(v.patch),
    code: string(v.code),
    head: string(v.head),
    revision: string(v.revision),
    fingerprint: string(v.fingerprint),
    binary: boolean(v.binary),
    code_omitted: boolean(v.code_omitted),
  };
}
export function parseSetup(value: unknown): Setup {
  const v = object(value);
  return {
    home: string(v.home),
    agent: string(v.agent),
    projects_root: string(v.projects_root),
    checkouts: strings(v.checkouts),
    problem: string(v.problem),
    agents: array(v.agents).map((value) => {
      const agent = object(value);
      return { id: string(agent.id), name: string(agent.name), recommended: boolean(agent.recommended), note: string(agent.note), installed: boolean(agent.installed), signed_in: boolean(agent.signed_in), reason: string(agent.reason) };
    }),
    cfo_runs: boolean(v.cfo_runs),
  };
}
export function parseHistory(value: unknown): Commit[] {
  return array(value).map((value) => {
    const v = object(value);
    return {
      sha: string(v.sha),
      short: string(v.short),
      subject: string(v.subject),
      author: string(v.author),
      date: string(v.date),
    };
  });
}
