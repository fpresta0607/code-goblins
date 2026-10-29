// HarnessUpdate is an update of the harness a terminal runs that was
// installed after it started: line is the harness's own words for it on its
// screen and installed when its program was installed since, whichever
// showed it.
export interface HarnessUpdate { harness: string; line: string; installed: string }
// CFOUpdate is the CFO's, with its Update button's state: pending once he
// pressed it, until the CFO's turn ends; updating while the restart runs;
// problem why the last press did not restart it.
export interface CFOUpdate extends HarnessUpdate { pending: boolean; updating: boolean; problem: string }
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
  action_error: string;
  pending_engine?: { harness: string; model: string; effort: string; when: string };
  // harness_update is an update of the harness the goblin runs, installed
  // since it started, for the Update button on its card.
  harness_update?: HarnessUpdate;
  switching?: boolean;
  branch: string;
  // parent is the goblin a helper works for, empty for every other task.
  parent: string;
  runtime?: RuntimeEvidence;
  id: string;
  title: string;
  // goblin_name and goblin_title are the fun name and title the goblin was
  // given at spawn, empty before it has one.
  goblin_name: string;
  goblin_title: string;
  project: string;
  harness: string;
  // backend is the terminal the task runs in: native, herdr, or empty before it starts.
  backend: string;
  model: string;
  effort: string;
  mode: string;
  generation: string;
  session: string;
  // waits is what a queued task's row still waits for before the scheduler
  // starts it.
  waits: Wait[];
  activity: string;
  handoff?: boolean;
  last_report?: string;
  // reported_at is when the goblin wrote its latest report.
  reported_at?: string;
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
  // history_unavailable says GitHub no longer holds a finished task's pull
  // request, as when a repository was recreated under the same name: why what
  // became of it cannot be read, while its outcome and link stay as reported.
  history_unavailable: string;
  // since is when a live task's session started, or when queued work's brief
  // was written; empty when neither is known.
  since: string;
  // brief says queued work has its brief, which Start needs; starting that
  // its Start runs cfo spawn now or waits its turn, start_error why its last
  // Start failed, and finished why it never starts again by itself: what
  // says it already finished. asked says a Start or Resume the Overlord
  // clicked waits its turn behind another start or resume, or for memory or
  // disk. overlord_start says the start under way is the Overlord's own,
  // which puts the task at the top of In progress, where one the supervisor
  // makes by itself joins the bottom.
  brief: boolean;
  starting: boolean;
  overlord_start: boolean;
  asked: boolean;
  start_error: string;
  finished: string;
  // priority is a queued task's backlog priority: production-defect starts
  // it ahead of every other start and resume.
  priority: string;
  // progress is a live goblin's last real progress: a commit, a push, a gate
  // step or a new status report, and when.
  progress?: WorkProgress;
  // ticket is the GitHub issue kept for a task in a repository other people
  // work in, and overlaps is their open work in the same area as a live
  // goblin's branch.
  ticket?: Ticket;
  overlaps: Overlap[];
  // hosted_checks is what its pull request's hosted checks said at the last
  // CI poll, local_checks its change's newest cfo gate test run, and
  // deployment how the deploy of its merge stands, apart.
  hosted_checks?: HostedChecks;
  local_checks?: LocalChecks;
  // tree is what a live goblin has running under it, absent for one that
  // runs nothing the supervisor could read.
  tree?: FleetTree;
  deployment?: Deployment;
  // comeback is where a live goblin the last restart ended is in coming
  // back: waiting for its turn, or stopped with the reason.
  comeback?: ComebackEntry;
}
// FleetTree is a goblin and what runs under it as the supervisor last read
// it: memory is its harness and everything under it, own_memory the harness
// alone, both private bytes; unread names evidence that could not be read.
export interface FleetTree {
  task_id: string; generation: string; harness: string; memory: number; own_memory: number; conversation_at: string;
  children: TreeNode[]; unread: string[]; source_updated_at: string; fetched_at: string;
}
// TreeNode is one child of a goblin: a sub-agent, a background shell or
// monitor, a job of processes (whose group says what it does: dev-server,
// test, build, browser or other) or its gate run. state is working, waiting,
// done, failed or silent; memory is its processes' private bytes.
// task is a named helper goblin's task, which its label leaves to its tip.
// detached is set on a job whose processes reach the goblin's harness through
// no parent any more, such as a server left by a command that returned.
export interface TreeNode {
  model?: string;
  id: string; kind: string; group: string; parent: string; label: string; detail: string; task: string; state: string;
  started: string; last_activity: string; finished: string; last_line: string; memory: number;
  source_updated_at: string; fetched_at: string; detached?: boolean;
}
function parseTree(value: unknown): FleetTree {
  const t = object(value);
  return {
    task_id: string(t.task_id), generation: string(t.generation), harness: string(t.harness), memory: number(t.memory), own_memory: number(t.own_memory),
    conversation_at: string(t.conversation_at), unread: strings(t.unread), source_updated_at: string(t.source_updated_at), fetched_at: string(t.fetched_at),
    children: array(t.children).map((child) => {
      const c = object(child);
      return { id: string(c.id), kind: string(c.kind), group: string(c.group), parent: string(c.parent), label: string(c.label), detail: string(c.detail), task: string(c.task), state: string(c.state), model: string(c.model),
        started: string(c.started), last_activity: string(c.last_activity), finished: string(c.finished), last_line: string(c.last_line), memory: number(c.memory),
        source_updated_at: string(c.source_updated_at), fetched_at: string(c.fetched_at), ...(c.detached === true ? { detached: true } : {}) };
    }),
  };
}
export interface WorkProgress { at: string; source: string }
// ComebackEntry is the CFO or one goblin the supervisor brings back after a
// restart or sign-out: waiting for its turn, back, or stopped with the reason.
export interface ComebackEntry { id: string; state: "waiting" | "back" | "stopped" | ""; reason: string }
// Comeback is what the supervisor brings back after the last restart or
// sign-out: the CFO first, then each goblin that was working.
export interface Comeback { signed_in: string; cfo?: ComebackEntry; goblins: ComebackEntry[] }
// StartAtLoginView is whether Windows starts this home at login, or why it
// cannot be started at login.
export interface StartAtLoginView { on: boolean; unavailable: string }
// DevDriveView is the home's Dev Drive: its state and the line saying it, the
// short note the Workspace panel shows under its row, the sentence explaining
// what one is, the person's answer to the offer, the step whose Command
// Center item waits, and the button to press, if any.
export interface DevDriveView { state: string; line: string; note: string; explain: string; choice: string; waiting: string; action: "" | "set-up" | "try-again" | "attach" }
// HostedChecks is a pull request's hosted checks at its head: state is
// pending while one runs and none has failed, failed as soon as one has,
// cancelled when they ended with one cancelled, and passed when all passed;
// failed names the checks that failed or were cancelled and link is the first
// one's page, or while they run the first running one's; approved says a
// reviewer approved the pull request; rate_limited names the deploys the host
// rate limited, which never built the head and checks leaves out.
export interface HostedChecks { head: string; state: string; checks: number; failed: string[]; rate_limited: string[]; link: string; approved: boolean }
// LocalChecks is the newest cfo gate test run of a task's change: the commit,
// the level it ran and the one required, passed or failed, how long it took
// and waited for its turn, and the packages or commands that failed.
export interface LocalChecks { commit: string; level: string; required_level: string; status: string; duration_seconds: number; queue_seconds: number; failed: string[]; at: string }
// Deployment is how the deploy of a merged pull request stands, from the
// default branch's deploy workflows for its merge commit: state is deploying,
// deployed, failed or cancelled, workflows names them, and link is the page
// of the run that failed or was cancelled, else of the newest one.
export interface Deployment { commit: string; state: string; workflows: string[]; link: string; at: string }
// MergeTrain is a merge train: green pull requests merged onto base on a
// branch of its own, which its pull request lets CI test together once.
// state is testing, landed, stopped or failed, runs counts its CI runs and
// history keeps each, and note says what happened last. earlier are the
// trains before it that landed nothing and whose pull requests it took on,
// oldest first, so the board shows each batch once.
export interface MergeTrain { id: string; repository: string; base: string; pr: string; state: string; runs: number; started: string; finished: string; note: string; cars: TrainCar[]; history: TrainRun[]; earlier: MergeTrain[] }
// TrainRun is one CI run of a train: its number among the train's runs, the
// pull requests it tested by number, its base and head commits, when it was
// pushed, how it ended (landed, failed, moved, changed, repushed or stopped,
// empty while CI tests it) and a red run's first failed check. pr is the
// train's pull request the run was tested on: a run that lands merges its
// pull request, so a train that was halved has one for each half, and a run
// an older build pushed names none. failed_once are the checks that were red
// on its first try, when its failed checks ran again, which a run's do once
// before the train acts on it.
export interface TrainRun { number: number; riders: number[]; base: string; head: string; pushed: string; result: string; link: string; pr: string; failed_once: TrainCheck[] }
// TrainCheck is a check that ended red: its name and its page.
export interface TrainCheck { name: string; link: string }
// TrainCar is one pull request on a train: state is riding, waiting,
// landed, culprit, conflict or returned, and note says why.
// goblin and goblin_title are the name and title of the goblin that reported
// it done, and head the commit of it that rode.
export interface TrainCar { number: number; url: string; title: string; task: string; goblin: string; goblin_title: string; head: string; state: string; note: string }
// Ticket is a task's issue: its number, its link and where it stands, one
// of queued, in progress, pr open, paused, blocked, merged or closed.
export interface Ticket { number: number; url: string; state: string }
// Person is someone on GitHub: a login, or only a git name for a commit that
// links to no account, and an avatar.
export interface Person { login: string; name: string; avatar_url: string }
// Overlap is one piece of a teammate's open work, such as "PR #1446", that
// changes the files a goblin's branch changes or names its area.
export interface Overlap extends Person { what: string; url: string }
// ProjectPeople is a project on the board whose origin is on GitHub: its
// repository, and the people other than the Overlord, bots aside, who
// committed to its default branch or opened a pull request in it in the last
// 30 days, most recent first, none while only he works there.
export interface ProjectPeople { name: string; repository: string; contributors: Person[] }
export interface LifecycleStatus {
  phase: string; action: string; at: string; kept: string[]; stopped: string[]; problems: string[];
  handoff_saved: boolean; validation_restarts: boolean;
  // with_parent is a helper's pause or stop its parent's made: paused, it
  // resumes once its parent runs again.
  with_parent?: boolean;
  // pause is what a paused task waits for: memory, allowance, overlord,
  // dependency, question, ci or deploy, with until naming the reset time,
  // task:, pr:, date:, question id or awaited run; absent on an older pause.
  pause?: PauseCondition;
  // kept_messages are his messages, as he wrote them, that a paused
  // goblin's resume will carry, each of which he can delete before it does.
  // It is absent where none waits for a resume.
  kept_messages?: string[];
}
export interface PauseCondition { reason: string; until: string; at: string }
// Wait is one thing a queued row waits for: kind is time, memory, pr or task,
// target its words as the row wrote them, until the time and bytes the free
// memory it waits for, and problem why the scheduler cannot read it.
export interface Wait { kind: string; target: string; until: string; bytes: number; problem: string }
// Memory is the machine's free memory and free commit (memory plus page file)
// in bytes beside the fleet's floor, under which nothing starts, and the mark
// at which the next queued task starts; with the kernel's pools, the line past
// which the PC is better restarted (0 when the snapshot names none) and the
// pool tags that grew most since the sign-in began, and, while commit is the
// tighter, the apps holding the most of it.
export interface Memory {
  available: number; total: number; commit_available: number; commit_limit: number;
  paged_pool: number; nonpaged_pool: number; pool_line: number; pool_growth: PoolGrowth[];
  floor: number; next: number; holders: CommitHolder[];
}
// PoolGrowth is a kernel pool tag that grew in one pool, paged or nonpaged,
// since the sign-in began: how much, and how much it holds now.
export interface PoolGrowth { tag: string; pool: string; grown: number; holds: number }
// PCRestart is the restart of the PC his Restart PC began: stopping while the
// fleet stops, restarting once Windows was asked to, and stopped when it
// stopped short, with the problem.
export interface PCRestart { state: "stopping" | "restarting" | "stopped"; problem: string; at: string }
// CIDuration is how long one finished CI run or deploy took in a repository.
export interface CIDuration { repository: string; kind: string; seconds: number }
// CommitHolder is one app's commit: its first process and every process it
// started.
export interface CommitHolder { name: string; commit: number }
// Disk is the free space of the home's drive in bytes beside the floor under
// which no goblin or gate test run starts and the lower mark at which the CFO
// is woken.
export interface Disk { drive: string; free: number; total: number; floor: number; wake: number }
// Processors is the machine's processor cores by kind and the shares of its
// performance and its efficiency cores that sat idle, from 0 to 1, beside the
// share of the performance cores that must be free for the supervisor to
// start a goblin by itself, and the apps that used the most processor. A
// machine with cores of one kind has performance cores only.
export interface Processors { performance_cores: number; efficiency_cores: number; free: number; efficiency_free: number; next: number; busiest: string[] }
// GpuAdapter is one graphics adapter: the share of its busiest engine in use,
// from 0 to 1, and the app using most of it, empty when none is.
export interface GpuAdapter { name: string; busy: number; busiest: string }
// Gpu is the machine's graphics adapters, at least one: a machine with none
// has no gpu in its snapshot.
export interface Gpu { adapters: GpuAdapter[] }
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
  // can_send_now is whether he may ask for a queued message to be typed now,
  // and send_now_why says why not when it is off.
  can_send_now: boolean;
  send_now_why: string;
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
  // watch is the line a goblin writes when it asks the Overlord to watch this
  // presentation; only such a presentation reaches his Command Center.
  watch?:string;
  id:string; kind:string; task_id:string; generation:string; source:string; target:string; state:string; url:string; at:string; until:string;
}
export interface SubscriptionUsage {
  provider: "claude" | "codex";
  status: "available" | "unavailable" | "stale" | "auth_required";
  percent_remaining: number | null;
  read_at: string;
  resets_at: string;
  source: "oauth" | "api" | "";
  // The weekly floor the supervisor last paused by, from config/fleet.json,
  // or null while it could not be read.
  floor_percent: number | null;
}
// KeySpend is the spend an API key reports at its provider: the dollars a
// provider that bills by the key, as OpenRouter does for Pi, has counted in
// the window the key's own limit resets over. The dollars are the key's own
// credit usage; the external BYOK spend is reported beside them and never
// added to them.
export interface KeySpend {
  provider: "openrouter";
  status: "available" | "missing" | "auth_required" | "rate_limited" | "unavailable" | "stale" | "unattributed";
  spent_usd: number | null;
  limit_usd: number | null;
  remaining_usd: number | null;
  // period is the window the limit resets over, "" for a window this build
  // cannot count.
  period: "daily" | "weekly" | "monthly" | "lifetime" | "";
  resets_at: string;
  read_at: string;
  byok_usd: number | null;
  includes_byok: boolean;
  // blocking is a budget layer that is not this key's own and stops requests
  // first, whose spend is that scope's and not this key's.
  blocking: { scope: string; spent_usd: number; limit_usd: number; period: string } | null;
  // profile names the Pi profile the key came from: default for Pi's own
  // folder, else that folder's own name.
  profile: string;
  // detail says in a few words why the status is what it is.
  detail: string;
}
export interface Snapshot {
  subscriptions?: SubscriptionUsage[];
  key_spend?: KeySpend[];
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
  // When the host of that terminal started; a new one, as after a restart,
  // is viewed afresh.
  cfo_terminal_since: string;
  // The harness the registered CFO runs, such as claude; empty while none is registered.
  cfo_harness: string;
  cfo_engine?: { harness: string; model: string; effort: string; pending: boolean; problem: string; waiting: string };
  // The registered CFO's session, as sessions key it; empty while no CFO is
  // registered or its registration names no session.
  cfo_session: string;
  // The conversation the CFO could not resume when it last came back, and how
  // to resume it by hand; empty when it came back on its own.
  cfo_conversation_left: string;
  // cfo_update is an update of the harness the CFO runs, installed since it
  // started, for the Update button on its header; null while none waits.
  cfo_update: CFOUpdate | null;
  // comeback is what the supervisor brings back after the last restart or
  // sign-out; absent when it brings nothing back.
  comeback?: Comeback;
  // start_at_login is whether Windows starts this home at login; absent on a
  // board that cannot change it.
  start_at_login?: StartAtLoginView;
  // dev_drive is the home's Dev Drive; absent on a board without the setting.
  dev_drive?: DevDriveView;
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
  // cfo_closed says the home's CFO registered and has since ended, with no
  // terminal up for a new one: the board says so and offers Reopen, and
  // shows no first-run page.
  cfo_closed: boolean;
  cfo_configured: boolean;
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
  browser_signins?: BrowserSignIn[];
  // memory is absent on a board that cannot start goblins or read it.
  memory: Memory | null;
  // pc_restart is absent until the Overlord's Restart PC began one.
  pc_restart: PCRestart | null;
  ci_durations: CIDuration[];
  // disk is absent on a board that cannot read it.
  disk: Disk | null;
  // processors and gpu are each absent on a board that cannot read them.
  processors: Processors | null;
  gpu: Gpu | null;
  afk: Afk;
  // projects names each live GitHub project's repository and the people
  // active there, by the project name tasks carry.
  projects: ProjectPeople[];
  // release is a newer published release, or any where this board was built
  // from a clone; null when there is none or the check is off.
  release: ReleaseView | null;
  // merge_trains are the trains running and those finished in the last
  // hours, newest first.
  merge_trains?: MergeTrain[];
}
// AfkHeld is an item that waited on the Overlord in the Command Center while
// AFK mode was on: its key there, its goblin (empty for the CFO's own), what
// it asks, whether it still waits on him and what became of it, what its
// goblin reported meanwhile, the choice its asker recommends, empty for none,
// and, in a report kept before nothing was left for him, what became of a
// line the CFO left for him and settled, which was never asked.
export interface AfkHeld {
  item: string; task: string; what: string; at: string; waiting: boolean; now: string; meanwhile: string; recommendation: string; settled: string;
}
// Afk is AFK mode, the Overlord's switch for running the fleet while he is
// away: on, off, or unreadable when the supervisor cannot read the switch.
// While on it says since when and from where, how many decisions the CFO
// logged and what is held for him; asked holds his words when the CFO turned
// it on at his ask, and is empty when he turned it on himself. While off,
// page is the board-local report of the last stretch that ended.
export interface Afk {
  state: "off" | "on" | "unreadable";
  since: string; from: string; asked: string; decided: number; held: AfkHeld[];
  page: string;
}
export interface Question {
  id: string; identity: string; text: string; options: string[]; recommended: string; answer: string; answer_kind: string; created_at: string; answer_id: string; status: string; message: string;
  // Who closed the question, with which choice and when ("" for a written answer).
  answered_option: string; answered_by: string; answered_at: string;
  // task names the goblin that asked; it is empty for the CFO's own question.
  task: string;
  // for_task names the goblin whose decision the CFO's own question puts to
  // him (cfo question --for); it is empty for a goblin's question.
  for_task: string;
  // answered_in says where an answer given outside its card came from, such
  // as "chat".
  answered_in: string;
  // image_count is how many review images the goblin attached, one for each choice in order.
  image_count: number;
  // generation is the asking goblin's session; empty for the CFO's question.
  generation: string;
  // answered_away marks the CFO's answer given while AFK mode was on;
  // change_id is his board action changing the CFO's answer to his own, and
  // replaced_answer the CFO's choice it replaced once the goblin has his.
  answered_away: boolean; change_id: string; replaced_answer: string;
  // mail is a message the question gives him to send himself, null for none.
  mail: Mail | null;
}
// A message a card gives the Overlord to send himself, such as an email:
// where it goes, an email address or an https page ("" when the card does not
// say), its subject ("" for none) and its body, which his card copies whole.
export interface Mail { to: string; subject: string; body: string }
// A review item waits on the Overlord until he answers or clears it, or its
// reporter withdraws it: an image review, a Lavish page, or a wait on him.
export interface Review {
  id: string; identity: string; task: string; title: string; image_count: number; lavish: string;
  // link is the web link a goblin's wait gave as the place to go, the only
  // one its card opens.
  link: string;
  // follows is the ID of the open decision a goblin's wait on him followed
  // when it was raised, the CFO's question for its task or the goblin's own
  // with choices: the board shows only that question while it is open, and
  // its answer closes the wait.
  follows: string;
  // watched: the supervisor polls the item's Lavish page, so his answer or
  // end of the review there closes the item.
  watched: boolean;
  // answered_by and answered_in say who answered the item outside the
  // Command Center and where, such as "overlord" on its "page".
  answered_by: string; answered_in: string;
  // window_closed_at is when the page's review window last closed, if it has.
  window_closed_at: string;
  // revising_since is when he sent a revision from the page without ending
  // its review: the item waits on its goblin's next version, not on him.
  revising_since: string;
  // document is a file cfo deliver handed him, which its Scrawl page shows,
  // or null for any other item.
  document: ReviewDocument | null;
  state: string; answer: string; answer_id: string; delivered: boolean; reason: string; created_at: string; updated_at: string;
}
// A file delivered with cfo deliver: kind is set when its page shows it, and
// download says its page offers it to download.
export interface ReviewDocument { name: string; size: number; kind: string; download: boolean }
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
  // remote_fix is the row of the Remote access setting whose mending command
  // the Overlord ran from it, which draws its terminal.
  remote_fix: string;
  // task is the goblin whose own command this is, empty for the CFO's;
  // terminal is whether the item's terminal runs, which its card draws.
  task: string; terminal: boolean;
  // update is the release an Update Code Goblins item installs, which the
  // board shows as its own card; null on every other item.
  update: ReleaseOffer | null;
  // cfo_unwoken is the oldest wake the supervisor could not type into the CFO,
  // on the item that offers the Overlord Restart the CFO; 0 on every other.
  cfo_unwoken: number;
  // cfo_stopped marks the item that says the supervisor stopped bringing back
  // a CFO that kept ending, or could not bring it back, and offers him Reopen
  // the CFO.
  cfo_stopped: boolean;
  // mail is a message the item gives him to send himself, null for none.
  mail: Mail | null;
}
// A newer release of Code Goblins, as the Update item offers it: the version
// this board runs and the one it installs, when that was published, a few
// lines of what is new, its notes' page, whether its notes say it is signed
// and by whom, and the SHA-256 they list for cfo.exe.
export interface ReleaseOffer {
  from: string; to: string; page: string; published: string; notes: string[];
  // signing is "signed", "unsigned", or empty when the notes do not say.
  signing: string; publisher: string; sum: string;
}
// The newest published release where it is newer than this board's build,
// or any release where the board was built from a clone, which updates from
// the clone instead.
export interface ReleaseView { installed: string; tag: string; page: string; published: string; source: boolean }
export interface BrowserSignIn {
  id: string; generation: string; login: string; provider: string; account: string; project: string; task: string;
  state: "ready" | "opening" | "open" | "signed_in" | "expired";
  reason: string; created_at: string; expires_at: string; closed_at: string;
}
// The card for a step only the Overlord can take: the page to open, its
// steps, and the inputs he enters. Each secret he pastes goes into the
// project's credential scope, and each plain value back to the goblin. It
// never carries a value.
export interface CredentialRequest {
  id: string; generation: string; identity: string;
  task_generation: string; grant_service: string;
  // by is "cfo" or "goblin"; task is the goblin that needs the values.
  by: string; task: string;
  project: string;
  // env_file is a local env file at the root of the project's checkout where each
  // saved value is also set; written are the names set there.
  env_file: string;
  // names are the secrets, plain the values that go back to the goblin, and
  // labels say what each input is in a few words.
  names: string[]; plain: string[]; labels: Record<string, string>; why: string; link: string;
  // steps say how to get the values, in order, for the card's header.
  steps: string[];
  // window says the step happens in the goblin's own browser, which the
  // card's button brings to him; window_state is "" while the button is
  // offered, "requested" once pressed and "open" once the window shows.
  window: boolean; window_state: string;
  // received are the plain values that reached the goblin.
  received: string[];
  // existing are the names the scope already held when the board last looked.
  existing: string[];
  hints: CredentialHint[];
  // services are the auth.json services that read each name.
  services: Record<string, string[]>;
  // state is open, saved once every input is in, done, dismissed, withdrawn or expired; typed are the saved names typed in its terminal, and fetched those a fetch read from its key page.
  state: string; saved: string[]; replaced: string[]; typed: string[]; fetched: string[]; written: string[]; reason: string;
  created_at: string; expires_at: string; closed_at: string;
  // fetch is what the card offers to fetch from a reviewed key page, null for none.
  fetch: CredentialFetch | null;
  // mail is a message the card gives him to send himself, null for none.
  mail: Mail | null;
}
// What a request's card offers to fetch: the names a reviewed key page
// reads, by provider and the login a fetch reads with, a fetch running now
// (state "fetching"), and why the last one stopped.
export interface CredentialFetch { provider: string; login: string; names: string[]; state: string; reason: string }
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
  // What the agent's own status command says, in the environment the CFO's
  // terminal starts with; unknown is an answer it did not give.
  sign_in: SignIn;
  reason: string;
}
export type SignIn = "signed_in" | "signed_out" | "unknown";
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
export function number(value: unknown): number {
  if (value == null) return 0;
  if (typeof value !== "number" || !Number.isFinite(value))
    throw new Error("Invalid response number");
  return value;
}
function pcRestartState(value: unknown): PCRestart["state"] {
  const state = string(value);
  if (state !== "stopping" && state !== "restarting" && state !== "stopped") throw new Error("Invalid restart state");
  return state;
}
export function boolean(value: unknown): boolean {
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
    can_send_now: v.can_send_now === true,
    send_now_why: string(v.send_now_why),
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
function parseHarnessUpdate(update: Record<string, unknown>): HarnessUpdate {
  return { harness: string(update.harness), line: update.line === undefined ? "" : string(update.line), installed: update.installed === undefined ? "" : string(update.installed) };
}
function parseMail(value: unknown): Mail | null {
  if (value == null) return null;
  const m = object(value);
  return { to: string(m.to), subject: string(m.subject), body: string(m.body) };
}
function parseCredentialRequest(value: unknown): CredentialRequest {
  const c = object(value);
  const services: Record<string, string[]> = {};
  for (const [name, users] of Object.entries(c.services == null ? {} : object(c.services))) services[name] = strings(users);
  const labels: Record<string, string> = {};
  for (const [name, label] of Object.entries(c.labels == null ? {} : object(c.labels))) labels[name] = string(label);
  return {
    id: string(c.id), generation: string(c.generation), identity: string(c.identity), by: string(c.by), task: string(c.task),
    task_generation: string(c.task_generation), grant_service: string(c.grant_service),
    project: string(c.project), env_file: string(c.env_file), names: strings(c.names), plain: strings(c.plain), labels, why: string(c.why), link: string(c.link), steps: strings(c.steps),
    window: c.window === undefined ? false : boolean(c.window), window_state: string(c.window_state), received: strings(c.received),
    existing: strings(c.existing), services,
    hints: array(c.hints).map((hint) => {
      const h = object(hint);
      return { name: string(h.name), prefixes: strings(h.prefixes), warn: array(h.warn).map((warning) => { const w = object(warning); return { prefix: string(w.prefix), say: string(w.say) }; }) };
    }),
    state: string(c.state), saved: strings(c.saved), replaced: strings(c.replaced), typed: strings(c.typed), fetched: strings(c.fetched), written: strings(c.written), reason: string(c.reason),
    created_at: string(c.created_at), expires_at: string(c.expires_at), closed_at: string(c.closed_at),
    fetch: c.fetch == null ? null : (() => { const f = object(c.fetch); return { provider: string(f.provider), login: string(f.login), names: strings(f.names), state: string(f.state), reason: string(f.reason) }; })(),
    mail: parseMail(c.mail),
  };
}
function parseAfkHeld(value: unknown): AfkHeld {
  const h = object(value);
  return { item: string(h.item), task: string(h.task), what: string(h.what), at: string(h.at), waiting: h.waiting === undefined ? false : boolean(h.waiting), now: string(h.now), meanwhile: string(h.meanwhile), recommendation: string(h.recommendation), settled: string(h.settled) };
}
// A supervisor from before AFK mode reached the board sends none, which is off.
function parseAfk(value: unknown): Afk {
  const a = value == null ? {} : object(value);
  const state = a.state == null ? "off" : string(a.state);
  if (state !== "off" && state !== "on" && state !== "unreadable") throw new Error("Invalid AFK state");
  return { state, since: string(a.since), from: string(a.from), asked: string(a.asked), decided: number(a.decided), held: array(a.held).map(parseAfkHeld), page: string(a.page) };
}
function itemLists(v: Record<string, unknown>) {
  return {
    questions: array(v.questions).map((value) => {
      const q = object(value);
      return { id: string(q.id), identity: string(q.identity), text: string(q.text), options: strings(q.options), recommended: string(q.recommended), answer: string(q.answer), answer_kind: string(q.answer_kind), created_at: string(q.created_at), answer_id: string(q.answer_id), status: string(q.status), message: string(q.message), answered_option: string(q.answered_option), answered_by: string(q.answered_by), answered_at: string(q.answered_at), task: string(q.task), image_count: number(q.image_count), generation: string(q.generation), answered_in: string(q.answered_in),
        answered_away: q.answered_away === undefined ? false : boolean(q.answered_away), change_id: string(q.change_id), replaced_answer: string(q.replaced_answer), for_task: string(q.for_task), mail: parseMail(q.mail) };
    }),
    reviews: array(v.reviews).map((value) => {
      const r = object(value);
      return { id: string(r.id), identity: string(r.identity), task: string(r.task), title: string(r.title), image_count: number(r.image_count), lavish: string(r.lavish), link: string(r.link), follows: string(r.follows), watched: string(r.lavish_page) !== "",
        document: r.document === undefined || r.document === null ? null : (({ name, size, kind, download }) => ({ name: string(name), size: number(size), kind: string(kind), download: download === true }))(object(r.document)),
        state: string(r.state), answer: string(r.answer), answer_id: string(r.answer_id), delivered: r.delivered === undefined ? false : boolean(r.delivered), reason: string(r.reason),
        answered_by: string(r.answered_by), answered_in: string(r.answered_in), window_closed_at: string(r.window_closed_at), revising_since: string(r.revising_since),
        created_at: string(r.created_at), updated_at: string(r.updated_at) };
    }),
    runs: array(v.runs).map((value) => {
      const r = object(value);
      return { id: string(r.id), identity: string(r.identity), title: string(r.title), shell: string(r.shell), admin: r.admin === undefined ? false : boolean(r.admin),
        command: string(r.command), cwd: string(r.cwd), state: string(r.state), exit_code: r.exit_code === undefined || r.exit_code === null ? null : number(r.exit_code),
        output: string(r.output), reason: string(r.reason), created_at: string(r.created_at), expires_at: string(r.expires_at), ran_at: string(r.ran_at), finished_at: string(r.finished_at),
        connection_task: string(r.connection_task), connection_generation: string(r.connection_generation),
        credential_request: string(r.credential_request), credential_names: strings(r.credential_names), remote_fix: string(r.remote_fix),
        task: string(r.task), terminal: r.terminal === undefined ? false : boolean(r.terminal), cfo_unwoken: r.cfo_unwoken === undefined ? 0 : number(r.cfo_unwoken), cfo_stopped: r.cfo_stopped === undefined ? false : boolean(r.cfo_stopped),
        update: r.update == null ? null : (({ from, to, page, published, notes, signing, publisher, sum }) => ({ from: string(from), to: string(to), page: string(page), published: string(published),
          notes: strings(notes), signing: string(signing), publisher: string(publisher), sum: string(sum) }))(object(r.update)), mail: parseMail(r.mail) };
    }),
    credentials: array(v.credentials).map(parseCredentialRequest),
    browser_signins: array(v.browser_signins).map((value): BrowserSignIn => {
      const signin = object(value);
      const state = string(signin.state);
      if (state !== "ready" && state !== "opening" && state !== "open" && state !== "signed_in" && state !== "expired") throw new Error("Invalid browser sign-in state");
      return { id: string(signin.id), generation: string(signin.generation), login: string(signin.login), provider: string(signin.provider), account: string(signin.account),
        project: string(signin.project), task: string(signin.task), state, reason: string(signin.reason), created_at: string(signin.created_at), expires_at: string(signin.expires_at), closed_at: string(signin.closed_at) };
    }),
    actions: array(v.actions).map(parseAction),
  };
}
function parsePerson(p: Record<string, unknown>): Person {
  return { login: string(p.login), name: string(p.name), avatar_url: string(p.avatar_url) };
}
function parseMergeTrain(value: unknown): MergeTrain {
  const t = object(value);
  return {
    id: string(t.id), repository: string(t.repository), base: string(t.base), pr: string(t.pr), state: string(t.state), runs: number(t.runs),
    started: string(t.started), finished: string(t.finished), note: string(t.note),
    cars: array(t.cars).map((car) => { const c = object(car); return { number: number(c.number), url: string(c.url), title: string(c.title), task: string(c.task), goblin: string(c.goblin), goblin_title: string(c.goblin_title), head: string(c.head), state: string(c.state), note: string(c.note) }; }),
    history: array(t.history).map((run) => { const r = object(run); return { number: number(r.number), riders: array(r.riders).map(number), base: string(r.base), head: string(r.head), pushed: string(r.pushed), result: string(r.result), link: string(r.link), pr: string(r.pr), failed_once: array(r.failed_once).map((check) => { const c = object(check); return { name: string(c.name), link: string(c.link) }; }) }; }),
    earlier: array(t.earlier).map(parseMergeTrain),
  };
}
function parseComebackEntry(value: unknown): ComebackEntry {
  const entry = object(value);
  const state = string(entry.state);
  return { id: string(entry.id), state: state === "waiting" || state === "back" || state === "stopped" ? state : "", reason: string(entry.reason) };
}
function parseDevDrive(drive: Record<string, unknown>): DevDriveView {
  const optional = (value: unknown) => value === undefined ? "" : string(value);
  const action = optional(drive.action);
  return {
    state: string(drive.state), line: string(drive.line), note: string(drive.note), explain: string(drive.explain), choice: optional(drive.choice), waiting: optional(drive.waiting),
    action: action === "set-up" || action === "try-again" || action === "attach" ? action : "",
  };
}
// parseKeySpend reads one key's spend. Money that is not a measured dollar
// figure stays unknown rather than becoming a zero the dial would show, and
// a provider the board has no dial for is no entry at all.
export function parseKeySpend(value: unknown): KeySpend | null {
  const usage = object(value);
  if (usage.provider !== "openrouter") return null;
  const dollars = (held: unknown) => typeof held === "number" && Number.isFinite(held) && held >= 0 ? held : null;
  const period = usage.period === "daily" || usage.period === "weekly" || usage.period === "monthly" || usage.period === "lifetime" ? usage.period : "";
  const status = keySpendStatus(usage.status);
  const blocking = usage.blocking == null ? null : ((budget) => ({ scope: string(budget.scope), spent_usd: number(budget.spent_usd), limit_usd: number(budget.limit_usd), period: string(budget.period) }))(object(usage.blocking));
  return { provider: "openrouter", status, spent_usd: dollars(usage.spent_usd), limit_usd: dollars(usage.limit_usd), remaining_usd: dollars(usage.remaining_usd), period,
    resets_at: string(usage.resets_at), read_at: string(usage.read_at), byok_usd: dollars(usage.byok_usd), includes_byok: usage.includes_byok === true,
    blocking, profile: string(usage.profile), detail: string(usage.detail) };
}
function keySpendStatus(value: unknown): KeySpend["status"] {
  switch (value) {
    case "available": case "missing": case "auth_required": case "rate_limited": case "stale": case "unattributed": return value;
  }
  return "unavailable";
}
export function parseSnapshot(value: unknown): Snapshot {
  const v = object(value);
  return {
    key_spend: array(v.key_spend).flatMap((value) => { const spend = parseKeySpend(value); return spend ? [spend] : []; }),
    subscriptions: array(v.subscriptions).flatMap((value): SubscriptionUsage[] => {
      const usage = object(value);
      if (usage.provider !== "claude" && usage.provider !== "codex") return [];
      const status = usage.status === "available" || usage.status === "stale" || usage.status === "auth_required" ? usage.status : "unavailable";
      const remaining = usage.percent_remaining;
      const isMeasured = status === "available" && usage.source === "oauth" && typeof remaining === "number" && Number.isFinite(remaining) && remaining >= 0 && remaining <= 100;
      const floor = usage.floor_percent;
      return [{ provider: usage.provider, status: status === "available" && !isMeasured ? "unavailable" : status, percent_remaining: isMeasured ? remaining : null,
        read_at: string(usage.read_at), resets_at: string(usage.resets_at), source: usage.source === "oauth" || usage.source === "api" ? usage.source : "",
        floor_percent: typeof floor === "number" && Number.isFinite(floor) && floor >= 0 && floor < 100 ? floor : null }];
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
    cfo_terminal_since: v.cfo_terminal_since === undefined ? "" : string(v.cfo_terminal_since),
    cfo_harness: v.cfo_harness === undefined ? "" : string(v.cfo_harness),
    ...(v.cfo_engine == null ? {} : { cfo_engine: ((engine) => ({ harness: string(engine.harness), model: string(engine.model), effort: string(engine.effort), pending: engine.pending === undefined ? false : boolean(engine.pending), problem: engine.problem === undefined ? "" : string(engine.problem), waiting: engine.waiting === undefined ? "" : string(engine.waiting) }))(object(v.cfo_engine)) }),
    cfo_session: v.cfo_session === undefined ? "" : string(v.cfo_session),
    cfo_conversation_left: v.cfo_conversation_left === undefined ? "" : string(v.cfo_conversation_left),
    cfo_update: v.cfo_update == null ? null : ((update) => ({ ...parseHarnessUpdate(update), pending: update.pending === undefined ? false : boolean(update.pending), updating: update.updating === undefined ? false : boolean(update.updating), problem: update.problem === undefined ? "" : string(update.problem) }))(object(v.cfo_update)),
    ...(v.start_at_login == null ? {} : { start_at_login: ((setting) => ({ on: boolean(setting.on), unavailable: setting.unavailable === undefined ? "" : string(setting.unavailable) }))(object(v.start_at_login)) }),
    ...(v.dev_drive == null ? {} : { dev_drive: parseDevDrive(object(v.dev_drive)) }),
    ...(v.comeback == null ? {} : { comeback: ((c) => ({ signed_in: string(c.signed_in), ...(c.cfo == null ? {} : { cfo: parseComebackEntry(c.cfo) }), goblins: array(c.goblins).map(parseComebackEntry) }))(object(v.comeback)) }),
    build: string(v.build),
    cfo_runs: v.cfo_runs === undefined || boolean(v.cfo_runs),
    cfo_starting: v.cfo_starting === undefined ? false : boolean(v.cfo_starting),
    cfo_closed: v.cfo_closed === undefined ? false : boolean(v.cfo_closed),
    cfo_configured: v.cfo_configured === undefined ? false : boolean(v.cfo_configured),
    inbox: number(v.inbox),
    memory: v.memory === undefined || v.memory === null ? null : (({ available, total, commit_available, commit_limit, paged_pool, nonpaged_pool, pool_line, pool_growth, floor, next, holders }) => ({
      available: number(available), total: number(total), commit_available: number(commit_available), commit_limit: number(commit_limit),
      paged_pool: number(paged_pool), nonpaged_pool: number(nonpaged_pool), pool_line: number(pool_line),
      pool_growth: array(pool_growth).map((value) => { const g = object(value); return { tag: string(g.tag), pool: string(g.pool), grown: number(g.grown), holds: number(g.holds) }; }),
      floor: number(floor), next: number(next),
      holders: array(holders).map((value) => { const h = object(value); return { name: string(h.name), commit: number(h.commit) }; }),
    }))(object(v.memory)),
    pc_restart: v.pc_restart == null ? null : ((restart) => ({ state: pcRestartState(restart.state), problem: string(restart.problem), at: string(restart.at) }))(object(v.pc_restart)),
    ci_durations: array(v.ci_durations).map((value) => { const d = object(value); return { repository: string(d.repository), kind: string(d.kind), seconds: number(d.duration_seconds) }; }),
    disk: v.disk === undefined || v.disk === null ? null : (({ drive, free, total, floor, wake }) => ({
      drive: string(drive), free: number(free), total: number(total), floor: number(floor), wake: number(wake),
    }))(object(v.disk)),
    processors: v.processors === undefined || v.processors === null ? null : (({ performance_cores, efficiency_cores, free, efficiency_free, next, busiest }) => ({
      performance_cores: number(performance_cores), efficiency_cores: number(efficiency_cores), free: number(free), efficiency_free: number(efficiency_free), next: number(next),
      busiest: array(busiest).map((name) => string(name)),
    }))(object(v.processors)),
    gpu: v.gpu === undefined || v.gpu === null ? null : {
      adapters: array(object(v.gpu).adapters).map((value) => { const a = object(value); return { name: string(a.name), busy: number(a.busy), busiest: a.busiest === undefined ? "" : string(a.busiest) }; }),
    },
    projects: array(v.projects).map((value) => { const p = object(value); return { name: string(p.name), repository: string(p.repository), contributors: array(p.contributors).map((person) => parsePerson(object(person))) }; }),
    release: v.release == null ? null : (({ installed, tag, page, published, source }) => ({ installed: string(installed), tag: string(tag), page: string(page), published: string(published), source: boolean(source) }))(object(v.release)),
    merge_trains: array(v.merge_trains).map(parseMergeTrain),
    retired: strings(v.retired),
    issues: strings(v.issues),
    attention: strings(v.attention),
    activity: array(v.activity).map(value=>{const a=object(value);return {id:string(a.id),kind:string(a.kind),task_id:string(a.task_id),generation:string(a.generation),cfo_identity:string(a.cfo_identity),live:a.live===undefined?false:boolean(a.live),source:string(a.source),target:string(a.target),state:string(a.state),url:string(a.url),at:string(a.at),until:string(a.until),watch:string(a.watch)};}),
    ...itemLists(v),
    tasks: array(v.tasks).map((value) => {
      const t = object(value);
      return {
        lifecycle: t.lifecycle == null ? undefined : ((record) => ({ phase: string(record.phase), action: string(record.action), at: string(record.at), kept: strings(record.kept), stopped: strings(record.stopped), problems: strings(record.problems), handoff_saved: boolean(record.handoff_saved), validation_restarts: boolean(record.validation_restarts),
          ...(record.with_parent === undefined ? {} : { with_parent: boolean(record.with_parent) }),
          ...(record.kept_messages == null ? {} : { kept_messages: strings(record.kept_messages) }),
          ...(record.pause == null ? {} : { pause: ((pause) => ({ reason: string(pause.reason), until: string(pause.until), at: string(pause.at) }))(object(record.pause)) }) }))(object(t.lifecycle)),
        teardown: strings(t.teardown), detail: string(t.detail), queue_revision: string(t.queue_revision), action_error: string(t.action_error), branch: string(t.branch),
        pending_engine: t.pending_engine == null ? undefined : ((choice) => ({ harness: string(choice.harness), model: string(choice.model), effort: string(choice.effort), when: string(choice.when) }))(object(t.pending_engine)),
        ...(t.harness_update == null ? {} : { harness_update: parseHarnessUpdate(object(t.harness_update)) }),
        switching: t.switching === undefined ? false : boolean(t.switching),
        runtime: parseRuntime(t.runtime),
        id: string(t.id),
        title: string(t.title),
        goblin_name: string(t.goblin_name),
        goblin_title: string(t.goblin_title),
        project: string(t.project),
        harness: string(t.harness),
        backend: string(t.backend),
        model: string(t.model),
        effort: string(t.effort),
        mode: string(t.mode),
        generation: string(t.generation),
        session: string(t.session),
        waits: array(t.waits).map((value) => { const w = object(value); return { kind: string(w.kind), target: string(w.target), until: string(w.until), bytes: number(w.bytes), problem: string(w.problem) }; }),
        activity: t.activity === undefined ? "" : string(t.activity),
        handoff: t.handoff === undefined ? false : boolean(t.handoff),
        last_report: string(t.last_report),
        reported_at: string(t.reported_at),
        retired_at: string(t.retired_at),
        report: t.report === undefined ? "" : string(t.report),
        waiting_on: t.waiting_on === undefined ? "" : string(t.waiting_on),
        gate_step: t.gate_step === undefined ? "" : string(t.gate_step),
        archived: t.archived === undefined ? false : boolean(t.archived),
        merged: t.merged === undefined ? false : boolean(t.merged),
        closed: t.closed === undefined ? false : boolean(t.closed),
        history_unavailable: t.history_unavailable === undefined ? "" : string(t.history_unavailable),
        since: string(t.since),
        brief: t.brief === undefined ? false : boolean(t.brief),
        starting: t.starting === undefined ? false : boolean(t.starting),
        overlord_start: t.overlord_start === undefined ? false : boolean(t.overlord_start),
        asked: t.asked === undefined ? false : boolean(t.asked),
        start_error: string(t.start_error),
        finished: t.finished === undefined ? "" : string(t.finished),
        priority: t.priority === undefined ? "" : string(t.priority),
        parent: t.parent === undefined ? "" : string(t.parent),
        ...(t.progress == null ? {} : { progress: ((progress) => ({ at: string(progress.at), source: string(progress.source) }))(object(t.progress)) }),
        ...(t.ticket == null ? {} : { ticket: ((ticket) => ({ number: number(ticket.number), url: string(ticket.url), state: string(ticket.state) }))(object(t.ticket)) }),
        overlaps: array(t.overlaps).map((value) => { const o = object(value); return { ...parsePerson(o), what: string(o.what), url: string(o.url) }; }),
        ...(t.comeback == null ? {} : { comeback: parseComebackEntry(t.comeback) }),
        ...(t.hosted_checks == null ? {} : { hosted_checks: ((c) => ({ head: string(c.head), state: string(c.state), checks: number(c.checks), failed: strings(c.failed), rate_limited: strings(c.rate_limited), link: string(c.link), approved: c.approved === undefined ? false : boolean(c.approved) }))(object(t.hosted_checks)) }),
        ...(t.tree == null ? {} : { tree: parseTree(t.tree) }),
        ...(t.local_checks == null ? {} : { local_checks: ((c) => ({ commit: string(c.commit), level: string(c.level), required_level: string(c.required_level), status: string(c.status), duration_seconds: number(c.duration_seconds), queue_seconds: number(c.queue_seconds), failed: strings(c.failed), at: string(c.at) }))(object(t.local_checks)) }),
        ...(t.deployment == null ? {} : { deployment: ((d) => ({ commit: string(d.commit), state: string(d.state), workflows: strings(d.workflows), link: string(d.link), at: string(d.at) }))(object(t.deployment)) }),
        phase: string(t.phase),
        reason: string(t.reason),
        head: string(t.head),
        pr: string(t.pr),
        verified: boolean(t.verified),
        at: string(t.at),
      };
    }),
    // The supervisor lists the sessions by id, so one that reports keeps its
    // place in what it sends (#1611); the board holds them oldest update
    // first, so the newest CFO session is the last.
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
    }).sort((one, other) => (Date.parse(one.updated_at) || 0) - (Date.parse(other.updated_at) || 0)),
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
      const signIn = string(agent.sign_in);
      if (signIn !== "signed_in" && signIn !== "signed_out" && signIn !== "unknown") throw new Error("Invalid sign-in state");
      return { id: string(agent.id), name: string(agent.name), recommended: boolean(agent.recommended), note: string(agent.note), installed: boolean(agent.installed), sign_in: signIn, reason: string(agent.reason) };
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
