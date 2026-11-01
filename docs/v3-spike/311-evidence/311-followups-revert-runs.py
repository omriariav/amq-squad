#!/usr/bin/env python3
"""The revert runs of brief 3.1.1-2, the four follow-ups from the 3.1 run
(docs/v3-spike/311-evidence/311-followups.md, section "Revert runs").

Each case reverts one change in the working tree, runs the row that must
catch it, and expects it to fail; every touched file is restored from its
text before the run, in a finally, even when a run fails or is
interrupted. Run it from a clean checkout of the branch:
  python3 docs/v3-spike/311-evidence/311-followups-revert-runs.py
It refuses a tree with any change. No network, no real AMQ, tmux or Buzz:
the rows run on the in-process fakes and the stand-in pi."""
import pathlib, subprocess, sys

ROOT = pathlib.Path(subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip())
if subprocess.run(["git", "status", "--porcelain", "--untracked-files=all"], cwd=ROOT, capture_output=True, text=True).stdout:
    sys.exit("refused: the tree has changes; the runs rewrite files and restore them")

GO = lambda pkg, rx: f"go test -count=1 -run '^({rx})$' ./{pkg}"
NODE = lambda f: f"cd pi && node --test test/{f}"
PY = lambda f: f"python3 scripts/{f}"

CASES = [
 ("R1a failure() counts every commit refusal (old)", "internal/routing/refusal.go",
  "\t\tif work, ok := RefusalWorkFailure[a.Detail]; ok {\n\t\t\treturn work\n\t\t}\n\t\treturn a.Outcome == task.OutcomeBlocked\n", "\t\treturn true\n",
  GO("internal/routing", "TestPolicyRefusalIsNotAFailedAttempt")),
 ("R1b same revert, the end-to-end t27 row", "internal/routing/refusal.go",
  "\t\tif work, ok := RefusalWorkFailure[a.Detail]; ok {\n\t\t\treturn work\n\t\t}\n\t\treturn a.Outcome == task.OutcomeBlocked\n", "\t\treturn true\n",
  GO("internal/routing", "TestSizeCapRefusalRetriesOnTheSameSeat")),
 ("R1c an unclassified commit code escalates by default", "internal/routing/refusal.go",
  "\t\treturn a.Outcome == task.OutcomeBlocked\n", "\t\treturn true\n",
  GO("internal/routing", "TestPolicyRefusalIsNotAFailedAttempt")),
 ("R1d one code (pr_pending) missing from the table", "internal/routing/refusal.go",
  "\tseatcommit.ReasonPRPending:        false,\n", "",
  GO("internal/routing", "TestEveryCommitRefusalCodeIsClassified")),
 ("R1e verdict_failed classified not a work failure", "internal/routing/refusal.go",
  "\ttask.BlockVerdictFailed:          true,\n", "\ttask.BlockVerdictFailed:          false,\n",
  GO("internal/routing", "TestWorkFailureStillEscalates")),
 ("R1f size_cap_exceeded classified a work failure", "internal/routing/refusal.go",
  "\tseatcommit.ReasonSizeCap:    false,\n", "\tseatcommit.ReasonSizeCap:    true,\n",
  GO("internal/routing", "TestEveryCommitRefusalCodeIsClassified|TestPolicyRefusalIsNotAFailedAttempt")),
 ("R2a no roster seat logs seat_request_failed every tick (old)", "internal/scheduler/roster.go",
  "\t\ts.routeWaits[t.ID] = p\n\t\treturn nil, s.wait(t, WaitNoRosterSeat, now)\n",
  "\t\tlog(Decision{Kind: \"seat_request_failed\", TaskID: t.ID, Pool: p.Key(), Reason: fmt.Sprintf(\"no free seat in the squad's seat list for pool %s (no roster seat serves it)\", p.Key())})\n\t\treturn nil, nil\n",
  GO("internal/scheduler", "TestNoRosterSeatIsANamedWaitNotALogLine|TestRosterPoolRules")),
 ("R2b route waits not written to the state file", "internal/scheduler/scheduler.go",
  "\t\tst.RouteWaits = append(st.RouteWaits, RouteWait{TaskID: id, Reason: WaitNoRosterSeat, Pool: p})\n", "\t\t_, _ = id, p\n",
  GO("internal/scheduler", "TestNoRosterSeatIsANamedWaitNotALogLine")),
 ("R2c no backoff: a failed request every tick (old)", "internal/scheduler/scheduler.go",
  "b != nil && b.key == bkey && now.Before(b.next) {", "b != nil && b.key == bkey && now.Before(b.next) && false {",
  GO("internal/scheduler", "TestTransientSeatRequestFailureBacksOff")),
 ("R2d the backoff does not double", "internal/scheduler/scheduler.go",
  "b.delay = min(2*b.delay, SeatRequestBackoffMax)", "b.delay = SeatRequestBackoffMin",
  GO("internal/scheduler", "TestTransientSeatRequestFailureBacksOff")),
 ("R2e a task change does not reset the backoff", "internal/scheduler/scheduler.go",
  "b != nil && b.key == bkey && now.Before(b.next) {", "b != nil && now.Before(b.next) {",
  GO("internal/scheduler", "TestTransientSeatRequestFailureBacksOff")),
 ("R3a status names no wait_pool", "internal/status/classify.go",
  "\t\trow.WaitPool = routeWaitPool(t, in.Scheduler.RouteWaits)\n", "",
  GO("internal/status", "TestNoRosterSeatWaitNamesThePool")),
 ("R3b the attention item does not name the pool", "internal/status/classify.go",
  "\t\tif row.WaitPool != \"\" {\n", "\t\tif false {\n",
  GO("internal/status", "TestNoRosterSeatWaitNamesThePool")),
 ("R4a seats[] lists every record under the root (old)", "internal/status/classify.go",
  "\tif len(in.Scheduler.Roster) == 0 {\n\t\treturn nil\n\t}\n", "\treturn nil\n",
  GO("internal/status", "TestStatusSeatsAreTheRunningRoster")),
 ("R4b the running roster leaves out the lead", "internal/status/classify.go",
  "\tif in.Scheduler.Lead != \"\" {\n\t\tout[in.Scheduler.Lead] = true\n\t}\n\tif in.Lead != nil && in.Lead.Handle != \"\" {\n\t\tout[in.Lead.Handle] = true\n\t}\n", "",
  GO("internal/status", "TestStatusSeatsAreTheRunningRoster")),
 ("R4c stale_records null when empty", "internal/status/classify.go",
  "StaleRecords: []StaleRecord{}, ", "",
  GO("internal/status", "TestStatusSeatsAreTheRunningRoster|TestBuiltStatusValidatesAgainstTheGolden")),
 ("R4d stale_records not in the golden fixture", "internal/status/testdata/status-v1.golden.json",
  "  \"stale_records\": [\n    {\n      \"handle\": \"senior-dev\",\n      \"session\": \"squad\",\n      \"launch_id\": \"5f0c2a9e41d37b68\"\n    }\n  ],\n", "",
  GO("internal/status", "TestGoldenRoundTripAdditiveBothWays")),
 ("R5 the screen's task line does not name the wait", "pi/extensions/lib/screen.ts",
  "\t\tif (t.task_state === \"pending\" && t.wait_reason === \"no_roster_seat\") {\n", "\t\tif (false) {\n",
  NODE("screen.test.ts")),
 ("R6a team phase list is every registered tool, thirteen (old)", "pi/extensions/amq-squad.ts",
  "\tconst teamTools = TEAM_LEAD_TOOLS.filter((t) => registered.includes(t));\n", "\tconst teamTools = registered;\n",
  NODE("phase.test.ts")),
 ("R6b claim and done active for a lead (old)", "pi/extensions/lib/tools.ts",
  "reg(cfg.lead || cfg.solo ? { ...def, defaultActive: false } : def)", "reg(def)",
  NODE("tools.test.ts")),
 ("R6c same revert, lead --team's first turn", "pi/extensions/lib/tools.ts",
  "reg(cfg.lead || cfg.solo ? { ...def, defaultActive: false } : def)", "reg(def)",
  NODE("phase.test.ts")),
 ("R6d the extension's list drifts from Go's (amq_status dropped)", "pi/extensions/lib/tools.ts",
  "\t\"amq_status\",\n];", "];",
  NODE("tools.test.ts")),
 ("R7a detect-release-ci.py takes any Unicode digit (old)", "scripts/detect-release-ci.py",
  'VERSION_RE = re.compile(r"^v?([0-9]+\\.[0-9]+\\.[0-9]+(?:-rc\\.[0-9]+)?)$")', 'VERSION_RE = re.compile(r"^v?(\\d+\\.\\d+\\.\\d+(?:-rc\\.\\d+)?)$")',
  PY("test_release_version_forms.py")),
 ("R7b detect --check-version checks nothing", "scripts/detect-release-ci.py",
  "    match = VERSION_RE.fullmatch(version.strip())\n    if not match:\n        return [f\"VERSION", "    return []\n    match = VERSION_RE.fullmatch(version.strip())\n    if not match:\n        return [f\"VERSION",
  PY("test_detect_release_ci.py")),
 ("R7c make release-check runs only its old two validators (old)", "Makefile",
  "\t@python3 scripts/test_detect_release_ci.py\n\t@python3 scripts/test_release_version_forms.py\n\t@python3 scripts/detect-release-ci.py --check-version \"$(VERSION)\"\n", "",
  PY("test_release_version_forms.py")),
 ("R7d make ci does not run the version-forms row", "Makefile",
  "\t@python3 scripts/test_detect_release_ci.py\n\t@python3 scripts/test_release_version_forms.py\n\n", "\t@python3 scripts/test_detect_release_ci.py\n\n",
  PY("test_release_version_forms.py")),
 ("R6e amq_task_block active for a lead (#844)", "pi/extensions/lib/tools.ts",
  "\tseatReg({\n\t\tname: \"amq_task_block\",", "\treg({\n\t\tname: \"amq_task_block\",",
  NODE("tools.test.ts")),
 ("R6f same revert, the solo lead's yes", "pi/extensions/lib/tools.ts",
  "\tseatReg({\n\t\tname: \"amq_task_block\",", "\treg({\n\t\tname: \"amq_task_block\",",
  NODE("phase.test.ts")),
 ("R8a the Go widen refusal points at t20 (the tip)", "internal/cli/task_widen.go",
  "is not blocked for files outside its declared set or by a seat's widen request (state %s, reason %s); there is nothing to widen; to send it back to a seat, the lead returns the task with amq-squad task reset %s --pool RUNTIME:MODEL:EFFORT\"",
  "is not blocked for files outside its declared set (state %s, reason %s); there is nothing to widen; for a seat's own block with a widen request, the lead returns the task with amq-squad task reset %s --pool RUNTIME:MODEL:EFFORT (until the typed block for a widen request exists, t20)\"",
  GO("internal/cli", "TestWidenRefusalNamesTheTypedWidenRequest")),
 ("R8b the pi widen refusal points at t20 (the tip)", "pi/extensions/lib/tools.ts",
  "is not blocked for files outside its declared set or by a seat's widen request (state ${t.state ?? \"?\"}, reason ${t.block_reason || \"none\"}); there is nothing to widen; to send it back to a seat, the lead returns the task with amq-squad task reset ${t.id ?? \"ID\"} --pool RUNTIME:MODEL:EFFORT`",
  "is not blocked for files outside its declared set (state ${t.state ?? \"?\"}, reason ${t.block_reason || \"none\"}); there is nothing to widen; for a seat's own block with a widen request, the lead returns the task with amq-squad task reset ${t.id ?? \"ID\"} --pool RUNTIME:MODEL:EFFORT (until the typed block for a widen request exists, t20)`",
  NODE("tools.test.ts")),
 ("R1g widen_requested missing from the table", "internal/routing/refusal.go",
  "\ttask.ReasonWidenRequested:            false,\n", "",
  GO("internal/routing", "TestWidenRequestedBlockIsNotAFailedAttempt")),
 ("R9a the drift check reads the squad's own store only (the tip)", "internal/mergetrain/drift.go",
  "\t\tif !e.IsDir() {\n", "\t\tif !e.IsDir() || e.Name() != ownName {\n",
  GO("internal/mergetrain", "TestDriftReadsTheGateStoresOfEverySession")),
 ("R9b a gate clears on its accepted decision, no verifier (the tip)", "internal/mergetrain/drift.go",
  "\t\t\tif verify == nil || verify(e.Name(), store, rec) != nil {\n", "\t\t\tif store.Decision(id) != gatestore.StateAccepted {\n",
  GO("internal/mergetrain", "TestDriftClearsOnlyOnTheOwnerSignedAcceptance|TestDriftReadsTheGateStoresOfEverySession")),
 ("R9c another session's gated command clears a tag", "internal/mergetrain/drift.go",
  "if (g.own || slices.Contains(other, t)) && bindCovered", "if bindCovered",
  GO("internal/mergetrain", "TestDriftReadsTheGateStoresOfEverySession")),
 ("R9d an unreadable store is skipped silently", "internal/mergetrain/drift.go",
  "\t\t\tbad = append(bad, unreadableStore{path: store.Dir(\"gates\"), err: err})\n", "",
  GO("internal/mergetrain", "TestDriftNamesAnUnreadableGateStore")),
 ("R9e without the binding every gate verifies", "internal/cli/merge_train.go",
  "return func(string, gatestore.Store, gatestore.Record) error { return fmt.Errorf(\"gate binding: %w\", err) }", "return func(string, gatestore.Store, gatestore.Record) error { return nil }",
  GO("internal/cli", "TestReleaseGateVerifierFailsClosedWithoutTheBinding")),
 ("R10 the dry run ignores the binding of a PR-only request (the tip)", "internal/cli/merge_train.go",
  "\t\t\tif err := mergetrain.BindUnnamed(*store, req.Repository, req); err != nil {\n", "\t\t\tif err := error(nil); err != nil {\n",
  GO("internal/cli", "TestMergeTrainDryRunReadsTheBindingOfAPROnlyRequest")),
]

originals = {}
results = []
try:
    for name, rel, old, new, cmd in CASES:
        path = ROOT / rel
        if rel not in originals:
            originals[rel] = path.read_text()
        src = originals[rel]
        n = src.count(old)
        if n != 1:
            results.append((name, "SETUP", f"pattern found {n} times in {rel}"))
            continue
        try:
            path.write_text(src.replace(old, new))
            p = subprocess.run(cmd, shell=True, cwd=ROOT, capture_output=True, text=True, timeout=600)
            out = (p.stdout + p.stderr).strip().splitlines()
            tail = [l for l in out if any(k in l for k in ("FAIL", "fail ", "✖", "Error", "not ok", "FAILED"))][:2]
            results.append((name, "caught" if p.returncode != 0 else "MISSED", f"exit {p.returncode}; {cmd}; {' | '.join(tail)[:220]}"))
        finally:
            path.write_text(src)
finally:
    for rel, src in originals.items():
        (ROOT / rel).write_text(src)
caught = sum(1 for r in results if r[1] == "caught")
for r in results:
    print(f"{r[1]:7} {r[0]} :: {r[2]}")
print(f"\n{len(results)} runs, {caught} caught")
d = subprocess.run("git status --short", shell=True, cwd=ROOT, capture_output=True, text=True).stdout
print("tree after restore:", repr(d))
sys.exit(0 if caught == len(results) and len(results) == len(CASES) else 1)
