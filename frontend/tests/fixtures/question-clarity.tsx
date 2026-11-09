import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenterMenu } from "../../src/command-center-menu";
import type { CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window {
    // refresh is the board's next snapshot of the same fleet, as the stream
    // sends one every 15 seconds: a new revision and nothing else changed.
    refresh?: () => void;
  }
}

// The Overlord, 2026-11-02: CEO questions in the Command Center are
// "difficult to read, poorly formatted and full of gibberish". legacy is the
// question in his screenshot as the board stored it, words run together and
// all, with only the private project's file names made neutral, 24 minutes
// after Lyle asked it, so the CEO has left it to him too. clear is the
// same decision written as decisions.md asks, with its evidence after its
// Details line; hostile carries markup in both parts; answered is in History.
const LEGACY = "Authorize one managed round for the three new Review findings?\n- **New Review gate**: lifecycle-selected01M6EASKNZ3NNDFC6PWCBFHY7R is awaiting_approval on cleanf77bd7ee/tree74fbbb32; Review648.235s, risk high,1error+2warnings, all action auto-fix. Test/Document/Lint/Push/PR/CI remain pending, generation0. No worker repair or response has been submitted.\n- registry-parser-divergence | error | tools/select_tests.py:279: actual code reads only double-quoted testpaths lines, while classification uses shard.testpaths/tomllib. A valid single-quoted registered transitive consumer can be classified but silently omitted at execution. Concrete fix: reuse the existing TOML reader; regression must execute selection and prove that valid TOML forms preserve the registered consumer.\n- source-content-test-assertions | warning | tests/test_health_tooling.py, test_mirror_workflow.py and test_select_tests.py: inspected source confirms shell-line/merge-string/AST-name assertions that do not prove command execution, merge parents/tree or pytest collection. Replace only cited contracts with executable recording-stub, isolated Git-parent/tree and collection evidence; cover commented-out/nested commands and uncollectable tests as negative cases.\n- routing-test-oracle | warning | tests/test_changed_paths.py:82: expected is_broad repeats the implementation predicates. Replace it with explicit parameterized policy expectations: ordinary PR light; train/full-ci/manual broad.\n- Proposed scope is the existing runner plus four cited test files; no workflow, runtime application, dependency, manifest, cooldown, audit, cap, hold, documentation, deployment or train changes. One bounded managed red/green and affected verification round; no unchanged full baseline or direct worker edit.\n- The former lock-only release1015182 was consumed by its actual model-capacity failure. Original01M67Q2 remains retained as operator-cancelled after the pause, not passed. I do not treat lifecycle restart as restored allowance or authorization to retry that lock repair. This is a new scope decision; use ceo pipeline respond only within supported remaining frozen allowance and stop on refusal/exhaustion. No new run or budget reset.\n- Existing hold remains on PR2339; follow-redirects audit issue2342 remains unresolved. Any later verified repair publication and exact-head required hosted proof must respect the existing separate publication/integration restrictions; no automatic broad retry, merge or deploy is requested.\n- Full native findings/status are saved as resume-01M6EA-review-gate.stdout.log and resume-01M6EA-review-status.stdout.log under the task temporary directory.11protected receipts,4archive/recovery refs and1015182 actual failure evidence match. Relevant skill C:/Users/fpres/.agents/skills/no-mistakes/SKILL.md requires pipeline-owned fixes while active; the separate CEO lock-only/read-only restriction is why this new scope needs your decision.";

const RUN_ID = "01M6EASKNZ3NNDFC6PWCBFHY7R";
const LOG_LINK = "https://github.com/example/project/actions/runs/38280093670/job/112233445566778899?check_suite_focus=true";
const CLEAR = [
  "Fix the three review findings in one managed round?",
  "- **Recommended: fix them now.** One finding is a real bug: the test runner can skip a registered test without saying so, so CI could pass without running it.",
  "- Fix them now: one bounded repair round on the existing runner and four test files, then the gate goes on to Test, Lint, Push and CI. No workflow, dependency or deploy change.",
  "- Keep Review parked: nothing changes, the pull request stays held at Review and the skipped-test bug stays.",
  "- Not known yet: whether the run's remaining review allowance covers this round. If it does not, the run stops rather than retrying.",
  "",
  "Details:",
  "- Review gate: run `" + RUN_ID + "` awaits approval on clean f77bd7ee, tree 74fbbb32; Review took 648.235 s, risk high, 1 error and 2 warnings, all auto-fix.",
  "- registry-parser-divergence | error | `tools/select_tests.py:279`: execution reads only double-quoted testpaths lines while classification uses tomllib, so a valid single-quoted consumer is classified but omitted.",
  "- source-content-test-assertions | warning | tests/test_health_tooling.py, test_mirror_workflow.py and test_select_tests.py assert source text, not execution.",
  "- routing-test-oracle | warning | tests/test_changed_paths.py:82 repeats the implementation's predicates.",
  "- Lock-only release 1015182 was consumed by its model-capacity failure; original 01M67Q2 stays operator-cancelled, not passed.",
  "- Hold remains on PR 2339; follow-redirects audit issue 2342 is unresolved.",
  "- Full findings: resume-01M6EA-review-gate.stdout.log and resume-01M6EA-review-status.stdout.log in the task's temporary directory, and the failed job at " + LOG_LINK,
].join("\n");
const HOSTILE = "Keep <b>the</b> <img src=x onerror=\"window.hacked=1\"> page?\n- **Recommended: keep it.** <script>window.hacked=2</script>\n\nDetails:\n- <img src=x onerror=\"window.hacked=3\"> and `<script>window.hacked=4</script>`";
const ANSWERED = "Move the nightly import to 02:00?\n- **Recommended: move it.** The 01:00 slot collides with the backup.\n\nDetails:\n- backup job `nightly-backup-7f3a` holds the table lock from 00:55 to 01:40.";

const at = "2026-11-02T14:46:19Z";
const lyle = { id: "ci-simplify", title: "Simplify the CI", goblin_name: "Lyle", goblin_title: "CI Conjurer", phase: "blocked", generation: "s1", verified: false };
const base = {
  healthy: true, instance: "fixture-token", revision: 1, at: "2026-11-02T15:10:19Z", ceo_runs: true, tasks: [lyle], actions: [] as Record<string, unknown>[],
  questions: [
    { id: "notify-ci-simplify-1015292", identity: "a".repeat(64), task: lyle.id, generation: "s1", seq: 1015292, text: LEGACY, options: ["Authorize managed review repair", "Keep Review parked"], recommended: "Authorize managed review repair", status: "pending", created_at: at },
    { id: "ci-review-repair", identity: "b".repeat(64), for_task: lyle.id, text: CLEAR, options: ["Fix them now", "Keep Review parked"], recommended: "Fix them now", status: "pending", created_at: at },
    { id: "hostile-markup", identity: "c".repeat(64), text: HOSTILE, options: ["Keep it", "Drop it"], recommended: "Keep it", status: "pending", created_at: at },
    { id: "nightly-import-slot", identity: "d".repeat(64), text: ANSWERED, options: ["Move it", "Keep 01:00"], recommended: "Move it", status: "succeeded", answer_id: "board-nightly", answer: "Move it", answer_kind: "option", answered_option: "Move it", answered_by: "overlord", created_at: at, answered_at: "2026-11-02T15:00:00Z" },
  ],
};

let raw = base;

function QuestionClarity() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(raw));
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  const open = (key: string) => setFocus({ key, at: Date.now() });
  window.refresh = () => { raw = { ...raw, revision: raw.revision + 1 }; setSnapshot(parseSnapshot(raw)); };
  return <main>
    <button onClick={() => open("question:notify-ci-simplify-1015292")}>Open the screenshot question</button>
    <button onClick={() => open("question:ci-review-repair")}>Open the clear question</button>
    <button onClick={() => open("question:hostile-markup")}>Open the hostile question</button>
    <CommandCenterMenu snapshot={snapshot} connected presentations={[]} now={Date.now()} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<QuestionClarity />);
