RF = "./internal/releaseflow/"
MT = "./internal/mergetrain/"
LG = "./internal/ledger/"
REL = "internal/releaseflow/releaseflow.go"
GH = "internal/mergetrain/gh.go"
TR = "internal/mergetrain/train.go"
LED = "internal/ledger/ledger.go"
TL = "TestRepassForcePushTimelineIsReadWhole"
MR = "TestRepassLostMergeResponseIsReconciled$"
CASES = [
    # P1-1
    ("P1-1 a published release at its id is completion, never a deletion", REL,
     "\t\tcase !got.Draft && got.ID == prev.ID && got.Tag == want.Tag && got.TargetCommitish == want.TargetCommitish:",
     "\t\tcase false && !got.Draft && got.ID == prev.ID && got.Tag == want.Tag && got.TargetCommitish == want.TargetCommitish:",
     "TestRepassLostPublishResponseIsCompletionNotDeletion$", RF),
    # P1-2
    ("P1-2 the timeline is paginated with the cursor", GH, "\t\tif !pg.Next {", "\t\tif true {", TL + "$/whole_timeline", MT),
    ("P1-2 the page bound is history_incomplete", GH, "\t\tif page > maxPages {", "\t\tif false && page > maxPages {", TL + "$/page_bound", MT),
    ("P1-2 a rate limit is history_incomplete", GH, "\t\t\tif rateLimited(err) {", "\t\t\tif false && rateLimited(err) {", TL + "$/rate_limit", MT),
    ("P1-2 the check refuses history_incomplete", "internal/mergetrain/checks.go", "\tif in.pr.HistoryIncomplete != \"\" {", "\tif false && in.pr.HistoryIncomplete != \"\" {", TL + "$/page_bound", MT),
    # P1-3
    ("P1-3 an open quote redacts the rest from the secret flag", [
        (LED, "\t\t\treturn strings.Join(append(words[:i:i], \"[redacted]\"), \" \")", "\t\t\twords[i] = \"[redacted]\""),
        (LED, "\t\t\treturn strings.Join(append(words[:i:i], key+\"[redacted]\"), \" \")", "\t\t\twords[i] = key + \"[redacted]\""),
    ], "TestRepassUnterminatedQuotedSecretIsRedactedWhole$", LG),
    # P2-4
    ("P2-4 a lost draft creation is adopted by its nonce", REL, "\t\tmatches, err := f.Exec.FindDrafts(ctx, repo, in.Nonce)", "\t\tmatches, err := []Draft(nil), error(nil)",
     "TestRepassLostDraftCreationIsAdoptedByItsNonce$", RF),
    # P2-5
    ("P2-5 the same run reconciles a lost merge response", TR, "\t\tgot, ok := t.mergedAt(ctx, req.Repository, p.Number, p.Head)", "\t\tgot, ok := \"\", false", MR, MT),
    ("P2-5 the next run reconciles the journaled intent", TR, "\t\tif landed, err = t.reconcileMerges(ctx, req, entry); err != nil {", "\t\tif landed, err = map[int]string{}, error(nil); err != nil {", MR, MT),
    ("P2-5 the intent is journaled before the PUT", TR, "\tif err := entry(Entry{Kind: \"merge_intent\", PR: p.Number,", "\tif err := entry(Entry{Kind: \"merge_note\", PR: p.Number,", MR, MT),
    # P2-6
    ("P2-6 the audit tail is repaired before an append", "internal/mergetrain/audit.go", "err == nil && last[0] != '\\n' {", "err == nil && last[0] != '\\n' && false {", "TestRepassAuditTornTailIsRepaired$", MT),
    # P2-7
    ("P2-7 the task-filtered read streams the whole file", LED, "\tr := bufio.NewReaderSize(f, 64<<10)", "\tr := bufio.NewReaderSize(io.LimitReader(f, MaxRead), 64<<10)", "TestRepassTaskEvidenceIsReadWholeOrMarkedIncomplete$", LG),
    ("P2-7 unread evidence is incomplete with its reason", LED, "\t\t\tincomplete = append(incomplete, fmt.Sprintf(", "\t\t\t_ = append(incomplete, fmt.Sprintf(", "TestRepassTaskEvidenceIsReadWholeOrMarkedIncomplete$", LG),
    # P2-8
    ("P2-8 the live lease window ends at its expiry", LED, "\t\tif !l.ExpiresAt.IsZero() && l.ExpiresAt.Before(end) {", "\t\tif false && !l.ExpiresAt.IsZero() && l.ExpiresAt.Before(end) {", "TestRepassLiveLeaseWindowEndsAtItsExpiry$", LG),
]
