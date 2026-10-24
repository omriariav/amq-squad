PKG = "./internal/routing/"
R = "internal/routing/router.go"
D = "internal/routing/durable.go"
C = "internal/routing/chain.go"
TM = "internal/routing/tamper.go"
TR = "TestFix5TruncationToAValidPrefixAcrossARestart$"
HOLD = "TestFix5HoldNamesBreaksAndClearsOnlyAfterTheObservedBlock$"
CASES = [
    # (a) the head record
    ("(a) the outcome chain must reach the head", D, "\tproblems = append(problems, shortfall(\"outcome\", head, r.headRec.OutcomeSeq, r.headRec.OutcomeTag)...)\n", "", TR, PKG),
    ("(a) the head moves with every outcome append", R, "\t\t\t\t\t// The head moves with every append (Codex pass 5, (a)).\n\t\t\t\t\tif err := r.saveHead(); err != nil {\n\t\t\t\t\t\treturn err\n\t\t\t\t\t}\n", "", TR, PKG),
    ("(a) the decision chain must reach the head", R, "\tdecProblems = append(decProblems, shortfall(\"decision\", decHead, h.DecisionSeq, h.DecisionTag)...)\n", "", TR, PKG),
    ("(a) decisions carry seq and prev", D, "\t\td.Seq, d.Prev = r.decHead.Seq+1, r.decHead.Tag\n", "\t\t_ = r.decHead\n", TR, PKG),
    ("(a) a missing head in a routed session is invalid", R, "\tcase !found && sessionStarted(o.Root):", "\tcase false && !found && sessionStarted(o.Root):", "TestFix5HeadMissingOrBrokenIsInvalid$", PKG),
    ("(a) an edited head is invalid", "internal/routing/head.go", "\tif err := json.Unmarshal(raw, &h); err != nil || !h.verify(key) {", "\tif err := json.Unmarshal(raw, &h); err != nil {", "TestFix5HeadMissingOrBrokenIsInvalid$", PKG),
    # (e) the seed identity
    ("(e) a replaced seed is refused", R, "\tcase found && seedDev != 0 && (h.SeedDev != seedDev || h.SeedIno != seedIno):", "\tcase false && found && seedDev != 0 && (h.SeedDev != seedDev || h.SeedIno != seedIno):", "TestFix5SeedReplacedIsRefused$", PKG),
    # (b) the hold names its breaks; the clearance acknowledges them
    ("(b) the block detail names each break", TM, "\t\tt.ID, strings.Join(named, \"; \"), t.ID)}, true", "\t\tt.ID, \"(breaks)\"+strings.Join(named, \"\")[:0], t.ID)}, true", HOLD, PKG),
    ("(b) the clearance records the acknowledged ids", TM, "{Kind: tamperClear, Task: t.ID, Acked: acked, At: now}", "{Kind: tamperClear, Task: t.ID, Acked: acked[:0], At: now}", HOLD, PKG),
    # L1: clear only after the observed block
    ("L1 clearance keys on the observed block", TM, "\tif seen, ok := r.observedAt[t.ID]; ok && t.State == task.StatePending && t.UpdatedAt.After(seen) {", "\tif seen, ok := r.tamperBlockedAt[t.ID]; ok && t.State == task.StatePending && t.UpdatedAt.After(seen) {", HOLD, PKG),
    ("L1 the block is observed", TM, "\t\tif err := r.writeTamper(tamperRecord{Kind: tamperObserved, Task: t.ID, At: now}); err != nil {", "\t\tif err := error(nil); err != nil {", HOLD, PKG),
    # (c) torn tail and bad decision lines reported
    ("(c) the torn tail is reported with its length", D, "\tr.head, r.loaded, r.tornTail = head, true, tail", "\tr.head, r.loaded, r.tornTail = head, true, tail*0", "TestFix5TornTailAndBadDecisionAreReported$", PKG),
    ("(c) a bad decision line names its task", C, "\t\t\tproblems = append(problems, Problem{ID: lineID(log+\"_line\", line), Log: log, Task: task,", "\t\t\tproblems = append(problems, Problem{ID: lineID(log+\"_line\", line), Log: log, Task: task[:0],", "TestFix5TornTailAndBadDecisionAreReported$", PKG),
    # (d) Flush reports holds; a rooted router needs Tasks
    ("(d) Flush reports the holds it created", R, "\tif len(holds) > 0 {\n\t\treturn &HoldsCreated{Problems: holds}", "\tif false && len(holds) > 0 {\n\t\treturn &HoldsCreated{Problems: holds}", "TestFix5FlushReportsHoldsAndRootedRouterNeedsTasks$", PKG),
    ("(d) a rooted router needs Tasks", R, "\tif o.Root != \"\" && o.Tasks == nil {", "\tif false && o.Root != \"\" && o.Tasks == nil {", "TestFix5FlushReportsHoldsAndRootedRouterNeedsTasks$", PKG),
    # new 1: the gap is the missing seq's, not the successor's
    ("new 1 a gap is never the successor's", C, "\t\t\t\tproblems = append(problems, Problem{ID: fmt.Sprintf(\"gap:%s%d\", logPrefix(log), s), Log: log, Seq: s,", "\t\t\t\tproblems = append(problems, Problem{ID: fmt.Sprintf(\"gap:%s%d\", logPrefix(log), s), Log: log, Seq: s, Task: l.Task,", "TestFix5GapIsNotTheSuccessors$", PKG),
    # new 2: problems persist until acknowledged
    ("new 2 breaks persist across reloads", D, "\tr.problems = mergeProblems(r.problems, problems)\n", "\tr.problems = problems\n", "TestFix5ProblemsPersistUntilAcknowledged$", PKG),
    ("new 2 breaks persist across restarts", R, "\tr.problems = append(r.problems, h.Open...) // open breaks persist across restarts (new 2)\n", "", "TestFix5ProblemsPersistUntilAcknowledged$", PKG),
    # L2: the sweep holds ended attempts only
    ("L2 the sweep skips running attempts", TM, "\t\t\tfor _, t := range list {\n\t\t\t\tfor _, a := range t.Attempts { // ended attempts only (L2)", "\t\t\tfor _, t := range list {\n\t\t\t\tif t.State == task.StateInProgress {\n\t\t\t\t\theld[t.ID] = true\n\t\t\t\t}\n\t\t\t\tfor _, a := range t.Attempts { // ended attempts only (L2)", "TestFix5SweepSkipsRunningAttempts$", PKG),
]
