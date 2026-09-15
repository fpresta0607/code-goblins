# CFO Native Gate Binding Implementation Plan

**Goal:** Let CFO launch a no-mistakes v1.75.1 run only when the native run is bound to the checked task head, freshly verified trusted branch, and frozen primary.

**Architecture:** CFO captures a complete immutable launch snapshot, repeats the mutable checks immediately before native invocation, and submits a nonce plus validation generation through the native strict receipt interface.
The native agent path enters a CFO guard that verifies the durable launch contract before delegating to the real executable, and the driver verifies the returned receipt and native database identity before reporting success.

**Tech Stack:** Go, Git, no-mistakes v1.75.1 AXI, SQLite, PowerShell integration tests.

---

### Task 1: Define launch evidence and race checks

**Files:**

- Modify: `internal/pipeline/driver.go`
- Modify: `internal/pipeline/reader_test.go`

**Steps:**

1. Add failing tests for exact task head, trusted tracking SHA, fresh remote SHA, submitted and trusted configuration, and effective primary snapshots.
2. Run the focused package tests and confirm the new cases fail for the missing launch evidence API.
3. Implement the smallest typed launch snapshot and equality check.
4. Run the focused package tests and confirm they pass.

### Task 2: Bind and verify the native launch

**Files:**

- Modify: `cmd/cfo/pipeline.go`
- Modify: `cmd/cfo/pipeline_test.go`
- Create: `cmd/cfo/native_gate.go`
- Create: `cmd/cfo/native_gate_test.go`

**Steps:**

1. Replace the unconditional refusal test with failing success, mismatch, and deterministic race tests.
2. Add failing tests for launch receipt parsing, exact native database verification, and guarded executable delegation.
3. Run the focused command tests and confirm the new cases fail for the missing bridge.
4. Implement nonce-bound launch contracts, the final launch snapshot comparison, guarded delegation, receipt capture, and database verification.
5. Run the focused command tests and confirm they pass.

### Task 3: Configure and document the guard

**Files:**

- Modify: `internal/pipeline/config.go`
- Modify: `internal/pipeline/config_test.go`
- Modify: `docs/pipeline.md`

**Steps:**

1. Add a failing semantic configuration test for the owned guarded executable and marker.
2. Run the focused test and confirm it fails against the current unguarded configuration.
3. Implement the owned configuration fields without changing unrelated settings.
4. Document the pre-agent contract, receipt checks, persistence, and refusal behavior.
5. Run formatting and focused tests.

### Task 4: Verify and deliver

**Files:**

- Modify only files required by legitimate gate findings.

**Steps:**

1. Run the full Go test suite, formatting check, vet or repository lint, documentation checks, and an exact candidate build.
2. Commit the candidate, fetch current `origin/main`, rebase the clean branch, and repeat affected verification if main moved.
3. Prove the candidate source commit and SHA-256 from the uninstalled build.
4. Wait for the existing native singleton owner to finish, apply any required shared configuration only in an explicit idle window, and run this task's high-risk managed gate without `--yes`.
5. Resolve legitimate findings within the frozen three-cycle review budget, then wait for green CI.
6. Open and record the PR, verify the published branch, remove task-created temporary files, and notify CFO.
