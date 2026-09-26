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
  // waiting_on names what a waiting task waits on: another task's id,
  // overlord, ci or deploy; gate_step is the gate step of a task in review.
  waiting_on: string;
  gate_step: string;
  archived: boolean;
  merged: boolean;
}
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
export interface Snapshot {
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
  inbox: number;
  tasks: Task[];
  sessions: Session[];
  retired: string[];
  actions: Action[];
  decisions: Decision[];
  issues: string[];
  questions?: Question[];
  reviews?: Review[];
  runs?: Run[];
}
export interface Question {
  id: string; identity: string; text: string; options: string[]; recommended: string; answer: string; answer_kind: string; created_at: string; answer_id: string; status: string; message: string;
  // Who closed the question, with which choice and when ("" for a written answer).
  answered_option: string; answered_by: string; answered_at: string;
  // task names the goblin that asked; it is empty for the CFO's own question.
  task: string;
  // image_count is how many review images the goblin attached, one for each choice in order.
  image_count: number;
}
// A review item waits on the Overlord until he answers or clears it, or its
// reporter withdraws it: an image review, a Lavish page, or a wait on him.
export interface Review {
  id: string; identity: string; task: string; title: string; image_count: number; lavish: string;
  state: string; answer: string; answer_id: string; delivered: boolean; reason: string; created_at: string; updated_at: string;
}
// A command the CFO needs the Overlord to run (API8): the stored text is what
// runs; the board shows it verbatim and names the item when he presses Run.
export interface Run {
  id: string; identity: string; title: string; shell: string; admin: boolean; command: string; cwd: string;
  state: string; exit_code: number | null; output: string; reason: string;
  created_at: string; expires_at: string; ran_at: string; finished_at: string;
}
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
  fingerprint: string;
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
export function parseSnapshot(value: unknown): Snapshot {
  const v = object(value);
  return {
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
    inbox: number(v.inbox),
    retired: strings(v.retired),
    issues: strings(v.issues),
    activity: array(v.activity).map(value=>{const a=object(value);return {id:string(a.id),kind:string(a.kind),task_id:string(a.task_id),generation:string(a.generation),cfo_identity:string(a.cfo_identity),live:a.live===undefined?false:boolean(a.live),source:string(a.source),target:string(a.target),state:string(a.state),url:string(a.url),at:string(a.at),until:string(a.until)};}),
    questions: array(v.questions).map((value) => {
      const q = object(value);
      return { id: string(q.id), identity: string(q.identity), text: string(q.text), options: strings(q.options), recommended: string(q.recommended), answer: string(q.answer), answer_kind: string(q.answer_kind), created_at: string(q.created_at), answer_id: string(q.answer_id), status: string(q.status), message: string(q.message), answered_option: string(q.answered_option), answered_by: string(q.answered_by), answered_at: string(q.answered_at), task: string(q.task), image_count: number(q.image_count) };
    }),
    reviews: array(v.reviews).map((value) => {
      const r = object(value);
      return { id: string(r.id), identity: string(r.identity), task: string(r.task), title: string(r.title), image_count: number(r.image_count), lavish: string(r.lavish),
        state: string(r.state), answer: string(r.answer), answer_id: string(r.answer_id), delivered: r.delivered === undefined ? false : boolean(r.delivered), reason: string(r.reason),
        created_at: string(r.created_at), updated_at: string(r.updated_at) };
    }),
    runs: array(v.runs).map((value) => {
      const r = object(value);
      return { id: string(r.id), identity: string(r.identity), title: string(r.title), shell: string(r.shell), admin: r.admin === undefined ? false : boolean(r.admin),
        command: string(r.command), cwd: string(r.cwd), state: string(r.state), exit_code: r.exit_code === undefined || r.exit_code === null ? null : number(r.exit_code),
        output: string(r.output), reason: string(r.reason), created_at: string(r.created_at), expires_at: string(r.expires_at), ran_at: string(r.ran_at), finished_at: string(r.finished_at) };
    }),
    tasks: array(v.tasks).map((value) => {
      const t = object(value);
      return {
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
        waiting_on: t.waiting_on === undefined ? "" : string(t.waiting_on),
        gate_step: t.gate_step === undefined ? "" : string(t.gate_step),
        archived: t.archived === undefined ? false : boolean(t.archived),
        merged: t.merged === undefined ? false : boolean(t.merged),
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
        last_event_id: string(s.last_event_id),
        updated_at: string(s.updated_at),
      };
    }),
    actions: array(v.actions).map(parseAction),
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
