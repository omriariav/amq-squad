PKG = "./internal/routing/"
R = "internal/routing/router.go"
D = "internal/routing/durable.go"
C = "internal/routing/chain.go"
TM = "internal/routing/tamper.go"
SU = "internal/routing/seed_unix.go"
CH = "TestFix4ChainCatchesDeleteReorderDuplicate$"
HOLD = "TestHoldTamperedTaskOthersRoute$"
CASES = [
    # N1a: the hash chain
    ("N1a rows carry seq and prev when written", R, "\t\t\t\t\trow.Seq, row.Prev = r.head.Seq+1, r.head.Tag\n", "\t\t\t\t\t_ = r.head\n", CH, PKG),
    ("N1a a gap is a break", C, "\t\tcase row.Seq > head.Seq+1:\n", "\t\tcase false:\n", CH, PKG),
    ("N1a a duplicate or reorder is a break and not used", C, "\t\tdefault:\n\t\t\tproblems = append(problems, Problem{ID: lineID(\"order\", line), Task: row.Task,", "\t\tdefault:\n\t\t\t_ = append(problems, Problem{ID: lineID(\"order\", line), Task: row.Task,", CH, PKG),
    ("N1a duplicates are not counted", C, "\t\t\t\tWhat: fmt.Sprintf(\"row %d is a duplicate or out of order (chain at %d)\", row.Seq, head.Seq)})\n\t\t\tcontinue", "\t\t\t\tWhat: fmt.Sprintf(\"row %d is a duplicate or out of order (chain at %d)\", row.Seq, head.Seq)})", CH, PKG),
    ("N1a the router reads the chain", D, "\tgood, problems, head, err := verifyChain(path, r.key)", "\trows, _, err := ReadRows(path)\n\tgood, problems, head := rows, []Problem(nil), chainHead{}", CH, PKG),
    # N1b: the hold
    ("N1b a break holds the tasks it may have cost a row", D, "\tif err := r.holdForProblems(list, now); err != nil {\n\t\treturn err\n\t}", "", HOLD, PKG),
    ("N1b a held task's rows are not logged again", R, "\t\tif r.held(t.ID) {\n\t\t\tcontinue", "\t\tif false && r.held(t.ID) {\n\t\t\tcontinue", "TestFix3TamperedRowsAreAbsentAndReported$", PKG),
    ("N1b a held task routes no further", R, "\tif hold, held := r.holdTampered(t, now); held {", "\tif hold, held := r.holdTampered(t, now); held && false {", HOLD, PKG),
    ("N1b only the lead's reset clears it", TM, "\tif t.State == task.StatePending && t.UpdatedAt.After(at) {", "\tif t.State == task.StatePending && !at.IsZero() {", HOLD, PKG),
    ("N1b the hold survives a restart", TM, "\t\tr.tamperBlockedAt[rec.Task] = rec.At\n", "\t\t_ = rec.At\n", HOLD, PKG),
    ("N1b a break with no task holds the unlogged", TM, "\tif unknown {\n", "\tif false && unknown {\n", CH, PKG),
    ("N1b a row gone under the running router holds its task", D, "\t\t\tif !r.logged[k] {\n\t\t\t\tproblems = append(problems, Problem{ID: \"lost:\" + k", "\t\t\tif false && !r.logged[k] {\n\t\t\t\tproblems = append(problems, Problem{ID: \"lost:\" + k", "TestFix2LogDurabilityIsReverified$", PKG),
    ("N1b a known break holds once", TM, "\t\tif r.knownProblems[p.ID] {\n\t\t\tcontinue\n\t\t}", "\t\tif r.knownProblems[p.ID] && false {\n\t\t\tcontinue\n\t\t}", "TestFix4KnownBreakHoldsOnce$", PKG),
    # N1c: the tag before any field
    ("N1c a wrong-schema line is a break", C, "\t\tif json.Unmarshal(line, &row) != nil || row.Schema != RowSchema || row.Task == \"\" || !row.Verify(key) {", "\t\tif json.Unmarshal(line, &row) != nil || row.Task == \"\" || !row.Verify(key) {\n\t\t\tcontinue\n\t\t}\n\t\tif row.Schema != RowSchema {\n\t\t\tcontinue\n\t\t}\n\t\tif false {", "TestFix4WrongSchemaLineIsReported$", PKG),
    # P1-4: Flush runs the tail check
    ("P1-4 Flush runs the same checks as Route", R, "\treturn r.durable(list, r.o.Now())\n}\n\nfunc (r *Router) flush(", "\treturn r.flush(list, r.o.Now())\n}\n\nfunc (r *Router) flush(", "TestFix3TailBeforeFlushAndRecoveryKeepsPolicy$", PKG),
    # new: the FIFO seed
    ("new: the seed open does not block", SU, "os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK", "os.O_RDONLY|syscall.O_NOFOLLOW|0*syscall.O_NONBLOCK", "TestFix4SeedFIFODoesNotHang$", PKG),
]
